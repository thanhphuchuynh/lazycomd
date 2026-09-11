package tui

import (
	"fmt"
	"strings"
	"testing"
)

func newTestLogs(name string, lines ...string) logsModel {
	l := newLogs()
	l.SetSize(40, 5)
	l.Reset(name, lines)
	return l
}

func TestResetReplacesContentAndFollows(t *testing.T) {
	l := newTestLogs("tick", "one", "two")
	if l.Name() != "tick" {
		t.Fatalf("Name = %q, want tick", l.Name())
	}
	if !l.Following() {
		t.Fatal("Reset must turn follow on")
	}
	if !strings.Contains(l.View(), "two") {
		t.Fatalf("View missing content:\n%s", l.View())
	}

	l.Reset("greet", []string{"other"})
	if strings.Contains(l.View(), "two") {
		t.Fatalf("View still shows the previous command's lines:\n%s", l.View())
	}
}

func TestAppendFollowsBottom(t *testing.T) {
	l := newTestLogs("tick")
	for i := 0; i < 50; i++ {
		l.Append(fmt.Sprintf("line %d", i))
	}
	if !l.AtBottom() {
		t.Fatal("following pane should stay at the bottom")
	}
	if !strings.Contains(l.View(), "line 49") {
		t.Fatalf("newest line not visible:\n%s", l.View())
	}
}

func TestManualScrollStopsFollow(t *testing.T) {
	l := newTestLogs("tick")
	for i := 0; i < 50; i++ {
		l.Append(fmt.Sprintf("line %d", i))
	}

	l, _ = l.Update(key("k"))
	if l.Following() {
		t.Fatal("scrolling up must clear follow")
	}
	l.Append("line 50")
	if strings.Contains(l.View(), "line 50") {
		t.Fatalf("paused pane jumped to the new line:\n%s", l.View())
	}

	l, _ = l.Update(key("f"))
	if !l.Following() {
		t.Fatal("f must restore follow")
	}
	if !strings.Contains(l.View(), "line 50") {
		t.Fatalf("f must jump to the bottom:\n%s", l.View())
	}
}

func TestHalfPageAndJumpKeysClearFollow(t *testing.T) {
	for _, k := range []string{"ctrl+u", "ctrl+d", "g", "G", "j"} {
		l := newTestLogs("tick")
		for i := 0; i < 50; i++ {
			l.Append(fmt.Sprintf("line %d", i))
		}
		l, _ = l.Update(key(k))
		if l.Following() {
			t.Fatalf("%s must clear follow", k)
		}
	}
}

func TestLineCapDropsOldest(t *testing.T) {
	l := newTestLogs("tick")
	for i := 0; i < maxLogLines+10; i++ {
		l.Append(fmt.Sprintf("line %d", i))
	}
	if got := l.LineCount(); got != maxLogLines {
		t.Fatalf("LineCount = %d, want %d", got, maxLogLines)
	}
	if !strings.Contains(l.View(), fmt.Sprintf("line %d", maxLogLines+9)) {
		t.Fatal("newest line missing after the cap kicked in")
	}
}

func TestTitle(t *testing.T) {
	l := newLogs()
	l.SetSize(40, 5)
	if got := l.Title(); !strings.Contains(got, "no command") {
		t.Fatalf("empty Title = %q", got)
	}

	l.Reset("tick", []string{"a", "b"})
	if got := l.Title(); got != "tick — following" {
		t.Fatalf("Title = %q, want \"tick — following\"", got)
	}

	l, _ = l.Update(key("k"))
	if got := l.Title(); !strings.Contains(got, "paused") || !strings.Contains(got, "2 lines") {
		t.Fatalf("paused Title = %q", got)
	}
}

func TestLongLinesAreTruncatedNotWrapped(t *testing.T) {
	l := newLogs()
	l.SetSize(20, 4)
	l.Reset("tick", []string{strings.Repeat("x", 200)})

	for _, line := range strings.Split(l.View(), "\n") {
		if len([]rune(line)) > 20 {
			t.Fatalf("line is %d runes wide, want <= 20: %q", len([]rune(line)), line)
		}
	}
}
