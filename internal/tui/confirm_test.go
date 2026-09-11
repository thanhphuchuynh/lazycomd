package tui

import (
	"strings"
	"testing"
)

func TestTargetFileLabel(t *testing.T) {
	if got := targetFileLabel("web"); got != "config.yaml" {
		t.Fatalf("bare name = %q, want config.yaml", got)
	}
	if got := targetFileLabel("app:api"); got != "app/lazycomd.yaml" {
		t.Fatalf("namespaced = %q, want app/lazycomd.yaml", got)
	}
}

func TestConfirmViewNamesTheCommandAndFile(t *testing.T) {
	c := newConfirm()
	c.Open("app:api", targetFileLabel("app:api"))

	view := c.View(50, 7)
	for _, want := range []string{"app:api", "app/lazycomd.yaml", "y", "n"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestDAsksBeforeDeleting(t *testing.T) {
	m := modelWithRows(t, "web")

	m, cmd := step(t, m, key("d"))
	if m.overlay != overlayConfirm {
		t.Fatal("d should ask first")
	}
	if cmd != nil {
		t.Fatal("d fired a delete without asking")
	}
	if !strings.Contains(m.View(), "delete web") {
		t.Fatalf("prompt not rendered:\n%s", m.View())
	}
}

func TestConfirmNCancels(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("d"))

	m, cmd := step(t, m, key("n"))
	if m.overlay != overlayNone {
		t.Fatal("n should close the prompt")
	}
	if cmd != nil {
		t.Fatal("n fired a delete")
	}

	m, _ = step(t, m, key("d"))
	m, cmd = step(t, m, key("esc"))
	if m.overlay != overlayNone || cmd != nil {
		t.Fatal("esc should cancel too")
	}
}

func TestConfirmYDeletes(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("d"))

	m, cmd := step(t, m, key("y"))
	if m.overlay != overlayNone {
		t.Fatal("y should close the prompt")
	}
	if cmd == nil {
		t.Fatal("y produced no delete command")
	}
}

func TestDeletedMsgRefreshes(t *testing.T) {
	m := modelWithRows(t, "web", "other")
	m, cmd := step(t, m, deletedMsg{name: "web"})
	if cmd == nil {
		t.Fatal("a delete should refresh the list")
	}
	if !strings.Contains(m.bottom(), "web") {
		t.Fatalf("status line = %q, want it to mention the deleted command", m.bottom())
	}
}
