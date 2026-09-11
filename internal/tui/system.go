package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
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
	filter  filterState
	cursor  int
	offset  int
	width   int
	height  int
	now     func() time.Time
}

func newSystem() systemModel {
	return systemModel{now: time.Now, filter: newFilter()}
}

// ports is the visible list: every listener, or only the ones matching the
// filter. The cursor indexes this, not the raw sample, so the detail pane and
// the selection follow what you can actually see.
func (s systemModel) ports() []probe.Port {
	if s.filter.query == "" {
		return s.snap.Ports
	}
	out := make([]probe.Port, 0, len(s.snap.Ports))
	for _, p := range s.snap.Ports {
		if smartContains(portHaystack(p), s.filter.query) {
			out = append(out, p)
		}
	}
	return out
}

// portHaystack is every field worth searching on one line: you look for a
// port by number as often as by the process holding it.
func portHaystack(p probe.Port) string {
	return fmt.Sprintf("%d %s %s %d %s", p.Port, p.Addr, p.Process, p.PID, p.Command)
}

func (s systemModel) FilterEditing() bool { return s.filter.editing }
func (s systemModel) Query() string       { return s.filter.query }

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
		for i, p := range visible(snap, s.filter.query) {
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

// Selected is the port under the cursor.
func (s systemModel) Selected() (probe.Port, bool) {
	ports := s.ports()
	if s.cursor < 0 || s.cursor >= len(ports) {
		return probe.Port{}, false
	}
	return ports[s.cursor], true
}

func (s *systemModel) clampCursor() {
	if n := len(s.ports()); s.cursor >= n {
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
	ports := s.ports()
	if len(ports) == 0 {
		return append(s.filterRows(), s.emptyLine())
	}

	contested := s.contestedPorts()
	rows := s.filterRows()
	for i, p := range ports {
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
	n := len(s.snap.Ports)
	if s.filter.query != "" {
		return fmt.Sprintf("filter %q · %d of %d", s.filter.query, len(s.ports()), n)
	}
	if c := len(s.contestedPorts()); c > 0 {
		return fmt.Sprintf("%d, %d ⚠", n, c)
	}
	return fmt.Sprintf("%d listening", n)
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
		return []string{s.emptyLine()}
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
	return out
}

// Update handles scrolling only; opening and closing is the root's business.
func (s systemModel) Update(msg tea.Msg) (systemModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	if s.filter.editing {
		switch k.String() {
		case "enter":
			s.filter.query = s.filter.input.Value()
			s.filter.editing = false
			s.filter.input.Blur()
		case "esc":
			s.filter.editing = false
			s.filter.input.Blur()
			s.filter.query = ""
		default:
			var cmd tea.Cmd
			s.filter.input, cmd = s.filter.input.Update(msg)
			return s, cmd
		}
		s.clampCursor()
		s.clamp()
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
	case "/":
		s.filter.editing = true
		s.filter.input.Reset()
		s.filter.input.Focus()
		return s, textinput.Blink
	case "esc":
		if s.filter.query == "" {
			return s, nil
		}
		s.filter.query = ""
	case "g":
		s.cursor = 0
	case "G":
		s.cursor = len(s.ports()) - 1
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
	if limit := len(s.ports()) - room; s.offset > limit {
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

	ports := s.ports()
	if len(ports) == 0 {
		lines = append(lines, styleDim.Render(s.emptyLine()))
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
	if end > len(ports) {
		end = len(ports)
	}
	for _, p := range ports[s.offset:end] {
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

// filterRows is the filter input, shown only while you are typing it. Once
// committed, the query lives in the panel subtitle instead of a whole row.
func (s systemModel) filterRows() []string {
	if !s.filter.editing {
		return nil
	}
	return []string{s.filter.input.View()}
}

// emptyLine distinguishes "nothing is listening" from "nothing matched",
// which are very different things to see after typing a query.
func (s systemModel) emptyLine() string {
	if s.filter.query != "" {
		return fmt.Sprintf("no port matches %q", s.filter.query)
	}
	return "nothing listening"
}

// visible is ports() against a snapshot the model has not adopted yet, used
// while restoring the cursor onto an incoming sample.
func visible(snap probe.Snapshot, query string) []probe.Port {
	return systemModel{snap: snap, filter: filterState{query: query}}.ports()
}
