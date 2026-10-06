// Package db wraps a SQLite database for room snapshot persistence and
// historical statistics queries.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"mafuyu/internal/utils/model"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection with a 60-second in-memory cache for PastCount
// queries that would otherwise be expensive on every /status request.
type DB struct {
	db       *sql.DB
	keepDays int

	// Cache for PastCount to avoid hammering SQLite on every /status call.
	cacheMu   sync.RWMutex
	past1h    int
	past24h   int
	cacheTime int64 // unix seconds of last cache fill
}

// Open opens or creates the SQLite file at path, applies WAL mode, and runs
// schema migrations. keepDays controls how many days of snapshot history are
// retained before pruning.
func Open(path string, keepDays int) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return &DB{db: sqlDB, keepDays: keepDays}, nil
}

// migrate creates the rooms and channel_stats tables and their indexes if they
// do not already exist. Safe to call on an existing database.
func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS rooms (
			record_time INTEGER NOT NULL,
			room_id     TEXT NOT NULL,
			time        INTEGER NOT NULL,
			msg         TEXT NOT NULL,
			name        TEXT NOT NULL DEFAULT '',
			source      TEXT NOT NULL DEFAULT '',
			info_json   TEXT NOT NULL DEFAULT '{}',
			PRIMARY KEY (record_time, room_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rooms_time ON rooms(time)`,
		`CREATE INDEX IF NOT EXISTS idx_rooms_record_time ON rooms(record_time)`,
		`CREATE TABLE IF NOT EXISTS channel_stats (
			record_time  INTEGER NOT NULL,
			channel_name TEXT NOT NULL,
			count        INTEGER NOT NULL,
			PRIMARY KEY (record_time, channel_name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_channel_stats_record_time ON channel_stats(record_time)`,
		`CREATE TABLE IF NOT EXISTS usage_stats (
			record_time INTEGER PRIMARY KEY,
			past1h      INTEGER NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// SaveSnapshot writes the current room list and channel counts to SQLite in a
// single transaction and then prunes records older than keepDays days.
// Called every 60 seconds from a background goroutine in main.
func (d *DB) SaveSnapshot(rooms []*model.Room, channelCounts map[string]int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return d.SaveSnapshotContext(ctx, rooms, channelCounts)
}

func (d *DB) SaveSnapshotContext(ctx context.Context, rooms []*model.Room, channelCounts map[string]int) error {
	now := time.Now().Unix()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Bulk-upsert all current rooms under the current timestamp.
	roomStmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO rooms (record_time, room_id, time, msg, name, source, info_json) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer roomStmt.Close()

	for _, room := range rooms {
		infoJSON, err := json.Marshal(room.Info)
		if err != nil {
			return err
		}
		if _, err := roomStmt.ExecContext(ctx, now, room.ID, room.Time, room.Msg, room.Name, room.Source, string(infoJSON)); err != nil {
			return err
		}
	}

	// Persist per-channel submission counts flushed from the in-memory store.
	statStmt, err := tx.PrepareContext(ctx, `INSERT INTO channel_stats (record_time, channel_name, count) VALUES (?, ?, ?) ON CONFLICT(record_time, channel_name) DO UPDATE SET count = count + excluded.count`)
	if err != nil {
		return err
	}
	defer statStmt.Close()

	for ch, count := range channelCounts {
		if _, err := statStmt.ExecContext(ctx, now, ch, count); err != nil {
			return err
		}
	}

	// Record past-1h count as permanent historical usage (no pruning).
	var past1h int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT room_id) FROM rooms WHERE time > ?`, now-3600).Scan(&past1h); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO usage_stats (record_time, past1h) VALUES (?, ?)`, now, past1h); err != nil {
		return err
	}

	// Prune records beyond the retention window.
	cutoff := now - int64(d.keepDays)*86400
	if _, err := tx.ExecContext(ctx, `DELETE FROM rooms WHERE record_time < ?`, cutoff); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channel_stats WHERE record_time < ?`, cutoff); err != nil {
		return err
	}

	return tx.Commit()
}

// LoadLatestRooms retrieves rooms from the most recent snapshot that have not
// yet expired. Used on startup to warm the in-memory store without waiting for
// fresh UDS submissions.
func (d *DB) LoadLatestRooms(expireTime int) ([]*model.Room, error) {
	var maxRecord sql.NullInt64
	err := d.db.QueryRow(`SELECT MAX(record_time) FROM rooms`).Scan(&maxRecord)
	if err != nil || !maxRecord.Valid {
		return nil, err
	}

	now := time.Now().Unix()
	cutoff := now - int64(expireTime)

	rows, err := d.db.Query(
		`SELECT room_id, time, msg, name, source, info_json FROM rooms WHERE record_time = ? AND time >= ?`,
		maxRecord.Int64, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []*model.Room
	for rows.Next() {
		var r model.Room
		var infoJSON string
		if err := rows.Scan(&r.ID, &r.Time, &r.Msg, &r.Name, &r.Source, &infoJSON); err != nil {
			return nil, err
		}
		// Best-effort info parsing; fall back to raw JSON on failure.
		info, _, err := model.ParseInfo(json.RawMessage(infoJSON))
		if err != nil {
			r.Info = json.RawMessage(infoJSON)
		} else {
			r.Info = info
		}
		rooms = append(rooms, &r)
	}
	return rooms, rows.Err()
}

// PastCount returns room submission counts for the last 15 min, 1 h, and 24 h.
// The 1h and 24h values are cached for 60 seconds to limit SQLite reads.
// past15m is passed in from the caller (computed cheaply from in-memory store).
func (d *DB) PastCount(past15m int) (*model.PastCount, error) {
	// Return from cache if it is still fresh.
	d.cacheMu.RLock()
	if time.Now().Unix()-d.cacheTime < 60 {
		result := &model.PastCount{Past15m: past15m, Past1h: d.past1h, Past24h: d.past24h}
		d.cacheMu.RUnlock()
		return result, nil
	}
	d.cacheMu.RUnlock()

	now := time.Now().Unix()
	var past1h, past24h int

	if err := d.db.QueryRow(`SELECT COUNT(DISTINCT room_id) FROM rooms WHERE time > ?`, now-3600).Scan(&past1h); err != nil {
		return nil, err
	}
	if err := d.db.QueryRow(`SELECT COUNT(DISTINCT room_id) FROM rooms WHERE time > ?`, now-86400).Scan(&past24h); err != nil {
		return nil, err
	}

	d.cacheMu.Lock()
	d.past1h = past1h
	d.past24h = past24h
	d.cacheTime = now
	d.cacheMu.Unlock()

	return &model.PastCount{Past15m: past15m, Past1h: past1h, Past24h: past24h}, nil
}

// ChannelHealth returns per-channel submission counts bucketed into 60 one-minute
// slots covering the last hour. Tick[0] is the oldest minute.
func (d *DB) ChannelHealth() ([]model.ChannelHealth, error) {
	now := time.Now().Unix()
	cutoff := now - 3600

	rows, err := d.db.Query(
		`SELECT channel_name, record_time, count FROM channel_stats WHERE record_time >= ? ORDER BY channel_name, record_time`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type entry struct {
		name       string
		recordTime int64
		count      int
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.name, &e.recordTime, &e.count); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Map each entry into the appropriate minute slot of its channel's tick array.
	channelMap := make(map[string]*model.ChannelHealth)
	for _, e := range entries {
		h, ok := channelMap[e.name]
		if !ok {
			h = &model.ChannelHealth{Name: e.name, Tick: make([]int, 60)}
			channelMap[e.name] = h
		}
		minuteIdx := 59 - int((now-e.recordTime)/60)
		if minuteIdx >= 0 && minuteIdx < 60 {
			h.Tick[minuteIdx] += e.count
		}
	}

	result := make([]model.ChannelHealth, 0, len(channelMap))
	for _, h := range channelMap {
		result = append(result, *h)
	}
	return result, nil
}

// Close releases the underlying SQLite connection.
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// LoadStatistics restores the distinct-ID window across all recent snapshots,
// including rooms that have already expired from the display store.
func (d *DB) LoadStatistics() (map[string]int64, error) {
	rows, err := d.db.Query(`SELECT room_id, MAX(time) FROM rooms WHERE time >= ? GROUP BY room_id`, time.Now().Unix()-900)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	times := make(map[string]int64)
	for rows.Next() {
		var id string
		var timestamp int64
		if err := rows.Scan(&id, &timestamp); err != nil {
			return nil, err
		}
		times[id] = timestamp
	}
	return times, rows.Err()
}
