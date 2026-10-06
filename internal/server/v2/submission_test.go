package v2

import (
	"mafuyu/internal/data/store"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPSubmissionDisabledKeepsReads(t *testing.T) {
	h := NewHandler(store.New(0, 500), NewBroker(nil, false), nil, nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/station/api/v2/submit", nil))
	if w.Code != 404 {
		t.Fatalf("disabled submission: %d", w.Code)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/station/api/v2/recent", nil))
	if w.Code != 200 {
		t.Fatalf("read endpoint disabled: %d", w.Code)
	}
}
