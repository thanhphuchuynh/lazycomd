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
	for _, s := range []scope{scopeGlobal, scopeCommands, scopePorts, scopePalette} {
		if seen[s] == 0 {
			t.Fatalf("scope %d has no bindings", s)
		}
	}
}

func TestKeyBarShowsTheFocusedScopePlusGlobal(t *testing.T) {
	bar := keyBar(200, focusCommands)
	for _, want := range []string{"start", "stop", "restart", "search commands", "detail", "quit", "panel"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("commands key bar missing %q:\n%s", want, bar)
		}
	}

	bar = keyBar(200, focusPorts)
	if !strings.Contains(bar, "search ports") {
		t.Fatalf("ports key bar missing its own search binding:\n%s", bar)
	}
	if strings.Contains(bar, "restart") {
		t.Fatalf("ports key bar shows a commands binding:\n%s", bar)
	}
	if !strings.Contains(bar, "panel") {
		t.Fatalf("ports key bar missing the global bindings:\n%s", bar)
	}
}

func TestKeyBarFitsTheWidth(t *testing.T) {
	bar := keyBar(30, focusCommands)
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

func TestKeyBarKeepsGlobalKeysWhenSpaceIsTight(t *testing.T) {
	// At a realistic width the pane-specific hints get dropped first: losing
	// "q quit" and "? help" is the one thing the bar must never do.
	bar := keyBar(100, focusCommands)
	for _, want := range []string{"q quit", "? help"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("key bar dropped %q at width 100:\n%s", want, bar)
		}
	}
	if got := len([]rune(bar)); got > 100 {
		t.Fatalf("key bar is %d runes wide, want <= 100", got)
	}
}
