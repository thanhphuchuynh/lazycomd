package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/api"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// testDaemon serves a manager over a unix socket and points LAZYCOMD_ADDR at
// it. It returns the manager so a test can assert daemon-side state.
func testDaemon(t *testing.T, cmds map[string]config.Command) *manager.Manager {
	t.Helper()
	m := manager.New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.NewServer(m, "", func() (*config.Config, error) {
		return &config.Config{Commands: cmds}, nil
	}, nil)
	sock := filepath.Join(shortDir(t), "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	t.Setenv("LAZYCOMD_ADDR", "unix://"+sock)
	t.Setenv("LAZYCOMD_TOKEN", "")
	return m
}

// capture swaps os.Stdout for a pipe and returns everything fn printed.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	os.Stdout = orig
	w.Close()
	out := <-done
	r.Close()
	return out
}

func TestDispatchUsageErrors(t *testing.T) {
	if code := dispatch(nil); code != 2 {
		t.Fatalf("no args = %d, want 2", code)
	}
	if code := dispatch([]string{"nope"}); code != 2 {
		t.Fatalf("unknown subcommand = %d, want 2", code)
	}
	if code := dispatch([]string{"start"}); code != 2 {
		t.Fatalf("start without a name = %d, want 2", code)
	}
	if code := dispatch([]string{"help"}); code != 0 {
		t.Fatalf("help = %d, want 0", code)
	}
}

func TestDispatchNoDaemon(t *testing.T) {
	t.Setenv("LAZYCOMD_ADDR", "unix://"+filepath.Join(t.TempDir(), "absent.sock"))
	if code := dispatch([]string{"ls"}); code != 1 {
		t.Fatalf("ls with no daemon = %d, want 1", code)
	}
}

func TestLsListsCommands(t *testing.T) {
	testDaemon(t, map[string]config.Command{"proxy": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}})

	out := capture(t, func() {
		if code := dispatch([]string{"ls"}); code != 0 {
			t.Errorf("ls = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "proxy") || !strings.Contains(out, "stopped") {
		t.Fatalf("ls output = %q", out)
	}
}

func TestStartStopAndLogs(t *testing.T) {
	m := testDaemon(t, map[string]config.Command{
		"hello": {Cmd: []string{"sh", "-c", "echo hi; sleep 30"}, Cwd: "/tmp"},
	})

	if code := dispatch([]string{"start", "hello"}); code != 0 {
		t.Fatalf("start = %d, want 0", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := m.Logs("hello"); err == nil && len(b.Tail(1)) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	out := capture(t, func() {
		if code := dispatch([]string{"logs", "hello", "-n", "5"}); code != 0 {
			t.Errorf("logs = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "hi") {
		t.Fatalf("logs output = %q, want it to contain hi", out)
	}

	if code := dispatch([]string{"stop", "hello"}); code != 0 {
		t.Fatalf("stop = %d, want 0", code)
	}
	s, err := m.Status("hello")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != manager.Stopped {
		t.Fatalf("state = %q after stop, want stopped", s.State)
	}
}

func TestStartResolvesBareName(t *testing.T) {
	m := testDaemon(t, map[string]config.Command{
		"app:api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	out := capture(t, func() {
		if code := dispatch([]string{"start", "api"}); code != 0 {
			t.Errorf("start = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "app:api") {
		t.Fatalf("start output = %q, want the qualified name", out)
	}
	s, err := m.Status("app:api")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != manager.Running {
		t.Fatalf("state = %q, want running", s.State)
	}
}

func TestStartAmbiguousNameIsError(t *testing.T) {
	testDaemon(t, map[string]config.Command{
		"app:api":     {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"scraper:api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	if code := dispatch([]string{"start", "api"}); code != 1 {
		t.Fatalf("ambiguous start = %d, want 1", code)
	}
}

func TestRestartSubcommand(t *testing.T) {
	m := testDaemon(t, map[string]config.Command{
		"a": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	if code := dispatch([]string{"start", "a"}); code != 0 {
		t.Fatal("start failed")
	}
	before, _ := m.Status("a")

	out := capture(t, func() {
		if code := dispatch([]string{"restart", "a"}); code != 0 {
			t.Errorf("restart = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "running") {
		t.Fatalf("restart output = %q", out)
	}
	after, _ := m.Status("a")
	if after.PID == before.PID {
		t.Fatalf("pid unchanged after restart: %d", after.PID)
	}
}

func TestReloadSubcommand(t *testing.T) {
	testDaemon(t, map[string]config.Command{"a": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}})
	out := capture(t, func() {
		if code := dispatch([]string{"reload"}); code != 0 {
			t.Errorf("reload = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "a") {
		t.Fatalf("reload output = %q", out)
	}
}

func TestHoistFlags(t *testing.T) {
	got := hoistFlags([]string{"hello", "-n", "5", "-f"}, map[string]bool{"-n": true})
	want := "-n 5 -f hello"
	if strings.Join(got, " ") != want {
		t.Fatalf("hoistFlags = %q, want %q", strings.Join(got, " "), want)
	}
	got = hoistFlags([]string{"-d", "api"}, nil)
	if strings.Join(got, " ") != "-d api" {
		t.Fatalf("hoistFlags = %q", strings.Join(got, " "))
	}
}

func TestBareInvocationWithoutATTYPrintsUsage(t *testing.T) {
	// go test never gives us a terminal, so this exercises the guard: the
	// TUI must not launch, and the exit code stays 2 as before.
	testDaemon(t, map[string]config.Command{"a": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}})
	if code := dispatch(nil); code != 2 {
		t.Fatalf("bare dispatch = %d, want 2 without a TTY", code)
	}
}

func TestUsageMentionsTheTUI(t *testing.T) {
	if !strings.Contains(usage, "TUI") {
		t.Fatalf("usage should say the bare command opens the TUI:\n%s", usage)
	}
}
