package db

import (
	"context"
	"time"

	"mafuyu/internal/server/presence"
	"mafuyu/internal/utils/logger"
)

// RunOnlineRecorder schedules samples on hour-aligned slots and flushes peak
// changes each second. Peaks themselves are captured on every presence change.
func (d *DB) RunOnlineRecorder(ctx context.Context, tracker *presence.Tracker, intervalSeconds, retentionDays int) {
	lastSlot := int64(-1)
	savedPeaks := make(map[int64]int)
	persist := func() {
		now, online, peaks := tracker.Snapshot()
		hour := now.Unix() / 3600 * 3600
		slot := hour + (now.Unix()-hour)/int64(intervalSeconds)*int64(intervalSeconds)
		dirty := make(map[int64]int)
		for h, peak := range peaks {
			if old, ok := savedPeaks[h]; !ok || peak > old {
				dirty[h] = peak
			}
		}
		var sample *OnlineSample
		if slot != lastSlot {
			sample = &OnlineSample{Time: slot, Online: online}
		}
		if len(dirty) == 0 && sample == nil {
			return
		}
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := d.RecordOnline(writeCtx, now, retentionDays, dirty, sample); err != nil {
			logger.Error.Error("record online statistics", "error", err)
			return
		}
		lastSlot = slot
		for h, peak := range dirty {
			savedPeaks[h] = peak
		}
		for h := range savedPeaks {
			if h < hour-3600 {
				delete(savedPeaks, h)
			}
		}
	}
	persist()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			persist()
		case <-ctx.Done():
			return
		}
	}
}
