package tui

import (
	"strings"
	"testing"
)

func TestEveryScopeHasBindings(t *testing.T) {
	seen := map[scope]int{}
	for _, b := range bindings {
		if b.key == "" || b.desc == "" {
			t.Fatalf("incomplete binding: %+v", b)
		}
		seen[b.scope]++
	}
	for _, s := range []scope{scopeGlobal, scopeTable, scopeLogs, scopePalette} {
		if seen[s] == 0 {
			t.Fatalf("scope %d has no bindings", s)
		}
	}
}

func TestKeyBarShowsTheFocusedScopePlusGlobal(t *testing.T) {
	bar := keyBar(200, focusTable)
	for _, want := range []string{"start", "stop", "restart", "palette", "quit"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("table key bar missing %q:\n%s", want, bar)
		}
	}
	if strings.Contains(bar, "half page") {
		t.Fatalf("table key bar shows a log-pane binding:\n%s", bar)
	}

	bar = keyBar(200, focusLogs)
	if !strings.Contains(bar, "half page") {
		t.Fatalf("log key bar missing the half-page binding:\n%s", bar)
	}
	if strings.Contains(bar, "restart") {
		t.Fatalf("log key bar shows a table binding:\n%s", bar)
	}
}

func TestKeyBarFitsTheWidth(t *testing.T) {
	bar := keyBar(30, focusTable)
	if got := len([]rune(bar)); got > 30 {
		t.Fatalf("key bar is %d runes wide, want <= 30: %q", got, bar)
	}
}

func TestHelpOverlayListsEveryBinding(t *testing.T) {
	help := helpOverlay(80, 40)
	for _, b := range bindings {
		if !strings.Contains(help, b.desc) {
			t.Fatalf("help overlay missing %q:\n%s", b.desc, help)
		}
	}
	if !strings.Contains(help, "running*") {
		t.Fatalf("help overlay should explain the spec-changed asterisk:\n%s", help)
	}
}
