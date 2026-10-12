package presence

import (
	"strings"
	"sync"
	"testing"
)

const testID = "ce8bdafd-a8b7-49b0-80f3-ad945fbba189"

func TestDeduplicateAndRelease(t *testing.T) {
	tracker := New()
	first := tracker.Join(testID)
	second := tracker.Join(strings.ToUpper(testID))
	anonymous := tracker.Join("")
	invalid := tracker.Join("not-a-uuid")
	other := tracker.Join("5a53c21f-096a-4cf3-bb50-ade79efb2129")
	check := func(wantOnline, wantConnections int) {
		t.Helper()
		online, connections := tracker.Counts()
		if online != wantOnline || connections != wantConnections {
			t.Fatalf("counts = (%d, %d), want (%d, %d)", online, connections, wantOnline, wantConnections)
		}
	}
	check(2, 5)
	first()
	first() // duplicate cleanup must not unregister the second connection
	check(2, 4)
	second()
	check(1, 3)
	other()
	check(0, 2)
	anonymous()
	invalid()
	check(0, 0)
}

func TestConcurrentConnections(t *testing.T) {
	tracker := New()
	var ready, finished sync.WaitGroup
	ready.Add(100)
	finished.Add(100)
	disconnect := make(chan struct{})
	for range 100 {
		go func() {
			defer finished.Done()
			release := tracker.Join(testID)
			ready.Done()
			<-disconnect
			release()
			release()
		}()
	}
	ready.Wait()
	if online, connections := tracker.Counts(); online != 1 || connections != 100 {
		t.Fatalf("counts = (%d, %d)", online, connections)
	}
	close(disconnect)
	finished.Wait()
	if online, connections := tracker.Counts(); online != 0 || connections != 0 {
		t.Fatalf("connections leaked: (%d, %d)", online, connections)
	}
}
