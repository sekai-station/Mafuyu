package announce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mafuyu/internal/utils/logger"
)

func TestExactLocaleLookup(t *testing.T) {
	dir := t.TempDir()
	for locale, msg := range map[string]string{"en": "English", "zh-hans": "中文", "ja": ""} {
		if err := os.WriteFile(filepath.Join(dir, locale+".txt"), []byte(msg), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		locale string
		msg    string
		found  bool
	}{{"en", "English", true}, {" ZH-Hans ", "中文", true}, {"ja", "", true}, {"fr", "", false}} {
		msg, timestamp, found := a.Lookup(tc.locale)
		if msg != tc.msg || found != tc.found || (found && timestamp == 0) {
			t.Fatalf("Lookup(%q) = %q, %d, %v", tc.locale, msg, timestamp, found)
		}
	}
	if err := os.Remove(filepath.Join(dir, "zh-hans.txt")); err != nil {
		t.Fatal(err)
	}
	if err := a.loadAll(); err != nil {
		t.Fatal(err)
	}
	if _, _, found := a.Lookup("zh-Hans"); found {
		t.Fatal("deleted announcement retained after reload")
	}
}

func TestMissingDirectory(t *testing.T) {
	a, err := New(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found := a.Lookup("en"); found {
		t.Fatal("missing directory has an announcement")
	}
}

func TestWatchRemovesDeletedAnnouncement(t *testing.T) {
	logger.Init("error", "", "")
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Watch(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	path := filepath.Join(dir, "en.txt")
	deadline := time.Now().Add(5 * time.Second)
	// Retry writes until a reload confirms that the watcher has registered.
	for {
		if err := os.WriteFile(path, []byte("English"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, found := a.Lookup("en"); found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher did not load the created announcement")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for {
		if _, _, found := a.Lookup("en"); !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher retained deleted announcement")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWatchRecoversMissingAndRecreatedDirectory(t *testing.T) {
	logger.Init("error", "", "")
	dir := filepath.Join(t.TempDir(), "announcements")
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Watch(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("watcher did not stop")
		}
	}()
	waitFor := func(want string, exists bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			msg, _, found := a.Lookup("en")
			if found == exists && msg == want {
				return
			}
			select {
			case err := <-done:
				t.Fatalf("watcher exited: %v", err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("wanted %q/%v, got %q/%v", want, exists, msg, found)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	write := func(msg string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "en.txt"), []byte(msg), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	waitFor("first", true)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	waitFor("", false)
	write("replacement")
	waitFor("replacement", true)
	if err := os.WriteFile(filepath.Join(dir, "en.txt"), []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	waitFor("updated", true)
}
