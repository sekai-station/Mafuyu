package auth

import (
	"context"
	"mafuyu/internal/auth/credential"
	"mafuyu/internal/auth/settings"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeVerifier struct {
	calls int
	err   error
}

func (v *fakeVerifier) Verify(context.Context, string) (string, error) {
	v.calls++
	return "verified-user", v.err
}

func TestCredentialDispatchAndNoFallback(t *testing.T) {
	for _, tc := range []struct {
		key, bearer                     string
		err                             error
		status, staticCalls, oauthCalls int
	}{
		{"key", "", nil, 204, 1, 0},
		{"", "Bearer token", nil, 204, 0, 1},
		{"", "", nil, 401, 0, 0},
		{"key", "Bearer token", nil, 400, 0, 0},
		{"", "Basic abc", nil, 401, 0, 0},
		{"key", "", credential.ErrUnauthorized, 401, 1, 0},
		{"", "Bearer token", credential.ErrForbidden, 403, 0, 1},
		{"", "Bearer token", credential.ErrUnavailable, 503, 0, 1},
	} {
		s, o := &fakeVerifier{err: tc.err}, &fakeVerifier{err: tc.err}
		m := &Middleware{static: s, oauth: o}
		h := m.Wrap(func(w http.ResponseWriter, r *http.Request) {
			p, ok := FromContext(r.Context())
			if !ok || p.Subject != "verified-user" {
				t.Error("missing verified identity")
			}
			w.WriteHeader(204)
		})
		r := httptest.NewRequest("POST", "/", nil)
		if tc.key != "" {
			r.Header.Set("X-API-Key", tc.key)
		}
		if tc.bearer != "" {
			r.Header.Set("Authorization", tc.bearer)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || s.calls != tc.staticCalls || o.calls != tc.oauthCalls {
			t.Fatalf("dispatch: %d %d %d", w.Code, s.calls, o.calls)
		}
	}
}
func TestStartupRejectsUnusableAuth(t *testing.T) {
	if _, err := New(settings.Config{}); err == nil {
		t.Fatal("no provider accepted")
	}
	if _, err := New(settings.Config{Static: settings.Static{Enabled: true, Clients: []settings.Client{{Name: "collector", Token: ""}}}}); err == nil {
		t.Fatal("missing secret accepted")
	}
}
