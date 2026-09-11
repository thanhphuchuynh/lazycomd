package tui

import (
	"fmt"
	"strings"
)

// confirmModel asks before an irreversible edit to a file.
type confirmModel struct {
	name string
	file string
}

func newConfirm() confirmModel { return confirmModel{} }

func (c *confirmModel) Open(name, file string) {
	c.name, c.file = name, file
}

func (c confirmModel) Name() string { return c.name }

func (c confirmModel) View(width, height int) string {
	rows := []string{
		"",
		fmt.Sprintf("  delete %s from %s?", c.name, c.file),
		"",
		styleDim.Render("  this edits a file you may have committed"),
		"",
		styleDim.Render("  y delete · n or esc cancel"),
	}
	return panelView(panelSpec{Title: "Confirm", Width: width, Height: height, Rows: rows, Focused: true})
}

// targetFileLabel names the file a write to this command will change, so the
// prompt never surprises somebody by editing a project's shared config.
func targetFileLabel(name string) string {
	ns, _, namespaced := strings.Cut(name, ":")
	if !namespaced {
		return "config.yaml"
	}
	return ns + "/lazycomd.yaml"
}
