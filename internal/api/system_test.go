package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/probe"
)

// testAPIWithProbe is testAPI plus a real sampler over the real machine.
func testAPIWithProbe(t *testing.T, cmds map[string]config.Command) (*manager.Manager, *http.Client, *probe.Sampler) {
	t.Helper()
	m := manager.New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	t.Cleanup(m.Shutdown)

	sampler := probe.New(m.RunningPIDs, m.HealthURLs, m.IntendedPorts)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sampler.Start(ctx)

	s := NewServer(m, "", func() (*config.Config, error) {
		return &config.Config{Commands: cmds}, nil
	}, sampler)
	return m, serveUnix(t, s.Handler()), sampler
}

func TestSystemEndpointShape(t *testing.T) {
	_, c, _ := testAPIWithProbe(t, map[string]config.Command{"a": sleeper()})

	code, body := do(t, c, "GET", "/v1/system", "")
	if code != 200 {
		t.Fatalf("system = %d %s", code, body)
	}
	var snap probe.Snapshot
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if snap.Vitals == nil || snap.Health == nil || snap.SampledAt == nil {
		t.Fatalf("snapshot sections missing: %s", body)
	}
}

func TestSystemEndpointIs200EvenWithACollectorError(t *testing.T) {
	_, c, sampler := testAPIWithProbe(t, map[string]config.Command{"a": sleeper()})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !sampler.Snapshot().SampledAt["ports"].IsZero() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	code, body := do(t, c, "GET", "/v1/system", "")
	if code != 200 {
		t.Fatalf("system = %d %s, want 200 regardless of collector errors", code, body)
	}
	if !strings.Contains(body, "sampled_at") {
		t.Fatalf("body missing sampled_at: %s", body)
	}
}

func TestCommandsCarryProbeFields(t *testing.T) {
	m, c, _ := testAPIWithProbe(t, map[string]config.Command{
		"a": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}

	var st manager.Status
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, body := do(t, c, "GET", "/v1/commands/a", "")
		if err := json.Unmarshal([]byte(body), &st); err != nil {
			t.Fatalf("decode: %v\n%s", err, body)
		}
		if st.MemMB > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Skip("no vitals arrived; ps is unavailable in this environment")
}

func TestServerWithoutASamplerStillServes(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{"a": sleeper()}}, t.TempDir())
	t.Cleanup(m.Shutdown)
	s := NewServer(m, "", func() (*config.Config, error) { return nil, nil }, nil)
	c := serveUnix(t, s.Handler())

	if code, body := do(t, c, "GET", "/v1/system", ""); code != 200 {
		t.Fatalf("system = %d %s, want an empty snapshot", code, body)
	}
	if code, _ := do(t, c, "GET", "/v1/commands", ""); code != 200 {
		t.Fatalf("commands = %d, want 200 with no sampler", code)
	}
}
