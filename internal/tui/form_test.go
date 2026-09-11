package tui

import (
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
	if f.field != fieldRestart {
		t.Fatalf("shift+tab from name went to %v, want restart", f.field)
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
