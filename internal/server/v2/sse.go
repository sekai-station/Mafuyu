// Package v2 implements the SSE broker that fans out real-time events to all
// connected v2 /realtime SSE clients.
package v2

import (
	"context"
	"fmt"
	"sync"
	"time"

	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

// clientQueueSize bounds how many events a client may fall behind the broadcast
// before it is disconnected.
const clientQueueSize = 128

// Client represents one SSE subscriber. Ch receives pre-formatted SSE frames.
type Client struct {
	Ch        chan []byte
	CreatedAt time.Time
	Extra     bool // asked for info.extra (?extra=1)

	kicked   chan struct{}
	kickOnce sync.Once
}

// Kicked is closed when the client has fallen too far behind and its
// connection must be closed.
func (c *Client) Kicked() <-chan struct{} {
	return c.kicked
}

func (c *Client) kick() {
	c.kickOnce.Do(func() {
		close(c.kicked)
		logger.Info.Warn("SSE client too slow, disconnecting",
			"queue", clientQueueSize,
			"connected_for", time.Since(c.CreatedAt).Round(time.Second),
		)
	})
}

// Broker manages the set of active SSE clients and drives the heartbeat ticker.
type Broker struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}

	past15mFn  func() int // returns current past-15-min room count from the store
	onlineFn   func() int // returns v1 WS online count; injected after construction
	hideAvatar bool       // config v2.send_avatar: false
}

// NewBroker creates a Broker. past15mFn is called every 30 seconds to produce
// the "statistic" SSE event. hideAvatar sends every avatar as null, in SSE
// events and in /recent alike.
func NewBroker(past15mFn func() int, hideAvatar bool) *Broker {
	return &Broker{
		clients:    make(map[*Client]struct{}),
		past15mFn:  past15mFn,
		hideAvatar: hideAvatar,
	}
}

// roomOptions are the encoding options for a client that did or did not ask
// for info.extra.
func (b *Broker) roomOptions(extra bool) RoomOptions {
	return RoomOptions{HideAvatar: b.hideAvatar, Extra: extra}
}

// SetOnlineFn injects the function that returns the v1 WebSocket online count.
// Called after both the broker and the WS handler are constructed.
func (b *Broker) SetOnlineFn(fn func() int) {
	b.onlineFn = fn
}

// Subscribe registers a new SSE client and returns it. extra: the client asked
// for info.extra in room events. The caller must call Unsubscribe when the
// client disconnects.
func (b *Broker) Subscribe(extra bool) *Client {
	c := &Client{
		Ch:        make(chan []byte, clientQueueSize),
		CreatedAt: time.Now(),
		Extra:     extra,
		kicked:    make(chan struct{}),
	}
	b.mu.Lock()
	b.clients[c] = struct{}{}
	total := len(b.clients)
	b.mu.Unlock()
	logger.Info.Info("SSE client connected", "total", total)
	return c
}

// Unsubscribe removes a client from the broker.
func (b *Broker) Unsubscribe(c *Client) {
	duration := time.Since(c.CreatedAt).Round(time.Second)
	b.mu.Lock()
	delete(b.clients, c)
	total := len(b.clients)
	b.mu.Unlock()
	logger.Info.Info("SSE client disconnected", "total", total, "duration", duration)
}

// SSEOnlineCount returns the number of active SSE connections.
func (b *Broker) SSEOnlineCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// TotalOnlineCount returns SSE + v1 WebSocket clients combined.
func (b *Broker) TotalOnlineCount() int {
	count := b.SSEOnlineCount()
	if b.onlineFn != nil {
		count += b.onlineFn()
	}
	return count
}

// broadcast sends data to every client's channel without blocking.
// A client whose queue is full is disconnected rather than silently missing
// the event: the browser reconnects and the initial burst replays the last
// 5 minutes of rooms.
func (b *Broker) broadcast(data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for c := range b.clients {
		c.send(data)
	}
}

// send queues data for the client, or disconnects it when its queue is full.
func (c *Client) send(data []byte) {
	select {
	case c.Ch <- data:
	default:
		c.kick()
	}
}

// BroadcastRoom encodes room as an SSE "room" event and broadcasts it. The
// variant with info.extra is only encoded when a client asked for it.
func (b *Broker) BroadcastRoom(room *model.Room) {
	data, err := EncodeRoom(room, b.roomOptions(false))
	if err != nil {
		logger.Error.Error("SSE encode room", "error", err, "id", room.ID)
		return
	}
	plain := formatSSE("room", data)
	var full []byte

	b.mu.RLock()
	defer b.mu.RUnlock()
	for c := range b.clients {
		if !c.Extra {
			c.send(plain)
			continue
		}
		if full == nil {
			data, err := EncodeRoom(room, b.roomOptions(true))
			if err != nil {
				logger.Error.Error("SSE encode room", "error", err, "id", room.ID)
				return
			}
			full = formatSSE("room", data)
		}
		c.send(full)
	}
}

// BroadcastRoomSkills encodes skills as an SSE "roomSkills" event and broadcasts it.
func (b *Broker) BroadcastRoomSkills(skills *model.RoomSkills) {
	data := EncodeRoomSkills(skills)
	b.broadcast(formatSSE("roomSkills", data))
}

// StartHeartbeat runs a 15-second ticker that alternates between a "heartbeat"
// event (odd ticks) and a "statistic" event (even ticks). Blocks until ctx
// is cancelled.
func (b *Broker) StartHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	isHeartbeat := true
	for {
		select {
		case <-ticker.C:
			if isHeartbeat {
				data := EncodeHeartbeat(time.Now().UnixMilli())
				b.broadcast(formatSSE("heartbeat", data))
				logger.Info.Debug("SSE heartbeat sent", "clients", b.SSEOnlineCount())
			} else {
				online := b.TotalOnlineCount()
				past15m := 0
				if b.past15mFn != nil {
					past15m = b.past15mFn()
				}
				data := EncodeStatistic(online, past15m)
				b.broadcast(formatSSE("statistic", data))
				logger.Info.Debug("SSE statistic sent", "online", online, "past15m", past15m)
			}
			isHeartbeat = !isHeartbeat
		case <-ctx.Done():
			return
		}
	}
}

// formatSSE formats a single SSE frame: "event: <event>\ndata: <data>\n\n".
func formatSSE(event string, data []byte) []byte {
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, data))
}
