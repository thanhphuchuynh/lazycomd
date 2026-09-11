package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseValid(t *testing.T) {
	p := write(t, `
listen: ""
commands:
  proxy:
    cmd: ["cloudflared", "tunnel"]
    restart: on-failure
    log: true
`)
	f, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	c := f.Commands["proxy"]
	if len(c.Cmd) != 2 || c.Cmd[0] != "cloudflared" {
		t.Fatalf("cmd = %v", c.Cmd)
	}
	if c.Restart != RestartOnFailure {
		t.Fatalf("restart = %q", c.Restart)
	}
	if !c.Log {
		t.Fatal("log = false, want true")
	}
	if c.Size != DefaultBufSize {
		t.Fatalf("size = %d, want %d", c.Size, DefaultBufSize)
	}
}

func TestParseEmptyFileIsValid(t *testing.T) {
	f, err := ParseFile(write(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(f.Commands) != 0 {
		t.Fatalf("commands = %v", f.Commands)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"unknown top field", "listten: \":1\"\n", "field listten not found"},
		{"unknown command field", "commands:\n  a:\n    cmd: [\"x\"]\n    retsart: always\n", "field retsart not found"},
		{"duplicate command name", "commands:\n  a:\n    cmd: [\"x\"]\n  a:\n    cmd: [\"y\"]\n", "already defined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseFile(write(t, tc.body)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"missing cmd", "commands:\n  a:\n    cwd: /tmp\n", `command "a": cmd is empty`},
		{"bad restart", "commands:\n  a:\n    cmd: [\"x\"]\n    restart: sometimes\n", `invalid restart "sometimes"`},
		{"negative size", "commands:\n  a:\n    cmd: [\"x\"]\n    size: -1\n", "size must be >= 0"},
		{"listen without token", "listen: \":7777\"\ncommands: {}\n", "listen requires token_file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := ParseFile(write(t, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestHealthAndPortAccepted(t *testing.T) {
	p := write(t, `
commands:
  web:
    cmd: ["npm", "start"]
    port: 3000
    health: http://localhost:3000/healthz
`)
	f, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	c := f.Commands["web"]
	if c.Port != 3000 {
		t.Fatalf("port = %d, want 3000", c.Port)
	}
	if c.Health != "http://localhost:3000/healthz" {
		t.Fatalf("health = %q", c.Health)
	}
}

func TestHealthAndPortValidation(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"bad scheme", "commands:\n  a:\n    cmd: [\"x\"]\n    health: ftp://localhost/health\n", "scheme must be http or https"},
		{"no host", "commands:\n  a:\n    cmd: [\"x\"]\n    health: http:///health\n", "missing host"},
		{"unparseable", "commands:\n  a:\n    cmd: [\"x\"]\n    health: \"://\"\n", "health"},
		{"port too high", "commands:\n  a:\n    cmd: [\"x\"]\n    port: 70000\n", "out of range"},
		{"port negative", "commands:\n  a:\n    cmd: [\"x\"]\n    port: -1\n", "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := ParseFile(write(t, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestIntendedPort(t *testing.T) {
	cases := []struct {
		name string
		cmd  Command
		want int
	}{
		{"explicit wins", Command{Port: 4310, Health: "http://localhost:3000/h"}, 4310},
		{"from health url", Command{Health: "http://localhost:3000/h"}, 3000},
		{"http default", Command{Health: "http://example.test/h"}, 80},
		{"https default", Command{Health: "https://example.test/h"}, 443},
		{"neither", Command{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cmd.IntendedPort(); got != tc.want {
				t.Fatalf("IntendedPort() = %d, want %d", got, tc.want)
			}
		})
	}
}
