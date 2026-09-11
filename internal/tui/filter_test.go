package tui

import (
	"strings"
	"testing"
)

func typeInto(l logsModel, s string) logsModel {
	for _, r := range s {
		l, _ = l.Update(key(string(r)))
	}
	return l
}

func TestSmartContains(t *testing.T) {
	if !smartContains("ERROR: boom", "error") {
		t.Fatal("lowercase query must match case-insensitively")
	}
	if smartContains("error: boom", "Error") {
		t.Fatal("mixed-case query must be case-sensitive")
	}
	if !smartContains("Error: boom", "Error") {
		t.Fatal("exact case must match")
	}
}

func TestFilterShowsOnlyMatches(t *testing.T) {
	l := newTestLogs("tick", "starting up", "error: boom", "still fine", "error: again")

	l, _ = l.Update(key("/"))
	if !l.FilterEditing() {
		t.Fatal("/ must open the filter input")
	}
	l = typeInto(l, "error")
	l, _ = l.Update(key("enter"))

	if l.FilterEditing() {
		t.Fatal("enter must close the input")
	}
	if l.Query() != "error" {
		t.Fatalf("Query = %q, want error", l.Query())
	}

	view := l.View()
	if strings.Contains(view, "still fine") || strings.Contains(view, "starting up") {
		t.Fatalf("non-matching lines still visible:\n%s", view)
	}
	if !strings.Contains(view, "error: boom") || !strings.Contains(view, "error: again") {
		t.Fatalf("matching lines missing:\n%s", view)
	}
	if title := l.Title(); !strings.Contains(title, `filter "error"`) || !strings.Contains(title, "2 of 4") {
		t.Fatalf("Title = %q, want the filter and the counts", title)
	}
}

func TestFilterKeepsFollowingNewMatches(t *testing.T) {
	l := newTestLogs("tick", "error: one")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "error")
	l, _ = l.Update(key("enter"))

	l.Append("quiet line")
	l.Append("error: two")

	view := l.View()
	if strings.Contains(view, "quiet line") {
		t.Fatalf("non-matching new line leaked in:\n%s", view)
	}
	if !strings.Contains(view, "error: two") {
		t.Fatalf("matching new line missing:\n%s", view)
	}
}

func TestEscWhileEditingClearsTheFilter(t *testing.T) {
	l := newTestLogs("tick", "a", "b")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "a")
	l, _ = l.Update(key("esc"))

	if l.FilterEditing() || l.Query() != "" {
		t.Fatalf("esc must cancel: editing=%v query=%q", l.FilterEditing(), l.Query())
	}
	if !strings.Contains(l.View(), "b") {
		t.Fatalf("all lines should be back:\n%s", l.View())
	}
}

func TestEscAfterApplyingClearsTheFilter(t *testing.T) {
	l := newTestLogs("tick", "a", "b")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "a")
	l, _ = l.Update(key("enter"))
	l, _ = l.Update(key("esc"))

	if l.Query() != "" {
		t.Fatalf("Query = %q, want empty", l.Query())
	}
	if !strings.Contains(l.View(), "b") {
		t.Fatalf("all lines should be back:\n%s", l.View())
	}
}

func TestScrollKeysAreLiteralWhileEditing(t *testing.T) {
	l := newTestLogs("tick", "one", "two")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "j")

	if !l.FilterEditing() {
		t.Fatal("j must not close the input")
	}
	l, _ = l.Update(key("enter"))
	if l.Query() != "j" {
		t.Fatalf("Query = %q, want j typed as text", l.Query())
	}
}

func TestResetClearsTheFilter(t *testing.T) {
	l := newTestLogs("tick", "error: boom")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "error")
	l, _ = l.Update(key("enter"))

	l.Reset("greet", []string{"hello"})
	if l.Query() != "" {
		t.Fatalf("Query = %q after Reset, want empty", l.Query())
	}
	if !strings.Contains(l.View(), "hello") {
		t.Fatalf("new content missing:\n%s", l.View())
	}
}

func TestCancelFilterEdit(t *testing.T) {
	l := newTestLogs("tick", "a")
	l, _ = l.Update(key("/"))
	l.CancelFilterEdit()
	if l.FilterEditing() {
		t.Fatal("CancelFilterEdit must close the input")
	}
}

func TestFilterInputIsVisibleWhileEditing(t *testing.T) {
	l := newTestLogs("tick", "a")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "err")
	if !strings.Contains(l.View(), "err") {
		t.Fatalf("input line not rendered:\n%s", l.View())
	}
}
