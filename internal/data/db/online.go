package db

import (
	"context"
	"time"

	"mafuyu/internal/utils/model"
)

type OnlineSample struct {
	Time   int64
	Online int
}

// RecordOnline atomically persists peaks and a scheduled sample, updates the
// hourly average, discards completed hours' samples, and prunes old summaries.
func (d *DB) RecordOnline(ctx context.Context, now time.Time, retentionDays int, peaks map[int64]int, sample *OnlineSample) error {
	hour := now.Unix() / 3600 * 3600
	cutoff := hour - int64(retentionDays)*86400 + 3600
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for h, peak := range peaks {
		if h < cutoff || h > hour {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO online_hourly(hour, peak) VALUES (?, ?)
			ON CONFLICT(hour) DO UPDATE SET peak = MAX(peak, excluded.peak)`, h, peak); err != nil {
			return err
		}
	}
	if sample != nil {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO online_samples(sample_time, online) VALUES (?, ?)`, sample.Time, sample.Online); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE online_hourly SET
			average = (SELECT AVG(online) FROM online_samples WHERE sample_time >= ? AND sample_time < ?),
			samples = (SELECT COUNT(*) FROM online_samples WHERE sample_time >= ? AND sample_time < ?)
			WHERE hour = ?`, hour, hour+3600, hour, hour+3600, hour); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM online_samples WHERE sample_time < ?`, hour); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM online_hourly WHERE hour < ?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

// OnlineHistory returns up to 48 hourly buckets, including the current hour.
// Missing observations are absent rather than invented zero counts.
func (d *DB) OnlineHistory(ctx context.Context, now time.Time) ([]model.OnlineHour, error) {
	hour := now.Unix() / 3600 * 3600
	rows, err := d.db.QueryContext(ctx, `SELECT hour, peak, average, samples FROM online_hourly
		WHERE hour >= ? AND hour <= ? ORDER BY hour`, hour-47*3600, hour)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.OnlineHour, 0, 48)
	for rows.Next() {
		var entry model.OnlineHour
		if err := rows.Scan(&entry.Hour, &entry.Peak, &entry.Average, &entry.Samples); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}
