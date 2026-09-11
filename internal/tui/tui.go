package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/probe"
)

var styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

// Model is the whole TUI. Update is pure; every side effect is a tea.Cmd or
// the one stream goroutine.
type Model struct {
	client *client.Client
	sink   *sink

	table       tableModel
	logs        logsModel
	palette     paletteModel
	system      systemModel
	statusPanel statusModel

	focus   focus
	overlay overlay

	width     int
	height    int
	sideW     int
	bodyH     int
	statusH   int
	commandsH int
	portsH    int
	mainW     int
	mainH     int

	connected bool
	status    string
	statusAt  time.Time

	stream *streamHandle
	now    func() time.Time
}

// New builds the model. s must be the same sink Run wires to the program.
func New(c *client.Client, s *sink) Model {
	return Model{
		client:      c,
		sink:        s,
		table:       newTable(),
		logs:        newLogs(),
		palette:     newPalette(),
		system:      newSystem(),
		statusPanel: newStatus(c.Addr()),
		focus:       focusCommands,
		now:         time.Now,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), tickCmd(tickConnected), fetchSystem(m.client), systemTickCmd())
}

// tickInterval polls faster while the daemon is answering.
func (m Model) tickInterval() time.Duration {
	if m.connected {
		return tickConnected
	}
	return tickDisconnected
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tickMsg:
		return m, tea.Batch(fetchStatus(m.client), tickCmd(m.tickInterval()))

	case statusMsg:
		m.connected = true
		m.table.SetRows([]manager.Status(msg))
		m.statusPanel.SetRows([]manager.Status(msg))
		m.statusPanel.SetConnected(true)
		return m, m.syncStream()

	case statusErrMsg:
		m.connected = false
		m.statusPanel.SetConnected(false)
		m.setStatus(msg.err.Error())
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error())
			return m, nil
		}
		m.table.MergeStatus(msg.st)
		m.setStatus(fmt.Sprintf("%s %s: %s", msg.verb, msg.name, msg.st.State))
		return m, nil

	case logTailMsg:
		if sel, ok := m.table.Selected(); ok && sel.Name == msg.name {
			m.logs.Reset(msg.name, msg.lines)
		}
		return m, nil

	case logLineMsg:
		if msg.name == m.logs.Name() {
			m.logs.Append(msg.line)
		}
		return m, nil

	case systemTickMsg:
		return m, tea.Batch(fetchSystem(m.client), systemTickCmd())

	case systemMsg:
		m.system.SetSnapshot(probe.Snapshot(msg))
		m.statusPanel.SetSnapshot(probe.Snapshot(msg))
		return m, nil

	case systemErrMsg:
		// The banner already reports an unreachable daemon; keep the last
		// good snapshot on screen rather than blanking the view.
		return m, nil

	case streamEndedMsg:
		if m.stream != nil && m.stream.name == msg.name {
			m.stream = nil // the next selection sync reopens it
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()

	if s == "ctrl+c" {
		return m, tea.Quit
	}

	// Help swallows every key but its own dismissal.
	if m.overlay == overlayHelp {
		switch s {
		case "?", "esc", "q":
			m.overlay = overlayNone
		}
		return m, nil
	}
	if m.overlay == overlayPalette {
		return m.handlePaletteKey(k)
	}

	// Keys the filter input must swallow before anything else sees them.
	if m.logs.FilterEditing() {
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(k)
		return m, cmd
	}

	switch s {
	case "q":
		return m, tea.Quit
	case "?":
		m.overlay = overlayHelp
		return m, nil
	case "1":
		return m.focusPanel(focusStatus)
	case "2":
		return m.focusPanel(focusCommands)
	case "3":
		return m.focusPanel(focusPorts)
	case "tab":
		return m.focusPanel(m.focus.next())
	case "shift+tab":
		return m.focusPanel(m.focus.prev())
	case "p":
		m.palette.Open(m.table.rows)
		m.overlay = overlayPalette
		return m, textinput.Blink

	// The main pane scrolls from any panel, so j/k always belong to the
	// focused panel instead of being shared.
	case "ctrl+d", "ctrl+u", "f", "/":
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(k)
		return m, cmd
	case "esc":
		// Esc has one job now: drop the log filter. Panels are switched by
		// number, so it has nothing else to undo.
		if m.logs.Query() != "" {
			var cmd tea.Cmd
			m.logs, cmd = m.logs.Update(k)
			return m, cmd
		}
		return m, nil

	case "s", "S", "r":
		return m.runAction(s)
	}

	return m.handlePanelKey(k)
}

// focusPanel moves focus and resyncs whatever the main pane now shows.
func (m Model) focusPanel(f focus) (tea.Model, tea.Cmd) {
	m.focus = f
	return m, nil
}

// handlePanelKey routes navigation to whichever panel has focus.
func (m Model) handlePanelKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusPorts:
		var cmd tea.Cmd
		m.system, cmd = m.system.Update(k)
		return m, cmd
	case focusCommands:
		before, _ := m.table.Selected()
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(k)
		if after, _ := m.table.Selected(); after.Name != before.Name {
			return m, tea.Batch(cmd, m.syncStream())
		}
		return m, cmd
	}
	return m, nil // the status panel has nothing to navigate
}

// runAction fires one lifecycle verb, but only from the Commands panel: a
// keystroke that acts on something you cannot see is worse than an inert one.
func (m Model) runAction(key string) (tea.Model, tea.Cmd) {
	if m.focus != focusCommands {
		m.setStatus("press 2 for Commands first — " + key + " acts on the selected command")
		return m, nil
	}
	sel, ok := m.table.Selected()
	if !ok {
		return m, nil
	}
	if !m.connected {
		m.setStatus("daemon not running (start with: lazycomd serve)")
		return m, nil
	}
	verb := map[string]string{"s": "start", "S": "stop", "r": "restart"}[key]
	return m, doAction(m.client, verb, sel.Name)
}

func (m Model) handlePaletteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.palette.Close()
		m.overlay = overlayNone
		return m, nil
	case "enter":
		sel, ok := m.palette.Highlighted()
		m.palette.Close()
		m.overlay = overlayNone
		if !ok {
			return m, nil
		}
		m.focus = focusCommands
		m.table.SelectName(sel.Name)
		cmds := []tea.Cmd{m.syncStream()}
		if sel.State != manager.Running && sel.State != manager.Starting {
			cmds = append(cmds, doAction(m.client, "start", sel.Name))
		}
		return m, tea.Batch(cmds...)
	}
	var cmd tea.Cmd
	m.palette, cmd = m.palette.Update(k)
	return m, cmd
}

// Panel geometry. Status and Ports are fixed; Commands takes what is left.
const (
	sideWidth    = 34
	minSideW     = 26
	statusPanelH = 4
	portsPanelH  = 8
	stackedMinW  = 90 // below this the columns become rows
)

// stacked reports whether the terminal is too narrow for two columns.
func (m Model) stacked() bool { return m.width < stackedMinW }

func (m *Model) layout() {
	m.bodyH = m.height - 2 // header and key bar
	if m.bodyH < 6 {
		m.bodyH = 6
	}

	// A little wider on a big terminal, still a sidebar.
	m.sideW = maxInt(sideWidth, m.width*32/100)
	if m.sideW > 52 {
		m.sideW = 52
	}
	if m.stacked() {
		m.sideW = m.width
	}
	if m.sideW > m.width {
		m.sideW = m.width
	}
	if m.sideW < minSideW {
		m.sideW = minSideW
	}

	m.statusH, m.portsH = statusPanelH, portsPanelH
	if m.stacked() {
		// One column: the focused panel keeps its rows, the others collapse
		// to a title bar, and the main pane takes the bottom half.
		m.statusH, m.portsH = 1, 1
		if m.focus == focusStatus {
			m.statusH = statusPanelH
		}
		if m.focus == focusPorts {
			m.portsH = portsPanelH
		}
		m.commandsH = 3
		if m.focus == focusCommands {
			m.commandsH = maxInt(m.bodyH/2, 4)
		}
		m.mainW = m.width
		m.mainH = maxInt(m.bodyH-m.statusH-m.commandsH-m.portsH, 3)
	} else {
		m.commandsH = m.bodyH - m.statusH - m.portsH
		if m.commandsH < 4 {
			// A short terminal gives up the Ports panel before the list.
			m.portsH = maxInt(m.bodyH-m.statusH-4, 3)
			m.commandsH = maxInt(m.bodyH-m.statusH-m.portsH, 3)
		}
		m.mainW, m.mainH = m.width-m.sideW, m.bodyH
	}

	m.table.SetLayout(tableLayout{
		Width:   m.sideW - 2, // the panel border eats two columns
		Height:  m.commandsH - 2,
		Compact: m.sideW < 32,
		Wide:    m.sideW >= 46,
	})
	m.system.SetSize(m.sideW-2, m.portsH-2)
	m.logs.SetSize(maxInt(m.mainW-2, 1), maxInt(m.mainH-1, 2))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m Model) header() string {
	left := " lazycomd"
	right := m.headerRight()
	room := m.width - len([]rune(left)) - len([]rune(right)) - 1
	if room < 1 {
		return styleHeader.Render(truncate(left, m.width))
	}
	return styleHeader.Render(left) + strings.Repeat(" ", room) + styleDim.Render(right) + " "
}

// headerRight is the daemon in one phrase.
func (m Model) headerRight() string {
	if !m.connected {
		return "daemon not running — retrying"
	}
	return m.statusPanel.Summary()
}

// syncStream points the single SSE stream at the selected command and returns
// the command that seeds the pane's scrollback.
func (m *Model) syncStream() tea.Cmd {
	sel, ok := m.table.Selected()
	if !ok {
		m.stream.stop()
		m.stream = nil
		return nil
	}
	if m.stream != nil && m.stream.name == sel.Name {
		return nil
	}
	m.stream.stop()
	m.stream = startStream(m.client, m.sink, sel.Name)
	return fetchTail(m.client, sel.Name)
}

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusAt = m.now()
}

// bottom shows a transient status line, then falls back to the key bar.
func (m Model) bottom() string {
	if m.status != "" && m.now().Sub(m.statusAt) < statusLife {
		return truncate(" "+m.status, m.width)
	}
	return keyBar(m.width, m.focus)
}

func (m Model) View() string {
	if m.overlay == overlayHelp {
		return strings.Join([]string{m.header(), helpOverlay(m.width, m.bodyH), m.bottom()}, "\n")
	}

	side := m.sidebar()
	main := m.mainPane()
	if m.stacked() {
		return strings.Join([]string{m.header(), side, main, m.bottom()}, "\n")
	}
	return strings.Join([]string{
		m.header(),
		lipgloss.JoinHorizontal(lipgloss.Top, side, main),
		m.bottom(),
	}, "\n")
}

// sidebar stacks the three panels, or shows the palette in their place.
func (m Model) sidebar() string {
	if m.overlay == overlayPalette {
		return panelView(panelSpec{
			Title:   "Palette",
			Width:   m.sideW,
			Height:  m.bodyH,
			Focused: true,
			Rows:    strings.Split(m.palette.View(m.sideW-2, m.bodyH-2), "\n"),
		})
	}

	status := panelView(panelSpec{
		Title:    "Status",
		Number:   1,
		Width:    m.sideW,
		Height:   m.statusH,
		Focused:  m.focus == focusStatus,
		Subtitle: collapsedSubtitle(m.stacked() && m.focus != focusStatus, m.statusPanel.Summary()),
		Rows:     m.statusPanel.PanelRows(),
	})
	commands := panelView(panelSpec{
		Title:   "Commands",
		Number:  2,
		Width:   m.sideW,
		Height:  m.commandsH,
		Focused: m.focus == focusCommands,
		Rows:    strings.Split(m.table.View(), "\n"),
	})
	ports := panelView(panelSpec{
		Title:    "Ports",
		Number:   3,
		Width:    m.sideW,
		Height:   m.portsH,
		Focused:  m.focus == focusPorts,
		Subtitle: m.system.Subtitle(),
		Rows:     m.system.PanelRows(),
	})
	return strings.Join([]string{status, commands, ports}, "\n")
}

// collapsedSubtitle only shows a summary when the panel itself is collapsed.
func collapsedSubtitle(collapsed bool, s string) string {
	if collapsed {
		return s
	}
	return ""
}

// mainPane follows focus: logs for Commands, detail for the others.
func (m Model) mainPane() string {
	title, rows := m.logs.Title(), []string(nil)
	switch m.focus {
	case focusStatus:
		title, rows = "daemon", m.statusPanel.Detail()
	case focusPorts:
		title, rows = "port detail", m.system.Detail()
	default:
		title := m.logs.Title()
		if sel, ok := m.table.Selected(); ok && sel.PID > 0 {
			title = fmt.Sprintf("%s · pid %d", title, sel.PID)
		}
		return panelView(panelSpec{
			Title:  title,
			Width:  m.mainW,
			Height: m.mainH,
			Rows:   strings.Split(m.logs.Body(), "\n"),
		})
	}
	return panelView(panelSpec{Title: title, Width: m.mainW, Height: m.mainH, Rows: rows})
}

// Run starts the TUI and blocks until the user quits.
func Run(c *client.Client) error {
	s := &sink{}
	p := tea.NewProgram(New(c, s), tea.WithAltScreen())
	s.fn = p.Send

	final, err := p.Run()
	if fm, ok := final.(Model); ok {
		fm.stream.stop()
	}
	return err
}
