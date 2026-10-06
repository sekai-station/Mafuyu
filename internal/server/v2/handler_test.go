package v2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mafuyu/internal/auth"
	"mafuyu/internal/auth/settings"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mafuyu/internal/data/store"
	"mafuyu/internal/server/middleware"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

type failedStream struct{ header http.Header }

func submissionAuth(t *testing.T) *auth.Middleware {
	t.Helper()
	m, err := auth.New(settings.Config{Static: settings.Static{Enabled: true, Clients: []settings.Client{{Name: "collector", Token: "unit-api-key"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (w *failedStream) Header() http.Header       { return w.header }
func (w *failedStream) WriteHeader(int)           {}
func (w *failedStream) Flush()                    {}
func (w *failedStream) Write([]byte) (int, error) { return 0, errors.New("broken connection") }

func TestStreamWriteFailureUnsubscribes(t *testing.T) {
	logger.Init("error", "", "")
	b := NewBroker(nil, false)
	h := NewHandler(store.New(0, 500), b, nil, nil, nil)
	h.handleRealtime(&failedStream{header: make(http.Header)}, httptest.NewRequest("GET", "/", nil))
	if b.SSEOnlineCount() != 0 {
		t.Fatal("failed stream retained a subscriber")
	}
}

func TestStreamSurvivesIdleBeyondWriteDeadline(t *testing.T) {
	logger.Init("error", "", "")
	b := NewBroker(nil, false)
	h := NewHandler(store.New(0, 500), b, nil, nil, nil)
	srv := httptest.NewServer(http.HandlerFunc(h.handleRealtime))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("not an SSE response")
	}
	time.AfterFunc(6*time.Second, func() { b.broadcast([]byte("event: heartbeat\ndata: {}\n\n")) })
	want := []byte(": connected\n\nevent: heartbeat\ndata: {}\n\n")
	got := make([]byte, len(want))
	if _, err := io.ReadFull(res.Body, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("unexpected stream: %s", got)
	}
}

func TestSubmitLimitsAndFutureTimestamp(t *testing.T) {
	logger.Init("error", "", "")
	h := NewHandler(store.New(0, 500), NewBroker(nil, false), nil, nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, submissionAuth(t))
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"data":[{"id":"12345","time":99999999999,"info":{"tid":"1"}}]}`, 400},
		{`{"data":[]} {"data":[]}`, 400},
		{`{"data":[]}` + strings.Repeat(" ", middleware.MaxSubmitBytes), 413},
		{`{"data":[{"id":"12345","time":1,"msg":"` + strings.Repeat("x", middleware.MaxSubmitBytes) + `","info":{"tid":"1"}}]}`, 413},
	} {
		req := httptest.NewRequest("POST", "/station/api/v2/submit", strings.NewReader(tc.body))
		req.Header.Set("X-API-Key", "unit-api-key")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != tc.code {
			t.Fatalf("status = %d, want %d", response.Code, tc.code)
		}
	}
}

func TestSubmissionUsesConfiguredStaticIdentity(t *testing.T) {
	logger.Init("error", "", "")
	st := store.New(0, 500)
	h := NewHandler(st, NewBroker(nil, false), nil, nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, submissionAuth(t))
	body := fmt.Sprintf(`{"data":[{"id":"12345","time":%d,"info":{"tid":"1"}}]}`, time.Now().Unix())
	req := httptest.NewRequest("POST", "/station/api/v2/submit", strings.NewReader(body))
	req.Header.Set("X-API-Key", "unit-api-key")
	req.Header.Set("x-client-name", "spoofed-channel")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != 200 || st.Count() != 1 {
		t.Fatalf("static credential rejected: %d %s", response.Code, response.Body.String())
	}
	counts := st.FlushChannelCounts()
	if counts["collector"] != 1 || counts["spoofed-channel"] != 0 {
		t.Fatalf("wrong authenticated channel: %v", counts)
	}
}

func TestRecentExtraOnlyWhenAsked(t *testing.T) {
	logger.Init("error", "", "")
	st := store.New(0, 500)
	st.Add(&model.Room{Time: time.Now().Unix(), ID: "12345", Source: "x", Info: model.XInfo{TID: "7", UserName: "@u", Avatar: "https://a/b.png"}}, "test")
	for _, tc := range []struct {
		query      string
		hideAvatar bool
		want       []string
		absent     []string
	}{
		{"", false, []string{`"info":{"handle":"@u","url":"https://x.com/u/status/7","avatar":"https://a/b.png"}`}, []string{"extra"}},
		{"?extra=1", false, []string{`"extra":{"type":"XInfo","tid":"7"`}, nil},
		{"?extra=true", true, []string{`"avatar":null,"extra":{"avatar":null,`}, []string{"b.png"}},
		{"?extra=0", false, nil, []string{"extra"}},
	} {
		mux := http.NewServeMux()
		NewHandler(st, NewBroker(nil, tc.hideAvatar), nil, nil, nil).RegisterRoutes(mux, nil)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest("GET", "/station/api/v2/recent"+tc.query, nil))
		body := res.Body.String()
		for _, s := range tc.want {
			if !strings.Contains(body, s) {
				t.Fatalf("%q: %s lacks %s", tc.query, body, s)
			}
		}
		for _, s := range tc.absent {
			if strings.Contains(body, s) {
				t.Fatalf("%q: %s contains %s", tc.query, body, s)
			}
		}
	}
}
