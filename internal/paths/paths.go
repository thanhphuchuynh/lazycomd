// Package paths resolves the fixed filesystem locations lazycomd uses.
package paths

import (
	"os"
	"path/filepath"
)

// ConfigPath returns the global config file path. LAZYCOMD_CONFIG overrides it.
func ConfigPath() string {
	if p := os.Getenv("LAZYCOMD_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(configHome(), "lazycomd", "config.yaml")
}

// StateDir returns the directory holding the socket and logs.
func StateDir() string {
	if p := os.Getenv("XDG_STATE_HOME"); p != "" {
		return filepath.Join(p, "lazycomd")
	}
	return filepath.Join(home(), ".local", "state", "lazycomd")
}

// SocketPath returns the unix socket the daemon always listens on.
func SocketPath() string { return filepath.Join(StateDir(), "lazycomd.sock") }

// LogDir returns the directory for commands with log: true.
func LogDir() string { return filepath.Join(StateDir(), "logs") }

func configHome() string {
	if p := os.Getenv("XDG_CONFIG_HOME"); p != "" {
		return p
	}
	return filepath.Join(home(), ".config")
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}
