# lazycomd Daemon and API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a user-wide Go daemon that runs, supervises, and exposes long-running dev commands (proxies, tunnels, local stacks) over an HTTP API, plus a thin CLI client.

**Architecture:** One binary. `lazycomd serve` loads a YAML config (global plus registered project files), holds every command in an in-memory `Manager` guarded by a single mutex, streams each command's output into a per-command ring buffer, and serves a `net/http` API over a unix socket with an opt-in token-authenticated TCP listener. Every other subcommand is an HTTP client against that API. The TUI (spec #2) and dashboard (spec #3) are later specs that consume this API and add no process-management code.

**Tech Stack:** Go 1.22+ (standard library: `net/http`, `os/exec`, `syscall`, `flag`, `testing`), `gopkg.in/yaml.v3`.

**Spec:** `docs/superpowers/specs/2026-09-11-lazycomd-daemon-design.md`

## Global Constraints

- Module path: `github.com/tphuc/lazycomd`. Binary name: `lazycomd`.
- Go 1.22 minimum (`net/http.ServeMux` method+wildcard patterns). Toolchain on this machine: go1.27.1.
- `gopkg.in/yaml.v3` is the **only** permitted external dependency. No cobra, no testify, no router library. CLI parsing uses stdlib `flag`; tests use stdlib `testing`.
- `internal/manager` must never import `internal/api`. `internal/api` must never spawn a process.
- Unix socket path: `$XDG_STATE_HOME/lazycomd/lazycomd.sock`, default `~/.local/state/lazycomd/lazycomd.sock`, mode `0600`.
- Log dir: `$XDG_STATE_HOME/lazycomd/logs/`. Global config: `$XDG_CONFIG_HOME/lazycomd/config.yaml`, default `~/.config/lazycomd/config.yaml`.
- `token_file` must be mode `0600` or the daemon refuses to start. Token comparison uses `crypto/subtle.ConstantTimeCompare`.
- A bare port in `listen:` binds `127.0.0.1` only. `0.0.0.0` must be written out explicitly and logs a warning.
- Default ring buffer: 262144 bytes (256 KB). Disk log rotation threshold: 10 MB, one generation (`.log.1`).
- Stop grace period: 10s, then SIGKILL. Restart backoff: 1s doubling to a 30s cap, reset after 60s uptime. Dependency settle delay: 200ms.
- Restart policy values, exactly: `no`, `on-failure`, `always`.
- Process states, exactly: `stopped`, `starting`, `running`, `stopping`, `failed`.
- HTTP error body is always `{"error": "..."}`. Codes: 400 malformed / bad config, 401 bad token, 404 unknown command, 409 illegal in current state, 500 spawn or internal failure.
- Daemon runs in the foreground only. No forking, no PID files, no re-adopting orphans.
- Every task ends with `gofmt -l .` printing nothing and `go vet ./...` clean.

---

## File Structure

| Path | Responsibility |
|---|---|
| `go.mod`, `go.sum` | Module definition, single dependency |
| `internal/paths/paths.go` | XDG path resolution: config file, state dir, socket, log dir |
| `internal/config/config.go` | Types (`File`, `Command`, `Config`, `Restart`), `ParseFile`, `Validate` |
| `internal/config/load.go` | `Load`: project merge, namespacing, dependency resolution, cycle detection |
| `internal/config/resolve.go` | `Command.Resolve`: `~`/`$VAR` expansion, shell wrapping, env assembly |
| `internal/logbuf/logbuf.go` | Ring buffer, `Write`, `Bytes`, `Tail` |
| `internal/logbuf/subscribe.go` | `Subscribe` fan-out with drop-on-slow |
| `internal/logbuf/tee.go` | Optional disk tee, rotation, `FileName` |
| `internal/manager/manager.go` | `Manager`, `Process`, `Status`, errors, `List`/`Status`/`Logs` |
| `internal/manager/start.go` | `Start`, `startLocked`, `terminate`, `Stop`, `Restart` |
| `internal/manager/reap.go` | Reaper goroutine, restart policy, backoff |
| `internal/manager/deps.go` | `StartWithDeps` |
| `internal/manager/reload.go` | Config diff application |
| `internal/manager/lifecycle.go` | `StartAutostart`, `Shutdown`, `KillAll` |
| `internal/api/server.go` | `Server`, routes, JSON helpers, error mapping, `recover` |
| `internal/api/logs.go` | Log tail and SSE stream handlers |
| `internal/api/auth.go` | Token middleware, reload handler |
| `internal/client/client.go` | HTTP client over unix or TCP, typed API errors |
| `internal/client/resolve.go` | Bare-name to qualified-name resolution |
| `cmd/lazycomd/main.go` | Subcommand dispatch, exit codes |
| `cmd/lazycomd/serve.go` | Listeners, single-instance check, startup and shutdown sequence |
| `cmd/lazycomd/cli.go` | `ls`, `start`, `stop`, `restart`, `logs`, `reload`, `run` |
| `contrib/` | launchd plist, systemd unit |
| `README.md` | Config schema, every route, install notes |

---

### Task 1: Module bootstrap and path resolution

**Files:**
- Create: `go.mod`, `internal/paths/paths.go`
- Test: `internal/paths/paths_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `paths.ConfigPath() string`, `paths.StateDir() string`, `paths.SocketPath() string`, `paths.LogDir() string`.

- [ ] **Step 1: Initialize the module**

```bash
cd ~/coding/lazycomd
go mod init github.com/tphuc/lazycomd
go mod edit -go=1.22
go get gopkg.in/yaml.v3@v3.0.1
```

- [ ] **Step 2: Write the failing test**

Create `internal/paths/paths_test.go`:

```go
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/paths/ -v`
Expected: FAIL — `undefined: StateDir`.

- [ ] **Step 4: Write the implementation**

Create `internal/paths/paths.go`:

```go
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
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/paths/ -v`
Expected: PASS, three tests.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/paths/
git commit -m "feat(paths): resolve XDG config, socket and log locations"
```

---

### Task 2: Config types, parsing and single-file validation

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `config.Restart` string type with constants `RestartNo` (`"no"`), `RestartOnFailure` (`"on-failure"`), `RestartAlways` (`"always"`).
  - `config.Command` struct: `Cmd []string`, `Cwd string`, `Env map[string]string`, `Shell bool`, `Restart Restart`, `Autostart bool`, `Log bool`, `Size int`, `DependsOn []string`.
  - `config.File` struct: `Listen string`, `TokenFile string`, `Projects []string`, `Commands map[string]Command`.
  - `config.ParseFile(path string) (*File, error)`, `(*File).Validate() error`, `config.DefaultBufSize = 262144`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: ParseFile`.

- [ ] **Step 3: Write the implementation**

Create `internal/config/config.go`:

```go
// Package config loads and validates lazycomd's YAML configuration.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// DefaultBufSize is the per-command log ring buffer size when size: is unset.
const DefaultBufSize = 256 * 1024

// Restart is a command's restart policy.
type Restart string

const (
	RestartNo        Restart = "no"
	RestartOnFailure Restart = "on-failure"
	RestartAlways    Restart = "always"
)

// Command is one configured command.
type Command struct {
	Cmd       []string          `yaml:"cmd"`
	Cwd       string            `yaml:"cwd"`
	Env       map[string]string `yaml:"env"`
	Shell     bool              `yaml:"shell"`
	Restart   Restart           `yaml:"restart"`
	Autostart bool              `yaml:"autostart"`
	Log       bool              `yaml:"log"`
	Size      int               `yaml:"size"`
	DependsOn []string          `yaml:"depends_on"`
}

// File is one YAML file on disk. Listen, TokenFile and Projects are
// meaningful in the global config only.
type File struct {
	Listen    string             `yaml:"listen"`
	TokenFile string             `yaml:"token_file"`
	Projects  []string           `yaml:"projects"`
	Commands  map[string]Command `yaml:"commands"`
}

// ParseFile decodes one YAML file, rejecting unknown fields.
func ParseFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	var out File
	if err := dec.Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return &File{Commands: map[string]Command{}}, nil
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if out.Commands == nil {
		out.Commands = map[string]Command{}
	}
	return &out, nil
}

// Validate checks every command and applies defaults in place.
func (f *File) Validate() error {
	names := make([]string, 0, len(f.Commands))
	for n := range f.Commands {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		c := f.Commands[n]
		if err := c.validate(n); err != nil {
			return err
		}
		f.Commands[n] = c
	}
	if f.Listen != "" && f.TokenFile == "" {
		return errors.New("listen requires token_file")
	}
	return nil
}

func (c *Command) validate(name string) error {
	if len(c.Cmd) == 0 {
		return fmt.Errorf("command %q: cmd is empty", name)
	}
	switch c.Restart {
	case "":
		c.Restart = RestartNo
	case RestartNo, RestartOnFailure, RestartAlways:
	default:
		return fmt.Errorf("command %q: invalid restart %q (want no, on-failure or always)", name, c.Restart)
	}
	if c.Size < 0 {
		return fmt.Errorf("command %q: size must be >= 0", name)
	}
	if c.Size == 0 {
		c.Size = DefaultBufSize
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS. If the duplicate-key subtest fails, print the real error and relax that subtest's `want` to the substring yaml.v3 actually produces — do not delete the subtest.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): parse and validate a single config file"
```

---

### Task 3: Project merge, namespacing, dependency resolution and cycle detection

**Files:**
- Create: `internal/config/load.go`
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: `config.File`, `config.ParseFile`, `(*File).Validate` from Task 2.
- Produces:
  - `config.Config` struct: `Listen string`, `TokenFile string`, `Commands map[string]Command` keyed by `name` or `project:name`.
  - `config.Load(globalPath string) (*Config, error)`.
  - `config.ExpandUser(s string) string`.

**Decisions this task locks in (the spec is silent):**
- A project command's `depends_on` entry resolves inside its own project first, then against global commands; unresolvable is a load error. Stored resolved (fully qualified).
- A project command with no `cwd` defaults to its project directory.

- [ ] **Step 1: Write the failing test**

Create `internal/config/load_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes a global config plus project files and returns the global path.
// files maps a relative path to its contents.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.ReplaceAll(body, "@ROOT@", root)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "config.yaml")
}

func TestLoadNamespacesProjectCommands(t *testing.T) {
	g := tree(t, map[string]string{
		"config.yaml": "projects:\n  - @ROOT@/scraper\ncommands:\n  proxy:\n    cmd: [\"sleep\", \"1\"]\n",
		"scraper/lazycomd.yaml": "commands:\n  api:\n    cmd: [\"sleep\", \"1\"]\n",
	})
	cfg, err := Load(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Commands["proxy"]; !ok {
		t.Fatalf("global command missing: %v", keys(cfg))
	}
	c, ok := cfg.Commands["scraper:api"]
	if !ok {
		t.Fatalf("namespaced command missing: %v", keys(cfg))
	}
	if !strings.HasSuffix(c.Cwd, "/scraper") {
		t.Fatalf("cwd = %q, want the project dir", c.Cwd)
	}
}

func TestLoadResolvesDepsProjectFirst(t *testing.T) {
	g := tree(t, map[string]string{
		"config.yaml": "projects:\n  - @ROOT@/app\ncommands:\n  db:\n    cmd: [\"sleep\", \"1\"]\n",
		"app/lazycomd.yaml": "commands:\n  db:\n    cmd: [\"sleep\", \"1\"]\n  api:\n    cmd: [\"sleep\", \"1\"]\n    depends_on: [\"db\"]\n",
	})
	cfg, err := Load(g)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Commands["app:api"].DependsOn
	if len(got) != 1 || got[0] != "app:db" {
		t.Fatalf("depends_on = %v, want [app:db]", got)
	}
}

func TestLoadResolvesDepsFallsBackToGlobal(t *testing.T) {
	g := tree(t, map[string]string{
		"config.yaml": "projects:\n  - @ROOT@/app\ncommands:\n  db:\n    cmd: [\"sleep\", \"1\"]\n",
		"app/lazycomd.yaml": "commands:\n  api:\n    cmd: [\"sleep\", \"1\"]\n    depends_on: [\"db\"]\n",
	})
	cfg, err := Load(g)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Commands["app:api"].DependsOn
	if len(got) != 1 || got[0] != "db" {
		t.Fatalf("depends_on = %v, want [db]", got)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("basename collision", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml":             "projects:\n  - @ROOT@/a/api\n  - @ROOT@/b/api\ncommands: {}\n",
			"a/api/lazycomd.yaml":     "commands: {}\n",
			"b/api/lazycomd.yaml":     "commands: {}\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "basename") {
			t.Fatalf("err = %v, want a basename collision error", err)
		}
	})
	t.Run("global key in project file", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml":         "projects:\n  - @ROOT@/app\ncommands: {}\n",
			"app/lazycomd.yaml":   "listen: \":1\"\ncommands: {}\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "global-only") {
			t.Fatalf("err = %v, want a global-only error", err)
		}
	})
	t.Run("unknown dependency", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml": "commands:\n  a:\n    cmd: [\"x\"]\n    depends_on: [\"ghost\"]\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "unknown dependency") {
			t.Fatalf("err = %v, want an unknown dependency error", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml": "commands:\n  a:\n    cmd: [\"x\"]\n    depends_on: [\"b\"]\n  b:\n    cmd: [\"x\"]\n    depends_on: [\"a\"]\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("err = %v, want a cycle error", err)
		}
	})
	t.Run("self cycle", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml": "commands:\n  a:\n    cmd: [\"x\"]\n    depends_on: [\"a\"]\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("err = %v, want a cycle error", err)
		}
	})
	t.Run("missing project file", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml": "projects:\n  - @ROOT@/gone\ncommands: {}\n",
		})
		if _, err := Load(g); err == nil {
			t.Fatal("err = nil, want a missing file error")
		}
	})
}

func TestExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ExpandUser("~/x"), filepath.Join(home, "x"); got != want {
		t.Fatalf("ExpandUser(~/x) = %q, want %q", got, want)
	}
	if got := ExpandUser("~x"); got != "~x" {
		t.Fatalf("ExpandUser(~x) = %q, want it untouched", got)
	}
}

func keys(c *Config) []string {
	out := make([]string, 0, len(c.Commands))
	for k := range c.Commands {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run 'TestLoad|TestExpandUser' -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write the implementation**

Create `internal/config/load.go`:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the merged, validated configuration the daemon runs from.
// Commands is keyed by "name" for global commands and "project:name" for
// project commands.
type Config struct {
	Listen    string
	TokenFile string
	Commands  map[string]Command
}

// Load reads the global config plus every registered project file, merges
// them, resolves dependency names and rejects dependency cycles.
func Load(globalPath string) (*Config, error) {
	gf, err := ParseFile(globalPath)
	if err != nil {
		return nil, err
	}
	if err := gf.Validate(); err != nil {
		return nil, err
	}

	cfg := &Config{
		Listen:    gf.Listen,
		TokenFile: gf.TokenFile,
		Commands:  make(map[string]Command, len(gf.Commands)),
	}
	for n, c := range gf.Commands {
		cfg.Commands[n] = c
	}

	byBase := make(map[string]string, len(gf.Projects))
	for _, raw := range gf.Projects {
		dir := ExpandUser(raw)
		ns := filepath.Base(dir)
		if prev, ok := byBase[ns]; ok {
			return nil, fmt.Errorf("project basename %q is used by both %s and %s", ns, prev, dir)
		}
		byBase[ns] = dir

		path := filepath.Join(dir, "lazycomd.yaml")
		pf, err := ParseFile(path)
		if err != nil {
			return nil, err
		}
		if pf.Listen != "" || pf.TokenFile != "" || len(pf.Projects) > 0 {
			return nil, fmt.Errorf("%s: listen, token_file and projects are global-only", path)
		}
		if err := pf.Validate(); err != nil {
			return nil, err
		}
		for n, c := range pf.Commands {
			if c.Cwd == "" {
				c.Cwd = dir
			}
			cfg.Commands[ns+":"+n] = c
		}
	}

	if err := cfg.resolveDeps(); err != nil {
		return nil, err
	}
	return cfg, cfg.checkCycles()
}

// ExpandUser replaces a leading ~ with the user's home directory.
func ExpandUser(s string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	return filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(s, "~"), "/"))
}

// resolveDeps rewrites every depends_on entry to a fully qualified key.
func (c *Config) resolveDeps() error {
	for _, name := range c.sortedNames() {
		cmd := c.Commands[name]
		ns := ""
		if i := strings.Index(name, ":"); i >= 0 {
			ns = name[:i+1]
		}
		out := make([]string, 0, len(cmd.DependsOn))
		for _, d := range cmd.DependsOn {
			resolved, ok := c.resolveOne(ns, d)
			if !ok {
				return fmt.Errorf("command %q: unknown dependency %q", name, d)
			}
			out = append(out, resolved)
		}
		cmd.DependsOn = out
		c.Commands[name] = cmd
	}
	return nil
}

func (c *Config) resolveOne(ns, dep string) (string, bool) {
	if strings.Contains(dep, ":") {
		_, ok := c.Commands[dep]
		return dep, ok
	}
	if ns != "" {
		if _, ok := c.Commands[ns+dep]; ok {
			return ns + dep, true
		}
	}
	_, ok := c.Commands[dep]
	return dep, ok
}

func (c *Config) checkCycles() error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int, len(c.Commands))
	var path []string

	var visit func(string) error
	visit = func(n string) error {
		switch color[n] {
		case grey:
			return fmt.Errorf("dependency cycle: %s -> %s", strings.Join(path, " -> "), n)
		case black:
			return nil
		}
		color[n] = grey
		path = append(path, n)
		for _, d := range c.Commands[n].DependsOn {
			if err := visit(d); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		color[n] = black
		return nil
	}

	for _, n := range c.sortedNames() {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) sortedNames() []string {
	out := make([]string, 0, len(c.Commands))
	for n := range c.Commands {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS, all Task 2 and Task 3 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): merge project files, resolve deps, reject cycles"
```

---

### Task 4: Start-time expansion (`Command.Resolve`)

**Files:**
- Create: `internal/config/resolve.go`
- Test: `internal/config/resolve_test.go`

**Interfaces:**
- Consumes: `config.Command`, `config.ExpandUser`.
- Produces:
  - `config.Resolved` struct: `Argv []string`, `Cwd string`, `Env []string`.
  - `(Command).Resolve() (Resolved, error)`.

Expansion happens here, at start time, so a command picks up a changed
environment without a config reload. `$$` is the escape for a literal dollar
sign, because `os.ExpandEnv` would otherwise eat shell constructs like `$!`. `Cwd` defaults to the user's home
directory and must exist. `Shell: true` joins argv with spaces and wraps it in
`sh -c`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/resolve_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolveExpandsVars(t *testing.T) {
	t.Setenv("LZC_PORT", "8080")
	dir := t.TempDir()
	c := Command{
		Cmd: []string{"curl", "http://localhost:$LZC_PORT"},
		Cwd: dir,
		Env: map[string]string{"TOKEN": "t-$LZC_PORT", "PLAIN": "x"},
	}
	r, err := c.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if r.Argv[1] != "http://localhost:8080" {
		t.Fatalf("argv[1] = %q", r.Argv[1])
	}
	if !slices.Contains(r.Env, "TOKEN=t-8080") {
		t.Fatalf("env missing expanded TOKEN: %v", tail(r.Env, 3))
	}
	if !slices.Contains(r.Env, "PLAIN=x") {
		t.Fatalf("env missing PLAIN: %v", tail(r.Env, 3))
	}
}

func TestResolveExpandsHomeInCwd(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Command{Cmd: []string{"true"}, Cwd: "~"}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if r.Cwd != home {
		t.Fatalf("cwd = %q, want %q", r.Cwd, home)
	}
}

func TestResolveDefaultsCwdToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Command{Cmd: []string{"true"}}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if r.Cwd != home {
		t.Fatalf("cwd = %q, want %q", r.Cwd, home)
	}
}

func TestResolveDollarEscape(t *testing.T) {
	t.Setenv("LZC_X", "boom")
	r, err := Command{Cmd: []string{"sh", "-c", "echo $$LZC_X and $LZC_X"}, Cwd: t.TempDir()}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := "echo $LZC_X and boom"
	if r.Argv[2] != want {
		t.Fatalf("argv[2] = %q, want %q", r.Argv[2], want)
	}
}

func TestResolveShellWraps(t *testing.T) {
	r, err := Command{Cmd: []string{"echo", "hi", "|", "wc", "-l"}, Shell: true, Cwd: t.TempDir()}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sh", "-c", "echo hi | wc -l"}
	if !slices.Equal(r.Argv, want) {
		t.Fatalf("argv = %v, want %v", r.Argv, want)
	}
}

func TestResolveEnvIsSorted(t *testing.T) {
	r, err := Command{
		Cmd: []string{"true"},
		Cwd: t.TempDir(),
		Env: map[string]string{"B": "2", "A": "1", "C": "3"},
	}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	got := tail(r.Env, 3)
	want := []string{"A=1", "B=2", "C=3"}
	if !slices.Equal(got, want) {
		t.Fatalf("last three env entries = %v, want %v", got, want)
	}
}

func TestResolveErrors(t *testing.T) {
	if _, err := (Command{Cmd: nil}).Resolve(); err == nil {
		t.Fatal("empty cmd: err = nil, want an error")
	}
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := Command{Cmd: []string{"true"}, Cwd: missing}.Resolve()
	if err == nil || !strings.Contains(err.Error(), "cwd") {
		t.Fatalf("err = %v, want a cwd error", err)
	}
}

func tail(s []string, n int) []string {
	if len(s) < n {
		return s
	}
	return s[len(s)-n:]
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestResolve -v`
Expected: FAIL — `c.Resolve undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/config/resolve.go`:

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Resolved is a Command with ~ and $VAR expanded, ready to spawn.
type Resolved struct {
	Argv []string
	Cwd  string
	Env  []string
}

// Resolve expands the command for the current environment. It runs at start
// time, not config load time, so a restart picks up environment changes.
func (c Command) Resolve() (Resolved, error) {
	if len(c.Cmd) == 0 {
		return Resolved{}, errors.New("cmd is empty")
	}
	// os.ExpandEnv eats shell constructs like $! and $1, so $$ is the escape
	// for a literal dollar sign.
	expand := func(s string) string {
		const sentinel = "\x00"
		s = strings.ReplaceAll(s, "$$", sentinel)
		s = os.ExpandEnv(ExpandUser(s))
		return strings.ReplaceAll(s, sentinel, "$")
	}

	argv := make([]string, 0, len(c.Cmd))
	for _, a := range c.Cmd {
		argv = append(argv, expand(a))
	}
	if c.Shell {
		argv = []string{"sh", "-c", strings.Join(argv, " ")}
	}

	cwd := expand(c.Cwd)
	if cwd == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Resolved{}, fmt.Errorf("cwd unset and home unknown: %w", err)
		}
		cwd = h
	}
	if fi, err := os.Stat(cwd); err != nil {
		return Resolved{}, fmt.Errorf("cwd %s: %w", cwd, err)
	} else if !fi.IsDir() {
		return Resolved{}, fmt.Errorf("cwd %s: not a directory", cwd)
	}

	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	env := os.Environ()
	for _, k := range keys {
		env = append(env, k+"="+expand(c.Env[k]))
	}
	return Resolved{Argv: argv, Cwd: cwd, Env: env}, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS, every config test.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): expand ~ and \$VAR at command start time"
```

---
### Task 5: Log ring buffer

**Files:**
- Create: `internal/logbuf/logbuf.go`
- Test: `internal/logbuf/logbuf_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `logbuf.New(size int) *Buffer` — `size <= 0` means 262144.
  - `(*Buffer).Write(p []byte) (int, error)` — implements `io.Writer`, never blocks, never errors.
  - `(*Buffer).Bytes() []byte` — contents oldest-first.
  - `(*Buffer).Tail(n int) []string` — last `n` whole lines, `n <= 0` means all.

- [ ] **Step 1: Write the failing test**

Create `internal/logbuf/logbuf_test.go`:

```go
package logbuf

import (
	"fmt"
	"slices"
	"testing"
)

func TestTailReturnsLines(t *testing.T) {
	b := New(1024)
	fmt.Fprint(b, "one\ntwo\nthree\n")
	if got, want := b.Tail(0), []string{"one", "two", "three"}; !slices.Equal(got, want) {
		t.Fatalf("Tail(0) = %v, want %v", got, want)
	}
	if got, want := b.Tail(2), []string{"two", "three"}; !slices.Equal(got, want) {
		t.Fatalf("Tail(2) = %v, want %v", got, want)
	}
}

func TestTailEmptyBuffer(t *testing.T) {
	if got := New(64).Tail(0); len(got) != 0 {
		t.Fatalf("Tail(0) = %v, want empty", got)
	}
}

func TestTailUnterminatedLastLine(t *testing.T) {
	b := New(64)
	fmt.Fprint(b, "a\nb")
	if got, want := b.Tail(0), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("Tail(0) = %v, want %v", got, want)
	}
}

func TestWrappedBufferDropsPartialLine(t *testing.T) {
	b := New(32) // holds ~6 of the 5-byte lines below
	for i := 0; i < 100; i++ {
		fmt.Fprintf(b, "%04d\n", i)
	}
	lines := b.Tail(0)
	if len(lines) == 0 {
		t.Fatal("Tail(0) = empty, want some lines")
	}
	for _, l := range lines {
		if len(l) != 4 {
			t.Fatalf("partial line %q in %v", l, lines)
		}
	}
	if last := lines[len(lines)-1]; last != "0099" {
		t.Fatalf("last line = %q, want 0099", last)
	}
}

func TestWriteLargerThanBufferKeepsTail(t *testing.T) {
	b := New(8)
	n, err := b.Write([]byte("0123456789abc\n"))
	if err != nil || n != 14 {
		t.Fatalf("Write = %d, %v, want 14, nil", n, err)
	}
	if got, want := string(b.Bytes()), "89abc\n"; len(got) != 8 || got[len(got)-len(want):] != want {
		t.Fatalf("Bytes() = %q, want 8 bytes ending %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/logbuf/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation**

Create `internal/logbuf/logbuf.go`:

```go
// Package logbuf holds a fixed-size, per-command ring buffer of process
// output, with optional live subscribers and an optional disk tee.
package logbuf

import (
	"bytes"
	"strings"
	"sync"
)

// DefaultSize is the ring size used when New is given size <= 0.
const DefaultSize = 256 * 1024

// Buffer is a byte ring. Writes never block and never fail; the oldest bytes
// are dropped when it wraps.
type Buffer struct {
	mu   sync.Mutex
	buf  []byte
	w    int
	full bool
}

// New returns a Buffer holding the last size bytes.
func New(size int) *Buffer {
	if size <= 0 {
		size = DefaultSize
	}
	return &Buffer{buf: make([]byte, size)}
}

// Write appends p, dropping the oldest bytes if the ring is full.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.appendLocked(p)
	b.mu.Unlock()
	return len(p), nil
}

func (b *Buffer) appendLocked(p []byte) {
	if len(p) >= len(b.buf) {
		copy(b.buf, p[len(p)-len(b.buf):])
		b.w = 0
		b.full = true
		return
	}
	c := copy(b.buf[b.w:], p)
	if c < len(p) {
		copy(b.buf, p[c:])
		b.w = len(p) - c
		b.full = true
		return
	}
	b.w += c
	if b.w == len(b.buf) {
		b.w = 0
		b.full = true
	}
}

// Bytes returns the buffer contents, oldest byte first.
func (b *Buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.full {
		out := make([]byte, b.w)
		copy(out, b.buf[:b.w])
		return out
	}
	out := make([]byte, 0, len(b.buf))
	out = append(out, b.buf[b.w:]...)
	return append(out, b.buf[:b.w]...)
}

// Tail returns the last n whole lines, or every line when n <= 0. A wrapped
// buffer's leading fragment is discarded so no caller ever sees half a line.
//
// ponytail: a wrap that lands exactly on a line boundary costs one whole
// line. Track a line-start offset if that ever matters.
func (b *Buffer) Tail(n int) []string {
	data := b.Bytes()

	b.mu.Lock()
	wrapped := b.full
	b.mu.Unlock()

	if wrapped {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	if len(data) == 0 {
		return []string{}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/logbuf/ -v`
Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/logbuf/
git commit -m "feat(logbuf): line-aware ring buffer for command output"
```

---

### Task 6: Live log subscribers

**Files:**
- Create: `internal/logbuf/subscribe.go`
- Modify: `internal/logbuf/logbuf.go` — add the `subs`/`next` fields to `Buffer` and the fan-out call inside `Write`
- Test: `internal/logbuf/subscribe_test.go`

**Interfaces:**
- Consumes: `logbuf.Buffer` from Task 5.
- Produces: `(*Buffer).Subscribe() (<-chan []byte, func())` — a 64-chunk buffered channel plus an unsubscribe function that closes it. A subscriber that falls behind loses chunks; the writer is never blocked.

- [ ] **Step 1: Write the failing test**

Create `internal/logbuf/subscribe_test.go`:

```go
package logbuf

import (
	"fmt"
	"testing"
	"time"
)

func TestSubscribeReceivesChunks(t *testing.T) {
	b := New(1024)
	ch, cancel := b.Subscribe()
	defer cancel()

	fmt.Fprint(b, "hello\n")
	select {
	case got := <-ch:
		if string(got) != "hello\n" {
			t.Fatalf("chunk = %q, want %q", got, "hello\n")
		}
	case <-time.After(time.Second):
		t.Fatal("no chunk delivered")
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	b := New(1024)
	ch, cancel := b.Subscribe()
	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("channel still open after cancel")
	}
	fmt.Fprint(b, "after\n") // must not panic on a closed subscriber
	cancel()                 // must be idempotent
}

func TestSlowSubscriberNeverBlocksWriter(t *testing.T) {
	b := New(4096)
	_, cancel := b.Subscribe() // never drained
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			fmt.Fprintf(b, "line %d\n", i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writer blocked on a slow subscriber")
	}
	if got := b.Tail(1); len(got) != 1 || got[0] != "line 499" {
		t.Fatalf("Tail(1) = %v, want [line 499]", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/logbuf/ -run TestSubscribe -v`
Expected: FAIL — `b.Subscribe undefined`.

- [ ] **Step 3: Add the subscriber fields to `Buffer`**

In `internal/logbuf/logbuf.go`, extend the struct:

```go
type Buffer struct {
	mu   sync.Mutex
	buf  []byte
	w    int
	full bool

	subs map[int]chan []byte
	next int
}
```

and in `New`, initialize the map:

```go
	return &Buffer{buf: make([]byte, size), subs: make(map[int]chan []byte)}
```

- [ ] **Step 4: Fan out inside `Write`**

Replace the body of `Write` in `internal/logbuf/logbuf.go`:

```go
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.appendLocked(p)
	b.fanoutLocked(p)
	b.mu.Unlock()
	return len(p), nil
}
```

- [ ] **Step 5: Write the subscriber implementation**

Create `internal/logbuf/subscribe.go`:

```go
package logbuf

// subChanSize is how many chunks a subscriber may fall behind before it
// starts losing them.
const subChanSize = 64

// Subscribe returns a channel of live output chunks and a function that
// unsubscribes and closes it. The returned function is safe to call twice.
func (b *Buffer) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, subChanSize)

	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}
}

// fanoutLocked copies p to every subscriber, dropping chunks for any
// subscriber that is not keeping up. Never blocks the writer.
func (b *Buffer) fanoutLocked(p []byte) {
	if len(b.subs) == 0 {
		return
	}
	chunk := append([]byte(nil), p...)
	for _, ch := range b.subs {
		select {
		case ch <- chunk:
		default:
		}
	}
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/logbuf/ -v`
Expected: PASS, all eight tests.

- [ ] **Step 7: Commit**

```bash
git add internal/logbuf/
git commit -m "feat(logbuf): live subscribers with drop-on-slow fan-out"
```

---

### Task 7: Optional disk tee with rotation

**Files:**
- Create: `internal/logbuf/tee.go`
- Modify: `internal/logbuf/logbuf.go` — add the tee fields to `Buffer` and the tee call inside `Write`
- Test: `internal/logbuf/tee_test.go`

**Interfaces:**
- Consumes: `logbuf.Buffer` from Tasks 5 and 6.
- Produces:
  - `(*Buffer).AttachFile(path string) error` — creates parent dirs `0700`, opens the file `O_APPEND` mode `0600`.
  - `(*Buffer).Close() error` — closes the tee.
  - `logbuf.FileName(command string) string` — `"scraper:api"` becomes `"scraper__api.log"`.
  - `logbuf.RotateAt int64` — rotation threshold, `10 << 20`, a `var` so tests can lower it.

- [ ] **Step 1: Write the failing test**

Create `internal/logbuf/tee_test.go`:

```go
package logbuf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileName(t *testing.T) {
	if got, want := FileName("scraper:api"), "scraper__api.log"; got != want {
		t.Fatalf("FileName = %q, want %q", got, want)
	}
	if got, want := FileName("proxy"), "proxy.log"; got != want {
		t.Fatalf("FileName = %q, want %q", got, want)
	}
}

func TestAttachFileWritesThrough(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", "proxy.log")
	b := New(1024)
	if err := b.AttachFile(p); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(b, "hello\n")
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("file = %q, want %q", got, "hello\n")
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestAttachFileAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), "proxy.log")
	if err := os.WriteFile(p, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := New(1024)
	if err := b.AttachFile(p); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(b, "new\n")
	b.Close()

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\nnew\n" {
		t.Fatalf("file = %q, want %q", got, "old\nnew\n")
	}
}

func TestRotation(t *testing.T) {
	old := RotateAt
	RotateAt = 16
	defer func() { RotateAt = old }()

	dir := t.TempDir()
	p := filepath.Join(dir, "proxy.log")
	b := New(1024)
	if err := b.AttachFile(p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		fmt.Fprintf(b, "%04d\n", i) // 5 bytes each
	}
	b.Close()

	rotated, err := os.ReadFile(p + ".1")
	if err != nil {
		t.Fatalf("no rotated file: %v", err)
	}
	if !strings.HasPrefix(string(rotated), "0000\n") {
		t.Fatalf("rotated file = %q, want it to start with the oldest lines", rotated)
	}
	current, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(current)) > RotateAt {
		t.Fatalf("current file is %d bytes, want <= %d", len(current), RotateAt)
	}
	if !strings.Contains(string(current), "0009\n") {
		t.Fatalf("current file = %q, want the newest line", current)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/logbuf/ -run 'TestFileName|TestAttach|TestRotation' -v`
Expected: FAIL — `undefined: FileName`.

- [ ] **Step 3: Add the tee fields to `Buffer`**

In `internal/logbuf/logbuf.go`, extend the struct:

```go
type Buffer struct {
	mu   sync.Mutex
	buf  []byte
	w    int
	full bool

	subs map[int]chan []byte
	next int

	tee     *os.File
	teePath string
	teeN    int64
}
```

Add `"os"` to that file's imports, and call the tee from `Write`:

```go
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.appendLocked(p)
	b.fanoutLocked(p)
	b.teeLocked(p)
	b.mu.Unlock()
	return len(p), nil
}
```

- [ ] **Step 4: Write the tee implementation**

Create `internal/logbuf/tee.go`:

```go
package logbuf

import (
	"os"
	"path/filepath"
	"strings"
)

// RotateAt is the disk log size that triggers rotation. A var, not a const,
// so tests can lower it.
var RotateAt int64 = 10 << 20

// FileName maps a command name to its log file name. The ":" in a namespaced
// name becomes "__" so the path stays boring.
func FileName(command string) string {
	return strings.ReplaceAll(command, ":", "__") + ".log"
}

// AttachFile tees every subsequent write to path as well as the ring.
func (b *Buffer) AttachFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}

	b.mu.Lock()
	if b.tee != nil {
		b.tee.Close()
	}
	b.tee, b.teePath, b.teeN = f, path, fi.Size()
	b.mu.Unlock()
	return nil
}

// Close closes the disk tee, if any. The ring stays readable.
func (b *Buffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tee == nil {
		return nil
	}
	err := b.tee.Close()
	b.tee = nil
	return err
}

func (b *Buffer) teeLocked(p []byte) {
	if b.tee == nil {
		return
	}
	n, err := b.tee.Write(p)
	b.teeN += int64(n)
	if err != nil || b.teeN < RotateAt {
		return
	}
	b.rotateLocked()
}

// rotateLocked keeps one generation: proxy.log becomes proxy.log.1.
func (b *Buffer) rotateLocked() {
	b.tee.Close()
	b.tee = nil
	if err := os.Rename(b.teePath, b.teePath+".1"); err != nil {
		return
	}
	f, err := os.OpenFile(b.teePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	b.tee, b.teeN = f, 0
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/logbuf/ -v`
Expected: PASS, every logbuf test.

- [ ] **Step 6: Commit**

```bash
git add internal/logbuf/
git commit -m "feat(logbuf): optional disk tee with single-generation rotation"
```

---

### Task 8: Manager core — spawn, stop, process groups, status

**Files:**
- Create: `internal/manager/manager.go`, `internal/manager/start.go`, `internal/manager/reap.go`
- Test: `internal/manager/manager_test.go`

**Interfaces:**
- Consumes: `config.Config`, `config.Command`, `(Command).Resolve`, `logbuf.New`, `logbuf.FileName`.
- Produces:
  - `manager.State` string type with constants `Stopped`, `Starting`, `Running`, `Stopping`, `Failed` (values `"stopped"`, `"starting"`, `"running"`, `"stopping"`, `"failed"`).
  - `manager.ErrNotFound`, `manager.ErrWrongState`.
  - `manager.Status` struct: `Name string`, `State State`, `PID int`, `UptimeSec float64`, `ExitCode *int`, `Restarts int`, `SpecDirty bool`, `DependsOn []string`, JSON tags `name`, `state`, `pid`, `uptime_sec`, `exit_code`, `restarts`, `spec_dirty`, `depends_on`.
  - `manager.New(cfg *config.Config, logDir string) *Manager` with exported tunables `Grace`, `BackoffMin`, `BackoffMax`, `UptimeReset`, `SettleDelay`.
  - `(*Manager).Start(name string) error`, `.Stop(name string) error`, `.Restart(name string) error`, `.Status(name string) (Status, error)`, `.List() []Status`, `.Logs(name string) (*logbuf.Buffer, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/manager/manager_test.go`:

```go
package manager

import (
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
)

// testManager builds a Manager over an in-memory config with test-speed
// timings.
func testManager(t *testing.T, cmds map[string]config.Command) *Manager {
	t.Helper()
	m := New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.BackoffMin = 10 * time.Millisecond
	m.BackoffMax = 40 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	m.UptimeReset = time.Hour
	t.Cleanup(func() { m.mu.Lock(); defer m.mu.Unlock() })
	return m
}

// waitState polls until name reaches want, or fails the test.
func waitState(t *testing.T, m *Manager, name string, want State) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last Status
	for time.Now().Before(deadline) {
		s, err := m.Status(name)
		if err != nil {
			t.Fatal(err)
		}
		last = s
		if s.State == want {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s state = %q, want %q (logs: %v)", name, last.State, want, tailLogs(t, m, name))
	return last
}

func tailLogs(t *testing.T, m *Manager, name string) []string {
	t.Helper()
	b, err := m.Logs(name)
	if err != nil {
		return nil
	}
	return b.Tail(10)
}

func sleeper() config.Command {
	return config.Command{Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}
}

func TestStartThenStop(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})

	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, "a", Running)
	if s.PID <= 0 {
		t.Fatalf("pid = %d, want > 0", s.PID)
	}
	if s.UptimeSec < 0 {
		t.Fatalf("uptime = %v", s.UptimeSec)
	}

	if err := m.Stop("a"); err != nil {
		t.Fatal(err)
	}
	s = waitState(t, m, "a", Stopped)
	if s.PID != 0 {
		t.Fatalf("pid = %d after stop, want 0", s.PID)
	}
}

func TestStartTwiceIsConflict(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("a")
	waitState(t, m, "a", Running)

	err := m.Start("a")
	if !errors.Is(err, ErrWrongState) {
		t.Fatalf("err = %v, want ErrWrongState", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{})
	for _, err := range []error{
		m.Start("ghost"),
		m.Stop("ghost"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if _, err := m.Status("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Status err = %v, want ErrNotFound", err)
	}
}

func TestSpawnFailureIsFailedState(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"/nonexistent/binary"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err == nil {
		t.Fatal("Start err = nil, want a spawn error")
	}
	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Failed {
		t.Fatalf("state = %q, want failed", s.State)
	}
	if lines := tailLogs(t, m, "a"); len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "spawn failed") {
		t.Fatalf("logs = %v, want a spawn failure line", lines)
	}
}

func TestStopKillsChildTree(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		// The inner sh inherits the process group; killing the group must
		// take it with the parent.
		"a": {Cmd: []string{"sh", "-c", "sh -c 'exec sleep 30' & wait"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	pgid := waitState(t, m, "a", Running).PID

	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("process group %d not alive before stop: %v", pgid, err)
	}
	if err := m.Stop("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Stopped)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process group %d still alive after stop", pgid)
}

func TestExitZeroBecomesStopped(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"true"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, "a", Stopped)
	if s.ExitCode == nil || *s.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", s.ExitCode)
	}
}

func TestExitNonZeroBecomesFailed(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 3"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	s := waitState(t, m, "a", Failed)
	if s.ExitCode == nil || *s.ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", s.ExitCode)
	}
}

func TestListReturnsEveryCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper(), "b": sleeper()})
	got := m.List()
	if len(got) != 2 {
		t.Fatalf("List = %v, want 2 entries", got)
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("List not sorted by name: %v", got)
	}
	if got[0].State != Stopped {
		t.Fatalf("initial state = %q, want stopped", got[0].State)
	}
}

func TestLogFileWrittenWhenLogTrue(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "echo hi"}, Cwd: "/tmp", Log: true},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Stopped)

	b, err := m.Logs("a")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Tail(1); len(got) != 1 || got[0] != "hi" {
		t.Fatalf("Tail(1) = %v, want [hi]", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/manager/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the manager types**

Create `internal/manager/manager.go`:

```go
// Package manager owns the lifecycle of every configured command. It never
// imports internal/api.
package manager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/logbuf"
)

// Sentinel errors the API layer maps to HTTP status codes.
var (
	ErrNotFound   = errors.New("unknown command")
	ErrWrongState = errors.New("illegal in current state")
)

// State is a command's lifecycle state.
type State string

const (
	Stopped  State = "stopped"
	Starting State = "starting"
	Running  State = "running"
	Stopping State = "stopping"
	Failed   State = "failed"
)

// Process is one configured command and its running instance, if any.
type Process struct {
	Name      string
	Spec      config.Command
	State     State
	PID       int
	Started   time.Time
	ExitCode  *int
	Restarts  int
	SpecDirty bool
	Logs      *logbuf.Buffer

	cmd             *exec.Cmd
	cancel          context.CancelFunc
	done            chan struct{}
	pending         *config.Command
	intentionalStop bool
	backoff         time.Duration
	restartTimer    *time.Timer
}

// Status is the API view of a Process.
type Status struct {
	Name      string   `json:"name"`
	State     State    `json:"state"`
	PID       int      `json:"pid,omitempty"`
	UptimeSec float64  `json:"uptime_sec,omitempty"`
	ExitCode  *int     `json:"exit_code,omitempty"`
	Restarts  int      `json:"restarts"`
	SpecDirty bool     `json:"spec_dirty,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// Manager holds every command. One mutex guards the whole map; it is never
// held across a spawn's Wait or a terminate.
type Manager struct {
	mu     sync.Mutex
	procs  map[string]*Process
	cfg    *config.Config
	logDir string

	// Tunables, injectable so tests run in milliseconds.
	Grace       time.Duration
	BackoffMin  time.Duration
	BackoffMax  time.Duration
	UptimeReset time.Duration
	SettleDelay time.Duration

	startOrder []string
}

// New builds a Manager with every configured command in state stopped.
func New(cfg *config.Config, logDir string) *Manager {
	m := &Manager{
		procs:       make(map[string]*Process, len(cfg.Commands)),
		cfg:         cfg,
		logDir:      logDir,
		Grace:       10 * time.Second,
		BackoffMin:  time.Second,
		BackoffMax:  30 * time.Second,
		UptimeReset: 60 * time.Second,
		SettleDelay: 200 * time.Millisecond,
	}
	for name, c := range cfg.Commands {
		m.procs[name] = &Process{Name: name, Spec: c, State: Stopped}
	}
	return m
}

// List returns every command's status, sorted by name.
func (m *Manager) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.procs))
	for n := range m.procs {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Status, 0, len(names))
	for _, n := range names {
		out = append(out, m.procs[n].status())
	}
	return out
}

// Status returns one command's status.
func (m *Manager) Status(name string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[name]
	if !ok {
		return Status{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return p.status(), nil
}

// Logs returns a command's ring buffer, creating it if the command has never
// started.
func (m *Manager) Logs(name string) (*logbuf.Buffer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err := p.ensureLogs(m.logDir); err != nil {
		return nil, err
	}
	return p.Logs, nil
}

func (p *Process) status() Status {
	s := Status{
		Name:      p.Name,
		State:     p.State,
		PID:       p.PID,
		ExitCode:  p.ExitCode,
		Restarts:  p.Restarts,
		SpecDirty: p.SpecDirty,
		DependsOn: p.Spec.DependsOn,
	}
	if p.State == Running && !p.Started.IsZero() {
		s.UptimeSec = time.Since(p.Started).Seconds()
	}
	return s
}

// ensureLogs creates the ring buffer, attaching the disk tee when log: true.
func (p *Process) ensureLogs(logDir string) error {
	if p.Logs != nil {
		return nil
	}
	p.Logs = logbuf.New(p.Spec.Size)
	if !p.Spec.Log {
		return nil
	}
	return p.Logs.AttachFile(filepath.Join(logDir, logbuf.FileName(p.Name)))
}
```

- [ ] **Step 4: Write spawn and stop**

Create `internal/manager/start.go`:

```go
package manager

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// Start spawns one command.
func (m *Manager) Start(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(name)
}

func (m *Manager) startLocked(name string) error {
	p, ok := m.procs[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if p.State == Running || p.State == Starting {
		return fmt.Errorf("%w: %s is %s", ErrWrongState, name, p.State)
	}
	if err := p.ensureLogs(m.logDir); err != nil {
		return err
	}

	r, err := p.Spec.Resolve()
	if err != nil {
		p.State = Failed
		fmt.Fprintf(p.Logs, "lazycomd: %v\n", err)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, r.Argv[0], r.Argv[1:]...)
	cmd.Dir = r.Cwd
	cmd.Env = r.Env
	cmd.Stdout, cmd.Stderr = p.Logs, p.Logs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Escalation path: SIGKILL the whole group, not just the parent.
	cmd.Cancel = func() error { return killGroup(cmd.Process.Pid, syscall.SIGKILL) }

	p.State = Starting
	if err := cmd.Start(); err != nil {
		cancel()
		p.State = Failed
		fmt.Fprintf(p.Logs, "lazycomd: spawn failed: %v\n", err)
		return err
	}

	p.cmd, p.cancel = cmd, cancel
	p.PID, p.Started = cmd.Process.Pid, time.Now()
	p.ExitCode, p.intentionalStop = nil, false
	p.done = make(chan struct{})
	p.State = Running
	m.startOrder = append(m.startOrder, name)

	go m.reap(p, cmd)
	return nil
}

// Stop terminates a command and suppresses its restart policy.
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	p, ok := m.procs[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	m.markStopLocked(p)
	live := p.State == Stopping
	m.mu.Unlock()

	if live {
		m.terminate(p)
	}
	return nil
}

// Restart stops then starts, keeping the manual-stop suppression out of the
// way of the restart policy.
func (m *Manager) Restart(name string) error {
	if err := m.Stop(name); err != nil {
		return err
	}
	return m.Start(name)
}

// markStopLocked records an intentional stop and cancels a pending restart.
func (m *Manager) markStopLocked(p *Process) {
	p.intentionalStop = true
	if p.restartTimer != nil {
		p.restartTimer.Stop()
		p.restartTimer = nil
	}
	switch p.State {
	case Running, Starting:
		p.State = Stopping
	default:
		p.State = Stopped
	}
}

// terminate SIGTERMs the process group, then SIGKILLs it after Grace. Call it
// with the lock released.
func (m *Manager) terminate(p *Process) {
	m.mu.Lock()
	pid, done, cancel := p.PID, p.done, p.cancel
	m.mu.Unlock()

	if done == nil {
		return
	}
	_ = killGroup(pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(m.Grace):
		if cancel != nil {
			cancel()
		}
		<-done
	}
}

// killGroup signals the whole process group so child trees die with their
// parent.
func killGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, sig)
}
```

- [ ] **Step 5: Write the minimal reaper**

Create `internal/manager/reap.go`. Task 9 extends this file with the restart
policy; for now it only records the outcome.

```go
package manager

import (
	"errors"
	"os/exec"
)

// reap waits for one spawned process and records its outcome.
func (m *Manager) reap(p *Process, cmd *exec.Cmd) {
	code := exitCode(cmd.Wait())

	m.mu.Lock()
	p.ExitCode = &code
	p.PID = 0
	if p.intentionalStop || code == 0 {
		p.State = Stopped
	} else {
		p.State = Failed
	}
	close(p.done)
	m.mu.Unlock()
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/manager/ -v`
Expected: PASS, nine tests.

- [ ] **Step 7: Commit**

```bash
git add internal/manager/
git commit -m "feat(manager): spawn, stop and status with process-group kills"
```

---

### Task 9: Restart policy and backoff

**Files:**
- Modify: `internal/manager/reap.go` — add the restart decision to `reap`
- Test: `internal/manager/reap_test.go`

**Interfaces:**
- Consumes: `Manager`, `Process`, `reap`, `exitCode` from Task 8.
- Produces: `(*Manager).shouldRestart(r config.Restart, code int) bool`, `(*Manager).nextBackoff(p *Process) time.Duration`. Restart counting increments `Process.Restarts`; a pending restart lives in `Process.restartTimer` and is cancelled by `markStopLocked`.

- [ ] **Step 1: Write the failing test**

Create `internal/manager/reap_test.go`:

```go
package manager

import (
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
)

func TestOnFailureRestarts(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 1"}, Cwd: "/tmp", Restart: config.RestartOnFailure},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("a")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := m.Status("a")
		if err != nil {
			t.Fatal(err)
		}
		if s.Restarts >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := m.Status("a")
	t.Fatalf("restarts = %d, want >= 2", s.Restarts)
}

func TestOnFailureIgnoresCleanExit(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"true"}, Cwd: "/tmp", Restart: config.RestartOnFailure},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Stopped)

	time.Sleep(150 * time.Millisecond)
	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Stopped || s.Restarts != 0 {
		t.Fatalf("state = %q restarts = %d, want stopped and 0", s.State, s.Restarts)
	}
}

func TestAlwaysRestartsOnCleanExit(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"true"}, Cwd: "/tmp", Restart: config.RestartAlways},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("a")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := m.Status("a")
		if s.Restarts >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := m.Status("a")
	t.Fatalf("restarts = %d, want >= 1", s.Restarts)
}

func TestRestartNoStaysDown(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 1"}, Cwd: "/tmp", Restart: config.RestartNo},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Failed)

	time.Sleep(150 * time.Millisecond)
	s, _ := m.Status("a")
	if s.Restarts != 0 {
		t.Fatalf("restarts = %d, want 0", s.Restarts)
	}
}

func TestManualStopBeatsRestartPolicy(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 1"}, Cwd: "/tmp", Restart: config.RestartAlways},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop("a"); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Status("a")

	time.Sleep(200 * time.Millisecond)
	after, _ := m.Status("a")
	if after.State != Stopped {
		t.Fatalf("state = %q, want stopped", after.State)
	}
	if after.Restarts != before.Restarts {
		t.Fatalf("restarts went %d -> %d after a manual stop", before.Restarts, after.Restarts)
	}
}

func TestNextBackoffDoublesToCap(t *testing.T) {
	m := New(&config.Config{Commands: map[string]config.Command{}}, t.TempDir())
	m.BackoffMin = 1 * time.Second
	m.BackoffMax = 4 * time.Second
	p := &Process{Name: "a"}

	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	for i, w := range want {
		if got := m.nextBackoff(p); got != w {
			t.Fatalf("backoff %d = %v, want %v", i, got, w)
		}
	}
	p.backoff = 0
	if got := m.nextBackoff(p); got != time.Second {
		t.Fatalf("after reset = %v, want 1s", got)
	}
}

func TestShouldRestart(t *testing.T) {
	m := New(&config.Config{Commands: map[string]config.Command{}}, t.TempDir())
	cases := []struct {
		policy config.Restart
		code   int
		want   bool
	}{
		{config.RestartNo, 1, false},
		{config.RestartNo, 0, false},
		{config.RestartOnFailure, 1, true},
		{config.RestartOnFailure, 0, false},
		{config.RestartAlways, 0, true},
		{config.RestartAlways, 1, true},
	}
	for _, tc := range cases {
		if got := m.shouldRestart(tc.policy, tc.code); got != tc.want {
			t.Fatalf("shouldRestart(%q, %d) = %v, want %v", tc.policy, tc.code, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/manager/ -run 'TestOnFailure|TestAlways|TestRestartNo|TestManualStop|TestNextBackoff|TestShouldRestart' -v`
Expected: FAIL — `m.nextBackoff undefined`, and the restart tests time out on `restarts = 0`.

- [ ] **Step 3: Extend the reaper**

Replace `internal/manager/reap.go` with:

```go
package manager

import (
	"errors"
	"os/exec"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
)

// reap waits for one spawned process, records its outcome and applies the
// restart policy unless the stop was intentional.
func (m *Manager) reap(p *Process, cmd *exec.Cmd) {
	code := exitCode(cmd.Wait())

	m.mu.Lock()
	defer m.mu.Unlock()

	uptime := time.Since(p.Started)
	p.ExitCode = &code
	p.PID = 0
	if p.intentionalStop || code == 0 {
		p.State = Stopped
	} else {
		p.State = Failed
	}
	close(p.done)

	if p.intentionalStop || !m.shouldRestart(p.Spec.Restart, code) {
		return
	}
	if uptime >= m.UptimeReset {
		p.backoff = 0
	}
	delay := m.nextBackoff(p)
	p.Restarts++
	name := p.Name
	p.restartTimer = time.AfterFunc(delay, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		p.restartTimer = nil
		if p.intentionalStop {
			return
		}
		if err := m.startLocked(name); err != nil {
			// The failure is already in the command's log buffer.
			return
		}
	})
}

func (m *Manager) shouldRestart(r config.Restart, code int) bool {
	switch r {
	case config.RestartAlways:
		return true
	case config.RestartOnFailure:
		return code != 0
	default:
		return false
	}
}

// nextBackoff doubles the delay up to BackoffMax. A caller resets p.backoff
// to zero when the process earned a clean slate.
func (m *Manager) nextBackoff(p *Process) time.Duration {
	if p.backoff == 0 {
		p.backoff = m.BackoffMin
		return p.backoff
	}
	p.backoff *= 2
	if p.backoff > m.BackoffMax {
		p.backoff = m.BackoffMax
	}
	return p.backoff
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/manager/ -v`
Expected: PASS, all Task 8 and Task 9 tests.

- [ ] **Step 5: Check for data races**

Run: `go test -race ./internal/manager/`
Expected: PASS with no `DATA RACE` output.

- [ ] **Step 6: Commit**

```bash
git add internal/manager/
git commit -m "feat(manager): restart policies with capped exponential backoff"
```

---
### Task 10: Dependency-ordered start

**Files:**
- Create: `internal/manager/deps.go`
- Test: `internal/manager/deps_test.go`

**Interfaces:**
- Consumes: `Manager`, `startLocked`, `Process.Spec.DependsOn` (already fully qualified by `config.Load`).
- Produces: `(*Manager).StartWithDeps(name string) error`.

Readiness is shallow by design: a dependency counts as ready once it is
spawned plus `SettleDelay`. No probes.

- [ ] **Step 1: Write the failing test**

Create `internal/manager/deps_test.go`:

```go
package manager

import (
	"errors"
	"slices"
	"testing"

	"github.com/tphuc/lazycomd/internal/config"
)

func TestStartWithDepsOrdersDepthFirst(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":    {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"cache": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api":   {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db", "cache"}},
	})
	defer m.Shutdown()

	if err := m.StartWithDeps("api"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"db", "cache", "api"} {
		waitState(t, m, n, Running)
	}

	m.mu.Lock()
	order := slices.Clone(m.startOrder)
	m.mu.Unlock()

	want := []string{"db", "cache", "api"}
	if !slices.Equal(order, want) {
		t.Fatalf("startOrder = %v, want %v", order, want)
	}
}

func TestStartWithDepsSkipsRunningDeps(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":  {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}},
	})
	defer m.Shutdown()

	if err := m.Start("db"); err != nil {
		t.Fatal(err)
	}
	dbPID := waitState(t, m, "db", Running).PID

	if err := m.StartWithDeps("api"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "api", Running)

	if got := waitState(t, m, "db", Running).PID; got != dbPID {
		t.Fatalf("db pid changed %d -> %d, want the running dep left alone", dbPID, got)
	}
}

func TestStopDoesNotCascadeToDependents(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":  {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}},
	})
	defer m.Shutdown()

	if err := m.StartWithDeps("api"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "api", Running)

	if err := m.Stop("db"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "db", Stopped)

	s, err := m.Status("api")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Running {
		t.Fatalf("api state = %q after stopping db, want running", s.State)
	}
}

func TestStartWithDepsUnknownCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{})
	if err := m.StartWithDeps("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

Note: these tests call `m.Shutdown()`, which Task 12 adds. Until then, replace
the `defer m.Shutdown()` lines with explicit `defer m.Stop(...)` calls per
command, then restore them in Task 12. Either way the assertions are the same.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/manager/ -run TestStartWithDeps -v`
Expected: FAIL — `m.StartWithDeps undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/manager/deps.go`:

```go
package manager

import (
	"fmt"
	"time"
)

// StartWithDeps starts a command's dependencies depth-first, in config order,
// before the command itself. Already-running dependencies are left alone.
func (m *Manager) StartWithDeps(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startTreeLocked(name, make(map[string]bool))
}

// ponytail: the settle sleep holds the manager lock, so a three-deep chain
// blocks the API for ~600ms. Move to a per-process sync.Cond if the TUI
// stutters.
func (m *Manager) startTreeLocked(name string, seen map[string]bool) error {
	if seen[name] {
		return nil
	}
	seen[name] = true

	p, ok := m.procs[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	for _, dep := range p.Spec.DependsOn {
		if err := m.startTreeLocked(dep, seen); err != nil {
			return err
		}
	}
	if p.State == Running || p.State == Starting {
		return nil
	}
	if err := m.startLocked(name); err != nil {
		return err
	}
	time.Sleep(m.SettleDelay)
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/manager/ -v`
Expected: PASS, every manager test.

- [ ] **Step 5: Commit**

```bash
git add internal/manager/
git commit -m "feat(manager): start dependencies depth-first before a command"
```

---

### Task 11: Config reload diff

**Files:**
- Create: `internal/manager/reload.go`
- Modify: `internal/manager/start.go` — apply a staged spec at the top of `startLocked`
- Test: `internal/manager/reload_test.go`

**Interfaces:**
- Consumes: `Manager`, `Process.pending`, `markStopLocked`, `terminate`.
- Produces: `(*Manager).Reload(cfg *config.Config) error`.

Semantics: added commands appear `stopped`; removed commands are stopped and
dropped; a changed spec on a running or starting command is staged and
reported as `spec_dirty: true` until its next start; a changed spec on a
stopped command applies immediately. Reload never restarts anything.

- [ ] **Step 1: Write the failing test**

Create `internal/manager/reload_test.go`:

```go
package manager

import (
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
)

func TestReloadAddsCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	if err := m.Reload(&config.Config{Commands: map[string]config.Command{
		"a": sleeper(),
		"b": sleeper(),
	}}); err != nil {
		t.Fatal(err)
	}
	s, err := m.Status("b")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Stopped {
		t.Fatalf("new command state = %q, want stopped", s.State)
	}
}

func TestReloadRemovesAndStopsCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	pgid := waitState(t, m, "a", Running).PID

	if err := m.Reload(&config.Config{Commands: map[string]config.Command{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after removal", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process group %d still alive after removal", pgid)
}

func TestReloadStagesChangeOnRunningCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	defer m.Stop("a")
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	pgid := waitState(t, m, "a", Running).PID

	changed := config.Command{Cmd: []string{"sh", "-c", "echo v2"}, Cwd: "/tmp"}
	if err := m.Reload(&config.Config{Commands: map[string]config.Command{"a": changed}}); err != nil {
		t.Fatal(err)
	}

	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Running {
		t.Fatalf("state = %q, want the running process untouched", s.State)
	}
	if !s.SpecDirty {
		t.Fatal("spec_dirty = false, want true")
	}
	if s.PID != pgid {
		t.Fatalf("pid changed %d -> %d, want no restart on reload", pgid, s.PID)
	}

	// The staged spec takes effect on the next start.
	if err := m.Restart("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Stopped)

	s, _ = m.Status("a")
	if s.SpecDirty {
		t.Fatal("spec_dirty = true after restart, want false")
	}
	b, err := m.Logs("a")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Tail(1); len(got) != 1 || got[0] != "v2" {
		t.Fatalf("Tail(1) = %v, want [v2]", got)
	}
}

func TestReloadAppliesChangeOnStoppedCommand(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	changed := config.Command{Cmd: []string{"true"}, Cwd: "/tmp"}
	if err := m.Reload(&config.Config{Commands: map[string]config.Command{"a": changed}}); err != nil {
		t.Fatal(err)
	}
	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.SpecDirty {
		t.Fatal("spec_dirty = true on a stopped command, want false")
	}

	m.mu.Lock()
	got := m.procs["a"].Spec.Cmd[0]
	m.mu.Unlock()
	if got != "true" {
		t.Fatalf("spec cmd = %q, want the reloaded value", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/manager/ -run TestReload -v`
Expected: FAIL — `m.Reload undefined`.

- [ ] **Step 3: Apply a staged spec when starting**

In `internal/manager/start.go`, insert at the top of `startLocked`, directly
after the `ErrWrongState` check and before `p.ensureLogs`:

```go
	if p.pending != nil {
		p.Spec, p.pending = *p.pending, nil
		p.SpecDirty = false
		p.Logs = nil // size and log: may have changed; rebuild the buffer
	}
```

- [ ] **Step 4: Write the implementation**

Create `internal/manager/reload.go`:

```go
package manager

import (
	"reflect"

	"github.com/tphuc/lazycomd/internal/config"
)

// Reload applies a freshly loaded config. It never restarts a running
// command: a changed spec is staged for that command's next start.
func (m *Manager) Reload(cfg *config.Config) error {
	m.mu.Lock()

	var kill []*Process
	for name, p := range m.procs {
		if _, ok := cfg.Commands[name]; ok {
			continue
		}
		delete(m.procs, name)
		if p.State == Running || p.State == Starting {
			m.markStopLocked(p)
			kill = append(kill, p)
		}
	}

	for name, c := range cfg.Commands {
		p, ok := m.procs[name]
		if !ok {
			m.procs[name] = &Process{Name: name, Spec: c, State: Stopped}
			continue
		}
		if reflect.DeepEqual(p.Spec, c) {
			continue
		}
		if p.State == Running || p.State == Starting {
			staged := c
			p.pending = &staged
			p.SpecDirty = true
			continue
		}
		p.Spec = c
		p.pending = nil
		p.SpecDirty = false
		p.Logs = nil
	}

	m.cfg = cfg
	m.mu.Unlock()

	for _, p := range kill {
		m.terminate(p)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/manager/ -v`
Expected: PASS, every manager test, no data races.

- [ ] **Step 6: Commit**

```bash
git add internal/manager/
git commit -m "feat(manager): apply config reloads without restarting anything"
```

---

### Task 12: Autostart, drain and hard kill

**Files:**
- Create: `internal/manager/lifecycle.go`
- Test: `internal/manager/lifecycle_test.go`

**Interfaces:**
- Consumes: `Manager`, `StartWithDeps`, `markStopLocked`, `terminate`, `Process.cancel`.
- Produces: `(*Manager).StartAutostart()`, `(*Manager).Shutdown()`, `(*Manager).KillAll()`.

- [ ] **Step 1: Write the failing test**

Create `internal/manager/lifecycle_test.go`:

```go
package manager

import (
	"errors"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
)

func TestStartAutostartStartsDepsFirst(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"db":    {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"api":   {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}, Autostart: true},
		"quiet": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	defer m.Shutdown()

	m.StartAutostart()
	waitState(t, m, "api", Running)
	waitState(t, m, "db", Running)

	s, err := m.Status("quiet")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != Stopped {
		t.Fatalf("quiet state = %q, want stopped", s.State)
	}

	m.mu.Lock()
	order := slices.Clone(m.startOrder)
	m.mu.Unlock()
	if !slices.Equal(order, []string{"db", "api"}) {
		t.Fatalf("startOrder = %v, want [db api]", order)
	}
}

func TestShutdownStopsEverything(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "sh -c 'exec sleep 30' & wait"}, Cwd: "/tmp"},
		"b": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("b"); err != nil {
		t.Fatal(err)
	}
	pgidA := waitState(t, m, "a", Running).PID
	pgidB := waitState(t, m, "b", Running).PID

	m.Shutdown()

	for _, pgid := range []int{pgidA, pgidB} {
		if err := syscall.Kill(-pgid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("process group %d still alive after Shutdown (err = %v)", pgid, err)
		}
	}
	for _, n := range []string{"a", "b"} {
		s, err := m.Status(n)
		if err != nil {
			t.Fatal(err)
		}
		if s.State != Stopped {
			t.Fatalf("%s state = %q after Shutdown, want stopped", n, s.State)
		}
	}
}

func TestShutdownCancelsPendingRestart(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "exit 1"}, Cwd: "/tmp", Restart: config.RestartAlways},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "a", Failed)
	m.Shutdown()

	time.Sleep(200 * time.Millisecond)
	s, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	if s.State == Running {
		t.Fatal("command restarted after Shutdown")
	}
}

func TestKillAllIsImmediate(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		// Ignores SIGTERM: only SIGKILL ends it.
		"a": {Cmd: []string{"sh", "-c", "trap '' TERM; sleep 30"}, Cwd: "/tmp"},
	})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	pgid := waitState(t, m, "a", Running).PID

	m.KillAll()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process group %d survived KillAll", pgid)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/manager/ -run 'TestStartAutostart|TestShutdown|TestKillAll' -v`
Expected: FAIL — `m.StartAutostart undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/manager/lifecycle.go`:

```go
package manager

import (
	"log"
	"sort"
	"sync"
)

// StartAutostart starts every command marked autostart, dependencies first,
// in name order. A failure is logged, never fatal.
func (m *Manager) StartAutostart() {
	m.mu.Lock()
	names := make([]string, 0, len(m.procs))
	for name, p := range m.procs {
		if p.Spec.Autostart {
			names = append(names, name)
		}
	}
	m.mu.Unlock()
	sort.Strings(names)

	for _, name := range names {
		if err := m.StartWithDeps(name); err != nil {
			log.Printf("lazycomd: autostart %s: %v", name, err)
		}
	}
}

// Shutdown stops every running command: SIGTERM, then SIGKILL after Grace.
// It also cancels pending restarts and closes disk log tees.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	var kill []*Process
	for _, p := range m.procs {
		m.markStopLocked(p)
		if p.State == Stopping {
			kill = append(kill, p)
		}
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range kill {
		wg.Add(1)
		go func(p *Process) {
			defer wg.Done()
			m.terminate(p)
		}(p)
	}
	wg.Wait()

	m.mu.Lock()
	for _, p := range m.procs {
		if p.Logs != nil {
			_ = p.Logs.Close()
		}
	}
	m.mu.Unlock()
}

// KillAll SIGKILLs every process group immediately. Used when a second
// interrupt arrives while Shutdown is still draining.
func (m *Manager) KillAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.procs {
		p.intentionalStop = true
		if p.restartTimer != nil {
			p.restartTimer.Stop()
			p.restartTimer = nil
		}
		if p.cancel != nil {
			p.cancel()
		}
	}
}
```

- [ ] **Step 4: Restore `defer m.Shutdown()` in the Task 10 tests**

If Task 10's tests were written with per-command `defer m.Stop(...)` calls,
change them back to `defer m.Shutdown()` now.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/manager/ -v`
Expected: PASS, every manager test.

- [ ] **Step 6: Commit**

```bash
git add internal/manager/
git commit -m "feat(manager): autostart, graceful drain and immediate kill"
```

---

### Task 13: HTTP API — commands, status and error mapping

**Files:**
- Create: `internal/api/server.go`
- Test: `internal/api/server_test.go`

**Interfaces:**
- Consumes: `manager.Manager`, `manager.Status`, `manager.ErrNotFound`, `manager.ErrWrongState`, `config.Config`.
- Produces:
  - `api.NewServer(mgr *manager.Manager, token string, reload func() (*config.Config, error)) *Server`.
  - `(*Server).Handler() http.Handler` — unauthenticated, for the unix socket.
  - Routes: `GET /v1/healthz`, `GET /v1/commands`, `GET /v1/commands/{name}`, `POST /v1/commands/{name}/start`, `.../stop`, `.../restart`.
  - Internal helpers later tasks reuse: `(*Server).routes() *http.ServeMux`, `(*Server).fail`, `writeJSON(w, code, v)`, `errBody(err)`, `recoverMW(next)`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/server_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
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
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	var req *http.Request
	var err error
	if rdr == nil {
		req, err = http.NewRequest(method, "http://unix"+path, nil)
	} else {
		req, err = http.NewRequest(method, "http://unix"+path, rdr)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, buf.String()
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -v`
Expected: FAIL — `undefined: NewServer`.

- [ ] **Step 3: Write the implementation**

Create `internal/api/server.go`:

```go
// Package api serves the lazycomd HTTP API. It never spawns a process; every
// lifecycle action goes through the manager.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"runtime/debug"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// maxBody caps a request body; every body this API accepts is tiny.
const maxBody = 1 << 16

// Server holds the manager plus the config reloader the /v1/reload endpoint
// calls.
type Server struct {
	mgr    *manager.Manager
	token  string
	reload func() (*config.Config, error)
}

// NewServer builds a Server. token is used only by AuthHandler.
func NewServer(mgr *manager.Manager, token string, reload func() (*config.Config, error)) *Server {
	return &Server{mgr: mgr, token: token, reload: reload}
}

// Handler is the unauthenticated handler for the unix socket, where file
// permissions are the authentication.
func (s *Server) Handler() http.Handler { return recoverMW(s.routes()) }

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.healthz)
	mux.HandleFunc("GET /v1/commands", s.list)
	mux.HandleFunc("GET /v1/commands/{name}", s.get)
	mux.HandleFunc("POST /v1/commands/{name}/start", s.start)
	mux.HandleFunc("POST /v1/commands/{name}/stop", s.stop)
	mux.HandleFunc("POST /v1/commands/{name}/restart", s.restart)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.List())
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	st, err := s.mgr.Status(r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type startBody struct {
	WithDeps bool `json:"with_deps"`
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var body startBody
	if err := decodeOptional(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	name := r.PathValue("name")

	var err error
	if body.WithDeps {
		err = s.mgr.StartWithDeps(name)
	} else {
		err = s.mgr.Start(name)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.get(w, r)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Stop(r.PathValue("name")); err != nil {
		s.fail(w, err)
		return
	}
	s.get(w, r)
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Restart(r.PathValue("name")); err != nil {
		s.fail(w, err)
		return
	}
	s.get(w, r)
}

// fail maps a manager error to its HTTP status.
func (s *Server) fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, manager.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, manager.ErrWrongState):
		code = http.StatusConflict
	}
	writeJSON(w, code, errBody(err))
}

// decodeOptional decodes a JSON body that may be absent or empty.
func decodeOptional(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(err error) map[string]string {
	return map[string]string{"error": err.Error()}
}

// recoverMW keeps a client-triggered panic from taking down a running stack.
func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("lazycomd: panic in %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS, six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/api/
git commit -m "feat(api): command listing and lifecycle endpoints over HTTP"
```

---

### Task 14: Log tail and SSE streaming endpoints

**Files:**
- Create: `internal/api/logs.go`
- Modify: `internal/api/server.go` — register the two log routes in `routes()`
- Test: `internal/api/logs_test.go`

**Interfaces:**
- Consumes: `Server`, `writeJSON`, `errBody`, `(*Manager).Logs`, `(*logbuf.Buffer).Tail`, `.Subscribe`.
- Produces: `GET /v1/commands/{name}/logs?tail=N` returning a JSON array of strings (default 200, max 10000), and `GET /v1/commands/{name}/logs/stream` returning `text/event-stream` with one `data: ` line per output line.

- [ ] **Step 1: Write the failing test**

Create `internal/api/logs_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestLogs -v`
Expected: FAIL — 404 from the unregistered routes.

- [ ] **Step 3: Register the routes**

In `internal/api/server.go`, add to `routes()` before the closing `return mux`:

```go
	mux.HandleFunc("GET /v1/commands/{name}/logs", s.logs)
	mux.HandleFunc("GET /v1/commands/{name}/logs/stream", s.stream)
```

- [ ] **Step 4: Write the implementation**

Create `internal/api/logs.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTail = 200
	maxTail     = 10000
	pingEvery   = 30 * time.Second
)

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	tail := defaultTail
	if q := r.URL.Query().Get("tail"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 || n > maxTail {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("tail must be an integer between 0 and %d", maxTail),
			})
			return
		}
		tail = n
	}
	b, err := s.mgr.Logs(r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b.Tail(tail))
}

// stream sends live output as SSE. One data: line per output line, plus a
// comment ping so an idle connection stays open through proxies.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	b, err := s.mgr.Logs(r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	ch, cancel := b.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case chunk, open := <-ch:
			if !open {
				return
			}
			for _, line := range strings.Split(strings.TrimSuffix(string(chunk), "\n"), "\n") {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			flusher.Flush()
		}
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS, all ten API tests.

- [ ] **Step 6: Commit**

```bash
git add internal/api/
git commit -m "feat(api): log tail endpoint and SSE log streaming"
```

---

### Task 15: Token authentication and the reload endpoint

**Files:**
- Create: `internal/api/auth.go`
- Modify: `internal/api/server.go` — register `POST /v1/reload` in `routes()`
- Test: `internal/api/auth_test.go`

**Interfaces:**
- Consumes: `Server`, `Server.token`, `Server.reload`, `(*Manager).Reload`, `writeJSON`, `errBody`, `recoverMW`.
- Produces: `(*Server).AuthHandler() http.Handler` for the TCP listener, and `POST /v1/reload` which re-reads config and applies the diff.

- [ ] **Step 1: Write the failing test**

Create `internal/api/auth_test.go`:

```go
package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

func TestAuthHandlerRequiresToken(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{"a": sleeper()}}, t.TempDir())
	t.Cleanup(m.Shutdown)
	s := NewServer(m, "s3cret", func() (*config.Config, error) { return nil, nil })
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
	s := NewServer(m, "", func() (*config.Config, error) { return nil, nil })
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
	s := NewServer(m, "", func() (*config.Config, error) { return next, nil })
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
	s := NewServer(m, "", func() (*config.Config, error) { return nil, broken })
	c := serveUnix(t, s.Handler())

	code, body := do(t, c, "POST", "/v1/reload", "")
	if code != 400 {
		t.Fatalf("reload = %d %s, want 400", code, body)
	}
	if _, err := m.Status("a"); err != nil {
		t.Fatalf("old config dropped after a failed reload: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run 'TestAuth|TestReload' -v`
Expected: FAIL — `s.AuthHandler undefined`.

- [ ] **Step 3: Register the reload route**

In `internal/api/server.go`, add to `routes()`:

```go
	mux.HandleFunc("POST /v1/reload", s.doReload)
```

- [ ] **Step 4: Write the implementation**

Create `internal/api/auth.go`:

```go
package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// AuthHandler is the token-protected handler for the optional TCP listener.
// An empty configured token rejects every request: the daemon refuses to
// start with listen: and no token_file, so reaching here with one is a bug.
func (s *Server) AuthHandler() http.Handler {
	return recoverMW(tokenMW(s.token, s.routes()))
}

func tokenMW(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if len(want) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// doReload re-reads the config from disk and applies the diff. A broken
// config is a 400 and leaves the running config untouched.
func (s *Server) doReload(w http.ResponseWriter, _ *http.Request) {
	cfg, err := s.reload()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	if err := s.mgr.Reload(cfg); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.List())
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/api/ -v`
Expected: PASS, all fourteen API tests.

- [ ] **Step 6: Commit**

```bash
git add internal/api/
git commit -m "feat(api): bearer-token middleware and config reload endpoint"
```

---
### Task 16: HTTP client and name resolution

**Files:**
- Create: `internal/client/client.go`, `internal/client/resolve.go`
- Test: `internal/client/client_test.go`

**Interfaces:**
- Consumes: `manager.Status`, `paths.SocketPath`, and (in tests only) `api.NewServer`.
- Produces:
  - `client.New(addr, token string) (*Client, error)` — `addr` is `unix:///path/to.sock` or `http://host:port`.
  - `client.Default() (*Client, error)` — `LAZYCOMD_ADDR`, `LAZYCOMD_TOKEN`, else the default socket.
  - `client.ErrNoDaemon`, `client.APIError{Status int, Msg string}`.
  - `(*Client).Health() error`, `.List() ([]manager.Status, error)`, `.Get(name string) (manager.Status, error)`, `.Start(name string, withDeps bool) (manager.Status, error)`, `.Stop(name string) (manager.Status, error)`, `.Restart(name string) (manager.Status, error)`, `.Logs(name string, tail int) ([]string, error)`, `.Reload() ([]manager.Status, error)`, `.Stream(ctx context.Context, name string, w io.Writer) error`, `.Resolve(name string) (string, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/client/client_test.go`:

```go
package client

import (
	"context"
	"errors"
	"net"
	"net/http"
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

	s := api.NewServer(m, token, func() (*config.Config, error) {
		return &config.Config{Commands: cmds}, nil
	})
	h := s.Handler()
	if token != "" {
		h = s.AuthHandler()
	}

	sock := filepath.Join(t.TempDir(), "s.sock")
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

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lines, err := c.Logs("a", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) > 0 && lines[0] == "hi" {
			break
		}
		time.Sleep(10 * time.Millisecond)
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

func TestClientStream(t *testing.T) {
	c := daemon(t, map[string]config.Command{
		"a": {Cmd: []string{"sh", "-c", "sleep 0.2; echo streamed; sleep 5"}, Cwd: "/tmp"},
	}, "")

	if _, err := c.Start("a", false); err != nil {
		t.Fatal(err)
	}
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/client/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the client**

Create `internal/client/client.go`:

```go
// Package client talks to the lazycomd daemon over a unix socket or TCP.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/paths"
)

// ErrNoDaemon means nothing is listening at the configured address.
var ErrNoDaemon = errors.New("daemon not running")

// APIError is a non-2xx response carrying the daemon's error message.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// Client is a lazycomd API client.
type Client struct {
	http  *http.Client
	base  string
	token string
}

// New builds a client for addr: "unix:///path/to.sock" or "http://host:port".
func New(addr, token string) (*Client, error) {
	if sock, ok := strings.CutPrefix(addr, "unix://"); ok {
		return &Client{
			base:  "http://unix",
			token: token,
			http: &http.Client{Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", sock)
				},
			}},
		}, nil
	}
	if u, err := url.Parse(addr); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return &Client{
			base:  strings.TrimSuffix(addr, "/"),
			token: token,
			http:  &http.Client{Timeout: 30 * time.Second},
		}, nil
	}
	return nil, fmt.Errorf("bad address %q: want unix:///path/to.sock or http://host:port", addr)
}

// Default builds a client from LAZYCOMD_ADDR and LAZYCOMD_TOKEN, falling back
// to the default unix socket.
func Default() (*Client, error) {
	addr := os.Getenv("LAZYCOMD_ADDR")
	if addr == "" {
		addr = "unix://" + paths.SocketPath()
	}
	return New(addr, os.Getenv("LAZYCOMD_TOKEN"))
}

func (c *Client) Health() error {
	return c.do(context.Background(), "GET", "/v1/healthz", nil, nil)
}

func (c *Client) List() ([]manager.Status, error) {
	var out []manager.Status
	return out, c.do(context.Background(), "GET", "/v1/commands", nil, &out)
}

func (c *Client) Get(name string) (manager.Status, error) {
	var out manager.Status
	return out, c.do(context.Background(), "GET", "/v1/commands/"+url.PathEscape(name), nil, &out)
}

func (c *Client) Start(name string, withDeps bool) (manager.Status, error) {
	var out manager.Status
	body := map[string]bool{"with_deps": withDeps}
	return out, c.do(context.Background(), "POST", "/v1/commands/"+url.PathEscape(name)+"/start", body, &out)
}

func (c *Client) Stop(name string) (manager.Status, error) {
	var out manager.Status
	return out, c.do(context.Background(), "POST", "/v1/commands/"+url.PathEscape(name)+"/stop", nil, &out)
}

func (c *Client) Restart(name string) (manager.Status, error) {
	var out manager.Status
	return out, c.do(context.Background(), "POST", "/v1/commands/"+url.PathEscape(name)+"/restart", nil, &out)
}

func (c *Client) Logs(name string, tail int) ([]string, error) {
	var out []string
	path := "/v1/commands/" + url.PathEscape(name) + "/logs?tail=" + strconv.Itoa(tail)
	return out, c.do(context.Background(), "GET", path, nil, &out)
}

func (c *Client) Reload() ([]manager.Status, error) {
	var out []manager.Status
	return out, c.do(context.Background(), "POST", "/v1/reload", nil, &out)
}

// Stream copies live output lines to w until ctx is done or the command's
// buffer goes away.
func (c *Client) Stream(ctx context.Context, name string, w io.Writer) error {
	path := "/v1/commands/" + url.PathEscape(name) + "/logs/stream"
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if isDown(err) {
			return ErrNoDaemon
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &APIError{Status: resp.StatusCode, Msg: apiMessage(resp.Body, resp.StatusCode)}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue // blank separator or ": ping"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if isDown(err) {
			return ErrNoDaemon
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return &APIError{Status: resp.StatusCode, Msg: apiMessage(resp.Body, resp.StatusCode)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func apiMessage(r io.Reader, status int) string {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(r).Decode(&body); err == nil && body.Error != "" {
		return body.Error
	}
	return fmt.Sprintf("http %d", status)
}

// isDown distinguishes "no daemon there" from a real transport failure.
func isDown(err error) bool {
	return errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET)
}
```

- [ ] **Step 4: Write name resolution**

Create `internal/client/resolve.go`:

```go
package client

import (
	"fmt"
	"sort"
	"strings"
)

// Resolve turns a bare name into a qualified command name: an exact match
// wins, then a unique project-qualified match. An ambiguous name is an error
// listing every candidate.
func (c *Client) Resolve(name string) (string, error) {
	list, err := c.List()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, s := range list {
		if s.Name == name {
			return name, nil
		}
		if strings.HasSuffix(s.Name, ":"+name) {
			matches = append(matches, s.Name)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("unknown command %q", name)
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("ambiguous command %q: %s", name, strings.Join(matches, ", "))
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/client/ -v`
Expected: PASS, six tests.

- [ ] **Step 6: Commit**

```bash
git add internal/client/
git commit -m "feat(client): API client over unix or TCP with name resolution"
```

---

### Task 17: The `serve` subcommand — listeners, single instance, drain

**Files:**
- Create: `cmd/lazycomd/serve.go`
- Test: `cmd/lazycomd/serve_test.go`

**Interfaces:**
- Consumes: `config.Load`, `config.ExpandUser`, `manager.New`, `(*Manager).StartAutostart`, `.Shutdown`, `.KillAll`, `api.NewServer`, `(*Server).Handler`, `.AuthHandler`, `client.New`, `paths.*`.
- Produces (all in `package main`): `runServe(args []string) int`, `listenUnix(path string) (net.Listener, error)`, `bindAddr(listen string) string`, `readToken(path string) (string, error)`, `daemonAlive(sock string) bool`.

- [ ] **Step 1: Write the failing test**

Create `cmd/lazycomd/serve_test.go`:

```go
package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindAddrKeepsBarePortOnLoopback(t *testing.T) {
	if got, want := bindAddr(":7777"), "127.0.0.1:7777"; got != want {
		t.Fatalf("bindAddr(:7777) = %q, want %q", got, want)
	}
	if got, want := bindAddr("0.0.0.0:7777"), "0.0.0.0:7777"; got != want {
		t.Fatalf("bindAddr = %q, want it left alone", got)
	}
	if got, want := bindAddr("192.168.1.5:7777"), "192.168.1.5:7777"; got != want {
		t.Fatalf("bindAddr = %q, want %q", got, want)
	}
}

func TestReadToken(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "token")
	if err := os.WriteFile(good, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readToken(good)
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cret" {
		t.Fatalf("token = %q, want s3cret", got)
	}

	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(loose, []byte("s3cret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(loose); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("err = %v, want a mode complaint", err)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(empty); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want an empty-token complaint", err)
	}

	if _, err := readToken(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing file: err = nil, want an error")
	}
	if _, err := readToken(""); err == nil {
		t.Fatal("empty path: err = nil, want an error")
	}
}

func TestListenUnixClearsStaleSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "lazycomd.sock")
	// A leftover file with nothing behind it is stale garbage.
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := listenUnix(sock)
	if err != nil {
		t.Fatalf("listenUnix over a stale socket: %v", err)
	}
	defer l.Close()

	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestListenUnixRefusesWhenDaemonAlive(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "lazycomd.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/healthz" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})}
	go srv.Serve(l)
	defer srv.Close()

	if _, err := listenUnix(sock); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v, want an already-running error", err)
	}
}

func TestRunServeRejectsBadConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("listten: \":1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runServe([]string{"-config", cfg}); code != 1 {
		t.Fatalf("runServe = %d, want 1", code)
	}
}

func TestRunServeRejectsListenWithoutToken(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("listen: \":7777\"\ncommands: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runServe([]string{"-config", cfg}); code != 1 {
		t.Fatalf("runServe = %d, want 1", code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/lazycomd/ -v`
Expected: FAIL — `undefined: bindAddr`.

- [ ] **Step 3: Write the implementation**

Create `cmd/lazycomd/serve.go`:

```go
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tphuc/lazycomd/internal/api"
	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/paths"
)

// runServe runs the daemon in the foreground. Daemonization belongs to
// launchd or systemd.
func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", paths.ConfigPath(), "path to config.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
		return 1
	}

	token := ""
	if cfg.Listen != "" {
		if token, err = readToken(cfg.TokenFile); err != nil {
			fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
			return 1
		}
	}

	mgr := manager.New(cfg, paths.LogDir())
	srv := api.NewServer(mgr, token, func() (*config.Config, error) {
		return config.Load(*cfgPath)
	})

	sock := paths.SocketPath()
	ul, err := listenUnix(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
		return 1
	}
	defer os.Remove(sock)

	unixSrv := &http.Server{Handler: srv.Handler()}
	go serveLogged(unixSrv, ul)
	log.Printf("lazycomd: listening on %s", sock)

	if cfg.Listen != "" {
		addr := bindAddr(cfg.Listen)
		tl, err := net.Listen("tcp", addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
			return 1
		}
		tcpSrv := &http.Server{Handler: srv.AuthHandler()}
		go serveLogged(tcpSrv, tl)
		defer tcpSrv.Close()
		log.Printf("lazycomd: listening on %s (token required)", addr)
	}

	mgr.StartAutostart()

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Print("lazycomd: draining, interrupt again to kill immediately")

	unixSrv.Close()
	drained := make(chan struct{})
	go func() {
		mgr.Shutdown()
		close(drained)
	}()
	select {
	case <-drained:
	case <-sigs:
		mgr.KillAll()
		<-drained
	}
	log.Print("lazycomd: stopped")
	return 0
}

// listenUnix claims the socket. A live daemon is fatal; a leftover socket
// with nothing behind it is cleared.
func listenUnix(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if daemonAlive(path) {
		return nil, errors.New("already running")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// daemonAlive reports whether something is answering healthz on the socket.
func daemonAlive(sock string) bool {
	if _, err := os.Stat(sock); err != nil {
		return false
	}
	c, err := client.New("unix://"+sock, "")
	if err != nil {
		return false
	}
	return c.Health() == nil
}

// bindAddr keeps a bare port on loopback. Exposing every interface has to be
// written out, and says so in the log.
func bindAddr(listen string) string {
	if strings.HasPrefix(listen, ":") {
		return "127.0.0.1" + listen
	}
	if strings.HasPrefix(listen, "0.0.0.0:") {
		log.Printf("lazycomd: WARNING %s exposes the API on every interface", listen)
	}
	return listen
}

// readToken reads and sanity-checks the bearer token file.
func readToken(path string) (string, error) {
	if path == "" {
		return "", errors.New("listen requires token_file")
	}
	path = config.ExpandUser(path)

	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("%s: mode is %v, want 0600", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("%s: empty token", path)
	}
	return token, nil
}

func serveLogged(s *http.Server, l net.Listener) {
	if err := s.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("lazycomd: serve: %v", err)
	}
}
```

Note the local variable `fs` shadows the imported `io/fs` inside `runServe`.
Rename the flag set to `flags` there so `fs.ErrNotExist` still resolves in
`listenUnix`, or drop the `io/fs` import and compare with `os.IsNotExist`.
Pick one and keep it consistent.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/lazycomd/ -v`
Expected: PASS, six tests.

- [ ] **Step 5: Commit**

```bash
git add cmd/lazycomd/
git commit -m "feat(serve): listeners, single-instance guard and signal drain"
```

---

### Task 18: CLI subcommands

**Files:**
- Create: `cmd/lazycomd/main.go`, `cmd/lazycomd/cli.go`
- Test: `cmd/lazycomd/cli_test.go`

**Interfaces:**
- Consumes: `client.Default`, `client.ErrNoDaemon`, every `*client.Client` method, `manager.Running`, `manager.Starting`, `runServe`.
- Produces: `main()`, `dispatch(args []string) int`, `hoistFlags(args []string, valueFlags map[string]bool) []string`, and the per-subcommand runners `runLs`, `runStart`, `runSimple`, `runLogs`, `runReload`, `runRun`.

Exit codes: 0 success, 1 client or daemon error, 2 usage error.

- [ ] **Step 1: Write the failing test**

Create `cmd/lazycomd/cli_test.go`:

```go
package main

import (
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

// testDaemon serves a manager over a unix socket and points LAZYCOMD_ADDR at
// it. It returns the manager so a test can assert daemon-side state.
func testDaemon(t *testing.T, cmds map[string]config.Command) *manager.Manager {
	t.Helper()
	m := manager.New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.NewServer(m, "", func() (*config.Config, error) {
		return &config.Config{Commands: cmds}, nil
	})
	sock := filepath.Join(t.TempDir(), "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	t.Setenv("LAZYCOMD_ADDR", "unix://"+sock)
	t.Setenv("LAZYCOMD_TOKEN", "")
	return m
}

// capture swaps os.Stdout for a pipe and returns everything fn printed.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b := new(strings.Builder)
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()

	fn()
	os.Stdout = orig
	w.Close()
	return <-done
}

func TestDispatchUsageErrors(t *testing.T) {
	if code := dispatch(nil); code != 2 {
		t.Fatalf("no args = %d, want 2", code)
	}
	if code := dispatch([]string{"nope"}); code != 2 {
		t.Fatalf("unknown subcommand = %d, want 2", code)
	}
	if code := dispatch([]string{"start"}); code != 2 {
		t.Fatalf("start without a name = %d, want 2", code)
	}
	if code := dispatch([]string{"help"}); code != 0 {
		t.Fatalf("help = %d, want 0", code)
	}
}

func TestDispatchNoDaemon(t *testing.T) {
	t.Setenv("LAZYCOMD_ADDR", "unix://"+filepath.Join(t.TempDir(), "absent.sock"))
	if code := dispatch([]string{"ls"}); code != 1 {
		t.Fatalf("ls with no daemon = %d, want 1", code)
	}
}

func TestLsListsCommands(t *testing.T) {
	testDaemon(t, map[string]config.Command{"proxy": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}})

	out := capture(t, func() {
		if code := dispatch([]string{"ls"}); code != 0 {
			t.Errorf("ls = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "proxy") || !strings.Contains(out, "stopped") {
		t.Fatalf("ls output = %q", out)
	}
}

func TestStartStopAndLogs(t *testing.T) {
	m := testDaemon(t, map[string]config.Command{
		"hello": {Cmd: []string{"sh", "-c", "echo hi; sleep 30"}, Cwd: "/tmp"},
	})

	if code := dispatch([]string{"start", "hello"}); code != 0 {
		t.Fatalf("start = %d, want 0", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := m.Logs("hello"); err == nil && len(b.Tail(1)) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	out := capture(t, func() {
		if code := dispatch([]string{"logs", "hello", "-n", "5"}); code != 0 {
			t.Errorf("logs = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "hi") {
		t.Fatalf("logs output = %q, want it to contain hi", out)
	}

	if code := dispatch([]string{"stop", "hello"}); code != 0 {
		t.Fatalf("stop = %d, want 0", code)
	}
	s, err := m.Status("hello")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != manager.Stopped {
		t.Fatalf("state = %q after stop, want stopped", s.State)
	}
}

func TestStartResolvesBareName(t *testing.T) {
	m := testDaemon(t, map[string]config.Command{
		"app:api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	if code := dispatch([]string{"start", "api"}); code != 0 {
		t.Fatalf("start = %d, want 0", code)
	}
	s, err := m.Status("app:api")
	if err != nil {
		t.Fatal(err)
	}
	if s.State != manager.Running {
		t.Fatalf("state = %q, want running", s.State)
	}
}

func TestStartAmbiguousNameIsError(t *testing.T) {
	testDaemon(t, map[string]config.Command{
		"app:api":     {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"scraper:api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	if code := dispatch([]string{"start", "api"}); code != 1 {
		t.Fatalf("ambiguous start = %d, want 1", code)
	}
}

func TestReloadSubcommand(t *testing.T) {
	testDaemon(t, map[string]config.Command{"a": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}})
	out := capture(t, func() {
		if code := dispatch([]string{"reload"}); code != 0 {
			t.Errorf("reload = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "a") {
		t.Fatalf("reload output = %q", out)
	}
}

func TestHoistFlags(t *testing.T) {
	got := hoistFlags([]string{"hello", "-n", "5", "-f"}, map[string]bool{"-n": true})
	want := "-n 5 -f hello"
	if strings.Join(got, " ") != want {
		t.Fatalf("hoistFlags = %q, want %q", strings.Join(got, " "), want)
	}
	got = hoistFlags([]string{"-d", "api"}, nil)
	if strings.Join(got, " ") != "-d api" {
		t.Fatalf("hoistFlags = %q", strings.Join(got, " "))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/lazycomd/ -run 'TestDispatch|TestLs|TestStart|TestReloadSub|TestHoist' -v`
Expected: FAIL — `undefined: dispatch`.

- [ ] **Step 3: Write the dispatcher**

Create `cmd/lazycomd/main.go`:

```go
// Command lazycomd runs and supervises long dev commands.
package main

import (
	"fmt"
	"os"
)

const usage = `lazycomd - run and supervise long dev commands

usage: lazycomd <command> [flags]

  serve                    run the daemon in the foreground
  ls                       list commands and their state
  start <name> [-d]        start a command (-d starts dependencies first)
  stop <name>              stop a command
  restart <name>           restart a command
  logs <name> [-n N] [-f]  show, or follow, a command's output
  reload                   re-read the config and apply the diff
  run <name>               start with dependencies, then follow output

environment:
  LAZYCOMD_ADDR    unix:///path/to.sock or http://host:port
  LAZYCOMD_TOKEN   bearer token, for a TCP address
  LAZYCOMD_CONFIG  override the config path
`

func main() { os.Exit(dispatch(os.Args[1:])) }

func dispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "ls":
		return runLs(args[1:])
	case "start":
		return runStart(args[1:])
	case "stop":
		return runSimple("stop", args[1:])
	case "restart":
		return runSimple("restart", args[1:])
	case "logs":
		return runLogs(args[1:])
	case "reload":
		return runReload(args[1:])
	case "run":
		return runRun(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "lazycomd: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
```

- [ ] **Step 4: Write the subcommands**

Create `cmd/lazycomd/cli.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/manager"
)

// hoistFlags moves flags ahead of positional arguments so both orders work:
// "logs api -f" and "logs -f api". valueFlags names the flags that consume
// the next argument.
func hoistFlags(args []string, valueFlags map[string]bool) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		if valueFlags[a] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, rest...)
}

// fail prints an error and returns the process exit code for it.
func fail(err error) int {
	if errors.Is(err, client.ErrNoDaemon) {
		fmt.Fprintln(os.Stderr, "lazycomd: daemon not running (start with: lazycomd serve)")
		return 1
	}
	fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
	return 1
}

func usageErr(line string) int {
	fmt.Fprintf(os.Stderr, "usage: lazycomd %s\n", line)
	return 2
}

func runLs(args []string) int {
	flags := flag.NewFlagSet("ls", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	list, err := c.List()
	if err != nil {
		return fail(err)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tPID\tUPTIME\tRESTARTS")
	for _, s := range list {
		state := string(s.State)
		if s.SpecDirty {
			state += " (spec changed)"
		}
		pid := "-"
		if s.PID > 0 {
			pid = strconv.Itoa(s.PID)
		}
		up := "-"
		if s.UptimeSec > 0 {
			up = time.Duration(s.UptimeSec * float64(time.Second)).Round(time.Second).String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", s.Name, state, pid, up, s.Restarts)
	}
	tw.Flush()
	return 0
}

func runStart(args []string) int {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	deps := flags.Bool("d", false, "start dependencies first")
	if err := flags.Parse(hoistFlags(args, nil)); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return usageErr("start <name> [-d]")
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	name, err := c.Resolve(flags.Arg(0))
	if err != nil {
		return fail(err)
	}
	st, err := c.Start(name, *deps)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("%s %s\n", st.Name, st.State)
	return 0
}

// runSimple handles stop and restart, which take a name and nothing else.
func runSimple(verb string, args []string) int {
	flags := flag.NewFlagSet(verb, flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return usageErr(verb + " <name>")
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	name, err := c.Resolve(flags.Arg(0))
	if err != nil {
		return fail(err)
	}

	var st manager.Status
	if verb == "stop" {
		st, err = c.Stop(name)
	} else {
		st, err = c.Restart(name)
	}
	if err != nil {
		return fail(err)
	}
	fmt.Printf("%s %s\n", st.Name, st.State)
	return 0
}

func runLogs(args []string) int {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	n := flags.Int("n", 200, "lines of scrollback")
	follow := flags.Bool("f", false, "follow output")
	if err := flags.Parse(hoistFlags(args, map[string]bool{"-n": true})); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return usageErr("logs <name> [-n N] [-f]")
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	name, err := c.Resolve(flags.Arg(0))
	if err != nil {
		return fail(err)
	}
	lines, err := c.Logs(name, *n)
	if err != nil {
		return fail(err)
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	if !*follow {
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := c.Stream(ctx, name, os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
		return fail(err)
	}
	return 0
}

func runReload(args []string) int {
	flags := flag.NewFlagSet("reload", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	list, err := c.Reload()
	if err != nil {
		return fail(err)
	}
	for _, s := range list {
		suffix := ""
		if s.SpecDirty {
			suffix = " (spec changed, applies on next start)"
		}
		fmt.Printf("%s %s%s\n", s.Name, s.State, suffix)
	}
	return 0
}

// runRun starts a command with its dependencies and follows its output.
// SIGINT stops only what this invocation started.
func runRun(args []string) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return usageErr("run <name>")
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	name, err := c.Resolve(flags.Arg(0))
	if err != nil {
		return fail(err)
	}

	st, err := c.Get(name)
	if err != nil {
		return fail(err)
	}
	weStarted := false
	if st.State != manager.Running && st.State != manager.Starting {
		if _, err := c.Start(name, true); err != nil {
			return fail(err)
		}
		weStarted = true
	} else {
		fmt.Fprintf(os.Stderr, "lazycomd: %s already running, attaching\n", name)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	streamErr := c.Stream(ctx, name, os.Stdout)

	if weStarted {
		if _, err := c.Stop(name); err != nil {
			return fail(err)
		}
	}
	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		return fail(streamErr)
	}
	return 0
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/lazycomd/ -v`
Expected: PASS, every CLI and serve test.

- [ ] **Step 6: Build and smoke-test by hand**

```bash
go build -o /tmp/lazycomd ./cmd/lazycomd
mkdir -p /tmp/lzc-config
cat > /tmp/lzc-config/config.yaml <<'CFG'
commands:
  tick:
    cmd: ["sh", "-c", "while true; do date; sleep 1; done"]
    cwd: /tmp
    restart: on-failure
CFG
LAZYCOMD_CONFIG=/tmp/lzc-config/config.yaml XDG_STATE_HOME=/tmp/lzc-state /tmp/lazycomd serve &
sleep 1
XDG_STATE_HOME=/tmp/lzc-state /tmp/lazycomd start tick
XDG_STATE_HOME=/tmp/lzc-state /tmp/lazycomd ls
XDG_STATE_HOME=/tmp/lzc-state /tmp/lazycomd logs tick -n 3
curl --unix-socket /tmp/lzc-state/lazycomd/lazycomd.sock http://unix/v1/commands
XDG_STATE_HOME=/tmp/lzc-state /tmp/lazycomd stop tick
kill %1
```

Expected: `ls` shows `tick running` with a pid, `logs` shows timestamps, the
`curl` call returns the same JSON, and the daemon exits cleanly on `kill`.

- [ ] **Step 7: Commit**

```bash
git add cmd/lazycomd/
git commit -m "feat(cli): ls, start, stop, restart, logs, reload and run"
```

---

### Task 19: Unit files, README and full verification

**Files:**
- Create: `contrib/com.tphuc.lazycomd.plist`, `contrib/lazycomd.service`, `README.md`
- Modify: none

**Interfaces:**
- Consumes: everything above. Produces no code.

- [ ] **Step 1: Write the launchd plist**

Create `contrib/com.tphuc.lazycomd.plist`. The user installs it; the binary
never does.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.tphuc.lazycomd</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/lazycomd</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/lazycomd.out.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/lazycomd.err.log</string>
</dict>
</plist>
```

- [ ] **Step 2: Write the systemd unit**

Create `contrib/lazycomd.service`:

```ini
[Unit]
Description=lazycomd - run and supervise long dev commands
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/lazycomd serve
ExecReload=%h/.local/bin/lazycomd reload
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
```

- [ ] **Step 3: Write the README**

Create `README.md`. It must document the full config schema, every route, the
environment variables, and the known limitations. Use this structure and fill
every section from the implemented behavior:

````markdown
# lazycomd

One user-wide daemon that runs, supervises and exposes your long dev
commands — proxies, tunnels, local service stacks — over an HTTP API.

## Install

```bash
go build -o ~/.local/bin/lazycomd ./cmd/lazycomd
```

Run the daemon in the foreground, or install one of the unit files in
`contrib/`:

```bash
lazycomd serve
```

## Configuration

Global config: `~/.config/lazycomd/config.yaml` (`$XDG_CONFIG_HOME` and
`LAZYCOMD_CONFIG` override it).

```yaml
listen: ""                 # empty = unix socket only; ":7777" = loopback TCP
token_file: ""             # required when listen is set; must be mode 0600
projects:
  - ~/coding/scraper       # each holds a lazycomd.yaml
commands:
  proxy:
    cmd: ["cloudflared", "tunnel", "--url", "localhost:3000"]
    cwd: ~                 # default: your home dir, or the project dir
    env: {LOG: debug}
    shell: false           # true wraps cmd in sh -c
    restart: on-failure    # no | on-failure | always
    autostart: false       # start when the daemon starts
    log: true              # also tee output to ~/.local/state/lazycomd/logs/
    size: 262144           # ring buffer bytes
    depends_on: []         # started first, in order
```

A project's `lazycomd.yaml` holds a `commands:` block only. Its commands are
namespaced by the project directory's basename: `scraper:api`. Two project
directories with the same basename is a config error.

`~` and `$VAR` expand when a command starts, not when the config loads. Write
`$$` for a literal dollar sign. Every field documented above is the complete
set; an unknown field is a config error.

## Commands

| Command | What it does |
|---|---|
| `lazycomd serve` | Run the daemon in the foreground |
| `lazycomd ls` | Name, state, pid, uptime, restarts |
| `lazycomd start <name> [-d]` | Start; `-d` starts dependencies first |
| `lazycomd stop <name>` | SIGTERM the process group, SIGKILL after 10s |
| `lazycomd restart <name>` | Stop then start |
| `lazycomd logs <name> [-n N] [-f]` | Show or follow output |
| `lazycomd reload` | Re-read the config and apply the diff |
| `lazycomd run <name>` | Start with dependencies, follow output, stop on Ctrl-C |

A bare name matches a global command, then a unique `project:name`. An
ambiguous name lists the candidates and exits 1.

Exit codes: 0 success, 1 daemon or client error, 2 usage error.

## API

Always on `~/.local/state/lazycomd/lazycomd.sock` (mode 0600 — file
permissions are the authentication). Set `listen:` plus `token_file:` for a
TCP listener that requires `Authorization: Bearer <token>`.

| Method and path | Body / query | Returns |
|---|---|---|
| `GET /v1/healthz` | | `{"status":"ok"}` |
| `GET /v1/commands` | | array of status objects |
| `GET /v1/commands/{name}` | | one status object |
| `POST /v1/commands/{name}/start` | `{"with_deps":true}` optional | status |
| `POST /v1/commands/{name}/stop` | | status |
| `POST /v1/commands/{name}/restart` | | status |
| `GET /v1/commands/{name}/logs` | `?tail=200` (max 10000) | array of lines |
| `GET /v1/commands/{name}/logs/stream` | | SSE, `data: <line>` |
| `POST /v1/reload` | | array of status objects |

A status object: `name`, `state` (`stopped`, `starting`, `running`,
`stopping`, `failed`), `pid`, `uptime_sec`, `exit_code`, `restarts`,
`spec_dirty`, `depends_on`.

Errors are `{"error":"..."}` with 400 (malformed request or broken config),
401 (bad token), 404 (unknown command), 409 (illegal in the current state) or
500.

```bash
curl --unix-socket ~/.local/state/lazycomd/lazycomd.sock http://unix/v1/commands
```

## Behavior worth knowing

- **Reload never restarts anything.** A changed spec on a running command is
  staged and reported as `spec_dirty`; it takes effect on that command's next
  start.
- **Stopping is not recursive.** Stopping a dependency leaves its dependents
  running.
- **Dependencies are ordered, not probed.** A dependency counts as ready 200ms
  after it spawns. There are no port or HTTP readiness checks.
- **No orphan adoption.** Commands die with the daemon. Use `autostart: true`
  to bring a stack back up.
- **Logs are memory-first.** The ring buffer holds the last 256 KB per
  command. `log: true` also appends to disk, rotating once at 10 MB.
- **Commands get pipes, not a TTY.** Anything that needs a terminal will
  behave as though piped.

## Environment

| Variable | Meaning |
|---|---|
| `LAZYCOMD_ADDR` | `unix:///path/to.sock` or `http://host:port` |
| `LAZYCOMD_TOKEN` | Bearer token for a TCP address |
| `LAZYCOMD_CONFIG` | Override the config path |
| `XDG_CONFIG_HOME`, `XDG_STATE_HOME` | Standard overrides |

## Not in this version

A TUI, a ports and container dashboard, readiness probes, log search, and PTY
allocation. Those are later specs.
````

- [ ] **Step 4: Verify the whole repository**

Run each and fix anything it reports:

```bash
gofmt -l .
go vet ./...
go test -race ./...
go build -o /tmp/lazycomd ./cmd/lazycomd
```

Expected: `gofmt -l .` prints nothing, `go vet` is silent, every test passes
with no data races, and the build succeeds.

- [ ] **Step 5: Check the README against the code**

Open `README.md` beside `internal/api/server.go` and `cmd/lazycomd/cli.go`.
Every route in the table must exist in `routes()`, and every subcommand in the
table must exist in `dispatch()`. Fix the README where they disagree.

- [ ] **Step 6: Commit**

```bash
git add README.md contrib/
git commit -m "docs: README and launchd/systemd unit files"
```

---

## Self-Review

Checked after writing the plan, against
`docs/superpowers/specs/2026-09-11-lazycomd-daemon-design.md`:

| Spec section | Covered by |
|---|---|
| Repository layout | File Structure table; Tasks 1-19 create exactly those files |
| Configuration schema and defaults | Tasks 2, 3, 4 |
| Namespacing, basename collision, global-only keys | Task 3 |
| `cmd` as an array, `shell: true`, start-time expansion | Task 4 |
| Process state machine, `stopping` vs restart policy | Tasks 8, 9 |
| Process groups, child-tree kill | Task 8 |
| Reaper, backoff, uptime reset | Task 9 |
| `depends_on`, shallow readiness, no stop cascade | Task 10 |
| No orphan adoption | Task 8 (no PID files anywhere), Task 12 |
| Ring buffer, line-aware tail, subscriber drop | Tasks 5, 6 |
| Disk tee, 10 MB rotation, `:` to `__` naming | Task 7 |
| Every route, JSON shape, status codes, `recover()` | Tasks 13, 14, 15 |
| SSE rather than websockets | Task 14 |
| Reload diff and `spec_dirty` | Task 11, endpoint in Task 15 |
| Unix socket, mode 0600, TCP token, 0600 token file, loopback default | Tasks 15, 17 |
| CLI surface, name resolution, exit codes, no autospawn | Tasks 16, 18 |
| Startup sequence, drain, second-signal kill, single instance | Tasks 12, 17 |
| Test table from the spec | Tasks 2-18; every package in the spec's table has its listed cases |
| `contrib/` unit files and README | Task 19 |

Gaps found and closed while reviewing:
- The spec never said how a project command's `depends_on` resolves. Task 3
  decides it explicitly (own project first, then global) and tests both.
- The spec never said what a project command's default `cwd` is. Task 3 sets
  it to the project directory.
- `os.ExpandEnv` eats shell constructs like `$!`. Task 4 supports `$$` as an
  escape and the README documents it.
- `flag` stops at the first positional argument, which would break
  `lazycomd logs api -f`. Task 18 adds `hoistFlags` with a test.
- `runServe` shadowing the `io/fs` import with a local `fs` flag set is called
  out in Task 17 with two ways to fix it.

Type consistency: `manager.Status` is the single wire type shared by
`internal/api` (Tasks 13-15), `internal/client` (Task 16) and the CLI
(Task 18). `config.Command` and `config.Resolved` are produced in Tasks 2 and
4 and consumed unchanged in Task 8. `logbuf.Buffer` gains methods across
Tasks 5-7 without changing a signature.
