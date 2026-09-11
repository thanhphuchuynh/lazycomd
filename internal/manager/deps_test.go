package manager

import (
	"errors"
	"slices"
	"testing"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

func TestStartWithDepsOrdersDepthFirst(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":    {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"cache": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api":   {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db", "cache"}},
	})
	defer m.Shutdown()

	if err := m.StartWithDeps("api"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"db", "cache", "api"} {
		waitState(t, m, n, Running)
	}

	m.mu.Lock()
	order := slices.Clone(m.startOrder)
	m.mu.Unlock()

	want := []string{"db", "cache", "api"}
	if !slices.Equal(order, want) {
		t.Fatalf("startOrder = %v, want %v", order, want)
	}
}

func TestStartWithDepsSkipsRunningDeps(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":  {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}},
	})
	defer m.Shutdown()

	if err := m.Start("db"); err != nil {
		t.Fatal(err)
	}
	dbPID := waitState(t, m, "db", Running).PID

	if err := m.StartWithDeps("api"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "api", Running)

	if got := waitState(t, m, "db", Running).PID; got != dbPID {
		t.Fatalf("db pid changed %d -> %d, want the running dep left alone", dbPID, got)
	}
}

func TestStopDoesNotCascadeToDependents(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":  {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}},
	})
	defer m.Shutdown()

	if err := m.StartWithDeps("api"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "api", Running)

	if err := m.Stop("db"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "db", Stopped)

	s, err := m.Status("api")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Running {
		t.Fatalf("api state = %q after stopping db, want running", s.State)
	}
}

func TestStartWithDepsUnknownCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{})
	if err := m.StartWithDeps("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestStartWithDepsStillConflictsOnTheRequestedCommand guards the difference
// between the requested command and its dependencies: a running dependency is
// left alone, but starting a command that is already running is a conflict
// whether or not dependencies were asked for.
func TestStartWithDepsStillConflictsOnTheRequestedCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":  {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}},
	})
	defer m.Shutdown()

	if err := m.StartWithDeps("api"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "api", Running)

	if err := m.StartWithDeps("api"); !errors.Is(err, ErrWrongState) {
		t.Fatalf("err = %v, want ErrWrongState", err)
	}
}
