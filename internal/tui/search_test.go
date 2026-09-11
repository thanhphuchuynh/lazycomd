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

func typeIntoPorts(s systemModel, text string) systemModel {
	for _, r := range text {
		s, _ = s.Update(key(string(r)))
	}
	return s
}

func TestPortFilterKeepsOnlyMatchingRows(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)

	s, _ = s.Update(key("/"))
	if !s.FilterEditing() {
		t.Fatal("slash should open the port filter")
	}
	s = typeIntoPorts(s, "postgres")
	s, _ = s.Update(key("enter"))

	if got := s.Query(); got != "postgres" {
		t.Fatalf("query = %q, want postgres", got)
	}
	rows := strings.Join(s.PanelRows(), "\n")
	if !strings.Contains(rows, "5432") {
		t.Fatalf("the matching port is missing:\n%s", rows)
	}
	if strings.Contains(rows, "3000") || strings.Contains(rows, "7777") {
		t.Fatalf("non-matching ports survived the filter:\n%s", rows)
	}
	if sub := s.Subtitle(); !strings.Contains(sub, "1 of 3") {
		t.Fatalf("subtitle = %q, want a 1 of 3 count", sub)
	}
}

func TestPortFilterMatchesNumberAndOwner(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct{ query, want string }{
		{"5432", "postgres"},
		{"daemon", "lazycomd"},
		{"127.0.0.1", "node"},
	} {
		s := newTestSystem(t, snapshotFixture(now), now)
		s, _ = s.Update(key("/"))
		s = typeIntoPorts(s, tc.query)
		s, _ = s.Update(key("enter"))
		if rows := strings.Join(s.PanelRows(), "\n"); !strings.Contains(rows, tc.want) {
			t.Fatalf("query %q did not match %s:\n%s", tc.query, tc.want, rows)
		}
	}
}

func TestPortFilterSelectionAndEscape(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)

	s, _ = s.Update(key("/"))
	s = typeIntoPorts(s, "5432")
	s, _ = s.Update(key("enter"))

	// The cursor indexes the visible list, so detail must follow the filter.
	sel, ok := s.Selected()
	if !ok || sel.Port != 5432 {
		t.Fatalf("selected = %v (%v), want port 5432", sel, ok)
	}
	if d := strings.Join(s.Detail(), "\n"); !strings.Contains(d, "port 5432") {
		t.Fatalf("detail follows the wrong row:\n%s", d)
	}

	s, _ = s.Update(key("esc"))
	if s.Query() != "" {
		t.Fatalf("esc left the query %q in place", s.Query())
	}
	if got := len(s.ports()); got != 3 {
		t.Fatalf("ports after clearing = %d, want 3", got)
	}
}

func TestPortFilterCancelKeepsThePreviousQuery(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)
	s, _ = s.Update(key("/"))
	s = typeIntoPorts(s, "5432")
	s, _ = s.Update(key("enter"))

	s, _ = s.Update(key("/"))
	s = typeIntoPorts(s, "nope")
	s, _ = s.Update(key("esc"))

	if got := s.Query(); got != "" {
		t.Fatalf("query = %q; esc while editing clears the filter", got)
	}
}

func TestSlashRoutesToTheFocusedPanel(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, systemMsg(snapshotFixture(time.Now())))

	// Commands focused: the log filter still owns slash.
	m, _ = step(t, m, key("/"))
	if !m.logs.FilterEditing() {
		t.Fatal("slash with Commands focused should filter logs")
	}
	m, _ = step(t, m, key("esc"))

	m, _ = step(t, m, key("3"))
	m, _ = step(t, m, key("/"))
	if m.logs.FilterEditing() {
		t.Fatal("slash with Ports focused must not touch the log filter")
	}
	if !m.system.FilterEditing() {
		t.Fatal("slash with Ports focused should filter ports")
	}
	// Typing must reach the filter input, not the panel's own bindings.
	m, _ = step(t, m, key("g"))
	m, _ = step(t, m, key("enter"))
	if got := m.system.Query(); got != "g" {
		t.Fatalf("query = %q, want g", got)
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

	// Detail is about the selected command, so it only opens from Commands.
	m, _ = step(t, m, key("3"))
	m, _ = step(t, m, key("i"))
	if m.detail {
		t.Fatal("i with Ports focused should not open the command detail")
	}
}

func TestFocusGivesTheFocusedPanelItsRows(t *testing.T) {
	m := modelWithRows(t, "web")
	// A stacked (narrow) terminal, where only the focused panel gets height.
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

func TestPortFilterWithNoMatchSaysSo(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)
	s, _ = s.Update(key("/"))
	s = typeIntoPorts(s, "zzz")
	s, _ = s.Update(key("enter"))

	for _, got := range []string{strings.Join(s.PanelRows(), "\n"), strings.Join(s.Detail(), "\n")} {
		if !strings.Contains(got, `no port matches "zzz"`) {
			t.Fatalf("an empty result must not read as nothing listening:\n%s", got)
		}
	}
}
