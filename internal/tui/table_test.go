package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/manager"
)

func rows(names ...string) []manager.Status {
	out := make([]manager.Status, 0, len(names))
	for _, n := range names {
		out = append(out, manager.Status{Name: n, State: manager.Stopped})
	}
	return out
}

func key(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestSetRowsKeepsSelectionByName(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b", "c"))

	tbl, _ = tbl.Update(key("j")) // select b
	if got, _ := tbl.Selected(); got.Name != "b" {
		t.Fatalf("selected = %q, want b", got.Name)
	}

	// A new command sorts in ahead of b; the cursor must follow b, not index.
	tbl.SetRows(rows("a", "aa", "b", "c"))
	got, ok := tbl.Selected()
	if !ok || got.Name != "b" {
		t.Fatalf("selected = %q, want b after rows changed", got.Name)
	}
}

func TestSetRowsClampsWhenSelectionDisappears(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b", "c"))
	tbl, _ = tbl.Update(key("G")) // select c

	tbl.SetRows(rows("a", "b"))
	got, ok := tbl.Selected()
	if !ok || got.Name != "b" {
		t.Fatalf("selected = %q, want b (clamped)", got.Name)
	}
}

func TestSetRowsEmpty(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a"))
	tbl.SetRows(nil)
	if _, ok := tbl.Selected(); ok {
		t.Fatal("Selected() ok = true on an empty table")
	}
}

func TestCursorRespectsBounds(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b"))

	for i := 0; i < 5; i++ {
		tbl, _ = tbl.Update(key("k"))
	}
	if got, _ := tbl.Selected(); got.Name != "a" {
		t.Fatalf("selected = %q after 5 k, want a", got.Name)
	}
	for i := 0; i < 5; i++ {
		tbl, _ = tbl.Update(key("j"))
	}
	if got, _ := tbl.Selected(); got.Name != "b" {
		t.Fatalf("selected = %q after 5 j, want b", got.Name)
	}
}

func TestArrowKeysMoveToo(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b"))
	tbl, _ = tbl.Update(key("down"))
	if got, _ := tbl.Selected(); got.Name != "b" {
		t.Fatalf("selected = %q, want b", got.Name)
	}
	tbl, _ = tbl.Update(key("up"))
	if got, _ := tbl.Selected(); got.Name != "a" {
		t.Fatalf("selected = %q, want a", got.Name)
	}
}

func TestSelectName(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b", "c"))
	tbl.SelectName("c")
	if got, _ := tbl.Selected(); got.Name != "c" {
		t.Fatalf("selected = %q, want c", got.Name)
	}
	tbl.SelectName("ghost") // no such command: selection unchanged
	if got, _ := tbl.Selected(); got.Name != "c" {
		t.Fatalf("selected = %q, want c", got.Name)
	}
}

func TestMergeStatus(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b"))
	tbl.MergeStatus(manager.Status{Name: "b", State: manager.Running, PID: 42})

	if !strings.Contains(tbl.View(), "running") {
		t.Fatalf("View missing merged state:\n%s", tbl.View())
	}
	tbl.MergeStatus(manager.Status{Name: "ghost", State: manager.Running}) // must not panic
}

func TestNames(t *testing.T) {
	tbl := newTable()
	tbl.SetRows(rows("a", "b"))
	if got := tbl.Names(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Names = %v", got)
	}
}

func TestViewColumnsFullAndCompact(t *testing.T) {
	tbl := newTable()
	tbl.SetRows([]manager.Status{{Name: "tick", State: manager.Running, PID: 54405, UptimeSec: 133, Restarts: 2}})

	tbl.SetSize(40, 10, false) // 100-col terminal
	full := tbl.View()
	for _, want := range []string{"NAME", "STATE", "PID", "UPTIME", "RS", "tick", "running", "54405", "2m13s"} {
		if !strings.Contains(full, want) {
			t.Fatalf("full view missing %q:\n%s", want, full)
		}
	}

	tbl.SetSize(28, 10, true) // 70-col terminal
	compact := tbl.View()
	if !strings.Contains(compact, "NAME") || !strings.Contains(compact, "STATE") {
		t.Fatalf("compact view missing NAME/STATE:\n%s", compact)
	}
	for _, gone := range []string{"PID", "UPTIME", "RS"} {
		if strings.Contains(compact, gone) {
			t.Fatalf("compact view still has %q:\n%s", gone, compact)
		}
	}
}

func TestViewMarksCursorAndDirtySpec(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(40, 10, false)
	tbl.SetRows([]manager.Status{
		{Name: "a", State: manager.Running, SpecDirty: true},
		{Name: "b", State: manager.Stopped},
	})

	view := tbl.View()
	if !strings.Contains(view, "> a") {
		t.Fatalf("cursor marker missing:\n%s", view)
	}
	if !strings.Contains(view, "running*") {
		t.Fatalf("spec-dirty asterisk missing:\n%s", view)
	}
}

func TestViewScrollsToKeepCursorVisible(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(40, 4, false) // header plus 3 rows
	tbl.SetRows(rows("r0", "r1", "r2", "r3", "r4", "r5"))
	tbl, _ = tbl.Update(key("G"))

	view := tbl.View()
	if !strings.Contains(view, "r5") {
		t.Fatalf("last row not visible after G:\n%s", view)
	}
	if strings.Contains(view, "r0") {
		t.Fatalf("window did not scroll:\n%s", view)
	}
}

func TestFormatUptime(t *testing.T) {
	cases := map[float64]string{
		0:    "-",
		45:   "45s",
		133:  "2m13s",
		3840: "1h4m",
	}
	for in, want := range cases {
		if got := formatUptime(in); got != want {
			t.Fatalf("formatUptime(%v) = %q, want %q", in, got, want)
		}
	}
}
