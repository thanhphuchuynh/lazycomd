package client

import (
	"context"
	"errors"
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

// daemon starts a real manager plus API on a unix socket and returns a client
// for it.
func daemon(t *testing.T, cmds map[string]config.Command, token string) *Client {
	t.Helper()
	m := manager.New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.New(api.Options{
		Manager: m,
		Token:   token,
		Reload: func() (*config.Config, error) {
			return &config.Config{Commands: cmds}, nil
		},
	})
	h := s.Handler()
	if token != "" {
		h = s.AuthHandler()
	}

	// Not t.TempDir(): macOS caps a unix socket path at 104 bytes.
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
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	c, err := New("unix://"+sock, token)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func echoer() config.Command {
	return config.Command{Cmd: []string{"sh", "-c", "echo hi; sleep 30"}, Cwd: "/tmp"}
}

func TestClientRoundTrip(t *testing.T) {
	c := daemon(t, map[string]config.Command{"a": echoer()}, "")

	if err := c.Health(); err != nil {
		t.Fatal(err)
	}
	list, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "a" {
		t.Fatalf("List = %+v", list)
	}

	st, err := c.Start("a", false)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != manager.Running {
		t.Fatalf("state = %q, want running", st.State)
	}

	var lines []string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lines, err = c.Logs("a", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) > 0 && lines[0] == "hi" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(lines) == 0 || lines[0] != "hi" {
		t.Fatalf("Logs = %v, want [hi]", lines)
	}

	if st, err = c.Stop("a"); err != nil {
		t.Fatal(err)
	}
	if st.State != manager.Stopped {
		t.Fatalf("state = %q after stop, want stopped", st.State)
	}
	if _, err := c.Reload(); err != nil {
		t.Fatal(err)
	}
}

func TestClientNoDaemon(t *testing.T) {
	c, err := New("unix://"+filepath.Join(t.TempDir(), "absent.sock"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Health(); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("err = %v, want ErrNoDaemon", err)
	}
}

func TestClientBadAddress(t *testing.T) {
	if _, err := New("ftp://nope", ""); err == nil {
		t.Fatal("err = nil, want a bad address error")
	}
}

func TestClientAPIError(t *testing.T) {
	c := daemon(t, map[string]config.Command{}, "")

	_, err := c.Get("ghost")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != 404 || !strings.Contains(apiErr.Msg, "unknown command") {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestClientSendsToken(t *testing.T) {
	c := daemon(t, map[string]config.Command{"a": echoer()}, "s3cret")
	if _, err := c.List(); err != nil {
		t.Fatalf("with token: %v", err)
	}
}

func TestClientWithoutTokenIsRejected(t *testing.T) {
	withToken := daemon(t, map[string]config.Command{"a": echoer()}, "s3cret")
	noToken := &Client{http: withToken.http, base: withToken.base}

	_, err := noToken.List()
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("err = %v, want a 401 APIError", err)
	}
}

func TestClientStream(t *testing.T) {
	c := daemon(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "sleep 0.2; echo streamed; sleep 5"}, Cwd: "/tmp"},
	}, "")

	if _, err := c.Start("a", false); err != nil {
		t.Fatal(err)
	}
	defer c.Stop("a")

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	w := &lineCatcher{want: "streamed", done: make(chan struct{})}
	go c.Stream(ctx, "a", w)

	select {
	case <-w.done:
	case <-ctx.Done():
		t.Fatalf("stream never delivered %q, got %q", w.want, w.String())
	}
}

func TestResolve(t *testing.T) {
	c := daemon(t, map[string]config.Command{
		"proxy":       echoer(),
		"app:api":     echoer(),
		"scraper:api": echoer(),
		"app:web":     echoer(),
	}, "")

	if got, err := c.Resolve("proxy"); err != nil || got != "proxy" {
		t.Fatalf("Resolve(proxy) = %q, %v", got, err)
	}
	if got, err := c.Resolve("web"); err != nil || got != "app:web" {
		t.Fatalf("Resolve(web) = %q, %v", got, err)
	}
	if got, err := c.Resolve("app:api"); err != nil || got != "app:api" {
		t.Fatalf("Resolve(app:api) = %q, %v", got, err)
	}
	if _, err := c.Resolve("ghost"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err = %v, want unknown command", err)
	}
	_, err := c.Resolve("api")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want ambiguous", err)
	}
	if !strings.Contains(err.Error(), "app:api") || !strings.Contains(err.Error(), "scraper:api") {
		t.Fatalf("ambiguity error missing candidates: %v", err)
	}
}

// lineCatcher closes done once want shows up in the written output.
type lineCatcher struct {
	want string
	buf  strings.Builder
	done chan struct{}
	hit  bool
}

func (l *lineCatcher) Write(p []byte) (int, error) {
	l.buf.Write(p)
	if !l.hit && strings.Contains(l.buf.String(), l.want) {
		l.hit = true
		close(l.done)
	}
	return len(p), nil
}

func (l *lineCatcher) String() string { return l.buf.String() }

func TestClientSystem(t *testing.T) {
	c := daemon(t, map[string]config.Command{"a": echoer()}, "")
	snap, err := c.System()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Vitals == nil || snap.Health == nil {
		t.Fatalf("snapshot sections missing: %+v", snap)
	}
}
