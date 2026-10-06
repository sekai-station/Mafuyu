package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mafuyu/internal/utils/model"
)

func TestDatabaseFailuresAreReturned(t *testing.T) {
	d := tempDB(t)
	d.Close()
	if _, err := d.PastCount(0); err == nil {
		t.Fatal("query failure reported as zero counts")
	}
}

func TestSnapshotRollsBackWhenPruningFails(t *testing.T) {
	d := tempDB(t)
	_, err := d.db.Exec(`CREATE TRIGGER fail_prune BEFORE DELETE ON rooms BEGIN SELECT RAISE(ABORT, 'prune failed'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.db.Exec(`INSERT INTO rooms(record_time, room_id, time, msg) VALUES (0, 'old', 0, '')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SaveSnapshot(nil, map[string]int{"ch": 2}); err == nil {
		t.Fatal("pruning error ignored")
	}
	var count int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM channel_stats`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed transaction committed channel counts")
	}
}

func TestCancelledSnapshotAndStatisticsRestore(t *testing.T) {
	d := tempDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.SaveSnapshotContext(ctx, nil, nil); err == nil {
		t.Fatal("cancelled snapshot succeeded")
	}
	room := &model.Room{ID: "12345", Time: time.Now().Unix() - 600, Info: model.XInfo{}}
	if err := d.SaveSnapshot([]*model.Room{room}, nil); err != nil {
		t.Fatal(err)
	}
	times, err := d.LoadStatistics()
	if err != nil || times[room.ID] != room.Time {
		t.Fatalf("lost expired display room in statistics: %v %v", times, err)
	}
}

func TestChannelHealthIncludesCurrentMinute(t *testing.T) {
	d := tempDB(t)
	if err := d.SaveSnapshot(nil, map[string]int{"ch": 3}); err != nil {
		t.Fatal(err)
	}
	health, err := d.ChannelHealth()
	if err != nil {
		t.Fatal(err)
	}
	if len(health) != 1 || health[0].Tick[59] != 3 {
		t.Fatalf("latest minute missing: %v", health)
	}
}

func tempDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path, 14)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenCreatesTables(t *testing.T) {
	d := tempDB(t)
	var name string
	err := d.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='rooms'`).Scan(&name)
	if err != nil || name != "rooms" {
		t.Fatal("rooms table not created")
	}
	err = d.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='channel_stats'`).Scan(&name)
	if err != nil || name != "channel_stats" {
		t.Fatal("channel_stats table not created")
	}
}

func TestSaveAndLoad(t *testing.T) {
	d := tempDB(t)
	now := time.Now().Unix()
	group := int64(3333333)
	rooms := []*model.Room{
		{Time: now, ID: "12345", Msg: "hello", Name: "user", Source: "x", Info: model.XInfo{TID: "t1", UserName: "@u", ScreenName: "sn", Avatar: ""}},
		{Time: now - 100, ID: "67890", Msg: "world", Name: "user2", Source: "qq", Info: model.QQInfo{Group: &group, Nickname: "nick", Avatar: "av"}},
	}
	counts := map[string]int{"ch1": 5, "ch2": 3}

	if err := d.SaveSnapshot(rooms, counts); err != nil {
		t.Fatal(err)
	}

	loaded, err := d.LoadLatestRooms(500)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 rooms, got %d", len(loaded))
	}
	for _, room := range loaded {
		if room.ID == "67890" {
			info, ok := room.Info.(model.QQInfo)
			if !ok || info.Group == nil || *info.Group != group {
				t.Fatalf("QQ group lost after database restore: %+v", room.Info)
			}
		}
	}
}

func TestLoadFiltersExpired(t *testing.T) {
	d := tempDB(t)
	now := time.Now().Unix()
	rooms := []*model.Room{
		{Time: now, ID: "new", Msg: "a", Name: "u", Source: "x", Info: model.XInfo{}},
		{Time: now - 1000, ID: "old", Msg: "b", Name: "u", Source: "x", Info: model.XInfo{}},
	}
	d.SaveSnapshot(rooms, nil)

	loaded, _ := d.LoadLatestRooms(500)
	if len(loaded) != 1 || loaded[0].ID != "new" {
		t.Fatalf("expected only new room, got %v", loaded)
	}
}

func TestPastCount(t *testing.T) {
	d := tempDB(t)
	now := time.Now().Unix()

	rooms1 := []*model.Room{
		{Time: now - 600, ID: "r1", Msg: "a", Name: "u", Source: "x", Info: model.XInfo{}},
		{Time: now - 1800, ID: "r2", Msg: "b", Name: "u", Source: "x", Info: model.XInfo{}},
	}
	d.SaveSnapshot(rooms1, nil)

	pc, err := d.PastCount(5)
	if err != nil {
		t.Fatal(err)
	}
	if pc.Past15m != 5 {
		t.Fatalf("expected past15m=5, got %d", pc.Past15m)
	}
	if pc.Past1h < 1 {
		t.Fatalf("expected past1h >= 1, got %d", pc.Past1h)
	}
}

func TestCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, _ := Open(path, 14)
	defer d.Close()

	now := time.Now().Unix()
	d.db.Exec(`INSERT INTO rooms (record_time, room_id, time, msg, name, source, info_json) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		now-15*86400, "r1", now-15*86400, "a", "u", "x", "{}")
	d.db.Exec(`INSERT INTO channel_stats (record_time, channel_name, count) VALUES (?, ?, ?)`,
		now-15*86400, "ch1", 1)

	d.SaveSnapshot([]*model.Room{
		{Time: now, ID: "r2", Msg: "b", Name: "u", Source: "x", Info: model.XInfo{}},
	}, nil)

	var count int
	d.db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE room_id = 'r1'`).Scan(&count)
	if count != 0 {
		t.Fatalf("expected old room to be cleaned up, got %d", count)
	}
	d.db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE room_id = 'r2'`).Scan(&count)
	if count != 1 {
		t.Fatalf("expected new room to remain, got %d", count)
	}
}

func TestChannelHealth(t *testing.T) {
	d := tempDB(t)
	now := time.Now().Unix()

	for i := 0; i < 3; i++ {
		recordTime := now - int64(i)*60
		d.db.Exec(`INSERT INTO channel_stats (record_time, channel_name, count) VALUES (?, ?, ?)`,
			recordTime, "ch1", 10+i)
	}

	health, err := d.ChannelHealth()
	if err != nil {
		t.Fatal(err)
	}
	if len(health) != 1 || health[0].Name != "ch1" {
		t.Fatalf("expected 1 channel, got %v", health)
	}
	if len(health[0].Tick) != 60 {
		t.Fatalf("expected 60 ticks, got %d", len(health[0].Tick))
	}

	_ = os.Remove("")
}
