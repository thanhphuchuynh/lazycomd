package logbuf

import (
	"fmt"
	"slices"
	"testing"
)

func TestTailReturnsLines(t *testing.T) {
	b := New(1024)
	fmt.Fprint(b, "one\ntwo\nthree\n")
	if got, want := b.Tail(0), []string{"one", "two", "three"}; !slices.Equal(got, want) {
		t.Fatalf("Tail(0) = %v, want %v", got, want)
	}
	if got, want := b.Tail(2), []string{"two", "three"}; !slices.Equal(got, want) {
		t.Fatalf("Tail(2) = %v, want %v", got, want)
	}
}

func TestTailEmptyBuffer(t *testing.T) {
	if got := New(64).Tail(0); len(got) != 0 {
		t.Fatalf("Tail(0) = %v, want empty", got)
	}
}

func TestTailUnterminatedLastLine(t *testing.T) {
	b := New(64)
	fmt.Fprint(b, "a\nb")
	if got, want := b.Tail(0), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("Tail(0) = %v, want %v", got, want)
	}
}

func TestWrappedBufferDropsPartialLine(t *testing.T) {
	b := New(32) // holds ~6 of the 5-byte lines below
	for i := 0; i < 100; i++ {
		fmt.Fprintf(b, "%04d\n", i)
	}
	lines := b.Tail(0)
	if len(lines) == 0 {
		t.Fatal("Tail(0) = empty, want some lines")
	}
	for _, l := range lines {
		if len(l) != 4 {
			t.Fatalf("partial line %q in %v", l, lines)
		}
	}
	if last := lines[len(lines)-1]; last != "0099" {
		t.Fatalf("last line = %q, want 0099", last)
	}
}

func TestWriteLargerThanBufferKeepsTail(t *testing.T) {
	b := New(8)
	n, err := b.Write([]byte("0123456789abc\n"))
	if err != nil || n != 14 {
		t.Fatalf("Write = %d, %v, want 14, nil", n, err)
	}
	if got, want := string(b.Bytes()), "89abc\n"; len(got) != 8 || got[len(got)-len(want):] != want {
		t.Fatalf("Bytes() = %q, want 8 bytes ending %q", got, want)
	}
}
