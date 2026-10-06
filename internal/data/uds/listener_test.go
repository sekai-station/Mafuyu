package uds

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"mafuyu/internal/data/store"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

func TestRoomSkillsMessage(t *testing.T) {
	if err := logger.Init("error", "", ""); err != nil {
		t.Fatal(err)
	}
	var received []model.RoomSkills
	h := NewHandler(store.New(0, 500), nil, func(skills *model.RoomSkills) {
		received = append(received, *skills)
	})
	h.handleMessage([]byte(`{"type":"roomSkills","channel":"collector","data":[{"id":"12345","time":1777083784,"data":[100,200]}]}`))
	if len(received) != 1 || received[0].ID != "12345" || received[0].Time != 1777083784 || len(received[0].Data) != 2 || received[0].Data[0] != 100 || received[0].Data[1] != 200 {
		t.Fatalf("unexpected skills received: %+v", received)
	}
}

func TestListenPreservesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.sock")
	if err := os.WriteFile(path, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	if l, err := listenUnix(context.Background(), path); err == nil {
		l.Close()
		t.Fatal("bound over an existing file")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep me" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
}

func TestListenPreservesActiveSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket filesystem identity requires Unix")
	}
	path := filepath.Join(t.TempDir(), "collector.sock")
	first, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := listenUnix(context.Background(), path); err == nil {
		second.Close()
		t.Fatal("replaced an active socket")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("active socket was removed or replaced")
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("original listener no longer reachable: %v", err)
	}
	conn.Close()
}

func TestListenReplacesStaleSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket filesystem identity requires Unix")
	}
	path := filepath.Join(t.TempDir(), "collector.sock")
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	l, err := listenUnix(context.Background(), path)
	if err != nil {
		t.Fatalf("could not replace stale socket: %v", err)
	}
	defer l.Close()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}
