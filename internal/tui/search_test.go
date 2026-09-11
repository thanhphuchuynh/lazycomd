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

func testSearch(query string) searchModel {
	s := newSearch()
	s.Open(
		[]manager.Status{
			{Name: "web", State: manager.Running, PID: 4242},
			{Name: "worker", State: manager.Stopped},
		},
		[]probe.Port{
			{Port: 8099, Addr: "127.0.0.1", PID: 4242, Process: "Python", Command: "web"},
			{Port: 5432, Addr: "*", PID: 1183, Process: "postgres"},
		},
		"web",
		[]string{"12:03:11 GET / 200", "12:03:12 GET /favicon 404", "12:03:13 done"},
	)
	for _, r := range query {
		s, _ = s.Update(key(string(r)))
	}
	return s
}

func TestSearchRanksEverySource(t *testing.T) {
	s := testSearch("")
	kinds := map[searchKind]int{}
	for _, m := range s.matches {
		kinds[m.kind]++
	}
	for _, k := range []searchKind{kindCommand, kindPort, kindLog} {
		if kinds[k] == 0 {
			t.Fatalf("kind %v missing from an empty query: %+v", k, s.matches)
		}
	}

	view := s.View(60, 12)
	for _, want := range []string{"cmd", "web", "port", "8099", "log"} {
		if !strings.Contains(view, want) {
			t.Fatalf("search view missing %q:\n%s", want, view)
		}
	}
}

func TestSearchMatchesAcrossKinds(t *testing.T) {
	for _, tc := range []struct {
		query string
		kind  searchKind
		want  string
	}{
		{"worker", kindCommand, "worker"},
		{"5432", kindPort, "5432"},
		{"postgres", kindPort, "5432"},
		{"favicon", kindLog, "404"},
	} {
		s := testSearch(tc.query)
		if len(s.matches) == 0 {
			t.Fatalf("query %q matched nothing", tc.query)
		}
		top := s.matches[0]
		if top.kind != tc.kind || !strings.Contains(top.label+top.detail, tc.want) {
			t.Fatalf("query %q ranked %+v first, want a %v containing %q", tc.query, top, tc.kind, tc.want)
		}
	}
}

func TestSearchSelectingACommandFocusesIt(t *testing.T) {
	m := modelWithRows(t, "web", "worker")
	m, _ = step(t, m, key("/"))
	if m.overlay != overlaySearch {
		t.Fatal("slash should open the search overlay")
	}
	for _, r := range "worker" {
		m, _ = step(t, m, key(string(r)))
	}
	m, _ = step(t, m, key("enter"))

	if m.overlay != overlayNone {
		t.Fatal("enter should close the search overlay")
	}
	if m.focus != focusCommands {
		t.Fatalf("focus = %v, want Commands", m.focus)
	}
	if sel, _ := m.table.Selected(); sel.Name != "worker" {
		t.Fatalf("selected %q, want worker", sel.Name)
	}
}

func TestSearchSelectingAPortFocusesPorts(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, systemMsg(snapshotFixture(time.Now())))

	m, _ = step(t, m, key("/"))
	for _, r := range "5432" {
		m, _ = step(t, m, key(string(r)))
	}
	m, _ = step(t, m, key("enter"))

	if m.focus != focusPorts {
		t.Fatalf("focus = %v, want Ports", m.focus)
	}
	if sel, ok := m.system.Selected(); !ok || sel.Port != 5432 {
		t.Fatalf("selected %v (%v), want port 5432", sel, ok)
	}
}

func TestSearchSelectingALogOpensTheLogViewFiltered(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, logTailMsg{name: "web", lines: []string{"boot ok", "GET /favicon 404"}})

	m, _ = step(t, m, key("/"))
	for _, r := range "favicon" {
		m, _ = step(t, m, key(string(r)))
	}
	m, _ = step(t, m, key("enter"))

	if m.overlay != overlayLog {
		t.Fatalf("overlay = %v, want the full log view", m.overlay)
	}
	if got := m.logs.Query(); got != "favicon" {
		t.Fatalf("log filter = %q, want favicon", got)
	}
}

func TestLogOverlayOpensWithOAndFiltersInside(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, logTailMsg{name: "web", lines: []string{"boot ok", "GET /favicon 404"}})

	m, _ = step(t, m, key("o"))
	if m.overlay != overlayLog {
		t.Fatal("o should open the full log view")
	}
	if !strings.Contains(m.View(), "boot ok") {
		t.Fatalf("the log view does not show the log:\n%s", m.View())
	}

	m, _ = step(t, m, key("/"))
	for _, r := range "favicon" {
		m, _ = step(t, m, key(string(r)))
	}
	m, _ = step(t, m, key("enter"))
	if got := m.logs.Query(); got != "favicon" {
		t.Fatalf("filter inside the log view = %q, want favicon", got)
	}

	m, _ = step(t, m, key("esc")) // clears the filter
	m, _ = step(t, m, key("esc")) // closes the view
	if m.overlay != overlayNone {
		t.Fatalf("esc should close the log view, overlay = %v", m.overlay)
	}
}

func TestThePreviewPaneHasNoFilter(t *testing.T) {
	m := modelWithRows(t, "web")
	// Slash belongs to the search overlay now; the preview is read-only.
	m, _ = step(t, m, key("/"))
	if m.logs.FilterEditing() {
		t.Fatal("slash must not start a filter on the log preview")
	}
}

func TestSearchOverlayIsCentred(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = step(t, m, key("/"))

	lines := strings.Split(m.View(), "\n")
	var first, last, boxLeft int
	first, last = -1, -1
	for i, ln := range lines {
		if idx := strings.IndexAny(ln, "┏╭"); idx >= 0 {
			if first < 0 {
				first, boxLeft = i, idx
			}
		}
		if strings.ContainsAny(ln, "┗╰") {
			last = i
		}
	}
	if first < 0 || last < 0 {
		t.Fatalf("no box drawn:\n%s", m.View())
	}
	if boxLeft < 10 {
		t.Fatalf("box starts at column %d, not centred:\n%s", boxLeft, m.View())
	}
	if first < 5 {
		t.Fatalf("box starts at row %d, not centred vertically:\n%s", first, m.View())
	}
}

func TestCommandDetailShowsSpecAndRuntime(t *testing.T) {
	st := manager.Status{
		Name: "web", State: manager.Running, PID: 4242,
		UptimeSec: 75, Restarts: 2, DependsOn: []string{"db"},
		CPU: 1.5, MemMB: 24.5,
		Health: &manager.HealthView{URL: "http://localhost:8099/healthz", OK: true, Status: 200, LatencyMS: 3},
	}
	spec := config.Command{
		Cmd: []string{"python3", "-m", "http.server", "8099"}, Cwd: "/srv",
		Restart: config.RestartOnFailure, DependsOn: []string{"db"},
		Health: "http://localhost:8099/healthz",
	}
	ports := []probe.Port{
		{Port: 8099, Addr: "127.0.0.1", PID: 4242, Command: "web"},
		{Port: 5432, PID: 1183, Command: "db"},
	}

	got := strings.Join(commandDetail(st, spec, true, ports), "\n")
	for _, want := range []string{
		"web", "running", "pid 4242", "1m15s", "2 restarts",
		"python3 -m http.server 8099", "/srv", "on-failure",
		"db", "http://localhost:8099/healthz", "ok", "8099", "24.5M", "1.5%",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "5432") {
		t.Fatalf("detail listed a port owned by another command:\n%s", got)
	}
}

func TestCommandDetailWithoutASpecYet(t *testing.T) {
	st := manager.Status{Name: "web", State: manager.Stopped}
	got := strings.Join(commandDetail(st, config.Command{}, false, nil), "\n")
	if !strings.Contains(got, "web") || !strings.Contains(got, "stopped") {
		t.Fatalf("detail should still show what the status knows:\n%s", got)
	}
	if strings.Contains(got, "folder") {
		t.Fatalf("detail invented spec fields it has not loaded:\n%s", got)
	}
}

func TestIToggleSwapsTheMainPaneForDetail(t *testing.T) {
	m := modelWithRows(t, "web")

	m, _ = step(t, m, key("i"))
	if !m.detail {
		t.Fatal("i should open the command detail")
	}
	if !strings.Contains(m.mainPane(), "web") {
		t.Fatalf("detail pane does not name the command:\n%s", m.mainPane())
	}
	m, _ = step(t, m, key("i"))
	if m.detail {
		t.Fatal("i should close the command detail again")
	}
}

func TestFocusGivesTheFocusedPanelItsRows(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 84, Height: 24})
	if !m.stacked() {
		t.Fatal("this test needs the stacked layout")
	}

	m, _ = step(t, m, key("3"))
	if m.portsH <= 1 {
		t.Fatalf("Ports stayed collapsed at %d rows after taking focus", m.portsH)
	}
	m, _ = step(t, m, key("2"))
	if m.portsH > 1 {
		t.Fatalf("Ports kept %d rows after losing focus", m.portsH)
	}
}
