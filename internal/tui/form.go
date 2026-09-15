package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

type formField int

const (
	fieldName formField = iota
	fieldCommand
	fieldFolder
	fieldEnv
	fieldPort
	fieldHealth
	fieldRestart
	fieldAutostart
	fieldCount
)

// restartChoices is the cycle the RESTART field walks; three fixed values do
// not deserve free text.
var restartChoices = []config.Restart{config.RestartNo, config.RestartOnFailure, config.RestartAlways}

type formModel struct {
	editing bool
	base    config.Command // what we started from, so unshown fields survive

	name      textinput.Model
	command   textarea.Model
	folder    textinput.Model
	env       textinput.Model
	port      textinput.Model
	health    textinput.Model
	restart   int
	autostart bool
	field     formField

	// envLocked is set when the spec holds an env value this one-line field
	// cannot round-trip. Rewriting it would silently cut the value in half.
	envLocked bool

	folderTyped bool // you edited it, so stop prefilling over you
	err         string

	launchDir string
	remote    bool
	projects  map[string]string
}

func newForm(launchDir string, remote bool) formModel {
	mk := func(limit int) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = limit
		return ti
	}
	folder := mk(200)
	folder.ShowSuggestions = true
	return formModel{
		name:      mk(80),
		command:   newCommandArea(),
		folder:    folder,
		env:       mk(400),
		port:      mk(5),
		health:    mk(200),
		launchDir: launchDir,
		remote:    remote,
	}
}

// newCommandArea is the COMMAND field: a textarea, so a long command wraps
// onto more lines instead of scrolling sideways past the border. Enter is the
// form's save key, so this never holds more than one logical line.
func newCommandArea() textarea.Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 400
	ta.MaxHeight = commandMaxLines
	// The default focused style paints the cursor line and adds padding; both
	// fight the form's own focus marker and its label column.
	for _, st := range []*textarea.Style{&ta.FocusedStyle, &ta.BlurredStyle} {
		st.Base = lipgloss.NewStyle()
		st.CursorLine = lipgloss.NewStyle()
		st.CursorLineNumber = lipgloss.NewStyle()
		st.EndOfBuffer = lipgloss.NewStyle()
	}
	return ta
}

// commandMaxLines caps how tall the COMMAND field grows; past it the textarea
// scrolls, so the form never eats the whole screen.
const commandMaxLines = 4

// OpenCreate resets the form for a new command.
func (f *formModel) OpenCreate(projects map[string]string) {
	f.editing, f.base, f.err = false, config.Command{}, ""
	f.projects, f.folderTyped = projects, false
	f.restart, f.field = 0, fieldName

	f.name.Reset()
	f.command.Reset()
	f.folder.Reset()
	f.env.Reset()
	f.port.Reset()
	f.health.Reset()
	f.autostart, f.envLocked = false, false
	if !f.remote {
		// You add a command for the project you are sitting in.
		f.folder.SetValue(f.launchDir)
	}
	f.focus()
}

// OpenEdit fills the form from an existing command. The name is locked:
// renaming is a delete plus a create, possibly across two files.
func (f *formModel) OpenEdit(name string, c config.Command, projects map[string]string) {
	f.editing, f.base, f.err = true, c, ""
	f.projects, f.folderTyped = projects, true
	f.field = fieldCommand

	f.name.SetValue(name)
	line := strings.Join(c.Cmd, " ")
	if c.Shell && len(c.Cmd) == 1 {
		line = c.Cmd[0]
	}
	f.command.SetValue(line)
	f.folder.SetValue(c.Cwd)
	f.env.SetValue(formatEnv(c.Env))
	f.envLocked = !envRoundTrips(c.Env)
	f.port.SetValue(portString(c.Port))
	f.health.SetValue(c.Health)
	f.autostart = c.Autostart

	f.restart = 0
	for i, r := range restartChoices {
		if r == c.Restart {
			f.restart = i
		}
	}
	f.focus()
}

func (f *formModel) SetError(msg string) { f.err = msg }
func (f formModel) Editing() bool        { return f.editing }

// focus points the cursor at the active field.
func (f *formModel) focus() {
	f.name.Blur()
	f.command.Blur()
	f.folder.Blur()
	f.env.Blur()
	f.port.Blur()
	f.health.Blur()
	switch f.field {
	case fieldName:
		if !f.editing {
			f.name.Focus()
		}
	case fieldCommand:
		_ = f.command.Focus() // the blink cmd; the form redraws on every key anyway
	case fieldFolder:
		f.folder.Focus()
		f.folder.SetSuggestions(f.dirSuggestions())
	case fieldEnv:
		if !f.envLocked {
			f.env.Focus()
		}
	case fieldPort:
		f.port.Focus()
	case fieldHealth:
		f.health.Focus()
	}
}

func (f formModel) Update(msg tea.Msg) (formModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, nil
	}
	switch k.String() {
	case "tab", "down":
		// On FOLDER, tab finishes the path first: moving on is one more tab.
		if k.String() == "tab" && f.field == fieldFolder && f.completing() {
			f.folder, _ = f.folder.Update(msg)
			return f, nil
		}
		if k.String() == "down" && f.folderListOpen() {
			f.folder, _ = f.folder.Update(msg)
			return f, nil
		}
		f.field = (f.field + 1) % fieldCount
		f.focus()
		return f, nil
	case "shift+tab", "up":
		// With the dropdown open, up/down walk it rather than the fields.
		if k.String() == "up" && f.folderListOpen() {
			f.folder, _ = f.folder.Update(msg)
			return f, nil
		}
		f.field = (f.field + fieldCount - 1) % fieldCount
		f.focus()
		return f, nil
	}

	switch f.field {
	case fieldRestart:
		switch k.String() {
		case "right", "l", " ":
			f.restart = (f.restart + 1) % len(restartChoices)
		case "left", "h":
			f.restart = (f.restart + len(restartChoices) - 1) % len(restartChoices)
		}
		return f, nil
	case fieldAutostart:
		switch k.String() {
		case "right", "l", "left", "h", " ", "y", "n":
			f.autostart = !f.autostart
		}
		return f, nil
	}

	var cmd tea.Cmd
	switch f.field {
	case fieldName:
		if f.editing {
			return f, nil // locked
		}
		f.name, cmd = f.name.Update(msg)
		f.syncFolderToProject()
	case fieldCommand:
		f.command, cmd = f.command.Update(msg)
	case fieldFolder:
		f.folder, cmd = f.folder.Update(msg)
		f.folderTyped = true
		f.folder.SetSuggestions(f.dirSuggestions())
	case fieldEnv:
		if f.envLocked {
			return f, nil
		}
		f.env, cmd = f.env.Update(msg)
	case fieldPort:
		f.port, cmd = f.port.Update(msg)
	case fieldHealth:
		f.health, cmd = f.health.Update(msg)
	}
	return f, cmd
}

// syncFolderToProject repoints a prefilled folder at the project a namespaced
// name belongs to. A folder you typed yourself is never overwritten.
func (f *formModel) syncFolderToProject() {
	if f.folderTyped || f.remote {
		return
	}
	ns, _, namespaced := strings.Cut(f.name.Value(), ":")
	if !namespaced {
		f.folder.SetValue(f.launchDir)
		return
	}
	if dir, ok := f.projects[ns]; ok {
		f.folder.SetValue(dir)
	}
}

// Result is the name and the command the form describes. It starts from the
// spec it opened with, so depends_on, size and log survive an edit.
func (f formModel) Result() (string, config.Command) {
	c := f.base
	cmd, shell := splitCommand(strings.TrimSpace(f.command.Value()))
	c.Cmd, c.Shell = cmd, shell
	c.Cwd = strings.TrimSpace(f.folder.Value())
	c.Restart = restartChoices[f.restart]
	c.Autostart = f.autostart
	c.Health = strings.TrimSpace(f.health.Value())
	c.Port, _ = parsePort(f.port.Value())
	if !f.envLocked {
		c.Env, _ = parseEnv(f.env.Value())
	}
	return strings.TrimSpace(f.name.Value()), c
}

// Validate reports what the form cannot turn into a command, so a typo is
// caught in the field rather than by the daemon's reparse.
func (f formModel) Validate() error {
	if strings.TrimSpace(f.name.Value()) == "" {
		return errors.New("name is required")
	}
	if cmd, _ := splitCommand(strings.TrimSpace(f.command.Value())); len(cmd) == 0 {
		return errors.New("command is required")
	}
	if _, err := parsePort(f.port.Value()); err != nil {
		return err
	}
	if !f.envLocked {
		if _, err := parseEnv(f.env.Value()); err != nil {
			return err
		}
	}
	// The rest — health's scheme, the port's range — is config.Command's own
	// validation, which every write already runs.
	c := f.base
	c.Cmd, _ = splitCommand(strings.TrimSpace(f.command.Value()))
	c.Health = strings.TrimSpace(f.health.Value())
	c.Port, _ = parsePort(f.port.Value())
	file := config.File{Commands: map[string]config.Command{strings.TrimSpace(f.name.Value()): c}}
	return file.Validate()
}

// parsePort reads the PORT field. Blank means no port, which is how a command
// that binds nothing is spelled.
func parsePort(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("port %q is not a number", s)
	}
	return n, nil
}

// parseEnv reads the ENV field: KEY=VALUE pairs separated by spaces.
func parseEnv(s string) (map[string]string, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("env %q is not KEY=VALUE", f)
		}
		out[k] = v
	}
	return out, nil
}

// formatEnv writes env back out in the order a reader expects it: sorted, so
// opening the same command twice shows the same line.
func formatEnv(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+env[k])
	}
	return strings.Join(pairs, " ")
}

// envRoundTrips reports whether this env survives the one-line field. A value
// holding a space would come back as two pairs, so such a command keeps its
// env and the field goes read-only.
func envRoundTrips(env map[string]string) bool {
	for k, v := range env {
		if strings.ContainsAny(k, " =") || strings.ContainsAny(v, " ") {
			return false
		}
	}
	return true
}

// portString renders a port for the field, with 0 meaning "none" rather than
// a literal zero nobody typed.
func portString(port int) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}

// splitCommand reads the one-line command field. A shell metacharacter means
// the line only makes sense to a shell, so it is passed through whole rather
// than split into nonsense.
func splitCommand(line string) ([]string, bool) {
	if strings.ContainsAny(line, "|><&;$*") {
		return []string{line}, true
	}
	return strings.Fields(line), false
}

// folderListOpen reports whether the folder dropdown has something in it.
func (f formModel) folderListOpen() bool {
	return f.field == fieldFolder && len(f.folder.MatchedSuggestions()) > 0
}

// completing reports whether the folder input is showing a completion that
// is longer than what has been typed, so tab has something to accept.
func (f formModel) completing() bool {
	s := f.folder.CurrentSuggestion()
	return s != "" && len(s) > len(f.folder.Value())
}

// dirSuggestions lists the directories that could finish the path being
// typed. Only directories: a command runs in one, never in a file.
//
// The daemon may be on another machine, so a remote session completes
// nothing — the paths here are this machine's.
func (f formModel) dirSuggestions() []string {
	if f.remote {
		return nil
	}
	value := f.folder.Value()
	if value == "" {
		return nil
	}
	dir, prefix := filepath.Split(value)
	if dir == "" {
		return nil // a relative fragment names no directory to read
	}
	entries, err := os.ReadDir(expandHome(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		// Trailing separator, so accepting one completion sets up the next
		// level instead of stopping at the first directory.
		out = append(out, dir+e.Name()+string(filepath.Separator))
	}
	return out // os.ReadDir is already sorted by name
}

// expandHome resolves a leading ~ for reading, leaving the typed value alone.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(h, strings.TrimPrefix(p, "~"))
}

// hint explains the focused field.
func (f formModel) hint() string {
	switch f.field {
	case fieldName:
		if strings.TrimSpace(f.name.Value()) == "" {
			return "required · app:name puts it in that project"
		}
		return "app:name puts it in that project"
	case fieldCommand:
		if strings.TrimSpace(f.command.Value()) == "" {
			return "required · what you would type in a shell"
		}
		if _, shell := splitCommand(f.command.Value()); shell {
			return "shell mode: the whole line goes to sh -c"
		}
		return "split on spaces · pipes and $ switch to shell mode"
	case fieldFolder:
		if f.folderListOpen() {
			return "↑↓ pick · tab completes"
		}
		if !f.folderTyped && f.folder.Value() != "" {
			return "suggested from where you launched · blank = ~"
		}
		return "where the command runs · blank = ~"
	case fieldEnv:
		if f.envLocked {
			return "a value here holds a space · edit it in the file"
		}
		return "KEY=VALUE pairs, space separated"
	case fieldPort:
		return "the port it binds · shown in the ports panel · blank = none"
	case fieldHealth:
		return "http URL polled for readiness · blank = none"
	case fieldAutostart:
		return "start it when the daemon starts · space to change"
	default:
		return "when the command exits · ←/→ or space to change"
	}
}

// Height is the form's natural height at this width: every field, a blank
// row, the hint or the wrapped error, the key line and both borders, plus
// whatever the command wrapped to and the folder dropdown is showing.
func (f formModel) Height(width int) int {
	// Every field, the blank row, the keys, both borders, then however many
	// lines the error, the wrapped command and the folder dropdown want.
	fixed := int(fieldCount) + 4
	extra := len(f.commandLines(width)) - 1
	return fixed + maxInt(len(f.errLines(width)), 1) + len(f.suggestionRows(width)) + extra
}

// commandLines renders the COMMAND field at this width, wrapped. The textarea
// is sized here because a width only exists at render time.
func (f formModel) commandLines(width int) []string {
	f.command.SetWidth(maxInt(width-formPrefix-3, 8))
	// LineCount counts logical lines, and this field only ever holds one, so
	// the field is rendered at full height and the unused rows are dropped.
	f.command.SetHeight(commandMaxLines)
	lines := strings.Split(f.command.View(), "\n")
	for len(lines) > 1 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// maxSuggestRows is how much of the dropdown is drawn at once; the window
// follows the selection through a longer list.
const maxSuggestRows = 6

// suggestionRows is the dropdown under FOLDER: the directories the typed
// path matches, the current one marked. Inline ghost text only ever shows
// one of them, which hides how many there are.
func (f formModel) suggestionRows(width int) []string {
	if f.field != fieldFolder {
		return nil
	}
	matches := f.folder.MatchedSuggestions()
	if len(matches) == 0 {
		return nil
	}

	cur := f.folder.CurrentSuggestionIndex()
	start := 0
	if cur >= maxSuggestRows {
		start = cur - maxSuggestRows + 1
	}
	end := minInt(start+maxSuggestRows, len(matches))

	pad := strings.Repeat(" ", formPrefix)
	room := maxInt(width-formPrefix-4, 8)
	out := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		name := truncate(filepath.Base(strings.TrimSuffix(matches[i], "/")), room)
		if i == cur {
			out = append(out, pad+styleHeader.Render("▸ "+name))
			continue
		}
		out = append(out, pad+styleDim.Render("  "+name))
	}
	if rest := len(matches) - end; rest > 0 {
		out = append(out, pad+styleDim.Render(fmt.Sprintf("  +%d more", rest)))
	}
	return out
}

// errLines wraps the error to the box, so a long one from the daemon is read
// in full instead of being cut at the border.
func (f formModel) errLines(width int) []string {
	if f.err == "" {
		return nil
	}
	wrapped := lipgloss.NewStyle().Width(maxInt(width-6, 12)).Render(f.err)
	return strings.Split(wrapped, "\n")
}

func (f formModel) View(width, height int) string {
	title := "New command"
	if f.editing {
		title = "Edit " + f.name.Value()
	}

	// Inputs get a width so bubbles scrolls a long value; without one a long
	// folder path ran straight into the border with nothing to say it was cut.
	avail := maxInt(width-formPrefix-3, 8)
	f.name.Width, f.folder.Width = avail, avail
	f.env.Width, f.health.Width = avail, avail
	f.port.Width = 6

	// A width only exists at render time, so a value filled in by OpenEdit
	// computed its scroll offset against a width of zero and stayed pinned to
	// its first character. Re-seat the cursor to recompute that offset, or a
	// long command opens showing its head with the tail cut at the border.
	for _, ti := range []*textinput.Model{&f.name, &f.folder, &f.env, &f.port, &f.health} {
		ti.SetCursor(ti.Position())
	}

	cmdLines := f.commandLines(width)
	rows := []string{
		f.row(fieldName, "NAME", f.nameView()),
		f.row(fieldCommand, "COMMAND", cmdLines[0]),
	}
	for _, line := range cmdLines[1:] {
		rows = append(rows, strings.Repeat(" ", formPrefix)+line)
	}
	rows = append(rows, f.row(fieldFolder, "FOLDER", f.folderView()))
	rows = append(rows, f.suggestionRows(width)...)
	rows = append(rows,
		f.row(fieldEnv, "ENV", f.envView()),
		f.row(fieldPort, "PORT", f.port.View()),
		f.row(fieldHealth, "HEALTH", f.health.View()),
		f.row(fieldRestart, "RESTART", f.restartView()),
		f.row(fieldAutostart, "AUTOSTART", f.autostartView()),
		"",
	)
	if lines := f.errLines(width); len(lines) > 0 {
		for i, line := range lines {
			prefix := "  ⚠ "
			if i > 0 {
				prefix = "    "
			}
			rows = append(rows, styleWarn.Render(prefix+line))
		}
	} else {
		rows = append(rows, styleDim.Render("  "+f.hint()))
	}
	rows = append(rows, styleDim.Render("  tab next · enter save · esc cancel"))

	return panelView(panelSpec{Title: title, Width: width, Height: height, Rows: rows, Focused: true})
}

// row marks the focused field in the gutter and in the label, so focus is
// visible without relying on the terminal's cursor being mid-blink.
func (f formModel) row(field formField, label, value string) string {
	gutter, shown := "  ", label
	if f.field == field {
		gutter = "› "
		shown = styleHeader.Render(label)
	}
	return gutter + shown + strings.Repeat(" ", maxInt(formPrefix-2-len(label), 1)) + value
}

// formPrefix is the width of a row's gutter plus its label column, wide
// enough for the longest label with one space after it.
const formPrefix = 12

func (f formModel) nameView() string {
	if f.editing {
		return styleDim.Render(f.name.Value() + "  (rename is a file edit)")
	}
	return f.name.View()
}

// folderView shows an untouched suggestion dimmed, so it reads as an offer
// rather than as something you typed.
func (f formModel) folderView() string {
	if !f.folderTyped && f.folder.Value() != "" && f.field != fieldFolder {
		return styleDim.Render(tail(f.folder.Value(), f.folder.Width))
	}
	return f.folder.View()
}

// envView shows a locked env dimmed, so it reads as something the file owns.
func (f formModel) envView() string {
	if f.envLocked {
		return styleDim.Render(formatEnv(f.base.Env))
	}
	return f.env.View()
}

// autostartView marks the choice in place, like RESTART.
func (f formModel) autostartView() string {
	yes, no := "yes", "no"
	if f.autostart {
		if f.field == fieldAutostart {
			return styleHeader.Render("[yes]") + styleDim.Render(" · "+no)
		}
		return yes + styleDim.Render(" · "+no)
	}
	if f.field == fieldAutostart {
		return styleDim.Render(yes+" · ") + styleHeader.Render("[no]")
	}
	return styleDim.Render(yes+" · ") + no
}

// restartView shows all three choices with the current one marked, so the
// options are readable without tabbing onto the field and cycling it.
func (f formModel) restartView() string {
	parts := make([]string, 0, len(restartChoices))
	for i, r := range restartChoices {
		switch {
		case i != f.restart:
			parts = append(parts, styleDim.Render(string(r)))
		case f.field == fieldRestart:
			parts = append(parts, styleHeader.Render("["+string(r)+"]"))
		default:
			parts = append(parts, string(r))
		}
	}
	return strings.Join(parts, styleDim.Render(" · "))
}

// tail keeps the end of a path, which is the part that identifies it, and
// marks the cut with a leading ellipsis.
func tail(s string, width int) string {
	if width < 4 || len(s) <= width {
		return s
	}
	return "…" + s[len(s)-width+1:]
}
