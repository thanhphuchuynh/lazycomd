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
	snap   probe.Snapshot
	offset int
	width  int
	height int
	now    func() time.Time
}

func newSystem() systemModel {
	return systemModel{now: time.Now}
}

func (s *systemModel) SetSize(w, h int) {
	s.width, s.height = w, h
}

func (s *systemModel) SetSnapshot(snap probe.Snapshot) {
	s.snap = snap
	if s.offset >= len(snap.Ports) {
		s.offset = 0
	}
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
		s.offset++
	case "k", "up":
		s.offset--
	case "ctrl+d":
		s.offset += page
	case "ctrl+u":
		s.offset -= page
	case "g":
		s.offset = 0
	case "G":
		s.offset = len(s.snap.Ports)
	default:
		return s, nil
	}
	s.clamp()
	return s, nil
}

func (s *systemModel) clamp() {
	max := len(s.snap.Ports) - s.rowRoom()
	if s.offset > max {
		s.offset = max
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
