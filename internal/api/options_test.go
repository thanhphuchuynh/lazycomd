package api

import (
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

func TestNewWithOnlyAManagerServes(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{"a": sleeper()}}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	t.Cleanup(m.Shutdown)

	// Everything but the manager is optional.
	s := New(Options{Manager: m, Reload: func() (*config.Config, error) { return nil, nil }})
	c := serveUnix(t, s.Handler())

	if code, body := do(t, c, "GET", "/v1/commands", ""); code != 200 {
		t.Fatalf("commands = %d %s", code, body)
	}
	if code, _ := do(t, c, "GET", "/v1/system", ""); code != 200 {
		t.Fatalf("system with no sampler = %d, want 200", code)
	}
}
