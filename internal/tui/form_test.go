package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

func typeForm(f formModel, s string) formModel {
	for _, r := range s {
		f, _ = f.Update(key(string(r)))
	}
	return f
}

func TestSplitCommand(t *testing.T) {
	cmd, shell := splitCommand("npm start")
	if shell || !slices.Equal(cmd, []string{"npm", "start"}) {
		t.Fatalf("plain line = %v, shell=%v", cmd, shell)
	}

	for _, line := range []string{
		"while true; do echo hi; sleep 1; done",
		"tail -f log | grep error",
		"echo $HOME",
		"ls *.go",
		"cmd > out.txt",
	} {
		cmd, shell := splitCommand(line)
		if !shell {
			t.Fatalf("%q should switch to shell mode", line)
		}
		if len(cmd) != 1 || cmd[0] != line {
			t.Fatalf("%q should pass through whole, got %v", line, cmd)
		}
	}
}

func TestFormNavigationWraps(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)

	if f.field != fieldName {
		t.Fatal("should open on the name field")
	}
	for i := 0; i < int(fieldCount); i++ {
		f, _ = f.Update(key("tab"))
	}
	if f.field != fieldName {
		t.Fatalf("field = %v after a full cycle, want back to name", f.field)
	}
	f, _ = f.Update(key("shift+tab"))
	if f.field != fieldCount-1 {
		t.Fatalf("shift+tab from name went to %v, want the last field", f.field)
	}
}

func TestFormRestartCycles(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)
	for f.field != fieldRestart {
		f, _ = f.Update(key("tab"))
	}

	if _, c := f.Result(); c.Restart != config.RestartNo {
		t.Fatalf("restart starts at %q, want no", c.Restart)
	}
	f, _ = f.Update(key("right"))
	if _, c := f.Result(); c.Restart != config.RestartOnFailure {
		t.Fatalf("after right = %q, want on-failure", c.Restart)
	}
	f, _ = f.Update(key("right"))
	if _, c := f.Result(); c.Restart != config.RestartAlways {
		t.Fatalf("after two rights = %q, want always", c.Restart)
	}
	f, _ = f.Update(key("right"))
	if _, c := f.Result(); c.Restart != config.RestartNo {
		t.Fatalf("restart did not wrap: %q", c.Restart)
	}
	f, _ = f.Update(key("left"))
	if _, c := f.Result(); c.Restart != config.RestartAlways {
		t.Fatalf("left did not wrap backwards: %q", c.Restart)
	}
}

func TestFormPrefillsFolderFromTheLaunchDirectory(t *testing.T) {
	f := newForm("/home/me/coding/app", false)
	f.OpenCreate(nil)

	if _, c := f.Result(); c.Cwd != "/home/me/coding/app" {
		t.Fatalf("folder = %q, want the launch directory", c.Cwd)
	}
}

func TestFormLeavesFolderBlankForARemoteDaemon(t *testing.T) {
	f := newForm("/home/me/coding/app", true)
	f.OpenCreate(nil)

	if _, c := f.Result(); c.Cwd != "" {
		t.Fatalf("folder = %q, want blank: a local path means nothing on another machine", c.Cwd)
	}
}

func TestFormFolderFollowsTheProjectOnceNamespaced(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(map[string]string{"app": "/home/me/coding/app"})

	f = typeForm(f, "app:api")
	if _, c := f.Result(); c.Cwd != "/home/me/coding/app" {
		t.Fatalf("folder = %q, want the project directory", c.Cwd)
	}
}

func TestFormKeepsAFolderYouTyped(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(map[string]string{"app": "/home/me/coding/app"})

	for f.field != fieldFolder {
		f, _ = f.Update(key("tab"))
	}
	f, _ = f.Update(key("ctrl+u"))
	f = typeForm(f, "/srv")
	for f.field != fieldName {
		f, _ = f.Update(key("tab"))
	}
	f = typeForm(f, "app:api")

	if _, c := f.Result(); c.Cwd != "/srv" {
		t.Fatalf("folder = %q, want the value typed by hand", c.Cwd)
	}
}

func TestFormEditPreservesFieldsItDoesNotShow(t *testing.T) {
	base := config.Command{
		Cmd:       []string{"node", "server.js"},
		Cwd:       "/srv",
		Env:       map[string]string{"LOG": "debug"},
		DependsOn: []string{"db"},
		Health:    "http://localhost:3000/healthz",
		Port:      3000,
		Restart:   config.RestartAlways,
	}
	f := newForm("/home/me", false)
	f.OpenEdit("api", base, nil)

	if !f.Editing() {
		t.Fatal("Editing() = false after OpenEdit")
	}
	name, got := f.Result()
	if name != "api" {
		t.Fatalf("name = %q", name)
	}
	if got.Env["LOG"] != "debug" || got.Health != base.Health || got.Port != 3000 || len(got.DependsOn) != 1 {
		t.Fatalf("edit dropped fields the form never shows: %+v", got)
	}
	if got.Restart != config.RestartAlways {
		t.Fatalf("restart = %q, want the existing value prefilled", got.Restart)
	}
}

func TestFormEditLocksTheName(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenEdit("api", config.Command{Cmd: []string{"x"}}, nil)

	f = typeForm(f, "zzz")
	if name, _ := f.Result(); name != "api" {
		t.Fatalf("name = %q, want it unchanged: renaming is a file edit", name)
	}
}

func TestFormViewShowsFieldsErrorAndHint(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)
	f = typeForm(f, "web")

	view := f.View(60, 14)
	for _, want := range []string{"NAME", "COMMAND", "FOLDER", "RESTART", "web", "tab next"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}

	f.SetError(`command "web" already exists`)
	if v := f.View(60, 14); !strings.Contains(v, "already exists") || !strings.Contains(v, "web") {
		t.Fatalf("error not shown with input kept:\n%s", v)
	}
}

func TestFormViewFitsItsBox(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)

	view := f.View(48, 12)
	lines := strings.Split(view, "\n")
	if len(lines) != 12 {
		t.Fatalf("view is %d lines, want 12", len(lines))
	}
	for i, l := range lines {
		if w := runeWidth(l); w != 48 {
			t.Fatalf("line %d is %d wide, want 48: %q", i, w, l)
		}
	}
}

func TestFormErrorWrapsAndGrowsTheBox(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)
	plain := f.Height(50)

	f.SetError(strings.Repeat("long error ", 20))
	lines := f.errLines(50)
	if len(lines) < 2 {
		t.Fatalf("error should wrap, got %d line(s)", len(lines))
	}
	for _, l := range lines {
		if len(l) > 50 {
			t.Fatalf("line %q is wider than the box", l)
		}
	}
	if got := f.Height(50); got != plain+len(lines)-1 {
		t.Fatalf("height = %d, want %d", got, plain+len(lines)-1)
	}
	if !strings.Contains(f.View(50, f.Height(50)), "⚠") {
		t.Fatal("view should show the error")
	}
}

func TestFolderCompletesRealDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "proxy-tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proxy-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	f := newForm(dir, false)
	f.OpenCreate(nil)
	f.field = fieldFolder
	f.focus()
	f.folder.SetValue(filepath.Join(dir, "pro"))
	f, _ = f.Update(key("x")) // any keystroke refreshes the suggestions
	f.folder.SetValue(filepath.Join(dir, "pro"))
	f.folder.SetSuggestions(f.dirSuggestions())

	if !f.completing() {
		t.Fatalf("no completion for %q", f.folder.Value())
	}
	if got := f.folder.CurrentSuggestion(); got != filepath.Join(dir, "proxy-tools")+"/" {
		t.Fatalf("suggestion = %q, want the directory, never the file", got)
	}

	f, _ = f.Update(key("tab")) // tab finishes the path instead of moving on
	if f.field != fieldFolder {
		t.Fatalf("tab left the folder field: %v", f.field)
	}
	if got := f.folder.Value(); got != filepath.Join(dir, "proxy-tools")+"/" {
		t.Fatalf("value after tab = %q", got)
	}
	f, _ = f.Update(key("tab")) // nothing left to complete, so tab moves on
	if f.field != fieldEnv {
		t.Fatalf("second tab went to %v, want env", f.field)
	}
}

func TestFolderDropdownListsMatchesAndMovesWithArrows(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"proxy", "proxy-old"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := newForm(dir, false)
	f.OpenCreate(nil)
	f.field = fieldFolder
	f.folder.SetValue(filepath.Join(dir, "pro"))
	f.focus()

	view := f.View(76, f.Height(76))
	for _, want := range []string{"proxy", "proxy-old"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dropdown is missing %q:\n%s", want, view)
		}
	}
	if got, want := f.folder.CurrentSuggestionIndex(), 0; got != want {
		t.Fatalf("selection = %d, want %d", got, want)
	}

	// down walks the list instead of leaving the field.
	f, _ = f.Update(key("down"))
	if f.field != fieldFolder {
		t.Fatalf("down left the folder field: %v", f.field)
	}
	if got := f.folder.CurrentSuggestionIndex(); got != 1 {
		t.Fatalf("selection after down = %d, want 1", got)
	}
	if f.Height(76) <= newForm(dir, false).Height(76) {
		t.Fatal("the open dropdown should make the box taller")
	}
}

func TestLongCommandWrapsInsteadOfScrolling(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenEdit("db", config.Command{
		Cmd: []string{"./cloud-sql-proxy", "project-aviron:us-central1:aviron-postgres-server-alpha-dev", "--port", "5433"},
	}, nil)

	lines := f.commandLines(60)
	if len(lines) < 2 {
		t.Fatalf("a long command should wrap, got %d line(s): %q", len(lines), lines)
	}
	if len(lines) > commandMaxLines {
		t.Fatalf("wrapped to %d lines, past the %d cap", len(lines), commandMaxLines)
	}
	// Every word of the command is on screen, and the box grew to hold it.
	joined := strings.Join(lines, "")
	for _, want := range []string{"cloud-sql-proxy", "aviron-postgres-server-alpha-dev", "5433"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("wrapped view is missing %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
	if f.Height(60) <= 9 {
		t.Fatalf("height = %d, should grow with the wrapped field", f.Height(60))
	}
	// Wrapping is display only: the saved command is still one line.
	_, c := f.Result()
	if len(c.Cmd) != 4 || c.Shell {
		t.Fatalf("Result() = %v shell=%v", c.Cmd, c.Shell)
	}
}

func TestFormEditsEveryFieldItShows(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenEdit("db", config.Command{
		Cmd:    []string{"./proxy"},
		Env:    map[string]string{"LOG": "debug"},
		Port:   5433,
		Health: "http://localhost:5433/healthz",
	}, nil)

	if got := f.env.Value(); got != "LOG=debug" {
		t.Fatalf("env field = %q", got)
	}
	if got := f.port.Value(); got != "5433" {
		t.Fatalf("port field = %q", got)
	}

	f.env.SetValue("LOG=info PGPASSWORD=hunter2")
	f.port.SetValue("6000")
	f.health.SetValue("http://localhost:6000/up")
	f.field = fieldAutostart
	f, _ = f.Update(key(" "))

	_, c := f.Result()
	if c.Env["LOG"] != "info" || c.Env["PGPASSWORD"] != "hunter2" || len(c.Env) != 2 {
		t.Fatalf("env = %v", c.Env)
	}
	if c.Port != 6000 || c.Health != "http://localhost:6000/up" || !c.Autostart {
		t.Fatalf("port=%d health=%q autostart=%v", c.Port, c.Health, c.Autostart)
	}
}

func TestFormValidateCatchesBadInput(t *testing.T) {
	open := func() formModel {
		f := newForm("/home/me", false)
		f.OpenCreate(nil)
		f.name.SetValue("db")
		f.command.SetValue("./proxy")
		return f
	}

	if err := open().Validate(); err != nil {
		t.Fatalf("a good form should validate: %v", err)
	}
	for _, tc := range []struct{ field, value, want string }{
		{"port", "nope", "not a number"},
		{"port", "70000", "out of range"},
		{"env", "LOGdebug", "not KEY=VALUE"},
		{"health", "ftp://x/y", "scheme must be http"},
	} {
		f := open()
		switch tc.field {
		case "port":
			f.port.SetValue(tc.value)
		case "env":
			f.env.SetValue(tc.value)
		case "health":
			f.health.SetValue(tc.value)
		}
		err := f.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s=%q gave %v, want %q", tc.field, tc.value, err, tc.want)
		}
	}
}

// An env value with a space cannot survive the one-line field, so the field
// goes read-only rather than writing back half of it.
func TestFormLocksEnvItCannotRoundTrip(t *testing.T) {
	env := map[string]string{"GREETING": "hello there"}
	f := newForm("/home/me", false)
	f.OpenEdit("db", config.Command{Cmd: []string{"./proxy"}, Env: env}, nil)

	if !f.envLocked {
		t.Fatal("env holding a space should lock the field")
	}
	f.field = fieldEnv
	f.focus()
	f = typeForm(f, "XXX")
	if _, c := f.Result(); c.Env["GREETING"] != "hello there" || len(c.Env) != 1 {
		t.Fatalf("locked env was rewritten: %v", c.Env)
	}
}

func TestCommandFieldLeavesNoBlankRows(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenEdit("db", config.Command{Cmd: []string{"./proxy"}}, nil)

	// A one-line command is one row, not one row plus the textarea's padding.
	if got := f.commandLines(60); len(got) != 1 {
		t.Fatalf("a short command rendered %d rows: %q", len(got), got)
	}
}

func TestSecondTabLeavesTheFolderField(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nested", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newForm(dir, false)
	f.OpenCreate(nil)
	f.field = fieldFolder
	f.folder.SetValue(filepath.Join(dir, "nes"))
	f.focus()
	f.folder.SetSuggestions(f.dirSuggestions())

	f, _ = f.Update(key("tab")) // completes to .../nested/
	if f.field != fieldFolder {
		t.Fatalf("the completing tab left the field: %v", f.field)
	}
	// "nested/" offers "deeper/" next; without the guard tab would complete
	// forever and never reach the next field.
	f, _ = f.Update(key("tab"))
	if f.field != fieldEnv {
		t.Fatalf("the second tab went to %v, want env", f.field)
	}
}
