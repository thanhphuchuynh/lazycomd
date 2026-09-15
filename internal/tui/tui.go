package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

var styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

// Model is the whole TUI. Update is pure; every side effect is a tea.Cmd or
// the one stream goroutine.
type Model struct {
	client *client.Client
	sink   *sink

	table       tableModel
	logs        logsModel
	search      searchModel
	system      systemModel
	statusPanel statusModel
	form        formModel
	confirm     confirmModel
	projects    map[string]string

	focus   focus
	overlay overlay

	// startDaemon brings up the local daemon. Tests replace it so a keypress
	// never actually execs serve.
	startDaemon  func() error
	offeredStart bool

	// The detail pane replaces the log pane for the selected command. Its
	// spec is fetched on open, so detailOK guards the half that needs it.
	detail     bool
	detailOf   string
	detailSpec config.Command
	detailOK   bool

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
		search:      newSearch(),
		system:      newSystem(),
		statusPanel: newStatus(c.Addr()),
		form:        newForm(suggestFolder(), remoteClient(c)),
		confirm:     newConfirm(),
		focus:       focusCommands,
		now:         time.Now,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), tickCmd(tickConnected), fetchSystem(m.client), systemTickCmd(), fetchProjects(m.client))
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
		if m.overlay == overlayStartDaemon {
			m.overlay = overlayNone
		}
		m.table.SetRows([]manager.Status(msg))
		m.statusPanel.SetRows([]manager.Status(msg))
		m.statusPanel.SetConnected(true)
		return m, m.syncStream()

	case statusErrMsg:
		m.connected = false
		m.statusPanel.SetConnected(false)
		m.setStatus(msg.err.Error())
		if !m.offeredStart && !remoteClient(m.client) && m.overlay == overlayNone {
			m.offeredStart = true
			m.overlay = overlayStartDaemon
		}
		return m, nil

	case daemonStartMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error())
			return m, nil
		}
		m.setStatus("starting daemon")
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

	case projectsMsg:
		m.projects = map[string]string(msg)
		return m, nil

	case detailConfigMsg:
		if msg.name == m.detailOf {
			m.detailSpec, m.detailOK = msg.cmd, len(msg.cmd.Cmd) > 0
		}
		return m, nil

	case commandConfigMsg:
		m.form.OpenEdit(msg.name, msg.cmd, m.projects)
		m.overlay = overlayForm
		return m, textinput.Blink

	case formSavedMsg:
		m.overlay = overlayNone
		m.table.MergeStatus(msg.status)
		m.table.SelectName(msg.status.Name)
		m.setStatus(msg.status.Name + " saved")
		return m, tea.Batch(fetchStatus(m.client), m.syncStream())

	case formErrMsg:
		if m.overlay == overlayForm {
			m.form.SetError(msg.err.Error())
			return m, nil
		}
		m.setStatus(msg.err.Error())
		return m, nil

	case deletedMsg:
		m.setStatus(msg.name + " deleted from the config")
		return m, fetchStatus(m.client)

	case streamEndedMsg:
		if m.stream != nil && m.stream.name == msg.name {
			m.stream = nil // the next selection sync reopens it
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

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
	if m.overlay == overlaySearch {
		return m.handleSearchKey(k)
	}
	if m.overlay == overlayLog {
		return m.handleLogKey(k)
	}
	if m.overlay == overlayForm {
		switch s {
		case "esc":
			m.overlay = overlayNone
			return m, nil
		case "enter":
			if err := m.form.Validate(); err != nil {
				m.form.SetError(err.Error())
				return m, nil
			}
			name, cmd := m.form.Result()
			return m, saveCommand(m.client, m.form.Editing(), name, cmd)
		}
		var cmd tea.Cmd
		m.form, cmd = m.form.Update(k)
		return m, cmd
	}
	if m.overlay == overlayConfirm {
		switch s {
		case "y":
			m.overlay = overlayNone
			return m, deleteCommand(m.client, m.confirm.Name())
		case "n", "esc", "q":
			m.overlay = overlayNone
		}
		return m, nil
	}
	if m.overlay == overlayStartDaemon {
		switch s {
		case "y":
			m.overlay = overlayNone
			return m, startDaemonCmd(m.startDaemon)
		case "n", "esc", "q":
			m.overlay = overlayNone
		}
		return m, nil
	}

	// Keys the log filter must swallow before anything else sees them. The
	// filter only opens inside the log view, which handles its own keys.
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
	case "i":
		// Detail is about the selected command, so it belongs to that panel.
		if m.focus != focusCommands {
			m.setStatus("press 2 for Commands first — i shows the selected command")
			return m, nil
		}
		m.detail = !m.detail
		if !m.detail {
			return m, nil
		}
		sel, ok := m.table.Selected()
		if !ok {
			return m, nil
		}
		m.detailOf, m.detailSpec, m.detailOK = sel.Name, config.Command{}, false
		return m, fetchDetailConfig(m.client, sel.Name)
	case "/", "p":
		m.search.Open(m.table.rows, m.system.snap.Ports, m.logs.Name(), m.logs.Lines())
		m.overlay = overlaySearch
		return m, textinput.Blink
	case "o":
		if m.focus != focusCommands {
			m.setStatus("press 2 for Commands first — o opens the selected command's log")
			return m, nil
		}
		m.overlay = overlayLog
		m.layout()
		return m, nil
	case "a":
		m.form.OpenCreate(m.projects)
		m.overlay = overlayForm
		return m, textinput.Blink
	case "e":
		sel, ok := m.table.Selected()
		if !ok {
			return m, nil
		}
		return m, fetchCommandConfig(m.client, sel.Name)
	case "d":
		sel, ok := m.table.Selected()
		if !ok {
			return m, nil
		}
		m.confirm.Open(sel.Name, targetFileLabel(sel.Name))
		m.overlay = overlayConfirm
		return m, nil

	// The main pane scrolls from any panel, so j/k always belong to the
	// focused panel instead of being shared.
	case "ctrl+d", "ctrl+u", "f":
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(k)
		return m, cmd
	case "esc":
		if m.detail {
			m.detail = false
			return m, nil
		}
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
	// Panel heights depend on focus in the stacked layout, and layout() only
	// ran on a resize: the focused panel stayed collapsed until you resized
	// the terminal, which made its list — and its filter — invisible.
	m.layout()
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

// handleSearchKey drives the one search box. Enter goes to the match and
// nothing else: the old palette started a stopped command on enter, which is
// too much to do by accident from a box that also lists ports and log lines.
func (m Model) handleSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.search.Close()
		m.overlay = overlayNone
		return m, nil
	case "enter":
		sel, ok := m.search.Selected()
		query := m.search.Query()
		m.search.Close()
		m.overlay = overlayNone
		if !ok {
			return m, nil
		}
		switch sel.kind {
		case kindPort:
			m.focus = focusPorts
			m.system.SelectPort(sel.port)
			m.layout()
			return m, nil
		case kindLog:
			m.focus = focusCommands
			m.overlay = overlayLog
			m.logs.ApplyFilter(query)
			m.layout()
			return m, nil
		default:
			m.focus = focusCommands
			m.table.SelectName(sel.name)
			m.layout()
			return m, m.syncStream()
		}
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(k)
	return m, cmd
}

// handleLogKey drives the full log view, which is where the log filter lives
// now: the pane beside the panels is a preview, too short to filter usefully.
func (m Model) handleLogKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.logs.FilterEditing() {
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(k)
		return m, cmd
	}
	switch k.String() {
	case "esc":
		// One esc drops the filter, the next closes the view: leaving a
		// filter applied behind a closed view hides lines you never see set.
		if m.logs.Query() != "" {
			var cmd tea.Cmd
			m.logs, cmd = m.logs.Update(k)
			return m, cmd
		}
		m.overlay = overlayNone
		m.layout()
		return m, nil
	case "o", "q":
		m.overlay = overlayNone
		m.layout()
		return m, nil
	case "?":
		m.overlay = overlayHelp
		return m, nil
	}
	var cmd tea.Cmd
	m.logs, cmd = m.logs.Update(k)
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
	m.system.SetFocused(m.focus == focusPorts)
	if m.overlay == overlayLog {
		// The full log view owns the whole body, not the main pane's column.
		m.logs.SetSize(maxInt(m.width-2, 1), maxInt(m.bodyH-1, 2))
	} else {
		m.logs.SetSize(maxInt(m.mainW-2, 1), maxInt(m.mainH-1, 2))
	}
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
	switch m.overlay {
	case overlaySearch:
		return keyBarScope(m.width, scopeSearch)
	case overlayLog:
		return keyBarScope(m.width, scopeLog)
	}
	return keyBar(m.width, m.focus)
}

func (m Model) View() string {
	if m.overlay == overlayHelp {
		return strings.Join([]string{m.header(), helpOverlay(m.width, m.bodyH), m.bottom()}, "\n")
	}
	if m.overlay == overlayForm {
		// A dialog centred over the body, the same shape as search: pinned to
		// the corner it read as part of whatever was behind it.
		w := clampInt(m.width*3/4, 44, 96)
		box := m.form.View(w, minInt(m.bodyH, m.form.Height(w)))
		body := lipgloss.Place(m.width, m.bodyH, lipgloss.Center, lipgloss.Center, box)
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
	}
	if m.overlay == overlaySearch {
		w := clampInt(m.width*2/3, 40, 90)
		h := clampInt(len(m.search.matches)+3, 6, m.bodyH)
		box := panelView(panelSpec{
			Title:   "Search",
			Width:   w,
			Height:  h,
			Focused: true,
			Rows:    strings.Split(m.search.View(w-2, h-2), "\n"),
		})
		body := lipgloss.Place(m.width, m.bodyH, lipgloss.Center, lipgloss.Center, box)
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
	}
	if m.overlay == overlayLog {
		body := panelView(panelSpec{
			Title:   m.logs.Title(),
			Width:   m.width,
			Height:  m.bodyH,
			Focused: true,
			Rows:    strings.Split(m.logs.Body(), "\n"),
		})
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
	}
	if m.overlay == overlaySearch {
		w := clampInt(m.width*2/3, 40, 90)
		h := clampInt(len(m.search.matches)+3, 6, m.bodyH)
		box := panelView(panelSpec{
			Title:   "Search",
			Width:   w,
			Height:  h,
			Focused: true,
			Rows:    strings.Split(m.search.View(w-2, h-2), "\n"),
		})
		body := lipgloss.Place(m.width, m.bodyH, lipgloss.Center, lipgloss.Center, box)
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
	}
	if m.overlay == overlayLog {
		body := panelView(panelSpec{
			Title:   m.logs.Title(),
			Width:   m.width,
			Height:  m.bodyH,
			Focused: true,
			Rows:    strings.Split(m.logs.Body(), "\n"),
		})
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
	}
	if m.overlay == overlayConfirm {
		body := m.confirm.View(minInt(m.width, 56), minInt(m.bodyH, 8))
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
	}
	if m.overlay == overlayStartDaemon {
		body := startDaemonView(minInt(m.width, 56), minInt(m.bodyH, 8))
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
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

// sidebar stacks the three panels.
func (m Model) sidebar() string {
	// Focus is applied here rather than in layout(), which only runs on a
	// resize: a cursor drawn from a stale flag made two panels look live.
	m.table.SetFocused(m.focus == focusCommands)
	m.system.SetFocused(m.focus == focusPorts)

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
		if m.detail {
			sel, _ := m.table.Selected()
			title := "detail"
			if sel.Name != "" {
				title = sel.Name + " · detail"
			}
			return panelView(panelSpec{
				Title:  title,
				Width:  m.mainW,
				Height: m.mainH,
				Rows:   commandDetail(sel, m.detailSpec, m.detailOK, m.system.snap.Ports),
			})
		}
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
	p := tea.NewProgram(New(c, s), tea.WithAltScreen(), tea.WithMouseCellMotion())
	s.fn = p.Send

	final, err := p.Run()
	if fm, ok := final.(Model); ok {
		fm.stream.stop()
	}
	return err
}

// suggestFolder is the folder to offer for a new command: the git root you
// launched from, else the directory itself. It offers nothing when that would
// only be your home directory, because a blank folder already means home and
// a suggestion that repeats the default is noise.
func suggestFolder() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && wd == home {
		return ""
	}
	if root, ok := gitRoot(wd); ok {
		return root
	}
	return wd
}

// gitRoot walks up looking for a .git entry, which is a better guess at "the
// project" than whichever subdirectory you happened to be standing in.
func gitRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// remoteClient reports whether the daemon is reached over TCP, where a local
// path means nothing.
func remoteClient(c *client.Client) bool {
	return !strings.HasPrefix(c.Addr(), "unix://")
}

// clampInt keeps v inside [lo, hi]; the search box sizes itself from the
// terminal and from how many matches there are, and both can be extreme.
func clampInt(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	return minInt(maxInt(v, lo), hi)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
