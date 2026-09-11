package tui

import (
	"bytes"
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
)

// sink delivers messages from a goroutine into the bubbletea loop. Run sets
// fn to the program's Send before any goroutine starts.
type sink struct{ fn func(tea.Msg) }

func (s *sink) send(msg tea.Msg) {
	if s == nil || s.fn == nil {
		return
	}
	s.fn(msg)
}

// streamHandle owns one SSE stream goroutine.
type streamHandle struct {
	name   string
	cancel context.CancelFunc
	done   chan struct{}
}

// stop cancels the stream and waits for its goroutine to exit, so no
// goroutine outlives the program.
//
// ponytail: this blocks the update loop until the HTTP request unwinds — a
// few milliseconds over a unix socket. Make it fire-and-forget if switching
// selection ever feels sticky.
func (h *streamHandle) stop() {
	if h == nil {
		return
	}
	h.cancel()
	<-h.done
}

// startStream opens the log stream for name, sending one logLineMsg per line.
func startStream(c *client.Client, s *sink, name string) *streamHandle {
	ctx, cancel := context.WithCancel(context.Background())
	h := &streamHandle{name: name, cancel: cancel, done: make(chan struct{})}

	go func() {
		defer close(h.done)
		err := c.Stream(ctx, name, &lineWriter{name: name, sink: s})
		if ctx.Err() != nil {
			return // we cancelled it; nobody needs to hear about that
		}
		s.send(streamEndedMsg{name: name, err: err})
	}()
	return h
}

// lineWriter turns a byte stream into one logLineMsg per complete line.
type lineWriter struct {
	name string
	sink *sink
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		w.sink.send(logLineMsg{name: w.name, line: line})
	}
	return len(p), nil
}
