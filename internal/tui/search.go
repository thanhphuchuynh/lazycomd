package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/probe"
)

// searchKind is which list an entry came from. It decides what enter does
// with it and what tag the row carries.
type searchKind int

const (
	kindCommand searchKind = iota
	kindPort
	kindLog
)

func (k searchKind) tag() string {
	switch k {
	case kindPort:
		return "port"
	case kindLog:
		return "log"
	default:
		return "cmd"
	}
}

// searchItem is one row: label is what the query matches against and what
// gets highlighted, detail is the dim half that only explains it.
type searchItem struct {
	kind      searchKind
	label     string
	detail    string
	name      string // the command to select, for cmd and log rows
	port      probe.Port
	score     int
	positions []int
}

// maxLogHits caps the log half of the results: a filter that fills the box
// with one command's scrollback buries the commands and ports.
const maxLogHits = 20

// labelW is the column the matched half of a row occupies.
const labelW = 16

type searchModel struct {
	input   textinput.Model
	items   []searchItem
	matches []searchItem
	cursor  int
}

func newSearch() searchModel {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.CharLimit = 120
	return searchModel{input: ti}
}

// Open collects everything searchable as it stands right now: commands,
// listening ports, and the log lines already in memory for the selected
// command. The daemon is not asked for anything — this opens instantly.
func (s *searchModel) Open(rows []manager.Status, ports []probe.Port, logName string, logLines []string) {
	s.items = s.items[:0]
	for _, r := range rows {
		detail := string(r.State)
		if r.PID > 0 {
			detail = fmt.Sprintf("%s · pid %d", r.State, r.PID)
		}
		s.items = append(s.items, searchItem{kind: kindCommand, label: r.Name, detail: detail, name: r.Name})
	}
	for _, p := range ports {
		s.items = append(s.items, searchItem{
			kind:   kindPort,
			label:  fmt.Sprintf("%d", p.Port),
			detail: portDetailLine(p),
			port:   p,
		})
	}
	for _, line := range logLines {
		s.items = append(s.items, searchItem{kind: kindLog, label: line, detail: logName, name: logName})
	}

	s.cursor = 0
	s.input.Reset()
	s.input.Focus()
	s.refilter()
}

func portDetailLine(p probe.Port) string {
	parts := []string{p.Addr, p.Process, fmt.Sprintf("pid %d", p.PID)}
	if p.Command != "" {
		parts = append(parts, p.Command)
	}
	return strings.Join(parts, " · ")
}

func (s *searchModel) Close() { s.input.Blur() }

// refilter scores every item against the query. A port matches on its
// number or on anything in its detail line, so "postgres" and "5432" both
// find the same row.
func (s *searchModel) refilter() {
	query := s.input.Value()
	out := make([]searchItem, 0, len(s.items))
	logs := 0
	for _, it := range s.items {
		score, pos, ok := match(query, it.label)
		if !ok {
			if score2, ok2 := detailMatch(query, it.detail); ok2 {
				score, pos = score2, nil
			} else {
				continue
			}
		}
		if it.kind == kindLog {
			if query == "" && logs >= maxLogHits {
				continue
			}
			logs++
			if logs > maxLogHits {
				continue
			}
		}
		it.score, it.positions = score+kindBonus(it.kind), pos
		out = append(out, it)
	}
	if query != "" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	}
	s.matches = out
	s.clampCursor()
}

// kindBonus breaks ties towards what you most often mean: a command, then a
// port, then a line of log.
func kindBonus(k searchKind) int {
	switch k {
	case kindCommand:
		return 6
	case kindPort:
		return 3
	default:
		return 0
	}
}

// detailMatch is the fallback for rows whose label does not contain the
// query but whose explanation does — a port found by its process name.
func detailMatch(query, detail string) (int, bool) {
	if query == "" || detail == "" {
		return 0, false
	}
	if !smartContains(detail, query) {
		return 0, false
	}
	return 1, true
}

func (s *searchModel) clampCursor() {
	if s.cursor >= len(s.matches) {
		s.cursor = len(s.matches) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// Selected is the row under the cursor.
func (s searchModel) Selected() (searchItem, bool) {
	if s.cursor < 0 || s.cursor >= len(s.matches) {
		return searchItem{}, false
	}
	return s.matches[s.cursor], true
}

// ClickRow puts the cursor on the nth match drawn in a box with room for
// that many rows: the box scrolls, so the first drawn row is not always the
// first match.
func (s *searchModel) ClickRow(row, room int) {
	if i := drawStart(s.cursor, room) + row; i >= 0 && i < len(s.matches) {
		s.cursor = i
	}
}

// drawStart is the index of the topmost row drawn, for a cursor and a box.
func drawStart(cursor, room int) int {
	if room < 1 || cursor < room {
		return 0
	}
	return cursor - room + 1
}

// Query is what was typed, which a log result carries into the log filter.
func (s searchModel) Query() string { return s.input.Value() }

// Update handles typing and navigation. The root intercepts enter and esc.
func (s searchModel) Update(msg tea.Msg) (searchModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	switch k.String() {
	case "down", "ctrl+n":
		s.cursor++
		s.clampCursor()
		return s, nil
	case "up", "ctrl+p":
		s.cursor--
		s.clampCursor()
		return s, nil
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	s.refilter()
	return s, cmd
}

func (s searchModel) View(width, height int) string {
	lines := []string{truncate(s.input.View(), width)}
	if len(s.matches) == 0 {
		return strings.Join(append(lines, styleDim.Render("no match")), "\n")
	}

	room := maxInt(height-1, 1)
	start := drawStart(s.cursor, room)
	for i := start; i < len(s.matches) && i-start < room; i++ {
		m := s.matches[i]
		marker := "  "
		if i == s.cursor {
			marker = "> "
		}
		// Labels are padded to a column so the dim half lines up; a ragged
		// right edge made the three kinds hard to read as one list.
		label := pad(highlightMatch(truncate(m.label, labelW), m.positions), labelW)
		row := marker + styleDim.Render(pad(m.kind.tag(), 5)) + label
		if m.detail != "" {
			row += styleDim.Render(" " + m.detail)
		}
		lines = append(lines, truncate(row, width))
	}
	return strings.Join(lines, "\n")
}

var styleHit = lipgloss.NewStyle().Bold(true).Underline(true)

// highlightMatch emphasizes the runes the query matched.
func highlightMatch(name string, positions []int) string {
	hit := make(map[int]bool, len(positions))
	for _, p := range positions {
		hit[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(name) {
		if hit[i] {
			b.WriteString(styleHit.Render(string(r)))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
