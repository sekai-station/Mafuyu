// Package store implements the in-memory room store with deduplication,
// expiry, and per-channel submission counting.
package store

import (
	"sort"
	"sync"
	"time"

	"mafuyu/internal/utils/model"
)

// Store holds all active rooms and the metadata needed for deduplication.
// All exported methods are safe for concurrent use.
type Store struct {
	mu            sync.RWMutex
	rooms         map[string]*model.Room // keyed by room ID
	idMsgSet      map[[2]string]struct{} // (id, msg) exact-duplicate guard
	idLastSeen    map[string]int64       // unix seconds when an ID was last accepted
	statTimes     map[string]int64       // latest event time per ID, retained for 15m independently
	channelCounts map[string]int         // submission count per channel since last flush
	uniqueTime    int                    // minimum seconds between re-accepting the same ID
	expireTime    int                    // seconds after room.Time before expiry
}

// New creates an empty Store. uniqueTime and expireTime are in seconds.
func New(uniqueTime, expireTime int) *Store {
	return &Store{
		rooms:         make(map[string]*model.Room),
		idMsgSet:      make(map[[2]string]struct{}),
		idLastSeen:    make(map[string]int64),
		statTimes:     make(map[string]int64),
		channelCounts: make(map[string]int),
		uniqueTime:    uniqueTime,
		expireTime:    expireTime,
	}
}

// Add attempts to insert room into the store under the given channel.
// It returns false (duplicate) when:
//   - the exact (id, msg) pair was already accepted, or
//   - the same ID was accepted within the last uniqueTime seconds.
//
// If the same ID exists but uniqueTime has elapsed, the old entry is replaced
// and its idMsgSet entry is removed before adding the new one.
func (s *Store) Add(room *model.Room, channel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	key := [2]string{room.ID, room.Msg}

	// Reject exact (id, msg) duplicates regardless of time.
	if _, exists := s.idMsgSet[key]; exists {
		return false
	}

	// Reject same ID seen too recently (different msg counts as spam).
	if lastSeen, ok := s.idLastSeen[room.ID]; ok {
		if now-lastSeen < int64(s.uniqueTime) {
			return false
		}
	}

	// Replace an older entry for the same ID, cleaning up its idMsgSet key.
	if old, exists := s.rooms[room.ID]; exists {
		oldKey := [2]string{old.ID, old.Msg}
		delete(s.idMsgSet, oldKey)
	}

	s.rooms[room.ID] = room
	s.idMsgSet[key] = struct{}{}
	s.idLastSeen[room.ID] = now
	if room.Time > s.statTimes[room.ID] {
		s.statTimes[room.ID] = room.Time
	}
	s.channelCounts[channel]++
	return true
}

// Expire removes all rooms whose Time is older than (now - expireTime).
// It also cleans up idMsgSet and idLastSeen for each removed room.
func (s *Store) Expire(now int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := now - int64(s.expireTime)
	for id, timestamp := range s.statTimes {
		if timestamp < now-900 {
			delete(s.statTimes, id)
		}
	}
	for id, room := range s.rooms {
		if room.Time < cutoff {
			key := [2]string{room.ID, room.Msg}
			delete(s.idMsgSet, key)
			delete(s.idLastSeen, id)
			delete(s.rooms, id)
		}
	}
}

// Snapshot returns all rooms sorted by ascending Time. Used for SQLite persistence
// and for sending the full room list to new v1 WebSocket clients.
func (s *Store) Snapshot() []*model.Room {
	s.mu.RLock()

	result := make([]*model.Room, 0, len(s.rooms))
	for _, room := range s.rooms {
		result = append(result, room)
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		return result[i].Time < result[j].Time
	})
	return result
}

// Recent returns rooms whose Time is within the last `seconds` seconds,
// sorted by ascending Time.
func (s *Store) Recent(seconds int) []*model.Room {
	s.mu.RLock()

	cutoff := time.Now().Unix() - int64(seconds)
	result := make([]*model.Room, 0)
	for _, room := range s.rooms {
		if room.Time >= cutoff {
			result = append(result, room)
		}
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		return result[i].Time < result[j].Time
	})
	return result
}

// Count returns the total number of rooms currently in the store.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rooms)
}

// Past15mCount returns the number of rooms submitted in the last 15 minutes.
// The result is computed from in-memory data and is always current.
func (s *Store) Past15mCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cutoff := time.Now().Unix() - 900
	count := 0
	for _, timestamp := range s.statTimes {
		if timestamp >= cutoff {
			count++
		}
	}
	return count
}

// FlushChannelCounts atomically returns the accumulated per-channel submission
// counts and resets the internal map to zero. Called before each SQLite snapshot.
func (s *Store) FlushChannelCounts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.channelCounts
	s.channelCounts = make(map[string]int)
	return result
}

// RestoreChannelCounts merges a failed batch with submissions received since
// the flush, so transient persistence failures do not discard activity.
func (s *Store) RestoreChannelCounts(counts map[string]int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for channel, count := range counts {
		s.channelCounts[channel] += count
	}
}

func (s *Store) LoadStatistics(times map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, timestamp := range times {
		if timestamp > s.statTimes[id] {
			s.statTimes[id] = timestamp
		}
	}
}

// LoadFromDB pre-populates the store from a list of rooms loaded from SQLite on
// startup. It skips rooms whose (id, msg) key is already present and sets
// idLastSeen to the room's own Time rather than the current wall clock.
func (s *Store) LoadFromDB(rooms []*model.Room) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, room := range rooms {
		key := [2]string{room.ID, room.Msg}
		if _, exists := s.idMsgSet[key]; exists {
			continue
		}
		s.rooms[room.ID] = room
		s.idMsgSet[key] = struct{}{}
		s.idLastSeen[room.ID] = room.Time
		if room.Time > s.statTimes[room.ID] {
			s.statTimes[room.ID] = room.Time
		}
	}
}
