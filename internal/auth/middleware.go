package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"mafuyu/internal/auth/credential"
	"mafuyu/internal/server/middleware"
)

func (m *Middleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKeys, authHeaders := r.Header.Values("X-API-Key"), r.Header.Values("Authorization")
		if len(apiKeys) > 1 || len(authHeaders) > 1 || (len(apiKeys) != 0 && len(authHeaders) != 0) {
			middleware.WriteError(w, 400, 400, "provide exactly one credential")
			return
		}
		var verifier credential.Verifier
		var token, provider string
		if len(apiKeys) == 1 {
			token, verifier, provider = strings.TrimSpace(apiKeys[0]), m.static, "static"
		} else if len(authHeaders) == 1 {
			parts := strings.Fields(authHeaders[0])
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				token, verifier, provider = parts[1], m.oauth, "oauth"
			}
		}
		if verifier == nil || token == "" || len(token) > 16384 {
			m.fail(w, r, 401)
			return
		}
		subject, err := verifier.Verify(r.Context(), token)
		if err != nil {
			status := 401
			if errors.Is(err, credential.ErrForbidden) {
				status = 403
			}
			if errors.Is(err, credential.ErrUnavailable) {
				status = 503
			}
			m.fail(w, r, status)
			return
		}
		p := Principal{Subject: subject, Provider: provider}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	}
}
func (m *Middleware) fail(w http.ResponseWriter, r *http.Request, status int) {
	if status == 401 || status == 403 {
		challenge := ""
		if m.oauth != nil {
			challenge = `Bearer realm="mafuyu"`
			if status == 403 {
				challenge += `, error="insufficient_scope"`
			}
		}
		if m.static != nil && status == 401 {
			if challenge != "" {
				challenge += ", "
			}
			challenge += `ApiKey realm="mafuyu"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
	}
	middleware.WriteError(w, status, status, http.StatusText(status))
}
