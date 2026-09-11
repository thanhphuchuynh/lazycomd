package tui

import (
	"strings"
	"testing"

	"github.com/tphuc/lazycomd/internal/manager"
)

// runeWidth is what the terminal actually spends on a line, ignoring the
// escape sequences lipgloss adds.
func runeWidth(line string) int {
	var n int
	var inEsc bool
	for _, r := range line {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc && (r == 'm' || r == 'K'):
			inEsc = false
		case !inEsc:
			n++
		}
	}
	return n
}

func TestPanelExactSize(t *testing.T) {
	got := panelView(panelSpec{
		Title:  "Commands",
		Number: 2,
		Width:  30,
		Height: 6,
		Rows:   []string{"one", "two"},
	})
	lines := strings.Split(got, "\n")

	if len(lines) != 6 {
		t.Fatalf("got %d lines, want exactly 6:\n%s", len(lines), got)
	}
	for i, l := range lines {
		if w := runeWidth(l); w != 30 {
			t.Fatalf("line %d is %d wide, want 30: %q", i, w, l)
		}
	}
}

func TestPanelShowsTitleAndNumber(t *testing.T) {
	got := panelView(panelSpec{Title: "Ports", Number: 3, Width: 30, Height: 4})
	if !strings.Contains(got, "3") || !strings.Contains(got, "Ports") {
		t.Fatalf("title bar missing the number or the title:\n%s", got)
	}
}

func TestPanelFocusChangesTheBorder(t *testing.T) {
	spec := panelSpec{Title: "Commands", Number: 2, Width: 30, Height: 4}
	blurred := panelView(spec)
	spec.Focused = true
	focused := panelView(spec)

	if blurred == focused {
		t.Fatal("a focused panel must look different from a blurred one")
	}
	if !strings.Contains(focused, "┃") {
		t.Fatalf("focused panel should use the heavy border:\n%s", focused)
	}
	if strings.Contains(blurred, "┃") {
		t.Fatalf("blurred panel should use the light border:\n%s", blurred)
	}
}

func TestPanelPadsAndTruncatesRows(t *testing.T) {
	got := panelView(panelSpec{
		Title:  "T",
		Width:  14,
		Height: 5,
		Rows:   []string{"x", strings.Repeat("y", 100)},
	})
	for i, l := range strings.Split(got, "\n") {
		if w := runeWidth(l); w != 14 {
			t.Fatalf("line %d is %d wide, want 14: %q", i, w, l)
		}
	}
}

func TestPanelDropsRowsPastItsHeight(t *testing.T) {
	rows := []string{"a", "b", "c", "d", "e", "f"}
	got := panelView(panelSpec{Title: "T", Width: 20, Height: 5, Rows: rows})

	if strings.Contains(got, "d") {
		t.Fatalf("row past the panel's height was rendered:\n%s", got)
	}
	if !strings.Contains(got, "c") {
		t.Fatalf("row that fits was dropped:\n%s", got)
	}
}

func TestPanelTitleTruncates(t *testing.T) {
	got := panelView(panelSpec{Title: strings.Repeat("long ", 20), Number: 1, Width: 20, Height: 3})
	for _, l := range strings.Split(got, "\n") {
		if w := runeWidth(l); w != 20 {
			t.Fatalf("title bar is %d wide, want 20: %q", w, l)
		}
	}
}

func TestPanelSubtitleSitsInTheTitleBar(t *testing.T) {
	got := panelView(panelSpec{Title: "Ports", Number: 3, Subtitle: "4 listening", Width: 40, Height: 4})
	if !strings.Contains(got, "4 listening") {
		t.Fatalf("subtitle missing:\n%s", got)
	}
}

func TestPanelTooSmallStillRenders(t *testing.T) {
	// A pathological size must not panic or produce negative padding.
	for _, spec := range []panelSpec{
		{Title: "T", Width: 1, Height: 1},
		{Title: "T", Width: 0, Height: 0},
		{Title: "T", Width: 4, Height: 2, Rows: []string{"xxxx"}},
	} {
		_ = panelView(spec)
	}
}

func TestPanelKeepsStyledRowsInsideTheBorder(t *testing.T) {
	// Rows arrive styled; the escapes must not be counted as width.
	styled := stateStyles[manager.Running].Render("running") + "  " + styleDim.Render("54405")
	got := panelView(panelSpec{Title: "T", Width: 24, Height: 4, Rows: []string{styled}})

	for i, l := range strings.Split(got, "\n") {
		if w := runeWidth(l); w != 24 {
			t.Fatalf("line %d is %d visible runes, want 24: %q", i, w, l)
		}
	}
	if !strings.Contains(got, "running") {
		t.Fatalf("styled content was cut away:\n%s", got)
	}
}

func TestVisibleWidthIgnoresEscapes(t *testing.T) {
	if got := visibleWidth(styleDim.Render("abc")); got != 3 {
		t.Fatalf("visibleWidth = %d, want 3", got)
	}
	if got := visibleWidth("abc"); got != 3 {
		t.Fatalf("visibleWidth = %d, want 3", got)
	}
}
