package v2

import (
	"strings"
	"testing"

	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

func TestBroadcastDisconnectsClientWithFullQueue(t *testing.T) {
	if err := logger.Init("error", "", ""); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(nil, false)
	slow := b.Subscribe(false) // never reads
	fast := b.Subscribe(false)
	defer b.Unsubscribe(slow)
	defer b.Unsubscribe(fast)

	for i := 0; i < clientQueueSize; i++ {
		b.broadcast([]byte("event"))
		<-fast.Ch
	}
	select {
	case <-slow.Kicked():
		t.Fatal("client kicked before its queue was full")
	default:
	}

	b.broadcast([]byte("event"))
	<-fast.Ch
	select {
	case <-slow.Kicked():
	default:
		t.Fatal("client with a full queue was not kicked")
	}
	select {
	case <-fast.Kicked():
		t.Fatal("client that keeps up was kicked")
	default:
	}

	// Further events for an already kicked client must not panic.
	b.broadcast([]byte("event"))
}

func TestBroadcastRoomInfoExtraOnlyForClientsThatAsked(t *testing.T) {
	if err := logger.Init("error", "", ""); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(nil, false)
	plain := b.Subscribe(false)
	full := b.Subscribe(true)
	defer b.Unsubscribe(plain)
	defer b.Unsubscribe(full)

	b.BroadcastRoom(&model.Room{ID: "12345", Source: "x", Info: model.XInfo{TID: "7", UserName: "@u"}})
	if got := string(<-plain.Ch); !strings.HasPrefix(got, "event: room\n") || strings.Contains(got, "extra") {
		t.Fatalf("plain client got %q", got)
	}
	if got := string(<-full.Ch); !strings.Contains(got, `"extra":{"type":"XInfo"`) {
		t.Fatalf("extra client got %q", got)
	}
}

func TestBroadcastRoomSkills(t *testing.T) {
	if err := logger.Init("error", "", ""); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(nil, false)
	plain, full := b.Subscribe(false), b.Subscribe(true)
	defer b.Unsubscribe(plain)
	defer b.Unsubscribe(full)
	b.BroadcastRoomSkills(&model.RoomSkills{ID: "12345", Time: 1777083784, Data: []int{100, 200}})
	want := "event: roomSkills\ndata: {\"id\":\"12345\",\"time\":1777083784,\"data\":[100,200]}\n\n"
	for _, c := range []*Client{plain, full} {
		select {
		case data := <-c.Ch:
			if string(data) != want {
				t.Fatalf("unexpected skills event: %q", data)
			}
		default:
			t.Fatal("skills event was not broadcast")
		}
	}
}
