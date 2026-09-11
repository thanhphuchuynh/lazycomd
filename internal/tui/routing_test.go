package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

func modelWithRows(t *testing.T, names ...string) Model {
	t.Helper()
	cmds := map[string]config.Command{}
	for _, n := range names {
		cmds[n] = sleeper()
	}
	m, _, _ := newTestModel(t, cmds)

	list := make([]manager.Status, 0, len(names))
	for _, n := range names {
		list = append(list, manager.Status{Name: n, State: manager.Stopped})
	}
	m, _ = step(t, m, statusMsg(list))
	return m
}

func TestTabSwitchesFocus(t *testing.T) {
	m := modelWithRows(t, "a")
	if m.focus != focusTable {
		t.Fatal("should start on the table")
	}
	m, _ = step(t, m, key("tab"))
	if m.focus != focusLogs {
		t.Fatal("tab should focus the log pane")
	}
	m, _ = step(t, m, key("tab"))
	if m.focus != focusTable {
		t.Fatal("tab should come back to the table")
	}
}

func TestLifecycleKeysIssueActions(t *testing.T) {
	m := modelWithRows(t, "tick")
	for _, tc := range []struct{ k, verb string }{{"s", "start"}, {"S", "stop"}, {"r", "restart"}} {
		_, cmd := step(t, m, key(tc.k))
		if cmd == nil {
			t.Fatalf("%s produced no command", tc.k)
		}
		done, ok := cmd().(actionDoneMsg)
		if !ok {
			t.Fatalf("%s produced %T, want actionDoneMsg", tc.k, cmd())
		}
		if done.verb != tc.verb || done.name != "tick" {
			t.Fatalf("%s ran %+v, want %s tick", tc.k, done, tc.verb)
		}
	}
}

// errNoDaemonForTest stands in for a transport failure.
type errNoDaemonForTest struct{}

func (errNoDaemonForTest) Error() string { return "daemon not running" }

func TestLifecycleKeysAreInertWhileDisconnected(t *testing.T) {
	m := modelWithRows(t, "tick")
	m, _ = step(t, m, statusErrMsg{err: errNoDaemonForTest{}})

	m2, cmd := step(t, m, key("s"))
	if cmd != nil {
		t.Fatal("s should not fire an action while disconnected")
	}
	if !strings.Contains(m2.bottom(), "daemon not running") {
		t.Fatalf("status line = %q", m2.bottom())
	}
}

func TestHelpOverlaySwallowsKeys(t *testing.T) {
	m := modelWithRows(t, "a", "b")
	m, _ = step(t, m, key("?"))
	if m.overlay != overlayHelp {
		t.Fatal("? should open help")
	}
	if !strings.Contains(m.View(), "lazycomd — keys") {
		t.Fatalf("help not rendered:\n%s", m.View())
	}

	before, _ := m.table.Selected()
	m, cmd := step(t, m, key("j"))
	if cmd != nil {
		t.Fatal("keys should be ignored while help is open")
	}
	if after, _ := m.table.Selected(); after.Name != before.Name {
		t.Fatal("help must not pass navigation through")
	}

	m, _ = step(t, m, key("esc"))
	if m.overlay != overlayNone {
		t.Fatal("esc should close help")
	}

	m, _ = step(t, m, key("?"))
	_, cmd = step(t, m, key("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c must quit even with help open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c did not quit")
	}
}

func TestPaletteOpensStartsAndSelects(t *testing.T) {
	m := modelWithRows(t, "proxy", "app:api")

	m, _ = step(t, m, key("p"))
	if m.focus != focusPalette {
		t.Fatal("p should focus the palette")
	}
	for _, r := range "api" {
		m, _ = step(t, m, key(string(r)))
	}
	if !strings.Contains(m.View(), "app:api") {
		t.Fatalf("palette not rendered:\n%s", m.View())
	}

	m, cmd := step(t, m, key("enter"))
	if m.focus != focusTable {
		t.Fatal("enter should return focus to the table")
	}
	if got, _ := m.table.Selected(); got.Name != "app:api" {
		t.Fatalf("selected = %q, want app:api", got.Name)
	}
	if cmd == nil {
		t.Fatal("enter on a stopped command should start it")
	}
}

func TestPaletteEnterOnARunningCommandOnlySelects(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Running, PID: 5}})

	m, _ = step(t, m, key("p"))
	m, cmd := step(t, m, key("enter"))

	if got, _ := m.table.Selected(); got.Name != "tick" {
		t.Fatalf("selected = %q", got.Name)
	}
	if cmd != nil {
		if _, isAction := cmd().(actionDoneMsg); isAction {
			t.Fatal("enter on a running command must not start it again")
		}
	}
}

func TestPaletteEscCloses(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("p"))
	m, _ = step(t, m, key("esc"))
	if m.focus != focusTable || m.overlay != overlayNone {
		t.Fatal("esc should close the palette")
	}
}

func TestEscInLogPaneClearsFilterThenReturnsFocus(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, logTailMsg{name: "a", lines: []string{"one", "two"}})
	m, _ = step(t, m, key("tab"))

	m, _ = step(t, m, key("/"))
	m, _ = step(t, m, key("o"))
	m, _ = step(t, m, key("enter"))
	if m.logs.Query() != "o" {
		t.Fatalf("query = %q, want o", m.logs.Query())
	}

	m, _ = step(t, m, key("esc")) // clears the filter, keeps focus
	if m.logs.Query() != "" {
		t.Fatalf("query = %q, want cleared", m.logs.Query())
	}
	if m.focus != focusLogs {
		t.Fatal("focus should stay on the log pane while a filter was set")
	}

	m, _ = step(t, m, key("esc")) // no filter left: back to the table
	if m.focus != focusTable {
		t.Fatal("esc with no filter should return to the table")
	}
}

func TestSlashFromTheTableFocusesTheLogFilter(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("/"))
	if m.focus != focusLogs {
		t.Fatal("/ should focus the log pane")
	}
	if !m.logs.FilterEditing() {
		t.Fatal("/ should open the filter input")
	}
}

func TestQIsLiteralWhileAnInputIsOpen(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("/"))
	m, cmd := step(t, m, key("q"))
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q must be literal text while the filter input is open")
		}
	}
	m, _ = step(t, m, key("enter"))
	if m.logs.Query() != "q" {
		t.Fatalf("query = %q, want q", m.logs.Query())
	}
}

func TestSelectionMoveFetchesTheNewTail(t *testing.T) {
	m := modelWithRows(t, "a", "b")
	_, cmd := step(t, m, key("j"))
	if cmd == nil {
		t.Fatal("moving the selection should fetch the new command's tail")
	}
}

func TestNarrowTerminalHidesTheLogPane(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("tab"))
	if m.focus != focusLogs {
		t.Fatal("precondition: focus on the log pane")
	}
	m, _ = step(t, m, key("/"))

	m, _ = step(t, m, tea.WindowSizeMsg{Width: 50, Height: 20})
	if m.logPaneVisible() {
		t.Fatal("log pane should be hidden below 60 columns")
	}
	if m.focus != focusTable {
		t.Fatal("focus should fall back to the table")
	}
	if m.logs.FilterEditing() {
		t.Fatal("the filter input should close")
	}
	if strings.Contains(m.View(), "│") {
		t.Fatalf("the pane separator should be gone:\n%s", m.View())
	}
}
