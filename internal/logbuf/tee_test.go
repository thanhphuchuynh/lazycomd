package logbuf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileName(t *testing.T) {
	if got, want := FileName("scraper:api"), "scraper__api.log"; got != want {
		t.Fatalf("FileName = %q, want %q", got, want)
	}
	if got, want := FileName("proxy"), "proxy.log"; got != want {
		t.Fatalf("FileName = %q, want %q", got, want)
	}
}

func TestAttachFileWritesThrough(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", "proxy.log")
	b := New(1024)
	if err := b.AttachFile(p); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(b, "hello\n")
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("file = %q, want %q", got, "hello\n")
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestAttachFileAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), "proxy.log")
	if err := os.WriteFile(p, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := New(1024)
	if err := b.AttachFile(p); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(b, "new\n")
	b.Close()

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\nnew\n" {
		t.Fatalf("file = %q, want %q", got, "old\nnew\n")
	}
}

func TestRotation(t *testing.T) {
	old := RotateAt
	RotateAt = 16
	defer func() { RotateAt = old }()

	dir := t.TempDir()
	p := filepath.Join(dir, "proxy.log")
	b := New(1024)
	if err := b.AttachFile(p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		fmt.Fprintf(b, "%04d\n", i) // 5 bytes each
	}
	b.Close()

	rotated, err := os.ReadFile(p + ".1")
	if err != nil {
		t.Fatalf("no rotated file: %v", err)
	}
	// One generation only: .1 holds the most recent rotated batch, and every
	// batch before it is gone.
	if !strings.HasSuffix(string(rotated), "0007\n") {
		t.Fatalf("rotated file = %q, want it to end with the last rotated line", rotated)
	}
	if strings.Contains(string(rotated), "0000\n") {
		t.Fatalf("rotated file = %q, want older batches discarded", rotated)
	}
	current, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(current)) > RotateAt {
		t.Fatalf("current file is %d bytes, want <= %d", len(current), RotateAt)
	}
	if !strings.Contains(string(current), "0009\n") {
		t.Fatalf("current file = %q, want the newest line", current)
	}
}
