package configw

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFixture(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteAtomicReplacesContentAndKeepsMode(t *testing.T) {
	p := writeFixture(t, "old\n", 0o640)
	snap, err := read(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeAtomic(p, []byte("new\n"), snap); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Fatalf("file = %q, want new", got)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640 preserved", fi.Mode().Perm())
	}
}

func TestWriteAtomicRefusesAChangedFile(t *testing.T) {
	p := writeFixture(t, "old\n", 0o600)
	snap, err := read(p)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(p, []byte("theirs, longer\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = writeAtomic(p, []byte("ours\n"), snap)
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
	if !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("err = %v, want it to name the file", err)
	}

	got, _ := os.ReadFile(p)
	if string(got) != "theirs, longer\n" {
		t.Fatalf("their write was clobbered: %q", got)
	}
}

func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	p := writeFixture(t, "old\n", 0o600)
	snap, _ := read(p)
	if err := writeAtomic(p, []byte("new\n"), snap); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only config.yaml", names)
	}
}

func TestReadCapturesTheFile(t *testing.T) {
	p := writeFixture(t, "hello\n", 0o600)
	snap, err := read(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(snap.data) != "hello\n" {
		t.Fatalf("data = %q", snap.data)
	}
	if snap.size != 6 || snap.mode != 0o600 || snap.mod.IsZero() {
		t.Fatalf("snapshot = %+v", snap)
	}

	if _, err := read(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("read of a missing file returned no error")
	}
}
