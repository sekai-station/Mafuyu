package v2

import (
	"encoding/json"
	"fmt"
	"mafuyu/internal/auth"
	"mafuyu/internal/auth/settings"
	"mafuyu/internal/data/store"
	"mafuyu/internal/utils/logger"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOAuthChannelUsesVerifiedApplication(t *testing.T) {
	logger.Init("error", "", "")
	t.Setenv("CHANNEL_TEST_SECRET", "secret")
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		subject := "user-one"
		if r.Form.Get("token") == "second-user" {
			subject = "user-two"
		}
		json.NewEncoder(w).Encode(map[string]any{"active": true, "sub": subject, "client_id": "station-v1-relay", "aud": "station", "scope": "station:room:write"})
	}))
	defer issuer.Close()
	m, err := auth.New(settings.Config{OAuth: settings.OAuth{Enabled: true, Mode: "introspection", Issuer: issuer.URL, Audience: "station", IntrospectionURL: issuer.URL, ClientID: "station", ClientSecretEnv: "CHANNEL_TEST_SECRET", RequiredScopes: []string{"station:room:write"}}})
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(0, 500)
	h := NewHandler(st, NewBroker(nil, false), nil, nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, m)
	for i, token := range []string{"first-user", "second-user"} {
		body := fmt.Sprintf(`{"data":[{"id":"%05d","time":%d,"info":{"tid":"1"}}]}`, i, time.Now().Unix())
		r := httptest.NewRequest("POST", "/station/api/v2/submit", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("x-client-name", "spoofed")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("submission: %d %s", w.Code, w.Body.String())
		}
	}
	counts := st.FlushChannelCounts()
	if len(counts) != 1 || counts["station-v1-relay"] != 2 {
		t.Fatalf("wrong channels: %v", counts)
	}
}
