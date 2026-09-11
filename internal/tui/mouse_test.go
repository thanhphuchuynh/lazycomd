package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func clickAt(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Type: tea.MouseLeft, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func wheelAt(x, y int, up bool) tea.MouseMsg {
	t, b := tea.MouseWheelDown, tea.MouseButtonWheelDown
	if up {
		t, b = tea.MouseWheelUp, tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{X: x, Y: y, Type: t, Action: tea.MouseActionPress, Button: b}
}

func wideModel(t *testing.T, names ...string) Model {
	t.Helper()
	m := modelWithRows(t, names...)
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = step(t, m, systemMsg(snapshotFixture(time.Now())))
	return m
}

func TestClickFocusesAPanel(t *testing.T) {
	m := wideModel(t, "web", "worker")

	// The Ports panel sits below Status and Commands in the sidebar.
	m, _ = step(t, m, clickAt(3, m.portsTop()+1))
	if m.focus != focusPorts {
		t.Fatalf("focus = %v, want Ports after clicking it", m.focus)
	}

	m, _ = step(t, m, clickAt(3, m.statusTop()+1))
	if m.focus != focusStatus {
		t.Fatalf("focus = %v, want Status after clicking it", m.focus)
	}
}

func TestClickSelectsTheRowUnderThePointer(t *testing.T) {
	m := wideModel(t, "web", "worker", "db")

	// The panel's own row and its column header come first, then the rows
	// in the order the daemon reported them: web, worker, db.
	m, _ = step(t, m, clickAt(4, m.commandsTop()+2+1))
	if m.focus != focusCommands {
		t.Fatalf("focus = %v, want Commands", m.focus)
	}
	if sel, _ := m.table.Selected(); sel.Name != "worker" {
		t.Fatalf("selected %q, want worker", sel.Name)
	}

	m, _ = step(t, m, clickAt(4, m.commandsTop()+2+0))
	if sel, _ := m.table.Selected(); sel.Name != "web" {
		t.Fatalf("selected %q, want web", sel.Name)
	}
}

func TestClickBelowTheLastRowKeepsTheSelection(t *testing.T) {
	m := wideModel(t, "web")
	m, _ = step(t, m, clickAt(4, m.commandsTop()+8))
	if sel, _ := m.table.Selected(); sel.Name != "web" {
		t.Fatalf("a click on empty space changed the selection to %q", sel.Name)
	}
}

func TestClickSelectsAPort(t *testing.T) {
	m := wideModel(t, "web")
	m, _ = step(t, m, clickAt(4, m.portsTop()+2))
	if m.focus != focusPorts {
		t.Fatalf("focus = %v, want Ports", m.focus)
	}
	sel, ok := m.system.Selected()
	if !ok || sel.Port != 5432 {
		t.Fatalf("selected %v (%v), want the second port", sel, ok)
	}
}

func TestWheelScrollsThePaneUnderThePointer(t *testing.T) {
	m := wideModel(t, "a", "b", "c", "d")
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = strings.Repeat("x", 10)
	}
	m, _ = step(t, m, logTailMsg{name: "a", lines: lines})

	// Over the main pane: the log scrolls and the selection stays put.
	before, _ := m.table.Selected()
	m, _ = step(t, m, wheelAt(m.sideW+5, 6, true))
	if m.logs.AtBottom() {
		t.Fatal("the wheel over the main pane did not scroll the log")
	}
	if after, _ := m.table.Selected(); after.Name != before.Name {
		t.Fatalf("the wheel over the main pane moved the selection to %q", after.Name)
	}

	// Over the Commands panel: the cursor moves.
	m, _ = step(t, m, wheelAt(3, m.commandsTop()+3, false))
	if after, _ := m.table.Selected(); after.Name == before.Name {
		t.Fatal("the wheel over the Commands panel did not move the cursor")
	}
}

func TestClickInTheSearchBoxGoesToTheMatch(t *testing.T) {
	m := wideModel(t, "web", "worker")
	m, _ = step(t, m, key("/"))

	// The first match row sits under the box's border and input line.
	top, left := m.searchBoxTop(), m.searchBoxLeft()
	m, _ = step(t, m, clickAt(left+4, top+2))

	if m.overlay != overlayNone {
		t.Fatal("clicking a match should close the search box")
	}
	if m.focus != focusCommands {
		t.Fatalf("focus = %v, want Commands", m.focus)
	}
}

func TestClickOutsideTheSearchBoxClosesIt(t *testing.T) {
	m := wideModel(t, "web")
	m, _ = step(t, m, key("/"))
	m, _ = step(t, m, clickAt(1, 1))
	if m.overlay != overlayNone {
		t.Fatal("a click outside the box should close it")
	}
}
