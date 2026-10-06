// Package announce provides hot-reloadable announcement messages read from a
// directory of per-locale plain text files (e.g. announcements/en.txt,
// announcements/zh-hans.txt). The directory is watched with fsnotify; any
// write, create, remove or rename event triggers a reload with a 50ms debounce.
package announce

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"mafuyu/internal/utils/logger"
)

// entry holds a single locale's announcement text and its file mod time.
type entry struct {
	msg  string
	time int64 // unix seconds
}

// Announcement holds per-locale announcement messages loaded from a directory.
// All methods are safe for concurrent use.
type Announcement struct {
	mu      sync.RWMutex
	entries map[string]entry // locale code → entry
	dir     string
}

// New scans dir for *.txt files, treating each filename (without extension,
// lowercased) as a locale code. A missing or empty directory is not an error —
// the announcement set starts empty.
func New(dir string) (*Announcement, error) {
	a := &Announcement{
		dir:     dir,
		entries: make(map[string]entry),
	}
	if err := a.loadAll(); err != nil {
		if os.IsNotExist(err) {
			return a, nil
		}
		return nil, err
	}
	return a, nil
}

// Get returns an exact locale match, or empty when absent. Legacy callers
// without a locale use English.
func (a *Announcement) Get(lang string) (string, int64) {
	if lang == "" {
		lang = "en"
	}
	msg, timestamp, _ := a.Lookup(lang)
	return msg, timestamp
}

// Lookup distinguishes a missing locale from an existing empty announcement.
// It never falls back to another locale.
func (a *Announcement) Lookup(lang string) (string, int64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	lang = normLocale(lang)

	if e, ok := a.entries[lang]; ok {
		return e.msg, e.time, true
	}
	return "", 0, false
}

// loadAll reads every *.txt file in dir into entries.
func (a *Announcement) loadAll() error {
	files, err := os.ReadDir(a.dir)
	if err != nil {
		a.mu.Lock()
		a.entries = make(map[string]entry)
		a.mu.Unlock()
		return err
	}

	entries := make(map[string]entry, len(files))
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".txt") {
			continue
		}
		locale := normLocale(strings.TrimSuffix(f.Name(), ".txt"))
		path := filepath.Join(a.dir, f.Name())
		e, err := loadFile(path)
		if err != nil {
			logger.Error.Error("load announcement file", "path", path, "error", err)
			continue
		}
		entries[locale] = e
	}
	a.mu.Lock()
	a.entries = entries
	a.mu.Unlock()
	return nil
}

// loadFile reads a single file and returns an entry.
func loadFile(path string) (entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return entry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return entry{}, err
	}
	return entry{
		msg:  string(data),
		time: info.ModTime().Unix(),
	}, nil
}

// normLocale lowercases the locale string for consistent map lookups.
func normLocale(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Watch starts a fsnotify watcher on the announcement directory and reloads
// all files on Write, Create, Remove or Rename events. A periodic rescan restores
// watches after directory creation/replacement and covers missed filesystem events.
// It blocks until ctx is cancelled.
func (a *Announcement) Watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	var watched os.FileInfo
	reload := func() {
		info, err := os.Stat(a.dir)
		if err != nil || !info.IsDir() || (watched != nil && !os.SameFile(watched, info)) {
			if watched != nil {
				_ = watcher.Remove(a.dir)
				watched = nil
			}
		}
		if err == nil && info.IsDir() && watched == nil {
			if err := watcher.Add(a.dir); err != nil {
				logger.Error.Error("watch announcements", "error", err)
			} else {
				watched = info
			}
		}
		if err := a.loadAll(); err != nil && !os.IsNotExist(err) {
			logger.Error.Error("reload announcements", "error", err)
		}
	}
	reload()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				timer := time.NewTimer(50 * time.Millisecond)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return nil
				}
				reload()
			}
		case <-ticker.C:
			reload()
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			logger.Error.Error("fsnotify error", "error", err)
		case <-ctx.Done():
			return nil
		}
	}
}
