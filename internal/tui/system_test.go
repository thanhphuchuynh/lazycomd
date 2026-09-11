package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/probe"
)

func snapshotFixture(now time.Time) probe.Snapshot {
	return probe.Snapshot{
		Ports: []probe.Port{
			{Addr: "127.0.0.1", Port: 3000, PID: 51192, Process: "node"},
			{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"},
			{Addr: "127.0.0.1", Port: 7777, PID: 54405, Process: "lazycomd", Command: "daemon"},
		},
		Conflicts: []probe.Conflict{
			{Port: 3000, Command: "web", State: "taken", HeldBy: "node", PID: 51192},
		},
		SampledAt: map[string]time.Time{"ports": now.Add(-2 * time.Second)},
		Errors:    map[string]string{},
	}
}

func newTestSystem(t *testing.T, snap probe.Snapshot, now time.Time) systemModel {
	t.Helper()
	s := newSystem()
	s.now = func() time.Time { return now }
	s.SetSize(80, 12)
	s.SetSnapshot(snap)
	return s
}

func TestPortsViewRendersRows(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)

	view := s.View()
	for _, want := range []string{"PORT", "ADDR", "PID", "PROCESS", "OWNER", "3000", "postgres", "daemon"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestPortsViewLeadsWithConflicts(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)

	view := s.View()
	if !strings.Contains(view, "3000 wanted by web") || !strings.Contains(view, "node") {
		t.Fatalf("conflict summary missing:\n%s", view)
	}
	if !strings.Contains(view, "⚠") {
		t.Fatalf("conflict marker missing:\n%s", view)
	}
}

func TestPortsViewTitleShowsFreshnessThenStaleness(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)
	if got := s.title(); !strings.Contains(got, "sampled 2s ago") {
		t.Fatalf("title = %q, want the sample age", got)
	}

	stale := snapshotFixture(now)
	stale.SampledAt["ports"] = now.Add(-18 * time.Second)
	s.SetSnapshot(stale)
	if got := s.title(); !strings.Contains(got, "stale 18s") {
		t.Fatalf("title = %q, want a staleness warning", got)
	}
}

func TestPortsViewShowsACollectorError(t *testing.T) {
	now := time.Now()
	snap := snapshotFixture(now)
	snap.Ports = nil
	snap.Conflicts = nil
	snap.Errors["ports"] = `exec: "lsof": executable file not found in $PATH`

	s := newTestSystem(t, snap, now)
	view := s.View()
	if !strings.Contains(view, "ports unavailable") || !strings.Contains(view, "lsof") {
		t.Fatalf("error not explained:\n%s", view)
	}
}

func TestPortsViewScrolls(t *testing.T) {
	now := time.Now()
	snap := snapshotFixture(now)
	snap.Conflicts = nil
	for i := 0; i < 40; i++ {
		snap.Ports = append(snap.Ports, probe.Port{Addr: "*", Port: 9000 + i, PID: i, Process: "filler"})
	}
	s := newTestSystem(t, snap, now)

	if strings.Contains(s.View(), "9039") {
		t.Fatal("last row visible before scrolling")
	}
	for i := 0; i < 60; i++ {
		s, _ = s.Update(key("j"))
	}
	if !strings.Contains(s.View(), "9039") {
		t.Fatalf("scrolling never reached the last row:\n%s", s.View())
	}
}

func TestPortsViewEmpty(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, probe.Snapshot{SampledAt: map[string]time.Time{"ports": now}}, now)
	if !strings.Contains(s.View(), "nothing listening") {
		t.Fatalf("empty view should say so:\n%s", s.View())
	}
}
