package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

func TestAuthHandlerRequiresToken(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{"a": sleeper()}}, t.TempDir())
	t.Cleanup(m.Shutdown)
	s := New(Options{Manager: m, Token: "s3cret", Reload: func() (*config.Config, error) { return nil, nil }})
	c := serveUnix(t, s.AuthHandler())

	if code, _ := do(t, c, "GET", "/v1/commands", ""); code != 401 {
		t.Fatalf("no token = %d, want 401", code)
	}

	req, err := http.NewRequest("GET", "http://unix/v1/commands", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("wrong token = %d, want 401", resp.StatusCode)
	}

	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("right token = %d, want 200", resp.StatusCode)
	}
}

func TestAuthHandlerWithEmptyTokenRejectsEverything(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{}}, t.TempDir())
	t.Cleanup(m.Shutdown)
	s := New(Options{Manager: m, Reload: func() (*config.Config, error) { return nil, nil }})
	c := serveUnix(t, s.AuthHandler())

	req, err := http.NewRequest("GET", "http://unix/v1/commands", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("empty configured token = %d, want 401", resp.StatusCode)
	}
}

func TestReloadEndpointAppliesNewConfig(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{"a": sleeper()}}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	t.Cleanup(m.Shutdown)

	next := &config.Config{Commands: map[string]config.Command{"a": sleeper(), "b": sleeper()}}
	s := New(Options{Manager: m, Reload: func() (*config.Config, error) { return next, nil }})
	c := serveUnix(t, s.Handler())

	if code, body := do(t, c, "POST", "/v1/reload", ""); code != 200 {
		t.Fatalf("reload = %d %s", code, body)
	}
	if _, err := m.Status("b"); err != nil {
		t.Fatalf("command b missing after reload: %v", err)
	}
}

func TestReloadEndpointRejectsBadConfig(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{"a": sleeper()}}, t.TempDir())
	t.Cleanup(m.Shutdown)

	broken := errors.New(filepath.Join("x", "config.yaml") + ": field listten not found")
	s := New(Options{Manager: m, Reload: func() (*config.Config, error) { return nil, broken }})
	c := serveUnix(t, s.Handler())

	code, body := do(t, c, "POST", "/v1/reload", "")
	if code != 400 {
		t.Fatalf("reload = %d %s, want 400", code, body)
	}
	if _, err := m.Status("a"); err != nil {
		t.Fatalf("old config dropped after a failed reload: %v", err)
	}
}
