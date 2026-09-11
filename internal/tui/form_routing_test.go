package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

func TestAOpensAnEmptyForm(t *testing.T) {
	m := modelWithRows(t, "web")

	m, _ = step(t, m, key("a"))
	if m.overlay != overlayForm {
		t.Fatal("a should open the form")
	}
	if m.form.Editing() {
		t.Fatal("a opens a create, not an edit")
	}
	if !strings.Contains(m.View(), "New command") {
		t.Fatalf("form not rendered:\n%s", m.View())
	}
}

func TestEFetchesTheSpecThenOpensTheForm(t *testing.T) {
	m := modelWithRows(t, "web")

	_, cmd := step(t, m, key("e"))
	if cmd == nil {
		t.Fatal("e produced no command: it must fetch the spec first")
	}

	m, _ = step(t, m, commandConfigMsg{
		name: "web",
		cmd:  config.Command{Cmd: []string{"npm", "start"}, Health: "http://localhost:3000/h"},
	})
	if m.overlay != overlayForm || !m.form.Editing() {
		t.Fatal("the config reply should open the form in edit mode")
	}
	if _, got := m.form.Result(); got.Health == "" {
		t.Fatal("the fetched spec was not carried into the form")
	}
}

func TestFormEnterSaves(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("a"))
	for _, r := range "extra" {
		m, _ = step(t, m, key(string(r)))
	}

	// A name alone is not enough: the command is required too.
	m2, cmd := step(t, m, key("enter"))
	if cmd != nil {
		t.Fatal("enter saved with no command")
	}
	if !strings.Contains(m2.View(), "command is required") {
		t.Fatalf("missing command not explained:\n%s", m2.View())
	}

	m, _ = step(t, m, key("tab"))
	for _, r := range "sleep 30" {
		m, _ = step(t, m, key(string(r)))
	}
	_, cmd = step(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no save command")
	}
}

func TestFormEscCancels(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("a"))
	m, _ = step(t, m, key("esc"))

	if m.overlay != overlayNone {
		t.Fatal("esc should close the form")
	}
}

func TestFormErrorKeepsTheFormOpen(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("a"))
	for _, r := range "web" {
		m, _ = step(t, m, key(string(r)))
	}

	m, _ = step(t, m, formErrMsg{err: errors.New(`command already exists: web`)})
	if m.overlay != overlayForm {
		t.Fatal("an error must not close the form")
	}
	view := m.View()
	if !strings.Contains(view, "already exists") {
		t.Fatalf("error not shown:\n%s", view)
	}
	if !strings.Contains(view, "web") {
		t.Fatalf("input was lost:\n%s", view)
	}
}

func TestFormSaveClosesAndSelects(t *testing.T) {
	m := modelWithRows(t, "web", "extra")
	m, _ = step(t, m, key("a"))

	m, _ = step(t, m, formSavedMsg{status: manager.Status{Name: "extra", State: manager.Stopped}})
	if m.overlay != overlayNone {
		t.Fatal("a save should close the form")
	}
	if got, _ := m.table.Selected(); got.Name != "extra" {
		t.Fatalf("selected = %q, want the command just saved", got.Name)
	}
}

func TestFormKeysDoNotLeakToPanels(t *testing.T) {
	m := modelWithRows(t, "web", "other")
	before, _ := m.table.Selected()

	m, _ = step(t, m, key("a"))
	m, _ = step(t, m, key("j"))

	if after, _ := m.table.Selected(); after.Name != before.Name {
		t.Fatal("j moved the table cursor while the form was open")
	}
	if m.overlay != overlayForm {
		t.Fatal("the form closed on a plain keystroke")
	}
}

func TestProjectsMsgFeedsTheForm(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, projectsMsg(map[string]string{"app": "/home/me/coding/app"}))
	m, _ = step(t, m, key("a"))

	for _, r := range "app:api" {
		m, _ = step(t, m, key(string(r)))
	}
	if _, got := m.form.Result(); got.Cwd != "/home/me/coding/app" {
		t.Fatalf("folder = %q, want the project directory from projectsMsg", got.Cwd)
	}
}
