package logbuf

import (
	"fmt"
	"testing"
	"time"
)

func TestSubscribeReceivesChunks(t *testing.T) {
	b := New(1024)
	ch, cancel := b.Subscribe()
	defer cancel()

	fmt.Fprint(b, "hello\n")
	select {
	case got := <-ch:
		if string(got) != "hello\n" {
			t.Fatalf("chunk = %q, want %q", got, "hello\n")
		}
	case <-time.After(time.Second):
		t.Fatal("no chunk delivered")
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	b := New(1024)
	ch, cancel := b.Subscribe()
	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("channel still open after cancel")
	}
	fmt.Fprint(b, "after\n") // must not panic on a closed subscriber
	cancel()                 // must be idempotent
}

func TestSlowSubscriberNeverBlocksWriter(t *testing.T) {
	b := New(4096)
	_, cancel := b.Subscribe() // never drained
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			fmt.Fprintf(b, "line %d\n", i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writer blocked on a slow subscriber")
	}
	if got := b.Tail(1); len(got) != 1 || got[0] != "line 499" {
		t.Fatalf("Tail(1) = %v, want [line 499]", got)
	}
}
