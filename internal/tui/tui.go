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
)

var styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

// Model is the whole TUI. Update is pure; every side effect is a tea.Cmd or
// the one stream goroutine.
type Model struct {
	client *client.Client
	sink   *sink

	table   tableModel
	logs    logsModel
	palette paletteModel

	focus   focus
	overlay overlay

	width  int
	height int
	tableW int
	bodyH  int

	connected bool
	status    string
	statusAt  time.Time

	stream *streamHandle
	now    func() time.Time
}

// New builds the model. s must be the same sink Run wires to the program.
func New(c *client.Client, s *sink) Model {
	return Model{
		client:  c,
		sink:    s,
		table:   newTable(),
		logs:    newLogs(),
		palette: newPalette(),
		focus:   focusTable,
		now:     time.Now,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), tickCmd(tickConnected))
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
		return m, m.syncStream()

	case statusErrMsg:
		m.connected = false
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

	// Help swallows every key but its own dismissal; Ctrl-C always quits.
	if m.overlay == overlayHelp {
		switch s {
		case "ctrl+c":
			return m, tea.Quit
		case "?", "esc", "q":
			m.overlay = overlayNone
		}
		return m, nil
	}
	if s == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.focus {
	case focusPalette:
		return m.handlePaletteKey(k)
	case focusLogs:
		return m.handleLogKey(k)
	default:
		return m.handleTableKey(k)
	}
}

func (m Model) handleTableKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.overlay = overlayHelp
		return m, nil
	case "tab":
		if m.logPaneVisible() {
			m.focus = focusLogs
		}
		return m, nil
	case "p":
		m.palette.Open(m.table.rows)
		m.focus = focusPalette
		return m, textinput.Blink
	case "s", "S", "r":
		return m.runAction(k.String())
	case "f":
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(k)
		return m, cmd
	case "/":
		if !m.logPaneVisible() {
			return m, nil
		}
		m.focus = focusLogs
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(k)
		return m, cmd
	}

	before, _ := m.table.Selected()
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(k)
	if after, _ := m.table.Selected(); after.Name != before.Name {
		return m, tea.Batch(cmd, m.syncStream())
	}
	return m, cmd
}

// runAction fires one lifecycle verb for the selected command.
func (m Model) runAction(key string) (tea.Model, tea.Cmd) {
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

func (m Model) handleLogKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.logs.FilterEditing() {
		switch k.String() {
		case "q":
			return m, tea.Quit
		case "?":
			m.overlay = overlayHelp
			return m, nil
		case "tab":
			m.focus = focusTable
			return m, nil
		case "esc":
			// Esc wears two hats: clear the filter if there is one, else
			// hand focus back to the table.
			if m.logs.Query() == "" {
				m.focus = focusTable
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.logs, cmd = m.logs.Update(k)
	return m, cmd
}

func (m Model) handlePaletteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.palette.Close()
		m.focus = focusTable
		return m, nil
	case "enter":
		sel, ok := m.palette.Highlighted()
		m.palette.Close()
		m.focus = focusTable
		if !ok {
			return m, nil
		}
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

// logPaneVisible is false on a terminal too narrow to split.
func (m Model) logPaneVisible() bool { return m.width >= 60 }

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusAt = m.now()
}

func (m *Model) layout() {
	m.bodyH = m.height - 2 // header and bottom line
	if m.bodyH < 3 {
		m.bodyH = 3
	}

	// Too narrow to split: the table takes everything.
	if m.width < 60 {
		m.tableW = m.width
		m.table.SetLayout(tableLayout{Width: m.tableW, Height: m.bodyH, Compact: true})
		m.logs.SetSize(1, m.bodyH)
		if m.focus == focusLogs {
			m.focus = focusTable
		}
		m.logs.CancelFilterEdit()
		return
	}

	m.tableW = m.width * 40 / 100
	if m.tableW < 32 {
		m.tableW = 32
	}
	if m.tableW > m.width-20 {
		m.tableW = m.width - 20
	}
	m.table.SetLayout(tableLayout{
		Width:   m.tableW,
		Height:  m.bodyH,
		Compact: m.width < 80,
		Wide:    m.width >= 110,
	})
	m.logs.SetSize(m.width-m.tableW-1, m.bodyH)
}

func (m Model) header() string {
	if !m.connected {
		return styleWarn.Render(truncate(" lazycomd — daemon not running, retrying (start with: lazycomd serve)", m.width))
	}
	running := 0
	for _, r := range m.table.rows {
		if r.State == manager.Running {
			running++
		}
	}
	return styleHeader.Render(truncate(fmt.Sprintf(" lazycomd — %d commands · %d running", len(m.table.rows), running), m.width))
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

	left := m.table.View()
	if m.focus == focusPalette {
		left = m.palette.View(m.tableW, m.bodyH)
	}
	if !m.logPaneVisible() {
		return strings.Join([]string{m.header(), left, m.bottom()}, "\n")
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, styleDim.Render("│"), m.logs.View())
	return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
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
