package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Screen geometry. The header takes the first row, then the sidebar panels
// stack in order, each with a border row of its own. These are the only
// places that know where a panel starts, so a click and a render cannot
// disagree about it.
func (m Model) statusTop() int   { return 1 }
func (m Model) commandsTop() int { return m.statusTop() + m.statusH }
func (m Model) portsTop() int    { return m.commandsTop() + m.commandsH }

// searchBoxTop and searchBoxLeft place the centred search box, matching the
// lipgloss.Place call that draws it.
func (m Model) searchBoxTop() int {
	return 1 + (m.bodyH-m.searchBoxHeight())/2
}

func (m Model) searchBoxLeft() int {
	return (m.width - m.searchBoxWidth()) / 2
}

func (m Model) searchBoxWidth() int  { return clampInt(m.width*2/3, 40, 90) }
func (m Model) searchBoxHeight() int { return clampInt(len(m.search.matches)+3, 6, m.bodyH) }

// handleMouse routes a click or a wheel turn to whatever is under it. Every
// click also moves focus: clicking a row in a panel you are not in and
// having the keys still go somewhere else would be worse than not clicking.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		return m.wheel(msg)
	case tea.MouseButtonLeft:
		return m.click(msg)
	}
	return m, nil
}

func (m Model) click(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlaySearch:
		return m.clickSearch(msg)
	case overlayNone:
	default:
		return m, nil // a form or a confirmation owns the screen
	}

	if !m.inSidebar(msg.X) {
		return m, nil
	}

	switch {
	case msg.Y >= m.portsTop() && msg.Y < m.portsTop()+m.portsH:
		m.focus = focusPorts
		if row := msg.Y - m.portsTop() - 1; row >= 0 {
			m.system.ClickRow(row)
		}
		m.layout()
		return m, nil

	case msg.Y >= m.commandsTop() && msg.Y < m.commandsTop()+m.commandsH:
		m.focus = focusCommands
		// The border takes a row and the column header another.
		if row := msg.Y - m.commandsTop() - 2; row >= 0 {
			before, _ := m.table.Selected()
			m.table.ClickRow(row)
			m.layout()
			if after, _ := m.table.Selected(); after.Name != before.Name {
				return m, m.syncStream()
			}
		}
		m.layout()
		return m, nil

	case msg.Y >= m.statusTop() && msg.Y < m.statusTop()+m.statusH:
		m.focus = focusStatus
		m.layout()
		return m, nil
	}
	return m, nil
}

// clickSearch picks the match under the pointer and goes to it, because a
// click on a result that only moved a cursor would need a second gesture.
func (m Model) clickSearch(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	top, left := m.searchBoxTop(), m.searchBoxLeft()
	inside := msg.X >= left && msg.X < left+m.searchBoxWidth() &&
		msg.Y >= top && msg.Y < top+m.searchBoxHeight()
	if !inside {
		m.search.Close()
		m.overlay = overlayNone
		return m, nil
	}

	// The border takes a row and the input line another.
	row := msg.Y - top - 2
	if row < 0 {
		return m, nil
	}
	m.search.ClickRow(row, maxInt(m.searchBoxHeight()-3, 1))
	if _, ok := m.search.Selected(); !ok {
		return m, nil
	}
	return m.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
}

// wheel scrolls whatever the pointer is over: the main pane keeps its own
// scrollback, a panel moves its cursor.
func (m Model) wheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	up := msg.Button == tea.MouseButtonWheelUp
	if m.overlay == overlaySearch {
		k := "down"
		if up {
			k = "up"
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(keyOf(k))
		return m, cmd
	}
	if m.overlay == overlayLog || !m.inSidebar(msg.X) {
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(keyOf(wheelKey(up)))
		return m, cmd
	}

	dir := keyOf("j")
	if up {
		dir = keyOf("k")
	}
	switch {
	case msg.Y >= m.portsTop():
		m.focus = focusPorts
		m.layout()
		var cmd tea.Cmd
		m.system, cmd = m.system.Update(dir)
		return m, cmd
	case msg.Y >= m.commandsTop():
		m.focus = focusCommands
		m.layout()
		return m.handlePanelKey(dir)
	}
	return m, nil
}

func wheelKey(up bool) string {
	if up {
		return "ctrl+u"
	}
	return "ctrl+d"
}

// inSidebar reports whether a column belongs to the panels rather than the
// main pane. A stacked layout has no main pane beside them.
func (m Model) inSidebar(x int) bool { return m.stacked() || x < m.sideW }

// keyOf turns a binding name into the key message the panels already
// understand, so a wheel turn reuses the same paths as j and k.
func keyOf(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}
