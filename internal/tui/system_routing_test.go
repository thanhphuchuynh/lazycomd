package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/probe"
)

func TestPortsPanelIsAlwaysVisible(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, systemMsg(probe.Snapshot{
		Ports:     []probe.Port{{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"}},
		SampledAt: map[string]time.Time{"ports": time.Now()},
	}))

	// No key needed: the panel is on screen next to the commands.
	view := m.View()
	if !strings.Contains(view, "postgres") {
		t.Fatalf("ports panel not rendered without pressing anything:\n%s", view)
	}
	if !strings.Contains(view, "3 Ports") || !strings.Contains(view, "2 Commands") {
		t.Fatalf("panel titles missing:\n%s", view)
	}
}

func TestPortsPanelTakesNavigationWhenFocused(t *testing.T) {
	m := modelWithRows(t, "a", "b")
	m, _ = step(t, m, systemMsg(probe.Snapshot{
		Ports: []probe.Port{
			{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"},
			{Addr: "*", Port: 7777, PID: 54405, Process: "lazycomd"},
		},
		SampledAt: map[string]time.Time{"ports": time.Now()},
	}))

	m, _ = step(t, m, key("3"))
	if m.focus != focusPorts {
		t.Fatal("3 should focus the ports panel")
	}

	beforeCmd, _ := m.table.Selected()
	m, _ = step(t, m, key("j"))

	sel, ok := m.system.Selected()
	if !ok || sel.Port != 7777 {
		t.Fatalf("ports cursor did not move: %+v", sel)
	}
	if after, _ := m.table.Selected(); after.Name != beforeCmd.Name {
		t.Fatal("j moved the command cursor while Ports had focus")
	}
}

func TestSystemTickFetches(t *testing.T) {
	m := modelWithRows(t, "a")
	_, cmd := step(t, m, systemTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("system tick produced no command")
	}
}

func TestSystemErrorDoesNotClearTheLastSnapshot(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, systemMsg(probe.Snapshot{
		Ports:     []probe.Port{{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"}},
		SampledAt: map[string]time.Time{"ports": time.Now()},
	}))
	m, _ = step(t, m, systemErrMsg{err: errNoDaemonForTest{}})

	// The ports panel is always on screen, so no key is needed to check it.
	if !strings.Contains(m.View(), "postgres") {
		t.Fatalf("last snapshot dropped on a fetch error:\n%s", m.View())
	}
}

func TestHelpListsThePanelKeys(t *testing.T) {
	help := helpOverlay(80, 40)
	for _, want := range []string{"1-3", "panel", "Ports", "Commands"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, help)
		}
	}
	// d used to open a ports overlay; ports is a panel now, so d deletes.
	var deleteBinding bool
	for _, b := range bindings {
		if b.key == "d" {
			if b.desc != "delete" {
				t.Fatalf("d binding = %q, want delete", b.desc)
			}
			deleteBinding = true
		}
	}
	if !deleteBinding {
		t.Fatal("no d binding for delete")
	}
}

var _ = tea.KeyMsg{}
