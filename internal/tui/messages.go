// Package tui is lazycomd's terminal UI. It drives the daemon entirely
// through internal/client; it never spawns or signals a process.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/manager"
)

// Timings and limits. All of them are spec values, not taste.
const (
	tickConnected    = time.Second
	tickDisconnected = 2 * time.Second
	tailLines        = 500
	maxLogLines      = 5000
	statusLife       = 4 * time.Second
)

// tickMsg drives the status poll.
type tickMsg time.Time

// statusMsg is a successful GET /v1/commands.
type statusMsg []manager.Status

// statusErrMsg is a failed status poll: the daemon is unreachable or broken.
type statusErrMsg struct{ err error }

// logTailMsg seeds the log pane when the selection changes.
type logTailMsg struct {
	name  string
	lines []string
}

// logLineMsg is one live line from the SSE stream. It carries the command
// name because lines can arrive after the selection has moved on.
type logLineMsg struct {
	name string
	line string
}

// streamEndedMsg says the SSE stream closed on its own.
type streamEndedMsg struct {
	name string
	err  error
}

// actionDoneMsg is the result of a lifecycle key.
type actionDoneMsg struct {
	verb string
	name string
	st   manager.Status
	err  error
}

// tickCmd schedules the next poll.
func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// fetchStatus polls every command's state.
func fetchStatus(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		list, err := c.List()
		if err != nil {
			return statusErrMsg{err: err}
		}
		return statusMsg(list)
	}
}

// fetchTail loads the scrollback for one command. A failure becomes a single
// visible line rather than an error path: the pane always has content.
func fetchTail(c *client.Client, name string) tea.Cmd {
	return func() tea.Msg {
		lines, err := c.Logs(name, tailLines)
		if err != nil {
			return logTailMsg{name: name, lines: []string{"lazycomd: " + err.Error()}}
		}
		return logTailMsg{name: name, lines: lines}
	}
}

// doAction runs one lifecycle verb. start always passes with_deps: in a TUI
// that is what the keystroke means.
func doAction(c *client.Client, verb, name string) tea.Cmd {
	return func() tea.Msg {
		var (
			st  manager.Status
			err error
		)
		switch verb {
		case "start":
			st, err = c.Start(name, true)
		case "stop":
			st, err = c.Stop(name)
		case "restart":
			st, err = c.Restart(name)
		}
		return actionDoneMsg{verb: verb, name: name, st: st, err: err}
	}
}
