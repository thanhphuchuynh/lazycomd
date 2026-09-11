// Package logbuf holds a fixed-size, per-command ring buffer of process
// output, with optional live subscribers and an optional disk tee.
package logbuf

import (
	"bytes"
	"os"
	"strings"
	"sync"
)

// DefaultSize is the ring size used when New is given size <= 0.
const DefaultSize = 256 * 1024

// Buffer is a byte ring. Writes never block and never fail; the oldest bytes
// are dropped when it wraps.
type Buffer struct {
	mu   sync.Mutex
	buf  []byte
	w    int
	full bool

	subs map[int]chan []byte
	next int

	tee     *os.File
	teePath string
	teeN    int64
}

// New returns a Buffer holding the last size bytes.
func New(size int) *Buffer {
	if size <= 0 {
		size = DefaultSize
	}
	return &Buffer{buf: make([]byte, size), subs: make(map[int]chan []byte)}
}

// Write appends p, dropping the oldest bytes if the ring is full.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.appendLocked(p)
	b.fanoutLocked(p)
	b.teeLocked(p)
	b.mu.Unlock()
	return len(p), nil
}

func (b *Buffer) appendLocked(p []byte) {
	if len(p) >= len(b.buf) {
		copy(b.buf, p[len(p)-len(b.buf):])
		b.w = 0
		b.full = true
		return
	}
	c := copy(b.buf[b.w:], p)
	if c < len(p) {
		copy(b.buf, p[c:])
		b.w = len(p) - c
		b.full = true
		return
	}
	b.w += c
	if b.w == len(b.buf) {
		b.w = 0
		b.full = true
	}
}

// Bytes returns the buffer contents, oldest byte first.
func (b *Buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.full {
		out := make([]byte, b.w)
		copy(out, b.buf[:b.w])
		return out
	}
	out := make([]byte, 0, len(b.buf))
	out = append(out, b.buf[b.w:]...)
	return append(out, b.buf[:b.w]...)
}

// Tail returns the last n whole lines, or every line when n <= 0. A wrapped
// buffer's leading fragment is discarded so no caller ever sees half a line.
//
// ponytail: a wrap that lands exactly on a line boundary costs one whole
// line. Track a line-start offset if that ever matters.
func (b *Buffer) Tail(n int) []string {
	data := b.Bytes()

	b.mu.Lock()
	wrapped := b.full
	b.mu.Unlock()

	if wrapped {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	if len(data) == 0 {
		return []string{}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
