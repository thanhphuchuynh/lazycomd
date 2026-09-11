package tui

import (
	"fmt"
	"strings"
)

// panelSpec describes one bordered panel. Every panel in the layout renders
// through panelView, so the border math lives in exactly one place.
type panelSpec struct {
	Title    string
	Subtitle string // right-hand hint in the title bar, dropped when it will not fit
	Number   int    // the key that focuses this panel; 0 for none
	Width    int
	Height   int // total, including both borders
	Rows     []string
	Focused  bool
}

// Border runes, light when blurred and heavy when focused.
type borderSet struct{ tl, tr, bl, br, h, v string }

var (
	lightBorder = borderSet{"╭", "╮", "╰", "╯", "─", "│"}
	heavyBorder = borderSet{"┏", "┓", "┗", "┛", "━", "┃"}
)

// panelView renders exactly Height lines, each exactly Width runes wide.
func panelView(s panelSpec) string {
	if s.Width < 2 || s.Height < 1 {
		return strings.TrimRight(strings.Repeat(strings.Repeat(" ", max(s.Width, 0))+"\n", max(s.Height, 0)), "\n")
	}

	b := lightBorder
	style := styleDim
	if s.Focused {
		b = heavyBorder
		style = styleHeader
	}

	// Height 1 is a collapsed panel: the title bar carries the whole thing.
	if s.Height == 1 {
		return style.Render(titleBar(s, b))
	}

	lines := []string{style.Render(titleBar(s, b))}

	// Height minus both border rows is how many rows of content fit.
	room := s.Height - 2
	for i := 0; i < room; i++ {
		var row string
		if i < len(s.Rows) {
			row = s.Rows[i]
		}
		lines = append(lines, style.Render(b.v)+pad(row, s.Width-2)+style.Render(b.v))
	}
	lines = append(lines, style.Render(b.bl+strings.Repeat(b.h, s.Width-2)+b.br))
	return strings.Join(lines, "\n")
}

// titleBar is the top border with the panel's number, title and subtitle in
// it. Whatever does not fit is dropped, subtitle first.
func titleBar(s panelSpec, b borderSet) string {
	label := " " + s.Title + " "
	if s.Number > 0 {
		label = fmt.Sprintf(" %d %s ", s.Number, s.Title)
	}

	room := s.Width - 3 // both corners and one leading horizontal rune
	if len(label) > room {
		label = truncate(label, room)
	}

	rest := room - len([]rune(label))
	tail := ""
	if s.Subtitle != "" && rest > len(s.Subtitle)+2 {
		tail = " " + s.Subtitle + " "
		rest -= len([]rune(tail))
	}
	return b.tl + b.h + label + strings.Repeat(b.h, max(rest, 0)) + tail + b.tr
}

// pad fits s to exactly w visible runes, padding with spaces or cutting.
//
// Rows arrive already styled — the table and the log viewport both emit ANSI
// escapes — so width has to be measured in what the terminal draws, not in
// bytes or runes. Counting escapes as characters cuts rows early and pushes
// the panel border out of line.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var (
		b       strings.Builder
		visible int
		inEsc   bool
		styled  bool
	)
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc, styled = true, true
			b.WriteRune(r)
		case inEsc:
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		default:
			if visible == w {
				continue // keep consuming, only to catch trailing escapes
			}
			b.WriteRune(r)
			visible++
		}
	}
	if styled {
		b.WriteString("\x1b[0m")
	}
	if n := w - visible; n > 0 {
		b.WriteString(strings.Repeat(" ", n))
	}
	return b.String()
}

// visibleWidth is what the terminal spends on s, ignoring escape sequences.
func visibleWidth(s string) int {
	var n int
	var inEsc bool
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		default:
			n++
		}
	}
	return n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
