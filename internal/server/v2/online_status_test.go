package v2

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"mafuyu/internal/data/db"
	"mafuyu/internal/data/store"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

func TestStatusIncludesLivePeakAndPersistedAverage(t *testing.T) {
	logger.Init("error", "", "")
	database, err := db.Open(filepath.Join(t.TempDir(), "status.db"), 14)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now()
	hour := now.Unix() / 3600 * 3600
	if err := database.RecordOnline(context.Background(), now, 30, map[int64]int{hour: 1}, &db.OnlineSample{Time: hour, Online: 1}); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(nil, false)
	first := b.Subscribe(false, "ce8bdafd-a8b7-49b0-80f3-ad945fbba189")
	second := b.Subscribe(false, "5a53c21f-096a-4cf3-bb50-ade79efb2129")
	defer b.Unsubscribe(first)
	b.Unsubscribe(second)
	h := NewHandler(store.New(0, 500), b, database, nil, nil)
	response := httptest.NewRecorder()
	h.handleStatus(response, httptest.NewRequest("GET", "/station/api/v2/status", nil))
	if response.Code != 200 {
		t.Fatalf("status=%d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			History []model.OnlineHour `json:"onlineHistory"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.History) != 1 {
		t.Fatalf("unexpected history: %+v", envelope.Data.History)
	}
	current := envelope.Data.History[0]
	if current.Peak != 2 || current.Average == nil || *current.Average != 1 || current.Samples != 1 {
		t.Fatalf("lost live peak or persisted average: %+v", current)
	}
}
