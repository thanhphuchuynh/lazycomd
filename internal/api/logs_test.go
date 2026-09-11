package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

func TestLogsTail(t *testing.T) {
	m, c := testAPI(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "echo one; echo two; echo three"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, m, "a")

	code, body := do(t, c, "GET", "/v1/commands/a/logs?tail=2", "")
	if code != 200 {
		t.Fatalf("logs = %d %s", code, body)
	}
	var lines []string
	if err := json.Unmarshal([]byte(body), &lines); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "two" || lines[1] != "three" {
		t.Fatalf("lines = %v, want [two three]", lines)
	}
}

func TestLogsBadTail(t *testing.T) {
	_, c := testAPI(t, map[string]config.Command{"a": sleeper()})
	for _, q := range []string{"?tail=abc", "?tail=-1", "?tail=99999999"} {
		if code, body := do(t, c, "GET", "/v1/commands/a/logs"+q, ""); code != 400 {
			t.Fatalf("logs%s = %d %s, want 400", q, code, body)
		}
	}
}

func TestLogsUnknownCommand(t *testing.T) {
	_, c := testAPI(t, map[string]config.Command{})
	if code, _ := do(t, c, "GET", "/v1/commands/ghost/logs", ""); code != 404 {
		t.Fatalf("code = %d, want 404", code)
	}
	if code, _ := do(t, c, "GET", "/v1/commands/ghost/logs/stream", ""); code != 404 {
		t.Fatalf("stream code = %d, want 404", code)
	}
}

func TestLogsStream(t *testing.T) {
	m, c := testAPI(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "sleep 0.2; echo streamed; sleep 5"}, Cwd: "/tmp"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://unix/v1/commands/a/logs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}

	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("a")

	sc := bufio.NewScanner(resp.Body)
	deadline := time.Now().Add(4 * time.Second)
	for sc.Scan() && time.Now().Before(deadline) {
		if strings.TrimSpace(sc.Text()) == "data: streamed" {
			return
		}
	}
	t.Fatal("did not receive the streamed line")
}

func waitStopped(t *testing.T, m *manager.Manager, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := m.Status(name)
		if err != nil {
			t.Fatal(err)
		}
		if s.State == manager.Stopped || s.State == manager.Failed {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never stopped", name)
}
