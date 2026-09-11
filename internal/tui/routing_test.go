package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/probe"
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

func TestNumberKeysAndTabMoveBetweenPanels(t *testing.T) {
	m := modelWithRows(t, "a")
	if m.focus != focusCommands {
		t.Fatal("should open on the commands panel")
	}

	for _, tc := range []struct {
		key  string
		want focus
	}{{"1", focusStatus}, {"3", focusPorts}, {"2", focusCommands}} {
		m, _ = step(t, m, key(tc.key))
		if m.focus != tc.want {
			t.Fatalf("%s focused %v, want %v", tc.key, m.focus, tc.want)
		}
	}

	// tab wraps forward, shift+tab back.
	m, _ = step(t, m, key("tab"))
	if m.focus != focusPorts {
		t.Fatalf("tab from Commands went to %v, want Ports", m.focus)
	}
	m, _ = step(t, m, key("tab"))
	if m.focus != focusStatus {
		t.Fatalf("tab from Ports went to %v, want Status (wrapped)", m.focus)
	}
	m, _ = step(t, m, key("shift+tab"))
	if m.focus != focusPorts {
		t.Fatalf("shift+tab went to %v, want Ports", m.focus)
	}
}

func TestMainPaneFollowsFocus(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, logTailMsg{name: "web", lines: []string{"hello from web"}})

	if !strings.Contains(m.View(), "hello from web") {
		t.Fatalf("commands focus should show logs:\n%s", m.View())
	}

	m, _ = step(t, m, key("1"))
	if v := m.View(); !strings.Contains(v, "reachable") {
		t.Fatalf("status focus should show daemon detail:\n%s", v)
	}

	m, _ = step(t, m, systemMsg(probe.Snapshot{
		Ports:     []probe.Port{{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"}},
		SampledAt: map[string]time.Time{"ports": time.Now()},
	}))
	m, _ = step(t, m, key("3"))
	if v := m.View(); !strings.Contains(v, "port 5432") || !strings.Contains(v, "1183") {
		t.Fatalf("ports focus should show port detail:\n%s", v)
	}
}

func TestLifecycleKeysAreInertOutsideCommands(t *testing.T) {
	m := modelWithRows(t, "tick")
	m, _ = step(t, m, key("3")) // Ports has focus

	m2, cmd := step(t, m, key("s"))
	if cmd != nil {
		t.Fatal("s fired an action from the ports panel")
	}
	if !strings.Contains(m2.bottom(), "press 2") {
		t.Fatalf("status line = %q, want a hint about panel 2", m2.bottom())
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

func TestPKeyOpensTheSearchBoxAndSelects(t *testing.T) {
	m := modelWithRows(t, "proxy", "app:api")

	m, _ = step(t, m, key("p"))
	if m.overlay != overlaySearch {
		t.Fatal("p should open the search box")
	}
	for _, r := range "api" {
		m, _ = step(t, m, key(string(r)))
	}
	if !strings.Contains(m.View(), "app:api") {
		t.Fatalf("search box not rendered:\n%s", m.View())
	}

	m, _ = step(t, m, key("enter"))
	if m.overlay != overlayNone || m.focus != focusCommands {
		t.Fatal("enter should close the search box and focus Commands")
	}
	if got, _ := m.table.Selected(); got.Name != "app:api" {
		t.Fatalf("selected = %q, want app:api", got.Name)
	}
}

func TestPaletteEscCloses(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("p"))
	m, _ = step(t, m, key("esc"))
	if m.overlay != overlayNone {
		t.Fatal("esc should close the palette")
	}
}

func TestTheLogViewFiltersWithoutMovingFocus(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, logTailMsg{name: "a", lines: []string{"one", "two"}})

	m, _ = step(t, m, key("o"))
	m, _ = step(t, m, key("/"))
	if !m.logs.FilterEditing() {
		t.Fatal("/ should open the filter input inside the log view")
	}
	if m.focus != focusCommands {
		t.Fatal("the log view should not move panel focus")
	}

	m, _ = step(t, m, key("o"))
	m, _ = step(t, m, key("enter"))
	if m.logs.Query() != "o" {
		t.Fatalf("query = %q, want o", m.logs.Query())
	}

	m, _ = step(t, m, key("esc"))
	if m.logs.Query() != "" {
		t.Fatalf("query = %q, want cleared", m.logs.Query())
	}
}

func TestScrollKeysReachTheMainPaneFromAnyPanel(t *testing.T) {
	m := modelWithRows(t, "a")
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "line " + string(rune('a'+i%26))
	}
	m, _ = step(t, m, logTailMsg{name: "a", lines: lines})

	m, _ = step(t, m, key("3")) // focus Ports, then scroll the logs anyway
	m, _ = step(t, m, key("ctrl+u"))
	if m.logs.Following() {
		t.Fatal("ctrl+u should scroll the main pane and stop following")
	}
	m, _ = step(t, m, key("f"))
	if !m.logs.Following() {
		t.Fatal("f should restore follow from any panel")
	}
}

func TestQIsLiteralWhileAnInputIsOpen(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("o"))
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

func TestNarrowTerminalStacksInsteadOfHiding(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, logTailMsg{name: "a", lines: []string{"still visible"}})

	m, _ = step(t, m, tea.WindowSizeMsg{Width: 70, Height: 24})
	if !m.stacked() {
		t.Fatal("70 columns should stack the panels above the main pane")
	}

	view := m.View()
	// Everything stays reachable: panels on top, logs underneath.
	for _, want := range []string{"2 Commands", "3 Ports", "still visible"} {
		if !strings.Contains(view, want) {
			t.Fatalf("stacked view missing %q:\n%s", want, view)
		}
	}
	for i, line := range strings.Split(view, "\n") {
		if w := runeWidth(line); w > 70 {
			t.Fatalf("line %d is %d wide at 70 columns: %q", i, w, line)
		}
	}
}

func TestOnlyTheFocusedPanelDrawsALiveCursor(t *testing.T) {
	// Focus used to be applied in layout(), which only runs on a resize, so
	// moving focus left both panels drawing a live cursor.
	m := modelWithRows(t, "a", "b")
	m, _ = step(t, m, systemMsg(probe.Snapshot{
		Ports:     []probe.Port{{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"}},
		SampledAt: map[string]time.Time{"ports": time.Now()},
	}))

	// Commands has focus: it owns the live cursor.
	view := m.View()
	if strings.Count(view, "> ") != 1 {
		t.Fatalf("want exactly one live cursor with Commands focused:\n%s", view)
	}

	// Move to Ports without resizing; the live cursor must move with focus.
	m, _ = step(t, m, key("3"))
	view = m.View()
	if strings.Count(view, "> ") != 1 {
		t.Fatalf("want exactly one live cursor with Ports focused:\n%s", view)
	}
	if !strings.Contains(view, "·") {
		t.Fatalf("the unfocused panel lost its dimmed selection:\n%s", view)
	}
}
