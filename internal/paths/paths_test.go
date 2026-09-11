package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateDirUsesXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgstate")
	if got, want := StateDir(), "/tmp/xdgstate/lazycomd"; got != want {
		t.Fatalf("StateDir() = %q, want %q", got, want)
	}
	if got, want := SocketPath(), "/tmp/xdgstate/lazycomd/lazycomd.sock"; got != want {
		t.Fatalf("SocketPath() = %q, want %q", got, want)
	}
	if got, want := LogDir(), "/tmp/xdgstate/lazycomd/logs"; got != want {
		t.Fatalf("LogDir() = %q, want %q", got, want)
	}
}

func TestStateDirFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "lazycomd")
	if got := StateDir(); got != want {
		t.Fatalf("StateDir() = %q, want %q", got, want)
	}
}

func TestConfigPathOverride(t *testing.T) {
	t.Setenv("LAZYCOMD_CONFIG", "/tmp/other.yaml")
	if got, want := ConfigPath(), "/tmp/other.yaml"; got != want {
		t.Fatalf("ConfigPath() = %q, want %q", got, want)
	}
	t.Setenv("LAZYCOMD_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdgconf")
	if got, want := ConfigPath(), "/tmp/xdgconf/lazycomd/config.yaml"; got != want {
		t.Fatalf("ConfigPath() = %q, want %q", got, want)
	}
}
