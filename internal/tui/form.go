package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/config"
)

type formField int

const (
	fieldName formField = iota
	fieldCommand
	fieldFolder
	fieldRestart
	fieldCount
)

// restartChoices is the cycle the RESTART field walks; three fixed values do
// not deserve free text.
var restartChoices = []config.Restart{config.RestartNo, config.RestartOnFailure, config.RestartAlways}

type formModel struct {
	editing bool
	base    config.Command // what we started from, so unshown fields survive

	name    textinput.Model
	command textinput.Model
	folder  textinput.Model
	restart int
	field   formField

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
	return formModel{
		name:      mk(80),
		command:   mk(400),
		folder:    mk(200),
		launchDir: launchDir,
		remote:    remote,
	}
}

// OpenCreate resets the form for a new command.
func (f *formModel) OpenCreate(projects map[string]string) {
	f.editing, f.base, f.err = false, config.Command{}, ""
	f.projects, f.folderTyped = projects, false
	f.restart, f.field = 0, fieldName

	f.name.Reset()
	f.command.Reset()
	f.folder.Reset()
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
	switch f.field {
	case fieldName:
		if !f.editing {
			f.name.Focus()
		}
	case fieldCommand:
		f.command.Focus()
	case fieldFolder:
		f.folder.Focus()
	}
}

func (f formModel) Update(msg tea.Msg) (formModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, nil
	}
	switch k.String() {
	case "tab", "down":
		f.field = (f.field + 1) % fieldCount
		f.focus()
		return f, nil
	case "shift+tab", "up":
		f.field = (f.field + fieldCount - 1) % fieldCount
		f.focus()
		return f, nil
	}

	if f.field == fieldRestart {
		switch k.String() {
		case "right", "l", " ":
			f.restart = (f.restart + 1) % len(restartChoices)
		case "left", "h":
			f.restart = (f.restart + len(restartChoices) - 1) % len(restartChoices)
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
// spec it opened with, so env, depends_on, health and port survive an edit.
func (f formModel) Result() (string, config.Command) {
	c := f.base
	cmd, shell := splitCommand(strings.TrimSpace(f.command.Value()))
	c.Cmd, c.Shell = cmd, shell
	c.Cwd = strings.TrimSpace(f.folder.Value())
	c.Restart = restartChoices[f.restart]
	return strings.TrimSpace(f.name.Value()), c
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
		if !f.folderTyped && f.folder.Value() != "" {
			return "suggested from where you launched · blank = ~"
		}
		return "where the command runs · blank = ~"
	default:
		return "no · on-failure · always"
	}
}

// Height is the form's natural height, so the caller does not leave it
// padded with blank rows.
func (f formModel) Height() int { return 9 }

func (f formModel) View(width, height int) string {
	title := "New command"
	if f.editing {
		title = "Edit " + f.name.Value()
	}

	// Inputs get a width so bubbles scrolls a long value; without one a long
	// folder path ran straight into the border with nothing to say it was cut.
	avail := maxInt(width-formPrefix-3, 8)
	f.name.Width, f.command.Width, f.folder.Width = avail, avail, avail

	rows := []string{
		f.row(fieldName, "NAME", f.nameView()),
		f.row(fieldCommand, "COMMAND", f.command.View()),
		f.row(fieldFolder, "FOLDER", f.folderView()),
		f.row(fieldRestart, "RESTART", f.restartView()),
		"",
	}
	if f.err != "" {
		rows = append(rows, styleWarn.Render("  ⚠ "+f.err))
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
	return gutter + shown + strings.Repeat(" ", maxInt(9-len(label), 1)) + value
}

// formPrefix is the width of a row's gutter plus its label column.
const formPrefix = 11

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

// restartView keeps the value in the same column whether or not it has focus;
// it used to shift two places as you tabbed onto it.
func (f formModel) restartView() string {
	v := string(restartChoices[f.restart])
	if f.field == fieldRestart {
		return "‹ " + v + " ›"
	}
	return styleDim.Render("‹ ") + v + styleDim.Render(" ›")
}

// tail keeps the end of a path, which is the part that identifies it, and
// marks the cut with a leading ellipsis.
func tail(s string, width int) string {
	if width < 4 || len(s) <= width {
		return s
	}
	return "…" + s[len(s)-width+1:]
}
