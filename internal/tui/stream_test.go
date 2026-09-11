package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

// captureSink collects everything sent to it.
func captureSink() (*sink, chan tea.Msg) {
	ch := make(chan tea.Msg, 256)
	return &sink{fn: func(m tea.Msg) {
		select {
		case ch <- m:
		default: // a test that stops draining must not wedge a goroutine
		}
	}}, ch
}

// nextMsg waits for the next message of any type.
func nextMsg(t *testing.T, ch chan tea.Msg, within time.Duration) tea.Msg {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(within):
		t.Fatal("timed out waiting for a message")
		return nil
	}
}

func TestLineWriterSplitsAcrossWrites(t *testing.T) {
	s, ch := captureSink()
	w := &lineWriter{name: "tick", sink: s}

	for _, chunk := range []string{"hel", "lo\nwor", "ld\n"} {
		n, err := w.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}

	for _, want := range []string{"hello", "world"} {
		msg := nextMsg(t, ch, time.Second).(logLineMsg)
		if msg.name != "tick" || msg.line != want {
			t.Fatalf("msg = %+v, want line %q", msg, want)
		}
	}
	select {
	case extra := <-ch:
		t.Fatalf("unexpected extra message %+v", extra)
	default:
	}
}

func TestLineWriterHoldsAnIncompleteLine(t *testing.T) {
	s, ch := captureSink()
	w := &lineWriter{name: "tick", sink: s}
	w.Write([]byte("no newline yet"))

	select {
	case m := <-ch:
		t.Fatalf("sent %+v before a newline arrived", m)
	default:
	}
	w.Write([]byte("\n"))
	if got := nextMsg(t, ch, time.Second).(logLineMsg).line; got != "no newline yet" {
		t.Fatalf("line = %q", got)
	}
}

func TestSinkIsNilSafe(t *testing.T) {
	var s *sink
	s.send(logLineMsg{}) // must not panic

	empty := &sink{}
	empty.send(logLineMsg{}) // no fn set: also fine
}

func TestStreamHandleStopIsNilSafe(t *testing.T) {
	var h *streamHandle
	h.stop() // must not panic
}

func TestStartStreamDeliversLinesAndStopsCleanly(t *testing.T) {
	c, m := testDaemon(t, map[string]config.Command{
		"tick": {Cmd: []string{"sh", "-c", "while true; do echo tick; sleep 0.05; done"}, Cwd: "/tmp"},
	})
	if err := m.Start("tick"); err != nil {
		t.Fatal(err)
	}

	s, ch := captureSink()
	h := startStream(c, s, "tick")

	msg := nextMsg(t, ch, 3*time.Second)
	line, ok := msg.(logLineMsg)
	if !ok || line.name != "tick" || line.line != "tick" {
		t.Fatalf("msg = %+v, want a logLineMsg for tick", msg)
	}

	done := make(chan struct{})
	go func() {
		h.stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stop() did not return: the stream goroutine is stuck")
	}

	// A stream we cancelled ourselves must not report that it ended.
	drained := time.After(200 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			if _, bad := msg.(streamEndedMsg); bad {
				t.Fatal("cancelled stream sent streamEndedMsg")
			}
		case <-drained:
			return
		}
	}
}

func TestStartStreamReportsAnUnknownCommand(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{})

	s, ch := captureSink()
	h := startStream(c, s, "ghost")
	defer h.stop()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			if ended, ok := msg.(streamEndedMsg); ok {
				if ended.name != "ghost" || ended.err == nil {
					t.Fatalf("streamEndedMsg = %+v, want an error for ghost", ended)
				}
				return
			}
		case <-deadline:
			t.Fatal("no streamEndedMsg for an unknown command")
		}
	}
}
