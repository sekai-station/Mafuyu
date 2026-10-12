package db

import (
	"context"
	"fmt"
	"mafuyu/internal/server/presence"
	"mafuyu/internal/utils/logger"
	"path/filepath"
	"testing"
	"time"
)

func TestOnlineSamplesFinalizeAndSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "online.db")
	d, err := Open(path, 14)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { d.Close() }()
	ctx := context.Background()
	hour := int64(1791813600)
	record := func(at int64, peak int, sample *OnlineSample) {
		t.Helper()
		if err := d.RecordOnline(ctx, time.Unix(at, 0), 7, map[int64]int{at / 3600 * 3600: peak}, sample); err != nil {
			t.Fatal(err)
		}
	}
	record(hour, 10, &OnlineSample{Time: hour, Online: 10})
	record(hour+180, 40, &OnlineSample{Time: hour + 180, Online: 20})
	// A transient peak between scheduled samples must not alter the mean.
	record(hour+200, 80, nil)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path, 14)
	if err != nil {
		t.Fatal(err)
	}
	// Restarting in the same sampling slot neither replaces nor duplicates it.
	record(hour+210, 1, &OnlineSample{Time: hour + 180, Online: 1})
	entries, err := d.OnlineHistory(ctx, time.Unix(hour+210, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Peak != 80 || entries[0].Samples != 2 || entries[0].Average == nil || *entries[0].Average != 15 {
		t.Fatalf("unexpected current hour: %+v", entries)
	}
	record(hour+3600, 5, &OnlineSample{Time: hour + 3600, Online: 5})
	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM online_samples WHERE sample_time < ?`, hour+3600).Scan(&count); err != nil || count != 0 {
		t.Fatalf("completed-hour samples retained: %d, %v", count, err)
	}
	entries, err = d.OnlineHistory(ctx, time.Unix(hour+3600, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Peak != 80 || *entries[0].Average != 15 || entries[1].Peak != 5 || *entries[1].Average != 5 {
		t.Fatalf("unexpected finalized history: %+v", entries)
	}
}

func TestRecorderCapturesPeakBetweenSamples(t *testing.T) {
	logger.Init("error", "", "")
	d := tempDB(t)
	tracker := presence.New()
	first := tracker.Join("ce8bdafd-a8b7-49b0-80f3-ad945fbba189")
	defer first()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.RunOnlineRecorder(ctx, tracker, 3600, 30) }()
	defer func() { cancel(); <-done }()
	await := func(peak int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			entries, err := d.OnlineHistory(context.Background(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) > 0 && entries[len(entries)-1].Peak == peak {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("peak %d was not persisted", peak)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	await(1)
	second := tracker.Join("5a53c21f-096a-4cf3-bb50-ade79efb2129")
	second() // the spike is gone before the recorder's next tick
	await(2)
	entries, err := d.OnlineHistory(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	current := entries[len(entries)-1]
	if current.Samples != 1 || current.Average == nil || *current.Average != 1 {
		t.Fatalf("peak changed sample mean: %+v", current)
	}
}

func TestOnlineRetentionAndDisplayWindow(t *testing.T) {
	for _, days := range []int{7, 30} {
		t.Run(fmt.Sprintf("%d days", days), func(t *testing.T) {
			d := tempDB(t)
			ctx := context.Background()
			hour := int64(1791813600)
			steps := days*24 + 32
			for i := range steps {
				h := hour + int64(i)*3600
				if err := d.RecordOnline(ctx, time.Unix(h, 0), days, map[int64]int{h: i}, &OnlineSample{Time: h, Online: i}); err != nil {
					t.Fatal(err)
				}
			}
			var summaries, samples int
			if err := d.db.QueryRow(`SELECT COUNT(*) FROM online_hourly`).Scan(&summaries); err != nil {
				t.Fatal(err)
			}
			if err := d.db.QueryRow(`SELECT COUNT(*) FROM online_samples`).Scan(&samples); err != nil {
				t.Fatal(err)
			}
			if summaries != days*24 || samples != 1 {
				t.Fatalf("retained %d summaries, %d samples", summaries, samples)
			}
			entries, err := d.OnlineHistory(ctx, time.Unix(hour+int64(steps-1)*3600, 0))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 48 || entries[0].Peak != steps-48 || entries[47].Peak != steps-1 {
				t.Fatalf("incorrect display window: %+v", entries)
			}
		})
	}
}

func TestOnlineRecordingIsAtomic(t *testing.T) {
	d := tempDB(t)
	ctx := context.Background()
	hour := int64(1791813600)
	if err := d.RecordOnline(ctx, time.Unix(hour, 0), 7, map[int64]int{hour: 7}, &OnlineSample{Time: hour, Online: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`CREATE TRIGGER fail_online_prune BEFORE DELETE ON online_samples BEGIN SELECT RAISE(ABORT,'prune failed'); END`); err != nil {
		t.Fatal(err)
	}
	err := d.RecordOnline(ctx, time.Unix(hour+3600, 0), 7, map[int64]int{hour + 3600: 99}, &OnlineSample{Time: hour + 3600, Online: 99})
	if err == nil {
		t.Fatal("pruning failure ignored")
	}
	entries, err := d.OnlineHistory(ctx, time.Unix(hour+3600, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Peak != 7 {
		t.Fatalf("partial transaction committed: %+v", entries)
	}
}
