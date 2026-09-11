package logbuf

import (
	"os"
	"path/filepath"
	"strings"
)

// RotateAt is the disk log size that triggers rotation. A var, not a const,
// so tests can lower it.
var RotateAt int64 = 10 << 20

// FileName maps a command name to its log file name. The ":" in a namespaced
// name becomes "__" so the path stays boring.
func FileName(command string) string {
	return strings.ReplaceAll(command, ":", "__") + ".log"
}

// AttachFile tees every subsequent write to path as well as the ring.
func (b *Buffer) AttachFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}

	b.mu.Lock()
	if b.tee != nil {
		b.tee.Close()
	}
	b.tee, b.teePath, b.teeN = f, path, fi.Size()
	b.mu.Unlock()
	return nil
}

// Close closes the disk tee, if any. The ring stays readable.
func (b *Buffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tee == nil {
		return nil
	}
	err := b.tee.Close()
	b.tee = nil
	return err
}

func (b *Buffer) teeLocked(p []byte) {
	if b.tee == nil {
		return
	}
	n, err := b.tee.Write(p)
	b.teeN += int64(n)
	if err != nil || b.teeN < RotateAt {
		return
	}
	b.rotateLocked()
}

// rotateLocked keeps one generation: proxy.log becomes proxy.log.1.
func (b *Buffer) rotateLocked() {
	b.tee.Close()
	b.tee = nil
	if err := os.Rename(b.teePath, b.teePath+".1"); err != nil {
		return
	}
	f, err := os.OpenFile(b.teePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	b.tee, b.teeN = f, 0
}
