package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type logsModel struct {
	vp     viewport.Model
	name   string
	lines  []string
	follow bool
	filter filterState
}

func newLogs() logsModel {
	return logsModel{vp: viewport.New(0, 0), follow: true, filter: newFilter()}
}

func (l *logsModel) SetSize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 2 {
		h = 2
	}
	l.vp.Width = w
	l.vp.Height = h - 1 // the title takes a line
	l.refresh()
}

// Reset points the pane at a different command: new scrollback, follow on.
func (l *logsModel) Reset(name string, lines []string) {
	l.name = name
	l.lines = append([]string(nil), lines...)
	l.capLines()
	l.follow = true
	l.filter.query = ""
	l.filter.editing = false
	l.filter.input.Reset()
	l.filter.input.Blur()
	l.refresh()
	l.vp.GotoBottom()
}

// Append adds one live line, scrolling only while following.
func (l *logsModel) Append(line string) {
	l.lines = append(l.lines, line)
	l.capLines()
	l.refresh()
	if l.follow {
		l.vp.GotoBottom()
	}
}

func (l *logsModel) capLines() {
	if len(l.lines) <= maxLogLines {
		return
	}
	l.lines = append([]string(nil), l.lines[len(l.lines)-maxLogLines:]...)
}

// Lines is the buffered scrollback, which the search box reads to offer log
// results without asking the daemon for anything.
func (l logsModel) Lines() []string { return l.lines }

// ApplyFilter sets the filter from outside — a log result in the search box
// carries the query it was found with straight into the log view.
func (l *logsModel) ApplyFilter(query string) {
	l.filter.query = query
	l.filter.editing = false
	l.filter.input.SetValue(query)
	l.filter.input.Blur()
	l.refresh()
}

// visible is every line, or only the matching lines while a filter is set.
func (l logsModel) visible() []string {
	if l.filter.query == "" {
		return l.lines
	}
	out := make([]string, 0, len(l.lines))
	for _, line := range l.lines {
		if smartContains(line, l.filter.query) {
			out = append(out, line)
		}
	}
	return out
}

func (l logsModel) FilterEditing() bool { return l.filter.editing }
func (l logsModel) Query() string       { return l.filter.query }

// CancelFilterEdit closes the input without touching the applied query.
func (l *logsModel) CancelFilterEdit() {
	l.filter.editing = false
	l.filter.input.Blur()
}

// refresh rebuilds the viewport content, truncating each line to the pane
// width.
//
// ponytail: truncate, don't wrap. Keeps one line == one row, so filter and
// scroll math stay trivial. Wrap when reading long JSON lines actually hurts.
func (l *logsModel) refresh() {
	src := l.visible()
	l.filter.matches = len(src)
	out := make([]string, 0, len(src))
	for _, line := range src {
		out = append(out, truncate(line, l.vp.Width))
	}
	l.vp.SetContent(strings.Join(out, "\n"))
}

func (l logsModel) Name() string    { return l.name }
func (l logsModel) Following() bool { return l.follow }
func (l logsModel) LineCount() int  { return len(l.lines) }
func (l logsModel) AtBottom() bool  { return l.vp.AtBottom() }

func (l logsModel) Update(msg tea.Msg) (logsModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return l, nil
	}
	if l.filter.editing {
		switch k.String() {
		case "enter":
			l.filter.query = l.filter.input.Value()
			l.filter.editing = false
			l.filter.input.Blur()
			l.refresh()
			if l.follow {
				l.vp.GotoBottom()
			}
		case "esc":
			l.filter.editing = false
			l.filter.input.Blur()
			l.filter.query = ""
			l.refresh()
		default:
			var cmd tea.Cmd
			l.filter.input, cmd = l.filter.input.Update(msg)
			// Filter as you type: the count in the prompt is the feedback
			// that tells you whether the query is any good.
			l.filter.query = l.filter.input.Value()
			l.refresh()
			if l.follow {
				l.vp.GotoBottom()
			}
			return l, cmd
		}
		return l, nil
	}
	switch k.String() {
	case "/":
		l.filter.editing = true
		l.filter.input.Reset()
		l.filter.input.Focus()
		return l, textinput.Blink
	case "esc":
		if l.filter.query != "" {
			l.filter.query = ""
			l.refresh()
		}
		return l, nil
	case "f":
		l.follow = !l.follow
		if l.follow {
			l.vp.GotoBottom()
		}
	case "j", "down":
		l.follow = false
		l.vp.LineDown(1)
	case "k", "up":
		l.follow = false
		l.vp.LineUp(1)
	case "ctrl+d":
		l.follow = false
		l.vp.HalfViewDown()
	case "ctrl+u":
		l.follow = false
		l.vp.HalfViewUp()
	case "g":
		l.follow = false
		l.vp.GotoTop()
	case "G":
		l.follow = false
		l.vp.GotoBottom()
	}
	return l, nil
}

func (l logsModel) Title() string {
	if l.name == "" {
		return "no command selected"
	}
	if l.filter.query != "" {
		return fmt.Sprintf("%s — filter %q · %d of %d", l.name, l.filter.query, l.filter.matches, len(l.lines))
	}
	if l.follow {
		return l.name + " — following"
	}
	return fmt.Sprintf("%s — paused (%d lines)", l.name, len(l.lines))
}

// Body is the scrollback without the pane's own title, for a caller that
// draws its own border.
func (l logsModel) Body() string {
	body := l.vp.View()
	if !l.filter.editing {
		return body
	}
	// The viewport already fills the pane, so the input needs a line taken
	// from it: appending one pushed the input past the bottom border, where
	// nothing showed while you typed.
	lines := strings.Split(body, "\n")
	if len(lines) > 1 {
		lines = lines[1:] // drop the oldest visible line, keep the newest
	}
	prompt := styleHeader.Render("filter ") + l.filter.input.View() +
		styleDim.Render(fmt.Sprintf("  %d of %d · enter keeps it · esc clears", l.filter.matches, len(l.lines)))
	return strings.Join(append(lines, prompt), "\n")
}

func (l logsModel) View() string {
	title := styleHeader.Render(truncate(l.Title(), l.vp.Width))
	body := lipgloss.JoinVertical(lipgloss.Left, title, l.vp.View())
	if l.filter.editing {
		return lipgloss.JoinVertical(lipgloss.Left, body, l.filter.input.View())
	}
	return body
}
