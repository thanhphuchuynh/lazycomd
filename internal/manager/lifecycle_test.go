package manager

import (
	"errors"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

func TestStartAutostartStartsDepsFirst(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":    {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api":   {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}, Autostart: true},
		"quiet": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	defer m.Shutdown()

	m.StartAutostart()
	waitState(t, m, "api", Running)
	waitState(t, m, "db", Running)

	s, err := m.Status("quiet")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Stopped {
		t.Fatalf("quiet state = %q, want stopped", s.State)
	}

	m.mu.Lock()
	order := slices.Clone(m.startOrder)
	m.mu.Unlock()
	if !slices.Equal(order, []string{"db", "api"}) {
		t.Fatalf("startOrder = %v, want [db api]", order)
	}
}

func TestShutdownStopsEverything(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "sh -c 'exec sleep 30' & wait"}, Cwd: "/tmp"},
		"b": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("b"); err != nil {
		t.Fatal(err)
	}
	pgidA := waitState(t, m, "a", Running).PID
	pgidB := waitState(t, m, "b", Running).PID

	m.Shutdown()

	for _, pgid := range []int{pgidA, pgidB} {
		if err := syscall.Kill(-pgid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("process group %d still alive after Shutdown (err = %v)", pgid, err)
		}
	}
	for _, n := range []string{"a", "b"} {
		s, err := m.Status(n)
		if err != nil {
			t.Fatal(err)
		}
		if s.State != Stopped {
			t.Fatalf("%s state = %q after Shutdown, want stopped", n, s.State)
		}
	}
}

func TestShutdownCancelsPendingRestart(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 1"}, Cwd: "/tmp", Restart: config.RestartAlways},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Failed)
	m.Shutdown()

	time.Sleep(200 * time.Millisecond)
	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.State == Running {
		t.Fatal("command restarted after Shutdown")
	}
}

func TestKillAllIsImmediate(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		// Ignores SIGTERM: only SIGKILL ends it.
		"a": {Cmd: []string{"sh", "-c", "trap '' TERM; sleep 30"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	pgid := waitState(t, m, "a", Running).PID

	m.KillAll()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process group %d survived KillAll", pgid)
}
