package v1

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mafuyu/internal/auth"
	"mafuyu/internal/auth/settings"
	"mafuyu/internal/data/store"
	"mafuyu/internal/utils/logger"
)

func TestSubmissionSwitchAndAuth(t *testing.T) {
	logger.Init("error", "", "")
	m, err := auth.New(settings.Config{Static: settings.Static{Enabled: true, Clients: []settings.Client{{Name: "collector", Token: "v1-api-key"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		enabled   bool
		path, key string
		status    int
	}{
		{false, "/station/api/v1/", "", 405},
		{false, "/station/api/submitRoomNumber", "", 404},
		{true, "/station/api/v1/", "", 401},
		{true, "/station/api/submitRoomNumber", "", 401},
		{true, "/station/api/submitRoomNumber", "v1-api-key", 307},
		{true, "/station/api/v1/", "v1-api-key", 200},
	} {
		s := store.New(0, 500)
		h := NewHandler(NewWsHandler(s, nil, 60), s, nil)
		mux := http.NewServeMux()
		if tc.enabled {
			h.RegisterRoutes(mux, m)
		} else {
			h.RegisterRoutes(mux, nil)
		}
		body := fmt.Sprintf(`{"room_id":"12345","create_time":%d}`, time.Now().Unix())
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(body))
		if tc.key != "" {
			r.Header.Set("X-API-Key", tc.key)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d, want %d", tc.path, w.Code, tc.status)
		}
		if tc.status == 200 && s.FlushChannelCounts()["collector"] != 1 {
			t.Fatal("wrong authenticated channel")
		}
	}
}
