package tui

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/api"
	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// testDaemon runs a real manager and API on a unix socket and returns a
// client for it plus the manager, so a test can assert daemon-side state.
func testDaemon(t *testing.T, cmds map[string]config.Command) (*client.Client, *manager.Manager) {
	t.Helper()
	m := manager.New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.NewServer(m, "", func() (*config.Config, error) {
		return &config.Config{Commands: cmds}, nil
	}, nil)

	// Not t.TempDir(): macOS caps a unix socket path at 104 bytes and the
	// per-test temp path plus a long test name overruns it.
	dir, err := os.MkdirTemp("/tmp", "lzc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	c, err := client.New("unix://"+sock, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, m
}

// sleeper is a command that stays up until stopped.
func sleeper() config.Command {
	return config.Command{Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}
}

// echoer prints one line and stays up.
func echoer() config.Command {
	return config.Command{Cmd: []string{"sh", "-c", "echo hi; sleep 30"}, Cwd: "/tmp"}
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
