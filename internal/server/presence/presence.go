// Package presence tracks active browsers independently of their connections.
package presence

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Tracker struct {
	mu          sync.Mutex
	clients     map[string]int
	connections int
	peaks       map[int64]int
	now         func() time.Time
}

func New() *Tracker {
	return &Tracker{clients: make(map[string]int), peaks: make(map[int64]int), now: time.Now}
}

// Join counts every connection, but only valid UUIDs count as online browsers.
// The returned release function is safe to call more than once.
func (t *Tracker) Join(clientID string) func() {
	clientID = strings.ToLower(clientID)
	if !uuidV4.MatchString(clientID) {
		clientID = ""
	}
	t.mu.Lock()
	t.recordPeak(t.now())
	t.connections++
	if clientID != "" {
		t.clients[clientID]++
	}
	t.recordPeak(t.now())
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.recordPeak(t.now())
			t.connections--
			if clientID != "" {
				t.clients[clientID]--
				if t.clients[clientID] == 0 {
					delete(t.clients, clientID)
				}
			}
		})
	}
}

func (t *Tracker) recordPeak(now time.Time) {
	hour := now.Unix() / 3600 * 3600
	online := len(t.clients)
	if peak, exists := t.peaks[hour]; !exists || online > peak {
		t.peaks[hour] = online
	}
	for h := range t.peaks {
		if h < hour-3600 {
			delete(t.peaks, h)
		}
	}
}

// Snapshot includes peaks between samples, even after those browsers disconnect.
func (t *Tracker) Snapshot() (time.Time, int, map[int64]int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.recordPeak(now)
	peaks := make(map[int64]int, len(t.peaks))
	for hour, peak := range t.peaks {
		peaks[hour] = peak
	}
	return now, len(t.clients), peaks
}

func (t *Tracker) Counts() (online, connections int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.clients), t.connections
}
