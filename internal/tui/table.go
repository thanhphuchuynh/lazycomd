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
	colHealth = 2
	colState  = 10
	colPID    = 7
	colUptime = 8
	colRS     = 3
	colCPU    = 6
	colMEM    = 7
)

// tableLayout is how much room the table has and how much it may show.
type tableLayout struct {
	Width   int
	Height  int
	Compact bool // NAME and STATE only
	Wide    bool // room for CPU and MEM
}

type tableModel struct {
	rows     []manager.Status
	selected string // command name, never an index
	cursor   int
	width    int
	height   int
	compact  bool
	wide     bool
}

func newTable() tableModel { return tableModel{} }

func (t *tableModel) SetLayout(l tableLayout) {
	t.width, t.height = l.Width, l.Height
	t.compact, t.wide = l.Compact, l.Wide
}

// showHealth reports whether any command has a health result to show. The
// column stays hidden in a config with no health checks.
func (t tableModel) showHealth() bool {
	for _, r := range t.rows {
		if r.Health != nil {
			return true
		}
	}
	return false
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
	if t.showHealth() {
		w -= colHealth
	}
	if !t.compact {
		w -= colPID + colUptime + colRS
		if t.wide {
			w -= colCPU + colMEM
		}
	}
	if w < 8 {
		w = 8
	}
	return w
}

func (t tableModel) View() string {
	nameW := t.nameWidth()

	health := t.showHealth()

	var b strings.Builder
	header := "  "
	if health {
		header += cell("H", colHealth, styleHeader)
	}
	header += cell("NAME", nameW, styleHeader) + cell("STATE", colState, styleHeader)
	if !t.compact {
		header += cell("PID", colPID, styleHeader) + cell("UPTIME", colUptime, styleHeader) + cell("RS", colRS, styleHeader)
		if t.wide {
			header += cell("CPU", colCPU, styleHeader) + cell("MEM", colMEM, styleHeader)
		}
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
		line := marker
		if health {
			line += cell(healthDot(r.Health), colHealth, healthStyle(r.Health))
		}
		line += cell(r.Name, nameW, lipglossPlain()) +
			cell(state, colState, stateStyles[r.State])
		if !t.compact {
			pid := "-"
			if r.PID > 0 {
				pid = fmt.Sprintf("%d", r.PID)
			}
			line += cell(pid, colPID, styleDim) +
				cell(formatUptime(r.UptimeSec), colUptime, styleDim) +
				cell(fmt.Sprintf("%d", r.Restarts), colRS, styleDim)
			if t.wide {
				line += cell(formatCPU(r.CPU), colCPU, styleDim) +
					cell(formatMem(r.MemMB), colMEM, styleDim)
			}
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

// lipglossPlain is an unstyled style, for cells that only need padding.
func lipglossPlain() lipgloss.Style { return lipgloss.NewStyle() }

// healthDot is filled for a healthy service, hollow for an unhealthy one, and
// blank for a command that configured no check.
func healthDot(h *manager.HealthView) string {
	switch {
	case h == nil:
		return ""
	case h.OK:
		return "●"
	default:
		return "○"
	}
}

func healthStyle(h *manager.HealthView) lipgloss.Style {
	switch {
	case h == nil:
		return styleDim
	case h.OK:
		return stateStyles[manager.Running]
	default:
		return stateStyles[manager.Failed]
	}
}

// formatCPU shows whole percent; it is percent of one core, so past 100 is
// real and is not clamped.
func formatCPU(cpu float64) string {
	if cpu <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", cpu)
}

func formatMem(mb float64) string {
	switch {
	case mb <= 0:
		return "-"
	case mb < 1024:
		return fmt.Sprintf("%.0fM", mb)
	default:
		return fmt.Sprintf("%.1fG", mb/1024)
	}
}
