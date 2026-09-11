package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tphuc/lazycomd/internal/manager"
)

var styleHit = lipgloss.NewStyle().Bold(true).Underline(true)

type paletteModel struct {
	input   textinput.Model
	rows    []manager.Status
	matches []candidate
	cursor  int
}

func newPalette() paletteModel {
	ti := textinput.New()
	// Not ">": that is the selection marker one line below, and two meanings
	// on one glyph made the input line read as a highlighted row.
	ti.Prompt = "❯ "
	ti.CharLimit = 120
	return paletteModel{input: ti}
}

// Open shows the palette over the command list as it stands now.
func (p *paletteModel) Open(rows []manager.Status) {
	p.rows = rows
	p.cursor = 0
	p.input.Reset()
	p.input.Focus()
	p.refilter()
}

func (p *paletteModel) Close() { p.input.Blur() }

func (p *paletteModel) refilter() {
	names := make([]string, 0, len(p.rows))
	for _, r := range p.rows {
		names = append(names, r.Name)
	}
	p.matches = rank(p.input.Value(), names)
	p.clampCursor()
}

func (p *paletteModel) clampCursor() {
	if p.cursor >= len(p.matches) {
		p.cursor = len(p.matches) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// Update handles typing and navigation. The root model intercepts enter and
// esc before this is called.
func (p paletteModel) Update(msg tea.Msg) (paletteModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "down", "ctrl+n":
		p.cursor++
		p.clampCursor()
		return p, nil
	case "up", "ctrl+p":
		p.cursor--
		p.clampCursor()
		return p, nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.refilter()
	return p, cmd
}

// Highlighted is the status of the command under the palette cursor.
func (p paletteModel) Highlighted() (manager.Status, bool) {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return manager.Status{}, false
	}
	name := p.matches[p.cursor].name
	for _, r := range p.rows {
		if r.Name == name {
			return r, true
		}
	}
	return manager.Status{}, false
}

func (p paletteModel) View(width, height int) string {
	lines := []string{styleHeader.Render(truncate(p.input.View(), width))}

	if len(p.matches) == 0 {
		return strings.Join(append(lines, styleDim.Render("no match")), "\n")
	}
	room := height - 1
	for i, m := range p.matches {
		if i >= room {
			break
		}
		marker := "  "
		if i == p.cursor {
			marker = "> "
		}
		lines = append(lines, truncate(marker, width)+highlightMatch(m.name, m.positions))
	}
	return strings.Join(lines, "\n")
}

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
