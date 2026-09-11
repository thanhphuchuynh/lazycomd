package manager

import (
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
)

func TestOnFailureRestarts(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 1"}, Cwd: "/tmp", Restart: config.RestartOnFailure},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("a")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := m.Status("a")
		if err != nil {
			t.Fatal(err)
		}
		if s.Restarts >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := m.Status("a")
	t.Fatalf("restarts = %d, want >= 2", s.Restarts)
}

func TestOnFailureIgnoresCleanExit(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"true"}, Cwd: "/tmp", Restart: config.RestartOnFailure},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Stopped)

	time.Sleep(150 * time.Millisecond)
	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Stopped || s.Restarts != 0 {
		t.Fatalf("state = %q restarts = %d, want stopped and 0", s.State, s.Restarts)
	}
}

func TestAlwaysRestartsOnCleanExit(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"true"}, Cwd: "/tmp", Restart: config.RestartAlways},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("a")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := m.Status("a")
		if s.Restarts >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := m.Status("a")
	t.Fatalf("restarts = %d, want >= 1", s.Restarts)
}

func TestRestartNoStaysDown(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 1"}, Cwd: "/tmp", Restart: config.RestartNo},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Failed)

	time.Sleep(150 * time.Millisecond)
	s, _ := m.Status("a")
	if s.Restarts != 0 {
		t.Fatalf("restarts = %d, want 0", s.Restarts)
	}
}

func TestManualStopBeatsRestartPolicy(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 1"}, Cwd: "/tmp", Restart: config.RestartAlways},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop("a"); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Status("a")

	time.Sleep(200 * time.Millisecond)
	after, _ := m.Status("a")
	if after.State != Stopped {
		t.Fatalf("state = %q, want stopped", after.State)
	}
	if after.Restarts != before.Restarts {
		t.Fatalf("restarts went %d -> %d after a manual stop", before.Restarts, after.Restarts)
	}
}

func TestNextBackoffDoublesToCap(t *testing.T) {
	m := New(&config.Config{Commands: map[string]config.Command{}}, t.TempDir())
	m.BackoffMin = 1 * time.Second
	m.BackoffMax = 4 * time.Second
	p := &Process{Name: "a"}

	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	for i, w := range want {
		if got := m.nextBackoff(p); got != w {
			t.Fatalf("backoff %d = %v, want %v", i, got, w)
		}
	}
	p.backoff = 0
	if got := m.nextBackoff(p); got != time.Second {
		t.Fatalf("after reset = %v, want 1s", got)
	}
}

func TestShouldRestart(t *testing.T) {
	m := New(&config.Config{Commands: map[string]config.Command{}}, t.TempDir())
	cases := []struct {
		policy config.Restart
		code   int
		want   bool
	}{
		{config.RestartNo, 1, false},
		{config.RestartNo, 0, false},
		{config.RestartOnFailure, 1, true},
		{config.RestartOnFailure, 0, false},
		{config.RestartAlways, 0, true},
		{config.RestartAlways, 1, true},
	}
	for _, tc := range cases {
		if got := m.shouldRestart(tc.policy, tc.code); got != tc.want {
			t.Fatalf("shouldRestart(%q, %d) = %v, want %v", tc.policy, tc.code, got, tc.want)
		}
	}
}
