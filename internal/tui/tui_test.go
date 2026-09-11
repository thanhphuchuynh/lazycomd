package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// newTestModel returns a sized model plus the channel its sink writes to.
func newTestModel(t *testing.T, cmds map[string]config.Command) (Model, *manager.Manager, chan tea.Msg) {
	t.Helper()
	c, mgr := testDaemon(t, cmds)
	s, ch := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	out := next.(Model)
	t.Cleanup(func() { out.stream.stop() })
	return out, mgr, ch
}

// step applies one message and returns the model plus the command it emitted.
func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	out, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return out, cmd
}

func TestStatusMsgPopulatesTheTable(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})

	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Running, PID: 42, UptimeSec: 5}})
	view := m.View()
	for _, want := range []string{"tick", "running"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	// The sidebar is too narrow for a PID column, so the main pane's title
	// carries it instead.
	if !strings.Contains(view, "pid 42") {
		t.Fatalf("main pane should name the pid:\n%s", view)
	}
	if !strings.Contains(m.header(), "1 of 1 running") {
		t.Fatalf("header = %q", m.header())
	}
}

func TestStatusErrShowsTheBannerAndSlowsTheTick(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Stopped}})

	if m.tickInterval() != tickConnected {
		t.Fatalf("connected tick = %v, want %v", m.tickInterval(), tickConnected)
	}

	m, _ = step(t, m, statusErrMsg{err: client.ErrNoDaemon})
	if m.tickInterval() != tickDisconnected {
		t.Fatalf("disconnected tick = %v, want %v", m.tickInterval(), tickDisconnected)
	}
	if !strings.Contains(m.header(), "daemon not running") {
		t.Fatalf("header = %q, want the reconnect banner", m.header())
	}
	if !strings.Contains(m.View(), "tick") {
		t.Fatal("last known rows should stay visible while disconnected")
	}
}

func TestTickEmitsAFetchAndAnotherTick(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	_, cmd := step(t, m, tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("tick produced no command")
	}
}

func TestActionDoneMergesTheRow(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Stopped}})

	m, _ = step(t, m, actionDoneMsg{verb: "start", name: "tick", st: manager.Status{Name: "tick", State: manager.Running, PID: 7}})
	if !strings.Contains(m.View(), "running") {
		t.Fatalf("merged row not rendered:\n%s", m.View())
	}
	if !strings.Contains(m.bottom(), "start tick") {
		t.Fatalf("status line = %q", m.bottom())
	}
}

func TestActionErrorGoesToTheStatusLine(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, actionDoneMsg{verb: "start", name: "tick", err: errors.New("illegal in current state: tick is running")})
	if !strings.Contains(m.bottom(), "illegal in current state") {
		t.Fatalf("status line = %q, want the daemon's message", m.bottom())
	}
}

func TestStatusLineExpiresBackToTheKeyBar(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	now := time.Now()
	m.now = func() time.Time { return now }

	m, _ = step(t, m, actionDoneMsg{verb: "stop", name: "tick", st: manager.Status{Name: "tick", State: manager.Stopped}})
	if !strings.Contains(m.bottom(), "stop tick") {
		t.Fatalf("status line = %q", m.bottom())
	}

	now = now.Add(statusLife + time.Second)
	if strings.Contains(m.bottom(), "stop tick") {
		t.Fatalf("status line outlived %v: %q", statusLife, m.bottom())
	}
	if !strings.Contains(m.bottom(), "quit") {
		t.Fatalf("key bar should be back: %q", m.bottom())
	}
}

func TestLogLinesOnlyLandForTheSelectedCommand(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"a": sleeper(), "b": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "a", State: manager.Running}, {Name: "b", State: manager.Running}})
	m, _ = step(t, m, logTailMsg{name: "a", lines: []string{"from a"}})

	m, _ = step(t, m, logLineMsg{name: "a", line: "live a"})
	m, _ = step(t, m, logLineMsg{name: "b", line: "live b"})

	view := m.View()
	if !strings.Contains(view, "live a") {
		t.Fatalf("selected command's line missing:\n%s", view)
	}
	if strings.Contains(view, "live b") {
		t.Fatalf("another command's line leaked in:\n%s", view)
	}
}

func TestLogTailForAStaleSelectionIsIgnored(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"a": sleeper(), "b": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "a", State: manager.Running}, {Name: "b", State: manager.Running}})

	m, _ = step(t, m, logTailMsg{name: "b", lines: []string{"stale tail"}})
	if strings.Contains(m.View(), "stale tail") {
		t.Fatalf("tail for an unselected command was applied:\n%s", m.View())
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m, _, _ := newTestModel(t, map[string]config.Command{})
		_, cmd := step(t, m, key(k))
		if cmd == nil {
			t.Fatalf("%s produced no command, want tea.Quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s did not quit", k)
		}
	}
}

func TestResizeKeepsTheViewWithinWidth(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Running, PID: 42}})

	m, _ = step(t, m, tea.WindowSizeMsg{Width: 70, Height: 20})
	for _, line := range strings.Split(m.View(), "\n") {
		if got := len([]rune(line)); got > 70 {
			t.Fatalf("line is %d runes wide at width 70: %q", got, line)
		}
	}
}
