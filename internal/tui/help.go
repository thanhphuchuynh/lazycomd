package tui

import "strings"

// focus is which panel takes keys. The number is also the key that jumps to
// it, so panel 1 is Status, 2 is Commands, 3 is Ports.
type focus int

const (
	focusStatus focus = iota
	focusCommands
	focusPorts
	focusCount
)

// title is what the panel calls itself.
func (f focus) title() string {
	switch f {
	case focusStatus:
		return "Status"
	case focusPorts:
		return "Ports"
	default:
		return "Commands"
	}
}

// next cycles forward, wrapping.
func (f focus) next() focus { return (f + 1) % focusCount }

// prev cycles backward, wrapping.
func (f focus) prev() focus { return (f + focusCount - 1) % focusCount }

// overlay is a full-pane layer drawn over the body.
type overlay int

const (
	overlayNone overlay = iota
	overlayHelp
	overlayPalette
)

// scope is where a binding applies.
type scope int

const (
	scopeGlobal scope = iota
	scopeCommands
	scopePorts
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
	{"j/k", "move", scopeCommands},
	{"g/G", "first/last", scopeCommands},
	{"s", "start", scopeCommands},
	{"S", "stop", scopeCommands},
	{"r", "restart", scopeCommands},
	{"p", "palette", scopeCommands},
	{"j/k", "move", scopePorts},
	{"g/G", "first/last", scopePorts},
	{"enter", "start and select", scopePalette},
	{"esc", "close palette", scopePalette},
	{"1-3", "panel", scopeGlobal},
	{"tab", "cycle panels", scopeGlobal},
	{"ctrl+d/u", "scroll main", scopeGlobal},
	{"f", "follow", scopeGlobal},
	{"/", "filter logs", scopeGlobal},
	{"?", "help", scopeGlobal},
	{"q", "quit", scopeGlobal},
}

func scopeFor(f focus) scope {
	switch f {
	case focusPorts:
		return scopePorts
	default:
		return scopeCommands
	}
}

// keyBar renders the bottom hint line for the focused pane. The global keys
// are laid out first so a narrow terminal drops pane-specific hints rather
// than "? help" and "q quit", which are the ones you need when lost.
func keyBar(width int, f focus) string {
	want := scopeFor(f)
	var scoped, global []string
	for _, b := range bindings {
		entry := b.key + " " + b.desc
		switch b.scope {
		case scopeGlobal:
			global = append(global, entry)
		case want:
			scoped = append(scoped, entry)
		}
	}

	tail := strings.Join(global, "  ")
	room := width - len([]rune(tail)) - 3 // leading space and separator

	head := ""
	for _, entry := range scoped {
		next := entry
		if head != "" {
			next = head + "  " + entry
		}
		if len([]rune(next)) > room {
			break
		}
		head = next
	}

	bar := " " + head
	if head != "" {
		bar += "  "
	}
	return styleDim.Render(truncate(bar+tail, width))
}

// helpOverlay renders the full keymap, grouped by scope.
func helpOverlay(width, height int) string {
	groups := []struct {
		title string
		scope scope
	}{
		{"global", scopeGlobal},
		{"2 Commands", scopeCommands},
		{"3 Ports", scopePorts},
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
		styleDim.Render("  1 Status, 2 Commands, 3 Ports; the right-hand pane"),
		styleDim.Render("  follows whichever panel has focus"),
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
