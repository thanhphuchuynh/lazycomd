package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/manager"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

func testStatus(t *testing.T, connected bool, now time.Time) statusModel {
	t.Helper()
	s := newStatus("unix:///tmp/lzc/lazycomd.sock")
	s.now = func() time.Time { return now }
	s.SetConnected(connected)
	s.SetRows([]manager.Status{
		{Name: "web", State: manager.Running},
		{Name: "noisy", State: manager.Running},
		{Name: "flaky", State: manager.Failed},
	})
	s.SetSnapshot(probe.Snapshot{
		SampledAt: map[string]time.Time{
			"ports":  now.Add(-2 * time.Second),
			"vitals": now.Add(-1 * time.Second),
		},
		Errors: map[string]string{},
	})
	return s
}

func TestStatusSummaryCountsStates(t *testing.T) {
	now := time.Now()
	s := testStatus(t, true, now)
	if got := s.Summary(); !strings.Contains(got, "2 running") || !strings.Contains(got, "1 failed") {
		t.Fatalf("Summary = %q", got)
	}

	s.SetConnected(false)
	if got := s.Summary(); got != "daemon down" {
		t.Fatalf("disconnected Summary = %q", got)
	}
}

func TestStatusPanelRows(t *testing.T) {
	now := time.Now()
	s := testStatus(t, true, now)

	rows := s.PanelRows()
	if len(rows) != 2 {
		t.Fatalf("PanelRows = %v, want two lines", rows)
	}
	if !strings.Contains(rows[0], "●") || !strings.Contains(rows[1], "lazycomd.sock") {
		t.Fatalf("PanelRows = %v", rows)
	}
}

func TestStatusPanelWarnsAboutACollectorError(t *testing.T) {
	now := time.Now()
	s := testStatus(t, true, now)
	s.SetSnapshot(probe.Snapshot{
		SampledAt: map[string]time.Time{"ports": now},
		Errors:    map[string]string{"ports": `exec: "lsof": not found`},
	})

	rows := s.PanelRows()
	if !strings.Contains(rows[1], "⚠") || !strings.Contains(rows[1], "lsof") {
		t.Fatalf("PanelRows = %v, want the collector error surfaced", rows)
	}
}

func TestStatusDetailExplainsEachCollector(t *testing.T) {
	now := time.Now()
	s := testStatus(t, true, now)

	detail := strings.Join(s.Detail(), "\n")
	for _, want := range []string{"address", "reachable", "yes", "collectors", "ports", "sampled 2s ago", "vitals", "health", "no sample yet"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail missing %q:\n%s", want, detail)
		}
	}
}

func TestStatusDetailListsProblems(t *testing.T) {
	now := time.Now()
	s := testStatus(t, false, now)
	s.SetSnapshot(probe.Snapshot{
		SampledAt: map[string]time.Time{},
		Errors:    map[string]string{"ports": "lsof missing"},
	})

	detail := strings.Join(s.Detail(), "\n")
	if !strings.Contains(detail, "problems") {
		t.Fatalf("no problems section:\n%s", detail)
	}
	if !strings.Contains(detail, "daemon not running") || !strings.Contains(detail, "lsof missing") {
		t.Fatalf("problems incomplete:\n%s", detail)
	}
	if !strings.Contains(detail, "reachable  no") {
		t.Fatalf("reachability wrong:\n%s", detail)
	}
}

func TestStatusDetailMarksStaleCollectors(t *testing.T) {
	now := time.Now()
	s := testStatus(t, true, now)
	s.SetSnapshot(probe.Snapshot{
		SampledAt: map[string]time.Time{"ports": now.Add(-30 * time.Second)},
		Errors:    map[string]string{},
	})

	if detail := strings.Join(s.Detail(), "\n"); !strings.Contains(detail, "stale, 30s ago") {
		t.Fatalf("stale collector not marked:\n%s", detail)
	}
}
