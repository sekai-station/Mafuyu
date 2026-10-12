// Package presence tracks active browsers independently of their connections.
package presence

import (
	"regexp"
	"strings"
	"sync"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Tracker struct {
	mu          sync.Mutex
	clients     map[string]int
	connections int
}

func New() *Tracker {
	return &Tracker{clients: make(map[string]int)}
}

// Join counts every connection, but only valid UUIDs count as online browsers.
// The returned release function is safe to call more than once.
func (t *Tracker) Join(clientID string) func() {
	clientID = strings.ToLower(clientID)
	if !uuidV4.MatchString(clientID) {
		clientID = ""
	}
	t.mu.Lock()
	t.connections++
	if clientID != "" {
		t.clients[clientID]++
	}
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
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

func (t *Tracker) Counts() (online, connections int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.clients), t.connections
}
