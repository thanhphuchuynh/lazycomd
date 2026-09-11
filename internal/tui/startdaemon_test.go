package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
)

func TestDisconnectOffersToStartLocalDaemon(t *testing.T) {
	m := modelWithRows(t, "tick")
	m, _ = step(t, m, statusErrMsg{err: client.ErrNoDaemon})
	if m.overlay != overlayStartDaemon {
		t.Fatalf("overlay = %v, want start-daemon prompt", m.overlay)
	}
	view := m.View()
	for _, want := range []string{"daemon is down", "y start", "n"} {
		if !strings.Contains(view, want) {
			t.Fatalf("prompt missing %q:\n%s", want, view)
		}
	}
}

func TestStartDaemonPromptOncePerSession(t *testing.T) {
	m := modelWithRows(t, "tick")
	m, _ = step(t, m, statusErrMsg{err: client.ErrNoDaemon})
	m, cmd := step(t, m, key("n"))
	if m.overlay != overlayNone {
		t.Fatal("n should skip")
	}
	if cmd != nil {
		t.Fatal("n should not start the daemon")
	}
	m, _ = step(t, m, statusErrMsg{err: client.ErrNoDaemon})
	if m.overlay != overlayNone {
		t.Fatal("should not ask again this session")
	}
}

func TestStartDaemonYRunsStart(t *testing.T) {
	m := modelWithRows(t, "tick")
	started := 0
	m.startDaemon = func() error { started++; return nil }
	m, _ = step(t, m, statusErrMsg{err: client.ErrNoDaemon})
	m, cmd := step(t, m, key("y"))
	if m.overlay != overlayNone {
		t.Fatal("y should close the prompt")
	}
	if cmd == nil {
		t.Fatal("y produced no start command")
	}
	if _, ok := cmd().(daemonStartMsg); !ok {
		t.Fatalf("got %T, want daemonStartMsg", cmd())
	}
	if started != 1 {
		t.Fatalf("startDaemon called %d times, want 1", started)
	}
}

func TestRemoteClientDoesNotOfferStart(t *testing.T) {
	c, err := client.New("http://127.0.0.1:9", "")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	m, _ = step(t, m, statusErrMsg{err: client.ErrNoDaemon})
	if m.overlay != overlayNone {
		t.Fatal("TCP client should not offer to start a local daemon")
	}
}

func TestReconnectClosesStartPrompt(t *testing.T) {
	m := modelWithRows(t, "tick")
	m, _ = step(t, m, statusErrMsg{err: client.ErrNoDaemon})
	m, _ = step(t, m, statusMsg{{Name: "tick"}})
	if m.overlay != overlayNone {
		t.Fatal("a live daemon should close the prompt")
	}
}
