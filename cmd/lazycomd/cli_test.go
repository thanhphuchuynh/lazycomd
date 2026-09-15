package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/api"
	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/configw"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

// testDaemon serves a manager over a unix socket and points LAZYCOMD_ADDR at
// it. It returns the manager so a test can assert daemon-side state.
func testDaemon(t *testing.T, cmds map[string]config.Command) *manager.Manager {
	t.Helper()
	m := manager.New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.New(api.Options{
		Manager: m,
		Reload: func() (*config.Config, error) {
			return &config.Config{Commands: cmds}, nil
		},
	})
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

// testDaemonWithConfig serves a daemon whose catalog is one real config file,
// so a test can assert what a write put on disk.
func testDaemonWithConfig(t *testing.T, path string) *manager.Manager {
	t.Helper()
	load := func() (*config.Config, error) { return config.Load(path) }
	cfg, err := load()
	if err != nil {
		t.Fatal(err)
	}
	m := manager.New(cfg, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.New(api.Options{
		Manager:    m,
		Reload:     load,
		ConfigPath: path,
		Writers:    configw.NewRegistry(),
	})
	sock := filepath.Join(shortDir(t), "w.sock")
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
	if code := dispatch([]string{"version"}); code != 0 {
		t.Fatalf("version = %d, want 0", code)
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

func TestStartWaitReturnsWhenThePortAnswers(t *testing.T) {
	// A listener the "command" is pretending to be: the port is up before the
	// wait begins, so readiness is the thing under test, not the timing.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	testDaemon(t, map[string]config.Command{
		"api": {Cmd: []string{"sleep", "30"}, Port: port},
	})

	out := capture(t, func() {
		if code := dispatch([]string{"start", "api", "--wait", "10s", "--json"}); code != 0 {
			t.Errorf("start --wait = %d, want 0", code)
		}
	})
	var st manager.Status
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("start --json is not JSON: %v\n%s", err, out)
	}
	if st.Name != "api" || st.State != manager.Running {
		t.Fatalf("status = %+v", st)
	}
}

func TestStartWaitFailsWhenItNeverComesUp(t *testing.T) {
	testDaemon(t, map[string]config.Command{
		// Nothing ever listens on this port, so the wait has to time out.
		"api": {Cmd: []string{"sleep", "30"}, Port: 65533},
	})

	if code := dispatch([]string{"start", "api", "--wait", "1s"}); code != 1 {
		t.Fatalf("a start that never becomes ready = %d, want 1", code)
	}
}

func TestLifecycleJSON(t *testing.T) {
	testDaemon(t, map[string]config.Command{"api": {Cmd: []string{"sleep", "30"}}})

	for _, verb := range []string{"start", "restart", "stop"} {
		out := capture(t, func() {
			if code := dispatch([]string{verb, "api", "--json"}); code != 0 {
				t.Errorf("%s --json = %d, want 0", verb, code)
			}
		})
		var st manager.Status
		if err := json.Unmarshal([]byte(out), &st); err != nil {
			t.Fatalf("%s --json is not JSON: %v\n%s", verb, err, out)
		}
		if st.Name != "api" {
			t.Fatalf("%s returned %+v", verb, st)
		}
	}
}

func TestLogsJSON(t *testing.T) {
	m := testDaemon(t, map[string]config.Command{
		"noisy": {Cmd: []string{"sh", "-c", "echo hello from the test; sleep 30"}},
	})
	if err := m.Start("noisy"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if buf, err := m.Logs("noisy"); err == nil && len(buf.Tail(10)) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never logged anything")
		}
		time.Sleep(20 * time.Millisecond)
	}

	out := capture(t, func() {
		if code := dispatch([]string{"logs", "noisy", "--json"}); code != 0 {
			t.Errorf("logs --json = %d, want 0", code)
		}
	})
	var body struct {
		Name  string   `json:"name"`
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("logs --json is not JSON: %v\n%s", err, out)
	}
	if body.Name != "noisy" || len(body.Lines) == 0 || !strings.Contains(body.Lines[0], "hello from the test") {
		t.Fatalf("logs body = %+v", body)
	}
}
