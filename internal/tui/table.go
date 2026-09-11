package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tphuc/lazycomd/internal/manager"
)

// Styles shared across the package.
var (
	styleHeader = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	stateStyles = map[manager.State]lipgloss.Style{
		manager.Running:  lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		manager.Failed:   lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		manager.Starting: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		manager.Stopping: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		manager.Stopped:  lipgloss.NewStyle().Faint(true),
	}
)

// Fixed column widths; the name column takes whatever is left.
const (
	colState  = 10
	colPID    = 7
	colUptime = 8
	colRS     = 3
)

type tableModel struct {
	rows     []manager.Status
	selected string // command name, never an index
	cursor   int
	width    int
	height   int
	compact  bool
}

func newTable() tableModel { return tableModel{} }

func (t *tableModel) SetSize(w, h int, compact bool) {
	t.width, t.height, t.compact = w, h, compact
}

// SetRows replaces every row, keeping the cursor on the same command name.
// A selection that no longer exists clamps to the nearest index.
func (t *tableModel) SetRows(rows []manager.Status) {
	t.rows = rows
	if t.selected != "" {
		for i, r := range rows {
			if r.Name == t.selected {
				t.cursor = i
				return
			}
		}
	}
	t.clampCursor()
}

func (t *tableModel) clampCursor() {
	if len(t.rows) == 0 {
		t.cursor, t.selected = 0, ""
		return
	}
	if t.cursor >= len(t.rows) {
		t.cursor = len(t.rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	t.selected = t.rows[t.cursor].Name
}

func (t tableModel) Selected() (manager.Status, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return manager.Status{}, false
	}
	return t.rows[t.cursor], true
}

// SelectName moves the cursor to name when that command exists.
func (t *tableModel) SelectName(name string) {
	for i, r := range t.rows {
		if r.Name == name {
			t.cursor, t.selected = i, name
			return
		}
	}
}

// MergeStatus updates one row in place, ahead of the next poll.
func (t *tableModel) MergeStatus(st manager.Status) {
	for i, r := range t.rows {
		if r.Name == st.Name {
			t.rows[i] = st
			return
		}
	}
}

func (t tableModel) Names() []string {
	out := make([]string, 0, len(t.rows))
	for _, r := range t.rows {
		out = append(out, r.Name)
	}
	return out
}

// Update handles navigation only. Lifecycle keys live in the root model.
func (t tableModel) Update(msg tea.Msg) (tableModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return t, nil
	}
	switch k.String() {
	case "j", "down":
		t.cursor++
	case "k", "up":
		t.cursor--
	case "g":
		t.cursor = 0
	case "G":
		t.cursor = len(t.rows) - 1
	default:
		return t, nil
	}
	t.clampCursor()
	return t, nil
}

// window returns the visible row range, scrolled to keep the cursor in view.
func (t tableModel) window() (int, int) {
	h := t.height - 1 // the header takes a line
	if h < 1 {
		h = 1
	}
	start := 0
	if t.cursor >= h {
		start = t.cursor - h + 1
	}
	end := start + h
	if end > len(t.rows) {
		end = len(t.rows)
	}
	return start, end
}

func (t tableModel) nameWidth() int {
	w := t.width - 2 - colState // 2 for the cursor marker
	if !t.compact {
		w -= colPID + colUptime + colRS
	}
	if w < 8 {
		w = 8
	}
	return w
}

func (t tableModel) View() string {
	nameW := t.nameWidth()

	var b strings.Builder
	header := "  " + cell("NAME", nameW, styleHeader) + cell("STATE", colState, styleHeader)
	if !t.compact {
		header += cell("PID", colPID, styleHeader) + cell("UPTIME", colUptime, styleHeader) + cell("RS", colRS, styleHeader)
	}
	b.WriteString(header)

	start, end := t.window()
	for i := start; i < end; i++ {
		r := t.rows[i]
		marker := "  "
		if i == t.cursor {
			marker = "> "
		}
		state := string(r.State)
		if r.SpecDirty {
			state += "*"
		}
		line := marker +
			cell(r.Name, nameW, lipgloss.NewStyle()) +
			cell(state, colState, stateStyles[r.State])
		if !t.compact {
			pid := "-"
			if r.PID > 0 {
				pid = fmt.Sprintf("%d", r.PID)
			}
			line += cell(pid, colPID, styleDim) +
				cell(formatUptime(r.UptimeSec), colUptime, styleDim) +
				cell(fmt.Sprintf("%d", r.Restarts), colRS, styleDim)
		}
		b.WriteString("\n" + line)
	}
	return b.String()
}

// cell renders one fixed-width column: padded when short, cut when long.
func cell(s string, w int, st lipgloss.Style) string {
	return st.Width(w).MaxWidth(w).Render(s)
}

// truncate cuts s to w runes, marking a cut with an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

func formatUptime(sec float64) string {
	if sec <= 0 {
		return "-"
	}
	d := time.Duration(sec * float64(time.Second)).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
