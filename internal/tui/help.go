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
	overlaySearch
	overlayForm
	overlayConfirm
	overlayLog
	overlayStartDaemon
)

// scope is where a binding applies.
type scope int

const (
	scopeGlobal scope = iota
	scopeCommands
	scopePorts
	scopeSearch
	scopeLog
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
	{"i", "detail", scopeCommands},
	{"o", "open the full log", scopeCommands},
	{"a", "add", scopeCommands},
	{"e", "edit", scopeCommands},
	{"d", "delete", scopeCommands},
	{"P", "project, then s/S/r", scopeCommands},
	{"j/k", "move", scopePorts},
	{"g/G", "first/last", scopePorts},
	{"enter", "go to the match", scopeSearch},
	{"ctrl+n/p", "next/prev match", scopeSearch},
	{"esc", "close search", scopeSearch},
	{"/", "filter these lines", scopeLog},
	{"f", "follow", scopeLog},
	{"ctrl+d/u", "scroll", scopeLog},
	{"esc", "clear filter, else close", scopeLog},
	{"o", "close", scopeLog},
	{"1-3", "panel", scopeGlobal},
	{"tab", "cycle panels", scopeGlobal},
	{"ctrl+d/u", "scroll main", scopeGlobal},
	{"/", "search", scopeGlobal},
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
func keyBar(width int, f focus) string { return keyBarScope(width, scopeFor(f)) }

// keyBarScope renders the hint line for one scope. An overlay names its own:
// inside the log view the panel keys do nothing, and showing them lies.
func keyBarScope(width int, want scope) string {
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
	if want == scopeSearch || want == scopeLog {
		// An overlay swallows the panel keys, so listing them would be a
		// lie; help is the only global that still works.
		global = []string{"? help"}
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

// helpOverlay renders the full keymap. It gives up content in a deliberate
// order — explanatory notes, then the blank lines between groups, then two
// columns if the terminal is wide — and never drops the footer, because the
// line telling you how to close it used to be the first thing truncated.
func helpOverlay(width, height int) string {
	notes := []string{
		styleDim.Render("● healthy · ○ not answering · blank means no health: url"),
		styleDim.Render("running* means the config changed; it applies on the"),
		styleDim.Render("command's next start"),
		styleDim.Render("the mouse works too: click a panel or a row to go"),
		styleDim.Render("there, the wheel scrolls whatever is under it"),
	}
	footer := styleDim.Render("?, esc or q closes this help")

	room := height - 3 // title, one blank, footer
	body := helpBody(true)

	if len(body)+len(notes)+1 > room {
		if width >= 72 {
			body = twoColumns(body, notes, width)
		}
		notes = nil
	}
	if len(body) > room {
		body = helpBody(false) // drop the blank lines between groups
	}
	if len(body) > room && width >= 72 {
		// Still too tall: fold the list itself into two columns rather than
		// cutting groups off the bottom, which is where the newest keys are.
		body = twoColumns(body[:splitAt(body)], body[splitAt(body):], width)
	}

	lines := []string{styleHeader.Render("lazycomd — keys"), ""}
	lines = append(lines, body...)
	if len(notes) > 0 {
		lines = append(lines, "")
		lines = append(lines, notes...)
	}

	if cut := height - 1; len(lines) > cut {
		lines = lines[:maxInt(cut-1, 1)]
		lines = append(lines, styleDim.Render("…"))
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, footer)

	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, truncate(l, width))
	}
	return strings.Join(out, "\n")
}

// helpBody is every binding, grouped. spaced controls whether the groups are
// separated by a blank line, which is the first thing sacrificed for room.
func helpBody(spaced bool) []string {
	groups := []struct {
		title string
		scope scope
	}{
		{"global", scopeGlobal},
		{"2 Commands", scopeCommands},
		{"3 Ports", scopePorts},
		{"search", scopeSearch},
		{"log view", scopeLog},
	}

	var out []string
	for i, g := range groups {
		if i > 0 && spaced {
			out = append(out, "")
		}
		out = append(out, styleHeader.Render(g.title))
		for _, b := range bindings {
			if b.scope != g.scope {
				continue
			}
			out = append(out, "  "+cellPlain(b.key, 10)+b.desc)
		}
	}
	return out
}

// twoColumns lays the keymap beside the notes when one column will not fit.
func twoColumns(left, right []string, width int) []string {
	half := width/2 - 1
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, pad(l, half)+" "+r)
	}
	return out
}

// cellPlain pads without styling, for the help columns.
func cellPlain(s string, w int) string {
	if len([]rune(s)) >= w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-len([]rune(s)))
}

// splitAt is where to break the binding list into two columns: the group
// header nearest the middle, so a group is never split across columns.
func splitAt(body []string) int {
	mid, best := len(body)/2, len(body)/2
	for i, line := range body {
		if strings.HasPrefix(line, " ") || line == "" {
			continue // an indented line is a binding, not a group header
		}
		if abs(i-mid) < abs(best-mid) {
			best = i
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
