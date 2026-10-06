package v2

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mafuyu/internal/data/announce"
)

func TestAnnouncementLocaleResponses(t *testing.T) {
	dir := t.TempDir()
	for locale, msg := range map[string]string{"en": "English", "zh-hans": "中文", "ja": ""} {
		if err := os.WriteFile(filepath.Join(dir, locale+".txt"), []byte(msg), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := announce.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{announce: a}
	for _, tc := range []struct {
		query  string
		status int
		msg    string
	}{{"", 200, "English"}, {"?lang=zh-Hans", 200, "中文"}, {"?lang=ja", 200, ""}, {"?lang=fr", 404, ""}} {
		t.Run(tc.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.handleAnnouncement(w, httptest.NewRequest("GET", "/station/api/v2/announcement"+tc.query, nil))
			var body struct {
				Code int `json:"code"`
				Data struct {
					Msg string `json:"msg"`
				} `json:"data"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || body.Code != tc.status || body.Data.Msg != tc.msg || (tc.status == 404 && body.Error == "") {
				t.Fatalf("unexpected response: HTTP %d %s", w.Code, w.Body.String())
			}
		})
	}
}
