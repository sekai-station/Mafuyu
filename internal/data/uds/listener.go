// Package uds implements a Unix domain socket listener that receives newline-
// delimited JSON messages from local data-collection processes and forwards
// accepted rooms to the broadcast callbacks.
package uds

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"sync"
	"syscall"
	"time"

	"mafuyu/internal/data/filter"
	"mafuyu/internal/data/store"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

// roomIDRe matches exactly five decimal digits.
var roomIDRe = regexp.MustCompile(`^\d{5}$`)

// Handler listens on a UDS path and dispatches incoming room/roomSkills messages.
// It intentionally takes broadcast callbacks rather than importing server packages
// so the data layer remains independent of the server layer.
type Handler struct {
	store           *store.Store
	broadcastRoom   func(*model.Room)
	broadcastSkills func(*model.RoomSkills)
}

// NewHandler creates a Handler with the given store and broadcast callbacks.
func NewHandler(s *store.Store, broadcastRoom func(*model.Room), broadcastSkills func(*model.RoomSkills)) *Handler {
	return &Handler{store: s, broadcastRoom: broadcastRoom, broadcastSkills: broadcastSkills}
}

// Listen removes any stale socket file, binds a UNIX listener at sockPath, and
// accepts connections until ctx is cancelled. Each connection is served in its
// own goroutine.
func (h *Handler) Listen(ctx context.Context, sockPath string) error {
	l, err := listenUnix(ctx, sockPath)
	if err != nil {
		return err
	}

	defer l.Close()
	var mu sync.Mutex
	connections := make(map[net.Conn]struct{})
	var handlers sync.WaitGroup
	defer handlers.Wait()
	stopped := make(chan struct{})
	defer close(stopped)
	// Close both listener and accepted connections on cancellation.
	go func() {
		select {
		case <-ctx.Done():
		case <-stopped:
			return
		}
		l.Close()
		mu.Lock()
		for conn := range connections {
			conn.Close()
		}
		mu.Unlock()
	}()

	logger.Info.Info("UDS listening", "path", sockPath)
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				logger.Error.Error("UDS accept", "error", err)
				continue
			}
		}
		mu.Lock()
		if ctx.Err() != nil {
			conn.Close()
			mu.Unlock()
			continue
		}
		connections[conn] = struct{}{}
		mu.Unlock()
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
			h.handleConn(conn)
		}()
	}
}

// listenUnix binds first, and only removes a confirmed stale socket after a
// refused connection. Files, symlinks, active listeners and uncertain failures
// are preserved. Only one process should own a configured socket path.
func listenUnix(ctx context.Context, sockPath string) (net.Listener, error) {
	l, bindErr := net.Listen("unix", sockPath)
	if bindErr == nil {
		return l, nil
	}
	info, err := os.Lstat(sockPath)
	if err != nil {
		return nil, bindErr
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("UDS path is not a socket: %s", sockPath)
	}
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "unix", sockPath)
	if err == nil {
		conn.Close()
		return nil, fmt.Errorf("UDS socket already in use: %s", sockPath)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return nil, fmt.Errorf("cannot confirm stale UDS socket: %w", err)
	}
	current, err := os.Lstat(sockPath)
	if err != nil || !os.SameFile(info, current) {
		return nil, fmt.Errorf("UDS path changed during stale socket check: %s", sockPath)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Remove(sockPath); err != nil {
		return nil, fmt.Errorf("remove stale UDS socket: %w", err)
	}
	return net.Listen("unix", sockPath)
}

// handleConn reads newline-delimited JSON messages from a single connection.
// The scanner buffer is set to 1 MB to accommodate large room batches.
func (h *Handler) handleConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		h.handleMessage(scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		logger.Error.Error("UDS read", "error", err)
	}
}

// handleMessage parses one UDSMessage and routes it by type:
//   - "room" (default): parse each item, add to store, broadcast accepted ones.
//   - "roomSkills": broadcast each skill entry directly without deduplication.
func (h *Handler) handleMessage(data []byte) {
	var msg model.UDSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		logger.Error.Error("UDS parse", "error", err)
		return
	}

	// Default to "room" when the type field is absent.
	if len(msg.Channel) > 128 {
		logger.Error.Error("UDS channel name too long")
		return
	}
	if msg.Type == "" {
		msg.Type = "room"
	}

	switch msg.Type {
	case "room":
		var items []model.UDSRoomItem
		if err := json.Unmarshal(msg.Data, &items); err != nil {
			logger.Error.Error("UDS parse room data", "error", err)
			return
		}
		if len(items) > 1000 {
			logger.Error.Error("UDS room batch too large")
			return
		}
		for _, item := range items {
			if !roomIDRe.MatchString(item.ID) {
				logger.Error.Error("UDS invalid room id", "id", item.ID)
				continue
			}
			room, err := model.RoomFromItem(item)
			if err != nil {
				logger.Error.Error("UDS parse room item", "error", err, "id", item.ID)
				continue
			}
			if err := model.ValidateRoom(room); err != nil {
				logger.Error.Error("UDS invalid room", "error", err)
				continue
			}
			if !filter.Check(room) {
				continue
			}
			if h.store.Add(room, msg.Channel) {
				logger.Info.Info("new room", "id", room.ID, "channel", msg.Channel)
				if h.broadcastRoom != nil {
					h.broadcastRoom(room)
				}
			}
		}
	case "roomSkills":
		var skills []model.RoomSkills
		if err := json.Unmarshal(msg.Data, &skills); err != nil {
			logger.Error.Error("UDS parse roomSkills data", "error", err)
			return
		}
		for i := range skills {
			if h.broadcastSkills != nil {
				h.broadcastSkills(&skills[i])
			}
		}
	default:
		logger.Error.Error("UDS unknown type", "type", msg.Type)
	}
}
