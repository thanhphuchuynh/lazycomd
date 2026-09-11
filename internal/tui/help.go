package tui

import "strings"

// focus is which pane takes keys.
type focus int

const (
	focusTable focus = iota
	focusLogs
	focusPalette
)

// overlay is a full-pane layer drawn over the body.
type overlay int

const (
	overlayNone overlay = iota
	overlayHelp
)

// scope is where a binding applies.
type scope int

const (
	scopeGlobal scope = iota
	scopeTable
	scopeLogs
	scopePalette
)

type binding struct {
	key   string
	desc  string
	scope scope
}

// bindings is the single source of truth for the key bar and the help
// overlay. Adding a key here shows it in both.
var bindings = []binding{
	{"j/k", "move", scopeTable},
	{"g/G", "first/last", scopeTable},
	{"s", "start", scopeTable},
	{"S", "stop", scopeTable},
	{"r", "restart", scopeTable},
	{"p", "palette", scopeTable},
	{"j/k", "scroll", scopeLogs},
	{"ctrl+d/u", "half page", scopeLogs},
	{"g/G", "top/bottom", scopeLogs},
	{"esc", "clear filter", scopeLogs},
	{"enter", "start and select", scopePalette},
	{"esc", "close palette", scopePalette},
	{"f", "follow", scopeGlobal},
	{"/", "filter", scopeGlobal},
	{"tab", "switch pane", scopeGlobal},
	{"?", "help", scopeGlobal},
	{"q", "quit", scopeGlobal},
}

func scopeFor(f focus) scope {
	switch f {
	case focusLogs:
		return scopeLogs
	case focusPalette:
		return scopePalette
	default:
		return scopeTable
	}
}

// keyBar renders the bottom hint line for the focused pane.
func keyBar(width int, f focus) string {
	want := scopeFor(f)
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		if b.scope == want || b.scope == scopeGlobal {
			parts = append(parts, b.key+" "+b.desc)
		}
	}
	return styleDim.Render(truncate(" "+strings.Join(parts, "  "), width))
}

// helpOverlay renders the full keymap, grouped by scope.
func helpOverlay(width, height int) string {
	groups := []struct {
		title string
		scope scope
	}{
		{"global", scopeGlobal},
		{"command table", scopeTable},
		{"log pane", scopeLogs},
		{"palette", scopePalette},
	}

	lines := []string{styleHeader.Render("lazycomd — keys")}
	for _, g := range groups {
		lines = append(lines, "", styleHeader.Render(g.title))
		for _, b := range bindings {
			if b.scope != g.scope {
				continue
			}
			lines = append(lines, "  "+cellPlain(b.key, 10)+b.desc)
		}
	}
	lines = append(lines,
		"",
		styleDim.Render("  a state shown as running* means the config changed;"),
		styleDim.Render("  the new spec applies on that command's next start"),
		"",
		styleDim.Render("  ?, esc or q closes this help"),
	)

	if len(lines) > height {
		lines = lines[:height]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, truncate(l, width))
	}
	return strings.Join(out, "\n")
}

// cellPlain pads without styling, for the help columns.
func cellPlain(s string, w int) string {
	if len([]rune(s)) >= w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-len([]rune(s)))
}
