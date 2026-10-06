package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"mafuyu/internal/data/announce"
	"mafuyu/internal/data/store"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"

	"github.com/coder/websocket"
)

// clientQueueSize bounds how many messages a client may fall behind before it
// is disconnected.
const clientQueueSize = 128

type wsClient struct {
	conn        *websocket.Conn
	sendCh      chan []byte
	connectedAt time.Time
	done        chan struct{}
	kickOnce    sync.Once
}

// enqueue queues data for the write pump. A client whose queue is full is
// disconnected rather than silently missing the message; v1 clients already
// reconnect after the periodic forced disconnect (checkDisconnect).
func (c *wsClient) enqueue(data []byte) {
	select {
	case c.sendCh <- data:
	default:
		c.kickOnce.Do(func() {
			logger.Info.Warn("WS client too slow, disconnecting",
				"queue", clientQueueSize,
				"connected_for", time.Since(c.connectedAt).Round(time.Second),
			)
			// Close waits for the close handshake; callers may hold h.mu.
			go c.conn.Close(websocket.StatusTryAgainLater, "client too slow")
		})
	}
}

// WsHandler manages connected v1 WebSocket clients and handles the v1
// protocol (handshake, action dispatch, broadcast).
type WsHandler struct {
	store     *store.Store
	announce  *announce.Announcement
	breakTime int

	mu       sync.RWMutex
	clients  map[*wsClient]struct{}
	closing  bool
	handlers sync.WaitGroup
}

func NewWsHandler(s *store.Store, a *announce.Announcement, breakTime int) *WsHandler {
	return &WsHandler{
		store:     s,
		announce:  a,
		breakTime: breakTime,
		clients:   make(map[*wsClient]struct{}),
	}
}

func (h *WsHandler) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *WsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}

	client := &wsClient{
		conn:        conn,
		sendCh:      make(chan []byte, clientQueueSize),
		connectedAt: time.Now(),
		done:        make(chan struct{}),
	}

	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		conn.CloseNow()
		return
	}
	h.handlers.Add(1)
	h.clients[client] = struct{}{}
	h.mu.Unlock()
	defer h.handlers.Done()
	logger.Info.Info("WS connected", "total", h.OnlineCount(), "ip", r.RemoteAddr)

	defer func() {
		duration := time.Since(client.connectedAt).Round(time.Second)
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		conn.CloseNow()
		logger.Info.Info("WS disconnected", "total", h.OnlineCount(), "duration", duration)
	}()

	h.sendHandshake(r.Context(), client)

	writerDone := make(chan struct{})
	go func() { defer close(writerDone); h.writePump(client) }()

	h.readPump(r.Context(), client)
	conn.CloseNow()
	<-writerDone
}

func (h *WsHandler) sendHandshake(ctx context.Context, c *wsClient) {
	h.sendJSON(ctx, c, v1Msg("sendServerTime", map[string]any{
		"time": time.Now().UnixMilli(),
	}))

	rooms := h.store.Snapshot()
	v1Rooms := make([]any, 0, len(rooms))
	for _, room := range rooms {
		v1Rooms = append(v1Rooms, roomToV1(room))
	}
	h.sendJSON(ctx, c, v1Msg("sendRoomNumberList", v1Rooms))
	logger.Info.Debug("handshake: sent room list", "count", len(rooms))

	msg, annTime := h.announce.Get("")
	if msg != "" {
		annRoom := map[string]any{
			"number":      "",
			"raw_message": msg,
			"source_info": map[string]any{"name": "bot", "type": "system"},
			"type":        "15w",
			"time":        annTime*1000 + 30,
			"user_info":   map[string]any{"type": "twi", "user_id": 0, "username": "系统消息", "avatar": ""},
		}
		h.sendJSON(ctx, c, v1Msg("sendRoomNumberList", []any{annRoom}))
		logger.Info.Debug("handshake: sent announcement")
	}
}

func (h *WsHandler) readPump(ctx context.Context, c *wsClient) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			close(c.done)
			return
		}
		h.handleAction(ctx, c, data)
	}
}

func (h *WsHandler) writePump(c *wsClient) {
	for {
		select {
		case data, ok := <-c.sendCh:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := c.conn.Write(ctx, websocket.MessageText, data)
			cancel()
			if err != nil {
				c.conn.CloseNow()
				return
			}
		case <-c.done:
			return
		}
	}
}

// Close terminates hijacked connections, which http.Server.Shutdown skips.
func (h *WsHandler) Close() {
	h.mu.Lock()
	h.closing = true
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		c.conn.CloseNow()
	}
	h.handlers.Wait()
}

func (h *WsHandler) handleAction(ctx context.Context, c *wsClient, data []byte) {
	var msgs []json.RawMessage
	if err := json.Unmarshal(data, &msgs); err != nil {
		var single map[string]any
		if err := json.Unmarshal(data, &single); err != nil {
			return
		}
		h.processAction(ctx, c, single)
		return
	}
	for _, raw := range msgs {
		var msg map[string]any
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		h.processAction(ctx, c, msg)
	}
}

func (h *WsHandler) processAction(ctx context.Context, c *wsClient, msg map[string]any) {
	action, _ := msg["action"].(string)
	switch action {
	case "heartbeat":
		logger.Info.Debug("WS heartbeat received")
		h.sendJSON(ctx, c, v1Msg("heartbeat", "alive"))
	case "getRoomNumberList":
		rooms := h.store.Snapshot()
		v1Rooms := make([]any, 0, len(rooms))
		for _, room := range rooms {
			v1Rooms = append(v1Rooms, roomToV1(room))
		}
		logger.Info.Debug("WS getRoomNumberList", "count", len(rooms))
		h.sendJSON(ctx, c, v1Msg("sendRoomNumberList", v1Rooms))
	case "setClient":
	}
}

func (h *WsHandler) sendJSON(ctx context.Context, c *wsClient, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.enqueue(data)
}

func (h *WsHandler) Broadcast(room *model.Room) {
	data, err := json.Marshal(v1Msg("sendRoomNumberList", []any{roomToV1(room)}))
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.enqueue(data)
	}
}

func (h *WsHandler) StartDisconnectChecker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.checkDisconnect()
		case <-ctx.Done():
			return
		}
	}
}

func (h *WsHandler) checkDisconnect() {
	h.mu.RLock()
	var toClose []*wsClient
	for c := range h.clients {
		if time.Since(c.connectedAt) > time.Duration(h.breakTime)*time.Second {
			toClose = append(toClose, c)
		}
	}
	h.mu.RUnlock()

	if len(toClose) > 0 {
		logger.Info.Info("WS force-disconnecting timed-out clients", "count", len(toClose))
	}
	for _, c := range toClose {
		c.conn.Close(websocket.StatusNormalClosure, "connection timeout")
	}
}

func v1Msg(action string, response any) model.V1Message {
	return model.V1Message{
		Status:   "success",
		Action:   action,
		Response: response,
	}
}

func roomToV1(room *model.Room) map[string]any {
	return map[string]any{
		"number":      room.ID,
		"raw_message": room.Msg,
		"source_info": map[string]any{"name": room.Source, "type": room.Source},
		"type":        "15w",
		"time":        room.Time * 1000,
		"user_info":   map[string]any{"type": room.Source, "user_id": 0, "username": room.Name, "avatar": ""},
	}
}
