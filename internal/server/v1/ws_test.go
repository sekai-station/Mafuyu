package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

func TestBroadcastDisconnectsClientWithFullQueue(t *testing.T) {
	if err := logger.Init("error", "", ""); err != nil {
		t.Fatal(err)
	}
	h := NewWsHandler(nil, nil, 0)

	registered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		// No write pump, so nothing drains the queue.
		c := &wsClient{
			conn:        conn,
			sendCh:      make(chan []byte, clientQueueSize),
			connectedAt: time.Now(),
			done:        make(chan struct{}),
		}
		h.mu.Lock()
		h.clients[c] = struct{}{}
		h.mu.Unlock()
		close(registered)
		conn.Read(context.Background()) // lets the close handshake complete
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	<-registered

	readErr := make(chan error, 1)
	go func() {
		_, _, err := client.Read(ctx)
		readErr <- err
	}()

	room := &model.Room{ID: "12345", Info: model.XInfo{}}
	for i := 0; i < clientQueueSize; i++ {
		h.Broadcast(room)
	}
	select {
	case err := <-readErr:
		t.Fatalf("connection closed before the queue was full: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	h.Broadcast(room)
	select {
	case err := <-readErr:
		if got := websocket.CloseStatus(err); got != websocket.StatusTryAgainLater {
			t.Fatalf("close status = %v, want %v (err: %v)", got, websocket.StatusTryAgainLater, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client with a full queue was not disconnected")
	}
}
