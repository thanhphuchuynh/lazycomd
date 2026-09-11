package manager

import (
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

func TestReloadAddsCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	if err := m.Reload(&config.Config{Commands: map[string]config.Command{
		"a": sleeper(),
		"b": sleeper(),
	}}); err != nil {
		t.Fatal(err)
	}
	s, err := m.Status("b")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Stopped {
		t.Fatalf("new command state = %q, want stopped", s.State)
	}
}

func TestReloadRemovesAndStopsCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	pgid := waitState(t, m, "a", Running).PID

	if err := m.Reload(&config.Config{Commands: map[string]config.Command{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after removal", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process group %d still alive after removal", pgid)
}

func TestReloadStagesChangeOnRunningCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	defer m.Stop("a")
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	pgid := waitState(t, m, "a", Running).PID

	changed := config.Command{Cmd: []string{"sh", "-c", "echo v2"}, Cwd: "/tmp"}
	if err := m.Reload(&config.Config{Commands: map[string]config.Command{"a": changed}}); err != nil {
		t.Fatal(err)
	}

	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Running {
		t.Fatalf("state = %q, want the running process untouched", s.State)
	}
	if !s.SpecDirty {
		t.Fatal("spec_dirty = false, want true")
	}
	if s.PID != pgid {
		t.Fatalf("pid changed %d -> %d, want no restart on reload", pgid, s.PID)
	}

	// The staged spec takes effect on the next start.
	if err := m.Restart("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Stopped)

	s, _ = m.Status("a")
	if s.SpecDirty {
		t.Fatal("spec_dirty = true after restart, want false")
	}
	b, err := m.Logs("a")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Tail(1); len(got) != 1 || got[0] != "v2" {
		t.Fatalf("Tail(1) = %v, want [v2]", got)
	}
}

func TestReloadAppliesChangeOnStoppedCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	changed := config.Command{Cmd: []string{"true"}, Cwd: "/tmp"}
	if err := m.Reload(&config.Config{Commands: map[string]config.Command{"a": changed}}); err != nil {
		t.Fatal(err)
	}
	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.SpecDirty {
		t.Fatal("spec_dirty = true on a stopped command, want false")
	}

	m.mu.Lock()
	got := m.procs["a"].Spec.Cmd[0]
	m.mu.Unlock()
	if got != "true" {
		t.Fatalf("spec cmd = %q, want the reloaded value", got)
	}
}
