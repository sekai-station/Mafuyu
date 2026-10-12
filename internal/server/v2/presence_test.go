package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"mafuyu/internal/data/announce"
	"mafuyu/internal/data/store"
	"mafuyu/internal/server/v1"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

func TestRealtimePresenceAcrossConnectionsAndProtocols(t *testing.T) {
	logger.Init("error", "", "")
	st := store.New(0, 500)
	b := NewBroker(nil, false)
	ann, err := announce.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := v1.NewWsHandler(st, ann, 60, b.Presence())
	mux := http.NewServeMux()
	NewHandler(st, b, nil, ann, nil).RegisterRoutes(mux, nil)
	mux.Handle("/ws", ws)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer ws.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var streams []*http.Response
	defer func() {
		for _, res := range streams {
			res.Body.Close()
		}
	}()
	const id = "ce8bdafd-a8b7-49b0-80f3-ad945fbba189"
	check := func(wantOnline, wantConnections int) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for {
			req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/station/api/v2/statistic", nil)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Data model.Statistic `json:"data"`
			}
			err = json.NewDecoder(res.Body).Decode(&envelope)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if envelope.Data.Online == wantOnline && envelope.Data.Connections == wantConnections {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("unexpected statistic: %+v", envelope.Data)
			}
			time.Sleep(time.Millisecond)
		}
	}
	for _, query := range []string{"", "?client_id=invalid", "?client_id=" + id, "?client_id=" + strings.ToUpper(id)} {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/station/api/v2/realtime"+query, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatal("stream rejected")
		}
		streams = append(streams, res)
	}
	check(1, 4)
	conn, _, err := websocket.Dial(ctx, strings.Replace(srv.URL, "http", "ws", 1)+"/ws?client_id="+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	check(1, 5)
	streams[2].Body.Close()
	check(1, 4)
	streams[3].Body.Close()
	check(1, 3) // WS still keeps this browser online
	conn.CloseNow()
	check(0, 2) // anonymous SSE connections never count as online
	streams[0].Body.Close()
	streams[1].Body.Close()
	check(0, 0)
}
