package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// drive runs cmd and feeds every resulting message back into the model,
// stopping at a tick so the loop terminates.
func drive(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for depth := 0; cmd != nil && depth < 20; depth++ {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			cmd = nil
			for _, c := range batch {
				if c == nil {
					continue
				}
				m = drive(t, m, c)
			}
			continue
		}
		if _, isTick := msg.(tickMsg); isTick {
			return m
		}
		next, nextCmd := m.Update(msg)
		m = next.(Model)
		cmd = nextCmd
	}
	return m
}

func TestEndToEndStartAndWatch(t *testing.T) {
	c, mgr := testDaemon(t, map[string]config.Command{
		"tick":  {Cmd: []string{"sh", "-c", "while true; do echo tick; sleep 0.05; done"}, Cwd: "/tmp"},
		"greet": {Cmd: []string{"sh", "-c", "echo hello; sleep 30"}, Cwd: "/tmp"},
	})
	s, _ := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	t.Cleanup(func() { m.stream.stop() })

	// First poll: rows appear and the stream syncs to the selection.
	m = drive(t, m, fetchStatus(m.client))
	if !strings.Contains(m.View(), "greet") || !strings.Contains(m.View(), "tick") {
		t.Fatalf("view missing commands:\n%s", m.View())
	}

	// `s` starts the selected command; the daemon must really run it.
	m, cmd := step(t, m, key("s"))
	m = drive(t, m, cmd)

	sel, _ := m.table.Selected()
	waitFor(t, "the selected command to run", func() bool {
		st, err := mgr.Status(sel.Name)
		return err == nil && st.State == manager.Running
	})
	if !strings.Contains(m.View(), "running") {
		t.Fatalf("view does not show the started command:\n%s", m.View())
	}
}

func TestEndToEndSearchSelectsThenStartsAnotherCommand(t *testing.T) {
	c, mgr := testDaemon(t, map[string]config.Command{
		"tick":  {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"greet": {Cmd: []string{"sh", "-c", "echo hello; sleep 30"}, Cwd: "/tmp"},
	})
	s, _ := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = drive(t, next.(Model), fetchStatus(c))
	t.Cleanup(func() { m.stream.stop() })

	m, _ = step(t, m, key("p"))
	for _, r := range "greet" {
		m, _ = step(t, m, key(string(r)))
	}
	m, cmd := step(t, m, key("enter"))
	m = drive(t, m, cmd)

	if got, _ := m.table.Selected(); got.Name != "greet" {
		t.Fatalf("selected = %q, want greet", got.Name)
	}

	// Enter only goes to the match; starting is still an explicit keystroke.
	m, cmd = step(t, m, key("s"))
	m = drive(t, m, cmd)
	waitFor(t, "greet to run", func() bool {
		st, err := mgr.Status("greet")
		return err == nil && st.State == manager.Running
	})
}

func TestEndToEndLiveLinesReachThePane(t *testing.T) {
	c, mgr := testDaemon(t, map[string]config.Command{
		"tick": {Cmd: []string{"sh", "-c", "while true; do echo tick; sleep 0.05; done"}, Cwd: "/tmp"},
	})
	if err := mgr.Start("tick"); err != nil {
		t.Fatal(err)
	}

	s, ch := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = drive(t, next.(Model), fetchStatus(c))
	t.Cleanup(func() { m.stream.stop() })

	// A live line arrives through the sink; feed it in like the runtime does.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-ch:
			if line, ok := msg.(logLineMsg); ok {
				m, _ = step(t, m, line)
				if strings.Contains(m.View(), "tick") {
					return
				}
			}
		case <-deadline:
			t.Fatalf("no live line reached the pane:\n%s", m.View())
		}
	}
}

func TestEndToEndSurvivesADeadDaemon(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{"tick": sleeper()})
	s, _ := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = drive(t, next.(Model), fetchStatus(c))
	t.Cleanup(func() { m.stream.stop() })

	// Simulate the daemon going away, then coming back.
	m, _ = step(t, m, statusErrMsg{err: errNoDaemonForTest{}})
	if !strings.Contains(m.header(), "daemon not running") {
		t.Fatalf("header = %q", m.header())
	}
	m = drive(t, m, fetchStatus(c))
	if strings.Contains(m.header(), "daemon not running") {
		t.Fatalf("banner did not clear after recovery: %q", m.header())
	}
}
