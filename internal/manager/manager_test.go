package manager

import (
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

// testManager builds a Manager over an in-memory config with test-speed
// timings.
func testManager(t *testing.T, cmds map[string]config.Command) *Manager {
	t.Helper()
	m := New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.BackoffMin = 10 * time.Millisecond
	m.BackoffMax = 40 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	m.UptimeReset = time.Hour
	return m
}

// waitState polls until name reaches want, or fails the test.
func waitState(t *testing.T, m *Manager, name string, want State) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last Status
	for time.Now().Before(deadline) {
		s, err := m.Status(name)
		if err != nil {
			t.Fatal(err)
		}
		last = s
		if s.State == want {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s state = %q, want %q (logs: %v)", name, last.State, want, tailLogs(t, m, name))
	return last
}

func tailLogs(t *testing.T, m *Manager, name string) []string {
	t.Helper()
	b, err := m.Logs(name)
	if err != nil {
		return nil
	}
	return b.Tail(10)
}

func sleeper() config.Command {
	return config.Command{Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}
}

func TestStartThenStop(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})

	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, "a", Running)
	if s.PID <= 0 {
		t.Fatalf("pid = %d, want > 0", s.PID)
	}
	if s.UptimeSec < 0 {
		t.Fatalf("uptime = %v", s.UptimeSec)
	}

	if err := m.Stop("a"); err != nil {
		t.Fatal(err)
	}
	s = waitState(t, m, "a", Stopped)
	if s.PID != 0 {
		t.Fatalf("pid = %d after stop, want 0", s.PID)
	}
}

func TestStartTwiceIsConflict(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("a")
	waitState(t, m, "a", Running)

	err := m.Start("a")
	if !errors.Is(err, ErrWrongState) {
		t.Fatalf("err = %v, want ErrWrongState", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{})
	for _, err := range []error{
		m.Start("ghost"),
		m.Stop("ghost"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if _, err := m.Status("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Status err = %v, want ErrNotFound", err)
	}
}

func TestSpawnFailureIsFailedState(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"/nonexistent/binary"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err == nil {
		t.Fatal("Start err = nil, want a spawn error")
	}
	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Failed {
		t.Fatalf("state = %q, want failed", s.State)
	}
	if lines := tailLogs(t, m, "a"); len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "spawn failed") {
		t.Fatalf("logs = %v, want a spawn failure line", lines)
	}
}

func TestStopKillsChildTree(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		// The inner sh inherits the process group; killing the group must
		// take it with the parent.
		"a": {Cmd: []string{"sh", "-c", "sh -c 'exec sleep 30' & wait"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	pgid := waitState(t, m, "a", Running).PID

	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("process group %d not alive before stop: %v", pgid, err)
	}
	if err := m.Stop("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Stopped)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process group %d still alive after stop", pgid)
}

func TestExitZeroBecomesStopped(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"true"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, "a", Stopped)
	if s.ExitCode == nil || *s.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", s.ExitCode)
	}
}

func TestExitNonZeroBecomesFailed(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 3"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, "a", Failed)
	if s.ExitCode == nil || *s.ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", s.ExitCode)
	}
}

func TestListReturnsEveryCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper(), "b": sleeper()})
	got := m.List()
	if len(got) != 2 {
		t.Fatalf("List = %v, want 2 entries", got)
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("List not sorted by name: %v", got)
	}
	if got[0].State != Stopped {
		t.Fatalf("initial state = %q, want stopped", got[0].State)
	}
}

func TestLogFileWrittenWhenLogTrue(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "echo hi"}, Cwd: "/tmp", Log: true},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Stopped)

	b, err := m.Logs("a")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Tail(1); len(got) != 1 || got[0] != "hi" {
		t.Fatalf("Tail(1) = %v, want [hi]", got)
	}
}
