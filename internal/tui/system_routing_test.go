package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/probe"
)

func TestDTogglesThePortsView(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, systemMsg(probe.Snapshot{
		Ports:     []probe.Port{{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"}},
		SampledAt: map[string]time.Time{"ports": time.Now()},
	}))

	m, _ = step(t, m, key("d"))
	if m.overlay != overlayPorts {
		t.Fatal("d should open the ports view")
	}
	if !strings.Contains(m.View(), "postgres") {
		t.Fatalf("ports not rendered:\n%s", m.View())
	}

	m, _ = step(t, m, key("d"))
	if m.overlay != overlayNone {
		t.Fatal("d should close the ports view")
	}

	m, _ = step(t, m, key("d"))
	m, _ = step(t, m, key("esc"))
	if m.overlay != overlayNone {
		t.Fatal("esc should close the ports view")
	}
}

func TestPortsViewTakesScrollKeys(t *testing.T) {
	m := modelWithRows(t, "a", "b")
	m, _ = step(t, m, key("d"))

	before, _ := m.table.Selected()
	m, _ = step(t, m, key("j"))
	if after, _ := m.table.Selected(); after.Name != before.Name {
		t.Fatal("j moved the table cursor while the ports view was open")
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
	m, _ = step(t, m, key("d"))

	if !strings.Contains(m.View(), "postgres") {
		t.Fatalf("last snapshot dropped on a fetch error:\n%s", m.View())
	}
}

func TestHelpListsTheDBinding(t *testing.T) {
	if !strings.Contains(helpOverlay(80, 40), "ports") {
		t.Fatal("help overlay does not mention the ports view")
	}
	var found bool
	for _, b := range bindings {
		if b.key == "d" {
			found = true
		}
	}
	if !found {
		t.Fatal("no d binding registered")
	}
}

var _ = tea.KeyMsg{}
