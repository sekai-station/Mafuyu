package store

import (
	"sync"
	"testing"
	"time"

	"mafuyu/internal/utils/model"
)

func makeRoom(id, msg string, t int64) *model.Room {
	return &model.Room{
		Time:   t,
		ID:     id,
		Msg:    msg,
		Name:   "test",
		Source: "x",
		Info:   model.XInfo{TID: "t1", UserName: "@u", ScreenName: "sn", Avatar: ""},
	}
}

func TestAdd_NewRoom(t *testing.T) {
	s := New(10, 500)
	now := time.Now().Unix()
	room := makeRoom("12345", "hello", now)
	if !s.Add(room, "ch1") {
		t.Fatal("expected room to be added")
	}
	snap := s.Snapshot()
	if len(snap) != 1 || snap[0].ID != "12345" {
		t.Fatalf("expected 1 room with id 12345, got %d", len(snap))
	}
}

func TestAdd_DuplicateIdMsg(t *testing.T) {
	s := New(10, 500)
	now := time.Now().Unix()
	s.Add(makeRoom("12345", "hello", now), "ch1")
	if s.Add(makeRoom("12345", "hello", now+1), "ch1") {
		t.Fatal("expected duplicate (id,msg) to be rejected")
	}
}

func TestAdd_SameIdWithinUniqueTime(t *testing.T) {
	s := New(10, 500)
	now := time.Now().Unix()
	s.Add(makeRoom("12345", "hello", now), "ch1")
	if s.Add(makeRoom("12345", "world", now+1), "ch1") {
		t.Fatal("expected same id within unique_time to be rejected")
	}
}

func TestAdd_SameIdAfterUniqueTime(t *testing.T) {
	s := New(1, 500)
	now := time.Now().Unix()
	s.Add(makeRoom("12345", "hello", now), "ch1")
	time.Sleep(1100 * time.Millisecond)
	if !s.Add(makeRoom("12345", "world", now+2), "ch1") {
		t.Fatal("expected same id after unique_time to be accepted")
	}
	snap := s.Snapshot()
	if len(snap) != 1 || snap[0].Msg != "world" {
		t.Fatal("expected old room to be replaced")
	}
}

func TestExpire(t *testing.T) {
	s := New(10, 5)
	now := time.Now().Unix()
	s.Add(makeRoom("old", "msg", now-10), "ch1")
	s.Add(makeRoom("new", "msg2", now), "ch1")
	s.Expire(now)
	snap := s.Snapshot()
	if len(snap) != 1 || snap[0].ID != "new" {
		t.Fatalf("expected only new room, got %d rooms", len(snap))
	}
}

func TestFlushChannelCounts(t *testing.T) {
	s := New(10, 500)
	now := time.Now().Unix()
	s.Add(makeRoom("1", "a", now), "ch1")
	s.Add(makeRoom("2", "b", now), "ch1")
	s.Add(makeRoom("3", "c", now), "ch2")

	counts := s.FlushChannelCounts()
	if counts["ch1"] != 2 || counts["ch2"] != 1 {
		t.Fatalf("unexpected counts: %v", counts)
	}
	counts2 := s.FlushChannelCounts()
	if len(counts2) != 0 {
		t.Fatal("expected empty counts after flush")
	}
}

func TestRecent(t *testing.T) {
	s := New(10, 500)
	now := time.Now().Unix()
	s.Add(makeRoom("old", "msg", now-400), "ch1")
	s.Add(makeRoom("new", "msg2", now), "ch1")
	recent := s.Recent(300)
	if len(recent) != 1 || recent[0].ID != "new" {
		t.Fatalf("expected 1 recent room, got %d", len(recent))
	}
}

func TestPast15mCount(t *testing.T) {
	s := New(10, 2000)
	now := time.Now().Unix()
	s.Add(makeRoom("old", "msg", now-1000), "ch1")
	s.Add(makeRoom("new", "msg2", now), "ch1")
	if c := s.Past15mCount(); c != 1 {
		t.Fatalf("expected past15m count 1, got %d", c)
	}
}

func TestLoadFromDB(t *testing.T) {
	s := New(10, 500)
	rooms := []*model.Room{
		makeRoom("1", "a", time.Now().Unix()),
		makeRoom("2", "b", time.Now().Unix()),
	}
	s.LoadFromDB(rooms)
	if s.Count() != 2 {
		t.Fatalf("expected 2 rooms after load, got %d", s.Count())
	}
}

func TestConcurrency(t *testing.T) {
	s := New(10, 500)
	now := time.Now().Unix()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('A'+i%26)) + string(rune('0'+i/26))
			s.Add(makeRoom(id, "msg", now), "ch1")
			s.Snapshot()
			s.Expire(now)
		}(i)
	}
	wg.Wait()
}

func TestStatisticsSurviveDisplayExpiryAndReplacement(t *testing.T) {
	s := New(0, 500)
	now := time.Now().Unix()
	s.Add(makeRoom("12345", "old", now-600), "ch")
	s.Expire(now)
	if s.Count() != 0 || s.Past15mCount() != 1 {
		t.Fatal("display expiry discarded the 15-minute statistic")
	}
	s.Add(makeRoom("12345", "new", now), "ch")
	if s.Past15mCount() != 1 {
		t.Fatal("same ID must count only once")
	}
	s.Expire(now + 901)
	if s.Past15mCount() != 0 {
		t.Fatal("statistics were not pruned")
	}
}

func TestFailedFlushMergesConcurrentSubmissions(t *testing.T) {
	s := New(0, 500)
	now := time.Now().Unix()
	s.Add(makeRoom("12345", "one", now), "ch")
	failed := s.FlushChannelCounts()
	s.Add(makeRoom("23456", "two", now), "ch")
	s.RestoreChannelCounts(failed)
	if s.FlushChannelCounts()["ch"] != 2 {
		t.Fatal("failed batch lost counts submitted after the flush")
	}
}
