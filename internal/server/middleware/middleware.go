// Package middleware provides shared HTTP utilities: JSON response helpers,
// authentication middleware, and request logging.
package middleware

import (
	"encoding/json"
	"net/http"

	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

// WriteJSON writes a v2 success envelope as JSON with HTTP 200.
func WriteJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Envelope{Code: code, Data: data})
}

// WriteError sets httpCode on the response and writes a v2 error envelope.
func WriteError(w http.ResponseWriter, httpCode int, code int, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(model.Envelope{Code: code, Data: struct{}{}, Error: errMsg})
}

// LoggingMiddleware logs every incoming request (method, path, client IP) and
// delegates to next.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"ip", ClientIP(r),
		)
		next.ServeHTTP(w, r)
	})
}

// ClientIP returns the real client IP forwarded by nginx via X-Real-IP.
// Falls back to RemoteAddr when the header is absent.
func ClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}
