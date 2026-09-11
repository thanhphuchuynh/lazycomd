package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// serveUnix runs the handler on a unix socket and returns a client bound to
// it, proving the transport the daemon actually uses.
func serveUnix(t *testing.T, h http.Handler) *http.Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(l)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

func testAPI(t *testing.T, cmds map[string]config.Command) (*manager.Manager, *http.Client) {
	t.Helper()
	m := manager.New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := NewServer(m, "", func() (*config.Config, error) {
		return &config.Config{Commands: cmds}, nil
	})
	return m, serveUnix(t, s.Handler())
}

func do(t *testing.T, c *http.Client, method, path, body string) (int, string) {
	t.Helper()
	var req *http.Request
	var err error
	if body == "" {
		req, err = http.NewRequest(method, "http://unix"+path, nil)
	} else {
		req, err = http.NewRequest(method, "http://unix"+path, strings.NewReader(body))
	}
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(buf)
}

func sleeper() config.Command {
	return config.Command{Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}
}

func TestHealthz(t *testing.T) {
	_, c := testAPI(t, map[string]config.Command{})
	if code, body := do(t, c, "GET", "/v1/healthz", ""); code != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("healthz = %d %s", code, body)
	}
}

func TestListAndGet(t *testing.T) {
	_, c := testAPI(t, map[string]config.Command{"a": sleeper()})

	code, body := do(t, c, "GET", "/v1/commands", "")
	if code != 200 {
		t.Fatalf("list = %d %s", code, body)
	}
	var list []manager.Status
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "a" || list[0].State != manager.Stopped {
		t.Fatalf("list = %+v", list)
	}

	code, body = do(t, c, "GET", "/v1/commands/a", "")
	if code != 200 {
		t.Fatalf("get = %d %s", code, body)
	}
	var one manager.Status
	if err := json.Unmarshal([]byte(body), &one); err != nil {
		t.Fatal(err)
	}
	if one.Name != "a" {
		t.Fatalf("get = %+v", one)
	}
}

func TestLifecycleEndpoints(t *testing.T) {
	_, c := testAPI(t, map[string]config.Command{"a": sleeper()})

	code, body := do(t, c, "POST", "/v1/commands/a/start", "")
	if code != 200 {
		t.Fatalf("start = %d %s", code, body)
	}
	var st manager.Status
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st.State != manager.Running || st.PID <= 0 {
		t.Fatalf("start returned %+v, want a running pid", st)
	}

	if code, body := do(t, c, "POST", "/v1/commands/a/restart", ""); code != 200 {
		t.Fatalf("restart = %d %s", code, body)
	}
	if code, body := do(t, c, "POST", "/v1/commands/a/stop", ""); code != 200 {
		t.Fatalf("stop = %d %s", code, body)
	}

	code, body = do(t, c, "GET", "/v1/commands/a", "")
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st.State != manager.Stopped {
		t.Fatalf("state = %q after stop (%d %s)", st.State, code, body)
	}
}

func TestStartWithDepsBody(t *testing.T) {
	m, c := testAPI(t, map[string]config.Command{
		"db":  sleeper(),
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}},
	})
	if code, body := do(t, c, "POST", "/v1/commands/api/start", `{"with_deps":true}`); code != 200 {
		t.Fatalf("start = %d %s", code, body)
	}
	st, err := m.Status("db")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != manager.Running {
		t.Fatalf("db state = %q, want running", st.State)
	}
}

func TestErrorCodes(t *testing.T) {
	_, c := testAPI(t, map[string]config.Command{"a": sleeper()})

	if code, body := do(t, c, "GET", "/v1/commands/ghost", ""); code != 404 || !strings.Contains(body, `"error"`) {
		t.Fatalf("unknown command = %d %s, want 404", code, body)
	}
	if code, _ := do(t, c, "POST", "/v1/commands/a/start", ""); code != 200 {
		t.Fatal("first start failed")
	}
	if code, body := do(t, c, "POST", "/v1/commands/a/start", ""); code != 409 {
		t.Fatalf("double start = %d %s, want 409", code, body)
	}
	if code, body := do(t, c, "POST", "/v1/commands/a/start", "{not json"); code != 400 {
		t.Fatalf("bad body = %d %s, want 400", code, body)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	h := recoverMW(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/commands", nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("recovered response = %d %s", rec.Code, rec.Body.String())
	}
}
