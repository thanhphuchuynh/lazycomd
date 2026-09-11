package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/probe"
)

// staleAfter is when a sample stops being worth trusting silently.
const staleAfter = 15 * time.Second

// Ports view column widths.
const (
	pcolPort    = 7
	pcolAddr    = 11
	pcolPID     = 8
	pcolProcess = 13
)

type systemModel struct {
	focused bool
	snap    probe.Snapshot
	cursor  int
	offset  int
	width   int
	height  int
	now     func() time.Time
}

func newSystem() systemModel {
	return systemModel{now: time.Now}
}

func (s *systemModel) SetSize(w, h int) {
	s.width, s.height = w, h
}

// SetFocused controls whether this panel draws a live cursor.
func (s *systemModel) SetFocused(b bool) { s.focused = b }

// SetSnapshot replaces the sample, keeping the cursor on the same port where
// it still exists so a refresh never moves the selection under you.
func (s *systemModel) SetSnapshot(snap probe.Snapshot) {
	var was probe.Port
	if sel, ok := s.Selected(); ok {
		was = sel
	}
	s.snap = snap

	if was.Port != 0 {
		for i, p := range snap.Ports {
			if p.Port == was.Port && p.Addr == was.Addr && p.PID == was.PID {
				s.cursor = i
				s.clamp()
				return
			}
		}
	}
	s.clampCursor()
	s.clamp()
}

// ClickRow puts the cursor on the nth port row currently drawn.
func (s *systemModel) ClickRow(row int) {
	if i := s.offset + row; i < len(s.snap.Ports) {
		s.cursor = i
		s.clampCursor()
		s.clamp()
	}
}

// SelectPort puts the cursor on one port, for a jump from the search box.
func (s *systemModel) SelectPort(want probe.Port) {
	for i, p := range s.snap.Ports {
		if p.Port == want.Port && p.Addr == want.Addr && p.PID == want.PID {
			s.cursor = i
			s.clamp()
			return
		}
	}
}

// Selected is the port under the cursor.
func (s systemModel) Selected() (probe.Port, bool) {
	if s.cursor < 0 || s.cursor >= len(s.snap.Ports) {
		return probe.Port{}, false
	}
	return s.snap.Ports[s.cursor], true
}

func (s *systemModel) clampCursor() {
	if n := len(s.snap.Ports); s.cursor >= n {
		s.cursor = n - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// PanelRows is the port list as the panel body: one row per listener, the
// contested ones marked.
func (s systemModel) PanelRows() []string {
	if msg := s.snap.Errors["ports"]; msg != "" {
		return []string{"⚠ unavailable", truncate(msg, s.width-2)}
	}
	if len(s.snap.Ports) == 0 {
		return []string{"nothing listening"}
	}

	contested := s.contestedPorts()
	rows := make([]string, 0, len(s.snap.Ports))
	for i, p := range s.snap.Ports {
		marker := " "
		if contested[p.Port] {
			marker = "⚠"
		}
		cursor := " "
		if i == s.cursor {
			cursor = strings.TrimSuffix(cursorMark(s.focused), " ")
		}
		rows = append(rows, fmt.Sprintf("%s%s%-6d %-10s %s", cursor, marker, p.Port, truncate(p.Process, 10), p.Command))
	}
	return rows
}

// Subtitle is the freshness hint the panel shows in its title bar.
func (s systemModel) Subtitle() string {
	if s.snap.Errors["ports"] != "" {
		return "unavailable"
	}
	at, ok := s.snap.SampledAt["ports"]
	if !ok || at.IsZero() {
		return "sampling"
	}
	age := s.now().Sub(at).Round(time.Second)
	if age >= staleAfter {
		return fmt.Sprintf("stale %ds", int(age.Seconds()))
	}
	// The tag has to survive a narrow sidebar, where the panel title bar
	// drops a subtitle it cannot fit whole.
	n := len(s.snap.Ports)
	if c := len(s.contestedPorts()); c > 0 {
		if s.width < 34 {
			return fmt.Sprintf("view · %d, %d ⚠", n, c)
		}
		return fmt.Sprintf("view only · %d, %d ⚠", n, c)
	}
	if s.width < 34 {
		return fmt.Sprintf("view · %d", n)
	}
	return fmt.Sprintf("view only · %d listening", n)
}

func (s systemModel) contestedPorts() map[int]bool {
	out := make(map[int]bool, len(s.snap.Conflicts))
	for _, c := range s.snap.Conflicts {
		if c.State == "taken" {
			out[c.Port] = true
		}
	}
	return out
}

// Detail is the main pane while the ports panel has focus: everything known
// about the selected listener, and the conflict if it has one.
func (s systemModel) Detail() []string {
	if msg := s.snap.Errors["ports"]; msg != "" {
		return []string{"ports unavailable", "", "  " + msg, "", "  Install lsof, or ss on Linux, to fill this panel.", "  The other collectors keep working without it."}
	}
	sel, ok := s.Selected()
	if !ok {
		return []string{"nothing listening"}
	}

	owner := sel.Command
	if owner == "" {
		owner = "not a lazycomd command"
	}
	out := []string{
		fmt.Sprintf("port %d", sel.Port),
		"  address   " + sel.Addr,
		"  process   " + sel.Process,
		fmt.Sprintf("  pid       %d", sel.PID),
		"  owner     " + owner,
	}
	for _, c := range s.snap.Conflicts {
		if c.Port != sel.Port {
			continue
		}
		out = append(out, "", "conflict", "  "+conflictLine(c))
	}
	out = append(out, "", styleDim.Render("view only — every listener on this machine, not just"),
		styleDim.Render("lazycomd's. Start and stop act on commands, in panel 2."))
	return out
}

// Update handles scrolling only; opening and closing is the root's business.
func (s systemModel) Update(msg tea.Msg) (systemModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	page := s.rowRoom() / 2
	if page < 1 {
		page = 1
	}
	switch k.String() {
	case "j", "down":
		s.cursor++
	case "k", "up":
		s.cursor--
	case "ctrl+d":
		s.cursor += page
	case "ctrl+u":
		s.cursor -= page
	case "g":
		s.cursor = 0
	case "G":
		s.cursor = len(s.snap.Ports) - 1
	default:
		return s, nil
	}
	s.clampCursor()
	s.clamp()
	return s, nil
}

// clamp scrolls the window so the cursor stays visible.
func (s *systemModel) clamp() {
	room := s.rowRoom()
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+room {
		s.offset = s.cursor - room + 1
	}
	if limit := len(s.snap.Ports) - room; s.offset > limit {
		s.offset = limit
	}
	if s.offset < 0 {
		s.offset = 0
	}
}

// rowRoom is how many port rows fit under the title, the conflict lines and
// the column header.
func (s systemModel) rowRoom() int {
	room := s.height - 2 - len(s.snap.Conflicts)
	if room < 1 {
		room = 1
	}
	return room
}

// title reports the sample's age, and says so loudly once it is stale.
func (s systemModel) title() string {
	at, ok := s.snap.SampledAt["ports"]
	if !ok || at.IsZero() {
		return "ports — waiting for the first sample"
	}
	age := s.now().Sub(at).Round(time.Second)
	if age >= staleAfter {
		return fmt.Sprintf("ports — stale %ds", int(age.Seconds()))
	}
	return fmt.Sprintf("ports — sampled %ds ago", int(age.Seconds()))
}

func (s systemModel) View() string {
	lines := []string{styleHeader.Render(truncate(s.title(), s.width))}

	if msg := s.snap.Errors["ports"]; msg != "" {
		lines = append(lines, styleWarn.Render(truncate("ports unavailable: "+msg, s.width)))
		return strings.Join(lines, "\n")
	}

	for _, c := range s.snap.Conflicts {
		lines = append(lines, styleWarn.Render(truncate(conflictLine(c), s.width)))
	}

	if len(s.snap.Ports) == 0 {
		lines = append(lines, styleDim.Render("nothing listening"))
		return strings.Join(lines, "\n")
	}

	header := " " + cell("PORT", pcolPort, styleHeader) +
		cell("ADDR", pcolAddr, styleHeader) +
		cell("PID", pcolPID, styleHeader) +
		cell("PROCESS", pcolProcess, styleHeader) +
		"OWNER"
	lines = append(lines, truncate(header, s.width))

	contested := make(map[int]bool, len(s.snap.Conflicts))
	for _, c := range s.snap.Conflicts {
		if c.State == "taken" {
			contested[c.Port] = true
		}
	}

	end := s.offset + s.rowRoom()
	if end > len(s.snap.Ports) {
		end = len(s.snap.Ports)
	}
	for _, p := range s.snap.Ports[s.offset:end] {
		mark := " "
		if contested[p.Port] {
			mark = "⚠"
		}
		row := mark + cell(fmt.Sprintf("%d", p.Port), pcolPort, lipglossPlain()) +
			cell(p.Addr, pcolAddr, styleDim) +
			cell(fmt.Sprintf("%d", p.PID), pcolPID, styleDim) +
			cell(p.Process, pcolProcess, lipglossPlain()) +
			p.Command
		lines = append(lines, truncate(row, s.width))
	}
	return strings.Join(lines, "\n")
}

// conflictLine explains one contested or missing listener in one sentence.
func conflictLine(c probe.Conflict) string {
	if c.State == "free" {
		return fmt.Sprintf("⚠ %d wanted by %s — nothing is listening", c.Port, c.Command)
	}
	return fmt.Sprintf("⚠ %d wanted by %s — held by %s (pid %d)", c.Port, c.Command, c.HeldBy, c.PID)
}
