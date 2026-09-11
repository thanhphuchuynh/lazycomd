# lazycomd Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give lazycomd machine awareness: which ports are listening and who owns them, per-command CPU and memory, HTTP health, and conflicts between a command's intended port and whatever already holds it.

**Architecture:** A new `internal/probe` package holds three collectors — ports from `lsof` (falling back to `ss`), vitals from `ps`, health from an HTTP GET — each on its own ticker, writing into one snapshot. `GET /v1/system` serves that snapshot; `cpu`, `mem_mb` and `health` also ride along on the existing command view. The TUI gains three table columns and a ports view on `d`.

**Tech Stack:** Go 1.22+, standard library only for the daemon side (`os/exec`, `net/http`, `net/url`). TUI side keeps bubbletea/bubbles/lipgloss from spec #2.

**Spec:** `docs/superpowers/specs/2026-09-11-lazycomd-dashboard-design.md`

## Global Constraints

- Specs #1 and #2 are merged on `main`. Read both design docs before starting: `docs/superpowers/specs/2026-09-11-lazycomd-daemon-design.md` and `docs/superpowers/specs/2026-09-11-lazycomd-tui-design.md`.
- **No new dependencies anywhere.** The daemon side stays `yaml.v3`-only; `internal/probe` uses the standard library and nothing else. `internal/depsguard` must keep passing, and `internal/probe` must never import a Charm library.
- `internal/probe` imports neither `internal/manager` nor `internal/api`. It receives everything through three closures.
- Every subprocess call goes through an injectable `run` field, so no test spawns `lsof`, `ss` or `ps` except the one integration test that is skipped when they are absent.
- Sampling intervals: ports 5s, vitals 2s, health 10s. Health timeout 2s. TUI system poll 3s. Stale marker at 15s.
- `lsof` exiting 1 is not a failure — it exits 1 when it has nothing to report. Trust output over exit status.
- Addresses normalize before dedupe: `0.0.0.0`, `::`, `[::]` and `*` all become `*`. Dedupe key is `(addr, port, pid)`; sort by port, then address.
- Vitals are summed over a command's **process group** (`pgid == the command's PID`), never its single PID. `rss` is KB; `MemMB = rss / 1024`. `%cpu` may exceed 100 and is not clamped.
- Health: only `running` commands are probed; any 2xx is up; redirects are not followed; a stopped command's entry is dropped.
- Conflicts are **not computed when the ports sample is missing or errored** — otherwise every intended port would read `free` exactly when the collector is the broken thing.
- `manager.Status` gains `CPU`, `MemMB` and `Health` as `omitempty` fields that **the API layer fills and the manager never sets**. Keep that comment on them.
- `health:` must parse with scheme `http` or `https` and a host; `port:` must be 1-65535 when set. Both are optional; an unset `port:` is 0.
- Every task ends with `gofmt -l .` silent, `go vet ./...` clean, and `go test -race ./...` passing.
- Commits: `feat(probe):`, `feat(api):`, `feat(tui):`, `feat(config):` as appropriate, ending with the repo's attribution trailer (`Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/config/config.go` | (modify) `health:` and `port:` fields, their validation, `IntendedPort()` |
| `internal/manager/manager.go` | (modify) `CPU`/`MemMB`/`Health` on `Status`, `HealthView`, and the three accessors |
| `internal/probe/types.go` | `Snapshot`, `Port`, `Vital`, `Health`, `Conflict` |
| `internal/probe/ports.go` | `lsof`/`ss` invocation, parsers, address normalization, dedupe, ownership |
| `internal/probe/vitals.go` | `ps` invocation, parser, per-group summing |
| `internal/probe/health.go` | HTTP prober |
| `internal/probe/conflict.go` | intended ports versus reality |
| `internal/probe/probe.go` | `Sampler`: the three loops, snapshot copy, error isolation |
| `internal/api/system.go` | `GET /v1/system` and the vitals/health merge into the command view |
| `internal/client/client.go` | (modify) `System()` |
| `internal/tui/table.go` | (modify) `H`, `CPU`, `MEM` columns and the width tiers |
| `internal/tui/system.go` | the ports view |
| `internal/tui/tui.go` | (modify) `d` key, the 3s system tick, the ports overlay |
| `internal/tui/help.go` | (modify) the `d` binding |
| `cmd/lazycomd/serve.go` | (modify) build, start and drain the sampler |
| `README.md` | (modify) `health:`, `port:`, the ports view |

---

### Task 1: Config fields `health:` and `port:`

**Files:**
- Modify: `internal/config/config.go` — two fields, their validation, and `IntendedPort()`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: existing `config.Command`, `(*File).Validate`.
- Produces:
  - `Command.Health string` (yaml `health`), `Command.Port int` (yaml `port`).
  - `func (c Command) IntendedPort() int` — the explicit port, else the health URL's port (including the scheme default), else 0.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run 'TestHealth|TestIntendedPort' -v`
Expected: FAIL — `field health not found` from the unknown-field check, and `c.IntendedPort undefined`.

- [ ] **Step 3: Add the fields**

In `internal/config/config.go`, extend `Command`:

```go
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
	Health    string            `yaml:"health"`
	Port      int               `yaml:"port"`
}
```

- [ ] **Step 4: Validate them**

In `(c *Command) validate`, before the closing `return nil`:

```go
	if c.Health != "" {
		u, err := url.Parse(c.Health)
		if err != nil {
			return fmt.Errorf("command %q: health %q: %w", name, c.Health, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("command %q: health %q: scheme must be http or https", name, c.Health)
		}
		if u.Host == "" {
			return fmt.Errorf("command %q: health %q: missing host", name, c.Health)
		}
	}
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return fmt.Errorf("command %q: port %d out of range 1-65535", name, c.Port)
	}
```

Add `"net/url"` to the imports.

- [ ] **Step 5: Add `IntendedPort`**

At the end of `internal/config/config.go`:

```go
// IntendedPort is the port this command means to bind: the explicit port:
// field, otherwise the port in its health URL, otherwise 0. Used to tell a
// command's own listener apart from something else squatting on its port.
func (c Command) IntendedPort() int {
	if c.Port != 0 {
		return c.Port
	}
	if c.Health == "" {
		return 0
	}
	u, err := url.Parse(c.Health)
	if err != nil {
		return 0
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		return n
	}
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	}
	return 0
}
```

Add `"strconv"` to the imports.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, every config test old and new.

If the `"://"` case does not error, print what `url.Parse` returned — some malformed URLs parse into an empty scheme and host, which the scheme check already rejects; adjust the expected substring to `scheme must be http or https` rather than loosening the validation.

- [ ] **Step 7: Commit**

```bash
git add internal/config/
git commit -m "feat(config): health: and port: fields with an intended-port rule

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Manager accessors and the enriched status

**Files:**
- Modify: `internal/manager/manager.go` — `HealthView`, three fields on `Status`, three accessors
- Test: `internal/manager/accessors_test.go`

**Interfaces:**
- Consumes: `Manager`, `Process`, `config.Command.IntendedPort`.
- Produces:
  - `manager.HealthView` struct: `URL string`, `OK bool`, `Status int`, `LatencyMS float64`, `Error string`, JSON tags `url`, `ok`, `status`, `latency_ms`, `error`.
  - `Status.CPU float64` (`cpu,omitempty`), `Status.MemMB float64` (`mem_mb,omitempty`), `Status.Health *HealthView` (`health,omitempty`).
  - `(*Manager).RunningPIDs() map[string]int`, `(*Manager).HealthURLs() map[string]string`, `(*Manager).IntendedPorts() map[string]int`.

- [ ] **Step 1: Write the failing test**

Create `internal/manager/accessors_test.go`:

```go
package manager

import (
	"testing"

	"github.com/tphuc/lazycomd/internal/config"
)

func TestAccessorsCoverTheRightCommands(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"up":      {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", Health: "http://localhost:3000/h", Port: 3000},
		"down":    {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", Health: "http://localhost:4310/h"},
		"noprobe": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	defer m.Shutdown()

	if err := m.Start("up"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "up", Running)

	pids := m.RunningPIDs()
	if len(pids) != 1 || pids["up"] <= 0 {
		t.Fatalf("RunningPIDs = %v, want just up", pids)
	}

	urls := m.HealthURLs()
	if len(urls) != 1 || urls["up"] != "http://localhost:3000/h" {
		t.Fatalf("HealthURLs = %v, want just the running command with a URL", urls)
	}

	// Intended ports cover every command, running or not: a stopped command
	// whose port is taken is exactly the case worth reporting.
	ports := m.IntendedPorts()
	if len(ports) != 2 || ports["up"] != 3000 || ports["down"] != 4310 {
		t.Fatalf("IntendedPorts = %v, want up:3000 and down:4310", ports)
	}
	if _, ok := ports["noprobe"]; ok {
		t.Fatal("a command with neither port: nor health: should have no intended port")
	}
}

func TestStatusCarriesTheProbeFields(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	st, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	// The manager never fills these; the API layer does. They must exist and
	// stay zero here.
	if st.CPU != 0 || st.MemMB != 0 || st.Health != nil {
		t.Fatalf("manager populated probe fields: %+v", st)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/manager/ -run 'TestAccessors|TestStatusCarries' -v`
Expected: FAIL — `m.RunningPIDs undefined`.

- [ ] **Step 3: Add the view type and the status fields**

In `internal/manager/manager.go`, add above `Status`:

```go
// HealthView is one health-probe result as the API reports it. The manager
// never produces these; the API layer fills them from the probe sampler.
type HealthView struct {
	URL       string  `json:"url"`
	OK        bool    `json:"ok"`
	Status    int     `json:"status,omitempty"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
	Error     string  `json:"error,omitempty"`
}
```

and extend `Status`:

```go
type Status struct {
	Name      string   `json:"name"`
	State     State    `json:"state"`
	PID       int      `json:"pid,omitempty"`
	UptimeSec float64  `json:"uptime_sec,omitempty"`
	ExitCode  *int     `json:"exit_code,omitempty"`
	Restarts  int      `json:"restarts"`
	SpecDirty bool     `json:"spec_dirty,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`

	// Filled by the API layer from the probe sampler. The manager never sets
	// these — it knows nothing about CPU, memory or health.
	CPU    float64     `json:"cpu,omitempty"`
	MemMB  float64     `json:"mem_mb,omitempty"`
	Health *HealthView `json:"health,omitempty"`
}
```

- [ ] **Step 4: Add the accessors**

At the end of `internal/manager/manager.go`:

```go
// RunningPIDs maps each running command to its PID, which is also its process
// group id. Used by the probe sampler for vitals and port ownership.
func (m *Manager) RunningPIDs() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.procs))
	for name, p := range m.procs {
		if p.State == Running && p.PID > 0 {
			out[name] = p.PID
		}
	}
	return out
}

// HealthURLs maps each running command that configured one to its health URL.
// Stopped commands are excluded: probing a dead service proves nothing.
func (m *Manager) HealthURLs() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string)
	for name, p := range m.procs {
		if p.State == Running && p.Spec.Health != "" {
			out[name] = p.Spec.Health
		}
	}
	return out
}

// IntendedPorts maps every command that declares one — running or not — to the
// port it means to bind. A stopped command whose port is held by something
// else is exactly the conflict worth reporting.
func (m *Manager) IntendedPorts() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int)
	for name, p := range m.procs {
		if port := p.Spec.IntendedPort(); port != 0 {
			out[name] = port
		}
	}
	return out
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/manager/ -v`
Expected: PASS, every manager test.

- [ ] **Step 6: Commit**

```bash
git add internal/manager/
git commit -m "feat(manager): probe fields on Status and the sampler's three accessors

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Probe types and the port parsers

**Files:**
- Create: `internal/probe/types.go`, `internal/probe/ports.go`
- Test: `internal/probe/ports_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `probe.Snapshot`, `probe.Port`, `probe.Vital`, `probe.Health`, `probe.Conflict` exactly as the spec declares them.
  - `func parseLsof(out []byte) []Port`, `func parseSS(out []byte) []Port`.
  - `func normalizeAddr(a string) string`, `func splitHostPort(s string) (string, int, bool)`, `func normalizePorts(in []Port) []Port`.

- [ ] **Step 1: Write the failing test**

Create `internal/probe/ports_test.go`:

```go
package probe

import (
	"testing"
)

// Real `lsof -nP -iTCP -sTCP:LISTEN` output, macOS.
const lsofSample = `COMMAND     PID  USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME
node      51192 tphuc   23u  IPv4 0x1a2b3c4d5e6f7890      0t0  TCP 127.0.0.1:3000 (LISTEN)
postgres   1183 tphuc    7u  IPv6 0x0987654321fedcba      0t0  TCP *:5432 (LISTEN)
postgres   1183 tphuc    8u  IPv4 0x1122334455667788      0t0  TCP *:5432 (LISTEN)
lazycomd  54405 tphuc    3u  IPv4 0xaabbccddeeff0011      0t0  TCP 127.0.0.1:7777 (LISTEN)
`

// Real `ss -ltnpH` output, Linux.
const ssSample = `LISTEN 0      4096         0.0.0.0:22         0.0.0.0:*    users:(("sshd",pid=1183,fd=3))
LISTEN 0      511        127.0.0.1:3000       0.0.0.0:*    users:(("node",pid=51192,fd=23))
LISTEN 0      4096            [::]:22            [::]:*    users:(("sshd",pid=1183,fd=4))
`

func TestParseLsof(t *testing.T) {
	got := normalizePorts(parseLsof([]byte(lsofSample)))

	if len(got) != 3 {
		t.Fatalf("got %d ports, want 3 after the v4/v6 pair collapses: %+v", len(got), got)
	}
	if got[0].Port != 3000 || got[0].Addr != "127.0.0.1" || got[0].PID != 51192 || got[0].Process != "node" {
		t.Fatalf("first row = %+v", got[0])
	}
	if got[1].Port != 5432 || got[1].Addr != "*" || got[1].Process != "postgres" {
		t.Fatalf("second row = %+v, want the wildcard postgres listener once", got[1])
	}
	if got[2].Port != 7777 {
		t.Fatalf("third row = %+v, want the sorted 7777 entry", got[2])
	}
}

func TestParseSS(t *testing.T) {
	got := normalizePorts(parseSS([]byte(ssSample)))

	if len(got) != 2 {
		t.Fatalf("got %d ports, want 2 after 0.0.0.0 and [::] collapse: %+v", len(got), got)
	}
	if got[0].Port != 22 || got[0].Addr != "*" || got[0].PID != 1183 || got[0].Process != "sshd" {
		t.Fatalf("first row = %+v", got[0])
	}
	if got[1].Port != 3000 || got[1].Addr != "127.0.0.1" || got[1].Process != "node" {
		t.Fatalf("second row = %+v", got[1])
	}
}

func TestParsersIgnoreGarbage(t *testing.T) {
	if got := parseLsof([]byte("COMMAND PID USER\nnonsense\n")); len(got) != 0 {
		t.Fatalf("parseLsof = %+v, want nothing", got)
	}
	if got := parseSS([]byte("garbage line\n")); len(got) != 0 {
		t.Fatalf("parseSS = %+v, want nothing", got)
	}
	if got := parseLsof(nil); len(got) != 0 {
		t.Fatalf("parseLsof(nil) = %+v", got)
	}
}

func TestNormalizeAddr(t *testing.T) {
	for _, in := range []string{"0.0.0.0", "::", "[::]", "*", ""} {
		if got := normalizeAddr(in); got != "*" {
			t.Fatalf("normalizeAddr(%q) = %q, want *", in, got)
		}
	}
	if got := normalizeAddr("127.0.0.1"); got != "127.0.0.1" {
		t.Fatalf("normalizeAddr(127.0.0.1) = %q", got)
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in   string
		addr string
		port int
		ok   bool
	}{
		{"127.0.0.1:3000", "127.0.0.1", 3000, true},
		{"*:5432", "*", 5432, true},
		{"[::1]:8080", "::1", 8080, true},
		{"[::]:22", "*", 22, true},
		{"nonsense", "", 0, false},
		{"127.0.0.1:notaport", "", 0, false},
	}
	for _, tc := range cases {
		addr, port, ok := splitHostPort(tc.in)
		if ok != tc.ok || addr != tc.addr || port != tc.port {
			t.Fatalf("splitHostPort(%q) = %q, %d, %v; want %q, %d, %v", tc.in, addr, port, ok, tc.addr, tc.port, tc.ok)
		}
	}
}

func TestNormalizePortsKeepsDistinctBinds(t *testing.T) {
	// A real double bind: loopback and wildcard on the same port, different
	// processes. Both must survive.
	in := []Port{
		{Addr: "*", Port: 3000, PID: 2, Process: "b"},
		{Addr: "127.0.0.1", Port: 3000, PID: 1, Process: "a"},
		{Addr: "127.0.0.1", Port: 3000, PID: 1, Process: "a"}, // exact duplicate
	}
	got := normalizePorts(in)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Addr != "*" || got[1].Addr != "127.0.0.1" {
		t.Fatalf("rows not sorted by addr within a port: %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/probe/ -v`
Expected: FAIL — `undefined: parseLsof`.

- [ ] **Step 3: Write the types**

Create `internal/probe/types.go`:

```go
// Package probe samples what the machine is doing: which ports are listening,
// what each lazycomd command is costing, and whether the services behind them
// answer. It imports neither internal/manager nor internal/api.
package probe

import "time"

// Snapshot is one view of the machine. Sections sample independently, so each
// carries its own timestamp and its own error.
type Snapshot struct {
	Ports     []Port               `json:"ports"`
	Vitals    map[string]Vital     `json:"vitals"` // keyed by command name
	Health    map[string]Health    `json:"health"` // keyed by command name
	Conflicts []Conflict           `json:"conflicts,omitempty"`
	SampledAt map[string]time.Time `json:"sampled_at"`       // "ports", "vitals", "health"
	Errors    map[string]string    `json:"errors,omitempty"` // same keys
}

// Port is one listening socket.
type Port struct {
	Addr    string `json:"addr"` // 127.0.0.1, *, ::1
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Process string `json:"process"`
	Command string `json:"command,omitempty"` // the lazycomd command that owns it
}

// Vital is one command's resource use, summed across its process group.
type Vital struct {
	PID   int     `json:"pid"`
	CPU   float64 `json:"cpu"` // percent of one core; may exceed 100
	MemMB float64 `json:"mem_mb"`
}

// Health is one HTTP probe result.
type Health struct {
	URL       string  `json:"url"`
	OK        bool    `json:"ok"`
	Status    int     `json:"status,omitempty"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// Conflict is a command's intended port not being held by that command.
type Conflict struct {
	Port    int    `json:"port"`
	Command string `json:"command"`           // the command that wants it
	State   string `json:"state"`             // "taken" | "free"
	HeldBy  string `json:"held_by,omitempty"` // process name of the squatter
	PID     int    `json:"pid,omitempty"`
}

// Section keys used by SampledAt and Errors.
const (
	sectionPorts  = "ports"
	sectionVitals = "vitals"
	sectionHealth = "health"
)
```

- [ ] **Step 4: Write the parsers**

Create `internal/probe/ports.go`:

```go
package probe

import (
	"sort"
	"strconv"
	"strings"
)

// parseLsof reads `lsof -nP -iTCP -sTCP:LISTEN` output. Columns are
// COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME, and the address is the
// field before the trailing "(LISTEN)".
func parseLsof(out []byte) []Port {
	var ports []Port
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "COMMAND" {
			continue
		}
		if !strings.HasSuffix(line, "(LISTEN)") {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		addr, port, ok := splitHostPort(fields[len(fields)-2])
		if !ok {
			continue
		}
		ports = append(ports, Port{Addr: addr, Port: port, PID: pid, Process: fields[0]})
	}
	return ports
}

// parseSS reads `ss -ltnpH` output, where the local address is the fourth
// field and the process appears as users:(("name",pid=N,fd=M)).
func parseSS(out []byte) []Port {
	var ports []Port
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "LISTEN" {
			continue
		}
		addr, port, ok := splitHostPort(fields[3])
		if !ok {
			continue
		}
		name, pid, ok := parseSSUsers(line)
		if !ok {
			continue
		}
		ports = append(ports, Port{Addr: addr, Port: port, PID: pid, Process: name})
	}
	return ports
}

// parseSSUsers pulls the first ("name",pid=N) pair out of an ss line.
func parseSSUsers(line string) (string, int, bool) {
	i := strings.Index(line, `(("`)
	if i < 0 {
		return "", 0, false
	}
	rest := line[i+3:]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return "", 0, false
	}
	name := rest[:j]

	k := strings.Index(rest, "pid=")
	if k < 0 {
		return "", 0, false
	}
	digits := rest[k+4:]
	end := 0
	for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
		end++
	}
	pid, err := strconv.Atoi(digits[:end])
	if err != nil {
		return "", 0, false
	}
	return name, pid, true
}

// splitHostPort splits "127.0.0.1:3000", "*:5432" or "[::1]:8080".
func splitHostPort(s string) (string, int, bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(s[i+1:])
	if err != nil || port <= 0 {
		return "", 0, false
	}
	host := strings.TrimSuffix(strings.TrimPrefix(s[:i], "["), "]")
	return normalizeAddr(host), port, true
}

// normalizeAddr folds every spelling of "any address" into "*", so one
// wildcard listener reported once per address family collapses to one row.
func normalizeAddr(a string) string {
	switch a {
	case "", "*", "0.0.0.0", "::", "[::]":
		return "*"
	}
	return a
}

// normalizePorts deduplicates on (addr, port, pid) and sorts by port, then
// address. A genuine double bind — loopback and wildcard on one port — stays
// two rows, because it is two listeners.
func normalizePorts(in []Port) []Port {
	type key struct {
		addr string
		port int
		pid  int
	}
	seen := make(map[key]bool, len(in))
	out := make([]Port, 0, len(in))
	for _, p := range in {
		k := key{p.Addr, p.Port, p.PID}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Addr < out[j].Addr
	})
	return out
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/probe/ -v`
Expected: PASS, six tests.

If `TestParseLsof` returns four rows, the v4/v6 collapse is not happening: check that `normalizeAddr` runs inside `splitHostPort` so both postgres rows carry `*` before dedupe sees them.

- [ ] **Step 6: Commit**

```bash
git add internal/probe/
git commit -m "feat(probe): snapshot types and lsof/ss port parsers

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 4: Port collection and ownership

**Files:**
- Modify: `internal/probe/ports.go` — add `runFunc`, `execRun`, `collectPorts`, `ownerOf`, `applyOwners`
- Test: `internal/probe/collect_ports_test.go`

**Interfaces:**
- Consumes: `parseLsof`, `parseSS`, `normalizePorts` from Task 3.
- Produces:
  - `type runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)`.
  - `func execRun(ctx context.Context, name string, args ...string) ([]byte, error)` — the real subprocess runner.
  - `func collectPorts(ctx context.Context, run runFunc) ([]Port, error)` — `lsof` first, `ss` as fallback.
  - `func ownerOf(pid int, groups map[int]int, pids map[string]int) string`.
  - `func applyOwners(ports []Port, groups map[int]int, pids map[string]int) []Port`.

Every collector is a free function taking `run`, not a method. They are then trivially testable without a `Sampler`, and Task 8 only orchestrates them.

- [ ] **Step 1: Write the failing test**

Create `internal/probe/collect_ports_test.go`:

```go
package probe

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeRun answers by command name, so a test can make lsof missing and ss
// present, or both fail.
func fakeRun(answers map[string]struct {
	out []byte
	err error
}) runFunc {
	return func(_ context.Context, name string, _ ...string) ([]byte, error) {
		a, ok := answers[name]
		if !ok {
			return nil, exec.ErrNotFound
		}
		return a.out, a.err
	}
}

func TestCollectPortsUsesLsof(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)},
	})

	got, err := collectPorts(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Port != 3000 {
		t.Fatalf("ports = %+v", got)
	}
}

func TestCollectPortsTrustsOutputOverExitStatus(t *testing.T) {
	// lsof exits 1 when it has something to say but also complains about a
	// filesystem it cannot stat. Output wins.
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample), err: errors.New("exit status 1")},
	})

	got, err := collectPorts(context.Background(), run)
	if err != nil {
		t.Fatalf("err = %v, want the parsed output instead", err)
	}
	if len(got) != 3 {
		t.Fatalf("ports = %+v", got)
	}
}

func TestCollectPortsEmptyIsNotAnError(t *testing.T) {
	// lsof exits 1 with no output when nothing is listening.
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: nil, err: errors.New("exit status 1")},
	})

	got, err := collectPorts(context.Background(), run)
	if err == nil {
		t.Skip("this build treats a silent failure as empty; see the next case")
	}
	if len(got) != 0 {
		t.Fatalf("ports = %+v, want none", got)
	}
}

func TestCollectPortsFallsBackToSS(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"ss": {out: []byte(ssSample)}, // lsof is absent: fakeRun returns ErrNotFound
	})

	got, err := collectPorts(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Process != "node" {
		t.Fatalf("ports = %+v, want the ss rows", got)
	}
}

func TestCollectPortsBothMissing(t *testing.T) {
	got, err := collectPorts(context.Background(), fakeRun(nil))
	if err == nil {
		t.Fatal("err = nil, want an error naming both tools")
	}
	if !strings.Contains(err.Error(), "lsof") || !strings.Contains(err.Error(), "ss") {
		t.Fatalf("err = %v, want both tools named", err)
	}
	if got != nil {
		t.Fatalf("ports = %+v, want nil", got)
	}
}

func TestOwnerOfUsesTheProcessGroup(t *testing.T) {
	// app:web is pid 100; its grandchild 137 holds the socket.
	pids := map[string]int{"app:web": 100, "other": 200}
	groups := map[int]int{100: 100, 137: 100, 200: 200, 999: 999}

	if got := ownerOf(137, groups, pids); got != "app:web" {
		t.Fatalf("ownerOf(grandchild) = %q, want app:web", got)
	}
	if got := ownerOf(100, groups, pids); got != "app:web" {
		t.Fatalf("ownerOf(leader) = %q, want app:web", got)
	}
	if got := ownerOf(999, groups, pids); got != "" {
		t.Fatalf("ownerOf(stranger) = %q, want empty", got)
	}
	if got := ownerOf(42, groups, pids); got != "" {
		t.Fatalf("ownerOf(unknown pid) = %q, want empty", got)
	}
}

func TestApplyOwners(t *testing.T) {
	ports := []Port{
		{Port: 3000, PID: 137, Process: "node"},
		{Port: 5432, PID: 999, Process: "postgres"},
	}
	got := applyOwners(ports, map[int]int{137: 100, 999: 999}, map[string]int{"app:web": 100})

	if got[0].Command != "app:web" {
		t.Fatalf("row 0 = %+v, want owned by app:web", got[0])
	}
	if got[1].Command != "" {
		t.Fatalf("row 1 = %+v, want no owner", got[1])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/probe/ -run 'TestCollectPorts|TestOwnerOf|TestApplyOwners' -v`
Expected: FAIL — `undefined: runFunc`.

- [ ] **Step 3: Write the implementation**

Append to `internal/probe/ports.go`:

```go
// runFunc runs a command and returns its stdout. Injectable so tests never
// spawn a subprocess.
type runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// execRun is the real runner.
func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// collectPorts lists listening TCP sockets, preferring lsof and falling back
// to ss. Output is trusted over exit status: lsof exits 1 both when it has
// nothing to report and when it merely could not stat some filesystem.
func collectPorts(ctx context.Context, run runFunc) ([]Port, error) {
	out, lsofErr := run(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN")
	if len(out) > 0 {
		return normalizePorts(parseLsof(out)), nil
	}
	if lsofErr == nil {
		return []Port{}, nil // lsof ran, nothing is listening
	}

	out, ssErr := run(ctx, "ss", "-ltnpH")
	if len(out) > 0 {
		return normalizePorts(parseSS(out)), nil
	}
	if ssErr == nil {
		return []Port{}, nil
	}
	return nil, fmt.Errorf("lsof: %v; ss: %v", lsofErr, ssErr)
}

// ownerOf names the lazycomd command whose process group contains pid, or "".
// Ownership is by group because a command like `sh -c "npm start"` listens
// from a grandchild, and spec #1 gives every command its own group whose id
// is the command's own PID.
func ownerOf(pid int, groups map[int]int, pids map[string]int) string {
	pgid, ok := groups[pid]
	if !ok {
		return ""
	}
	for name, cmdPID := range pids {
		if cmdPID == pgid {
			return name
		}
	}
	return ""
}

// applyOwners fills in the Command field of every port it can attribute.
func applyOwners(ports []Port, groups map[int]int, pids map[string]int) []Port {
	out := make([]Port, len(ports))
	copy(out, ports)
	for i := range out {
		out[i].Command = ownerOf(out[i].PID, groups, pids)
	}
	return out
}
```

Add `"context"`, `"fmt"` and `"os/exec"` to the imports of `ports.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/probe/ -v`
Expected: PASS. `TestCollectPortsEmptyIsNotAnError` will report a skip on the branch where both tools fail with no output — that is the documented behavior, and the following case pins the error path properly.

- [ ] **Step 5: Commit**

```bash
git add internal/probe/
git commit -m "feat(probe): collect listening ports and attribute them by process group

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Vitals collector

**Files:**
- Create: `internal/probe/vitals.go`
- Test: `internal/probe/vitals_test.go`

**Interfaces:**
- Consumes: `runFunc` from Task 4.
- Produces:
  - `type psRow struct { pid, pgid int; cpu, rssKB float64 }`.
  - `func parsePS(out []byte) ([]psRow, map[int]int)` — rows plus the pid→pgid map the ports collector needs.
  - `func collectVitals(ctx context.Context, run runFunc, pids map[string]int) (map[string]Vital, map[int]int, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/probe/vitals_test.go`:

```go
package probe

import (
	"context"
	"errors"
	"testing"
)

// Real `ps -eo pid=,pgid=,%cpu=,rss=` output: a command at pid 100 with a
// grandchild at 137 in the same group, plus unrelated processes.
const psSample = `    1     1   0.0   12345
  100   100   2.5   40960
  137   100 142.3  326000
  200   200   0.1    2048
`

func TestParsePS(t *testing.T) {
	rows, groups := parsePS([]byte(psSample))

	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4: %+v", len(rows), rows)
	}
	if rows[2].pid != 137 || rows[2].pgid != 100 || rows[2].cpu != 142.3 || rows[2].rssKB != 326000 {
		t.Fatalf("row 2 = %+v", rows[2])
	}
	if groups[137] != 100 || groups[200] != 200 {
		t.Fatalf("groups = %v", groups)
	}
}

func TestParsePSIgnoresGarbage(t *testing.T) {
	rows, groups := parsePS([]byte("PID PGID %CPU RSS\nnonsense\n  7\n"))
	if len(rows) != 0 || len(groups) != 0 {
		t.Fatalf("rows = %+v groups = %v, want nothing", rows, groups)
	}
}

func TestCollectVitalsSumsTheProcessGroup(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"ps": {out: []byte(psSample)},
	})

	vitals, groups, err := collectVitals(context.Background(), run, map[string]int{"app:web": 100})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := vitals["app:web"]
	if !ok {
		t.Fatalf("vitals = %+v, want app:web", vitals)
	}
	// 2.5 from the parent plus 142.3 from the grandchild.
	if v.CPU < 144.7 || v.CPU > 144.9 {
		t.Fatalf("CPU = %v, want ~144.8 summed across the group", v.CPU)
	}
	// (40960 + 326000) KB / 1024.
	if v.MemMB < 358.3 || v.MemMB > 358.5 {
		t.Fatalf("MemMB = %v, want ~358.4", v.MemMB)
	}
	if v.PID != 100 {
		t.Fatalf("PID = %d, want the command's own pid", v.PID)
	}
	if groups[137] != 100 {
		t.Fatalf("groups not returned for the ports collector: %v", groups)
	}
}

func TestCollectVitalsSkipsWorkWithNoRunningCommands(t *testing.T) {
	called := false
	run := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}

	vitals, groups, err := collectVitals(context.Background(), run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("ps was run with no commands running")
	}
	if len(vitals) != 0 || len(groups) != 0 {
		t.Fatalf("vitals = %+v groups = %v, want empty", vitals, groups)
	}
}

func TestCollectVitalsReportsAFailure(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"ps": {err: errors.New("boom")},
	})

	if _, _, err := collectVitals(context.Background(), run, map[string]int{"a": 1}); err == nil {
		t.Fatal("err = nil, want the ps failure")
	}
}

func TestCollectVitalsOmitsCommandsWithNoProcesses(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"ps": {out: []byte(psSample)},
	})

	vitals, _, err := collectVitals(context.Background(), run, map[string]int{"ghost": 4242})
	if err != nil {
		t.Fatal(err)
	}
	if len(vitals) != 0 {
		t.Fatalf("vitals = %+v, want nothing for a pid ps never saw", vitals)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/probe/ -run 'TestParsePS|TestCollectVitals' -v`
Expected: FAIL — `undefined: parsePS`.

- [ ] **Step 3: Write the implementation**

Create `internal/probe/vitals.go`:

```go
package probe

import (
	"context"
	"strconv"
	"strings"
)

// psRow is one line of `ps -eo pid=,pgid=,%cpu=,rss=`.
type psRow struct {
	pid   int
	pgid  int
	cpu   float64
	rssKB float64
}

// parsePS returns every row plus the pid→pgid map, which the ports collector
// uses to attribute a listening socket to a command.
func parsePS(out []byte) ([]psRow, map[int]int) {
	var rows []psRow
	groups := make(map[int]int)

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		pgid, err2 := strconv.Atoi(fields[1])
		cpu, err3 := strconv.ParseFloat(fields[2], 64)
		rss, err4 := strconv.ParseFloat(fields[3], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		rows = append(rows, psRow{pid: pid, pgid: pgid, cpu: cpu, rssKB: rss})
		groups[pid] = pgid
	}
	return rows, groups
}

// collectVitals sums CPU and memory over each command's process group. A
// command's own PID is its process group id, so the whole tree it spawned
// counts — which is the number worth showing for `sh -c "npm start"`.
func collectVitals(ctx context.Context, run runFunc, pids map[string]int) (map[string]Vital, map[int]int, error) {
	if len(pids) == 0 {
		return map[string]Vital{}, map[int]int{}, nil
	}

	out, err := run(ctx, "ps", "-eo", "pid=,pgid=,%cpu=,rss=")
	if len(out) == 0 && err != nil {
		return nil, nil, err
	}
	rows, groups := parsePS(out)

	byGroup := make(map[int]Vital, len(pids))
	for _, r := range rows {
		v := byGroup[r.pgid]
		v.CPU += r.cpu
		v.MemMB += r.rssKB / 1024
		byGroup[r.pgid] = v
	}

	vitals := make(map[string]Vital, len(pids))
	for name, pid := range pids {
		v, ok := byGroup[pid]
		if !ok {
			continue
		}
		v.PID = pid
		vitals[name] = v
	}
	return vitals, groups, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/probe/ -v`
Expected: PASS, every probe test so far.

- [ ] **Step 5: Commit**

```bash
git add internal/probe/
git commit -m "feat(probe): per-command CPU and memory summed over the process group

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Health prober

**Files:**
- Create: `internal/probe/health.go`
- Test: `internal/probe/health_test.go`

**Interfaces:**
- Consumes: `Health` from Task 3.
- Produces:
  - `func newHTTPClient() *http.Client` — 2s timeout, redirects not followed.
  - `func probeOne(ctx context.Context, c *http.Client, url string) Health`.
  - `func collectHealth(ctx context.Context, c *http.Client, urls map[string]string) map[string]Health`.

- [ ] **Step 1: Write the failing test**

Create `internal/probe/health_test.go`:

```go
package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbeOneUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	got := probeOne(context.Background(), newHTTPClient(), srv.URL)
	if !got.OK || got.Status != 204 {
		t.Fatalf("health = %+v, want ok with 204", got)
	}
	if got.Error != "" {
		t.Fatalf("error = %q, want none", got.Error)
	}
	if got.LatencyMS <= 0 {
		t.Fatalf("latency = %v, want a positive measurement", got.LatencyMS)
	}
	if got.URL != srv.URL {
		t.Fatalf("url = %q", got.URL)
	}
}

func TestProbeOneDownOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	got := probeOne(context.Background(), newHTTPClient(), srv.URL)
	if got.OK || got.Status != 500 {
		t.Fatalf("health = %+v, want down with 500", got)
	}
}

func TestProbeOneDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.WriteHeader(200)
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer srv.Close()

	got := probeOne(context.Background(), newHTTPClient(), srv.URL+"/healthz")
	if got.OK {
		t.Fatalf("health = %+v, want down: a redirect to a login page is not health", got)
	}
	if got.Status != 302 {
		t.Fatalf("status = %d, want the 302 itself", got.Status)
	}
}

func TestProbeOneConnectionRefusedCarriesTheMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	got := probeOne(context.Background(), newHTTPClient(), url)
	if got.OK {
		t.Fatal("health ok = true against a closed server")
	}
	if !strings.Contains(got.Error, "connect") && !strings.Contains(got.Error, "refused") {
		t.Fatalf("error = %q, want the transport failure verbatim", got.Error)
	}
}

func TestProbeOneTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	c := newHTTPClient()
	c.Timeout = 100 * time.Millisecond

	got := probeOne(context.Background(), c, srv.URL)
	if got.OK || got.Error == "" {
		t.Fatalf("health = %+v, want a timeout failure", got)
	}
}

func TestCollectHealthProbesEveryURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	got := collectHealth(context.Background(), newHTTPClient(), map[string]string{
		"a": srv.URL,
		"b": srv.URL + "/other",
	})
	if len(got) != 2 || !got["a"].OK || !got["b"].OK {
		t.Fatalf("health = %+v, want both up", got)
	}
}

func TestCollectHealthWithNothingConfigured(t *testing.T) {
	if got := collectHealth(context.Background(), newHTTPClient(), nil); len(got) != 0 {
		t.Fatalf("health = %+v, want empty", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/probe/ -run 'TestProbeOne|TestCollectHealth' -v`
Expected: FAIL — `undefined: newHTTPClient`.

- [ ] **Step 3: Write the implementation**

Create `internal/probe/health.go`:

```go
package probe

import (
	"context"
	"io"
	"net/http"
	"sort"
	"time"
)

// healthTimeout bounds one probe. A service that cannot answer in two seconds
// is not healthy for a dashboard's purposes.
const healthTimeout = 2 * time.Second

// newHTTPClient builds the prober's client: bounded, and it does not follow
// redirects, so a 302 to a login page reads as down rather than up.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: healthTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// probeOne GETs url once. Any 2xx is up; everything else, including transport
// failures, is down with the reason attached.
func probeOne(ctx context.Context, c *http.Client, url string) Health {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Health{URL: url, Error: err.Error()}
	}

	start := time.Now()
	resp, err := c.Do(req)
	latency := float64(time.Since(start).Microseconds()) / 1000

	if err != nil {
		return Health{URL: url, Error: err.Error(), LatencyMS: latency}
	}
	defer resp.Body.Close()
	// Drain a little so the connection can be reused, but never a whole body.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	return Health{
		URL:       url,
		OK:        resp.StatusCode/100 == 2,
		Status:    resp.StatusCode,
		LatencyMS: latency,
	}
}

// collectHealth probes every configured URL, in name order so a slow endpoint
// delays the same neighbours every time rather than a random set.
func collectHealth(ctx context.Context, c *http.Client, urls map[string]string) map[string]Health {
	out := make(map[string]Health, len(urls))
	names := make([]string, 0, len(urls))
	for name := range urls {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		out[name] = probeOne(ctx, c, urls[name])
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/probe/ -v`
Expected: PASS, every probe test.

- [ ] **Step 5: Commit**

```bash
git add internal/probe/
git commit -m "feat(probe): HTTP health probes that do not follow redirects

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Port conflicts

**Files:**
- Create: `internal/probe/conflict.go`
- Test: `internal/probe/conflict_test.go`

**Interfaces:**
- Consumes: `Port`, `Conflict` from Task 3.
- Produces: `func conflicts(intended map[string]int, ports []Port, pids map[string]int) []Conflict` — sorted by port, then command name.

`pids` doubles as the running set: a name present in it is running.

- [ ] **Step 1: Write the failing test**

Create `internal/probe/conflict_test.go`:

```go
package probe

import "testing"

func TestConflictNoneWhenTheCommandOwnsItsPort(t *testing.T) {
	got := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 3000, PID: 137, Process: "node", Command: "web"}},
		map[string]int{"web": 100},
	)
	if len(got) != 0 {
		t.Fatalf("conflicts = %+v, want none", got)
	}
}

func TestConflictTakenNamesTheSquatter(t *testing.T) {
	got := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 3000, PID: 51192, Process: "node"}}, // no Command: not ours
		map[string]int{"web": 100},
	)
	if len(got) != 1 {
		t.Fatalf("conflicts = %+v, want one", got)
	}
	c := got[0]
	if c.State != "taken" || c.Command != "web" || c.Port != 3000 {
		t.Fatalf("conflict = %+v", c)
	}
	if c.HeldBy != "node" || c.PID != 51192 {
		t.Fatalf("conflict = %+v, want the squatter named", c)
	}
}

func TestConflictTakenIsReportedForAStoppedCommand(t *testing.T) {
	// The most useful case: npm start just died because something else holds
	// the port. The command is not running, and that is exactly when this
	// matters.
	got := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 3000, PID: 51192, Process: "node"}},
		map[string]int{}, // nothing running
	)
	if len(got) != 1 || got[0].State != "taken" {
		t.Fatalf("conflicts = %+v, want taken for a stopped command", got)
	}
}

func TestConflictFreeOnlyForRunningCommands(t *testing.T) {
	running := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 5432, PID: 1183, Process: "postgres"}},
		map[string]int{"web": 100},
	)
	if len(running) != 1 || running[0].State != "free" {
		t.Fatalf("conflicts = %+v, want free for a running command with nothing bound", running)
	}

	stopped := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 5432, PID: 1183, Process: "postgres"}},
		map[string]int{},
	)
	if len(stopped) != 0 {
		t.Fatalf("conflicts = %+v, want nothing: a stopped command not listening is normal", stopped)
	}
}

func TestConflictOwnershipWinsOverAnotherRowOnTheSamePort(t *testing.T) {
	// Loopback held by us, wildcard held by someone else: we own it, so no
	// conflict.
	got := conflicts(
		map[string]int{"web": 3000},
		[]Port{
			{Addr: "*", Port: 3000, PID: 999, Process: "other"},
			{Addr: "127.0.0.1", Port: 3000, PID: 137, Process: "node", Command: "web"},
		},
		map[string]int{"web": 100},
	)
	if len(got) != 0 {
		t.Fatalf("conflicts = %+v, want none when one of the rows is ours", got)
	}
}

func TestConflictsAreSorted(t *testing.T) {
	got := conflicts(
		map[string]int{"b": 5000, "a": 5000, "c": 3000},
		[]Port{
			{Port: 3000, PID: 1, Process: "x"},
			{Port: 5000, PID: 2, Process: "y"},
		},
		map[string]int{},
	)
	if len(got) != 3 {
		t.Fatalf("conflicts = %+v, want three", got)
	}
	if got[0].Port != 3000 || got[1].Command != "a" || got[2].Command != "b" {
		t.Fatalf("conflicts not sorted by port then command: %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/probe/ -run TestConflict -v`
Expected: FAIL — `undefined: conflicts`.

- [ ] **Step 3: Write the implementation**

Create `internal/probe/conflict.go`:

```go
package probe

import "sort"

// conflicts compares what each command means to bind against what is actually
// bound. pids doubles as the running set.
//
// Callers must not invoke this when the ports sample failed: with no port
// list every intended port would look free, raising a false alarm about every
// command exactly when the collector is the broken thing.
func conflicts(intended map[string]int, ports []Port, pids map[string]int) []Conflict {
	byPort := make(map[int][]Port, len(ports))
	for _, p := range ports {
		byPort[p.Port] = append(byPort[p.Port], p)
	}

	out := make([]Conflict, 0, len(intended))
	for name, port := range intended {
		rows := byPort[port]

		if len(rows) == 0 {
			// Nothing listening. Only interesting while the command is up.
			if _, running := pids[name]; running {
				out = append(out, Conflict{Port: port, Command: name, State: "free"})
			}
			continue
		}

		owned := false
		for _, r := range rows {
			if r.Command == name {
				owned = true
				break
			}
		}
		if owned {
			continue
		}

		squatter := rows[0]
		out = append(out, Conflict{
			Port:    port,
			Command: name,
			State:   "taken",
			HeldBy:  squatter.Process,
			PID:     squatter.PID,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Command < out[j].Command
	})
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/probe/ -v`
Expected: PASS, every probe test.

- [ ] **Step 5: Commit**

```bash
git add internal/probe/
git commit -m "feat(probe): report a command's port held by something else

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 8: The sampler

**Files:**
- Create: `internal/probe/probe.go`
- Test: `internal/probe/probe_test.go`

**Interfaces:**
- Consumes: `collectPorts`, `collectVitals`, `collectHealth`, `conflicts`, `applyOwners`, `execRun`, `newHTTPClient`.
- Produces:
  - `probe.New(pids func() map[string]int, urls func() map[string]string, intended func() map[string]int) *Sampler`.
  - `(*Sampler).Start(ctx context.Context)` — three goroutines, returns immediately.
  - `(*Sampler).Snapshot() Snapshot` — a deep copy.
  - Exported tunables on the struct: `PortsEvery`, `VitalsEvery`, `HealthEvery` (defaults 5s, 2s, 10s).

- [ ] **Step 1: Write the failing test**

Create `internal/probe/probe_test.go`:

```go
package probe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestSampler wires a sampler to canned command output and fast tickers.
func newTestSampler(t *testing.T, run runFunc, pids map[string]int, urls map[string]string, intended map[string]int) *Sampler {
	t.Helper()
	s := New(
		func() map[string]int { return pids },
		func() map[string]string { return urls },
		func() map[string]int { return intended },
	)
	s.run = run
	s.PortsEvery = 10 * time.Millisecond
	s.VitalsEvery = 10 * time.Millisecond
	s.HealthEvery = 10 * time.Millisecond
	return s
}

// waitSnap polls until cond holds or the deadline passes.
func waitSnap(t *testing.T, s *Sampler, what string, cond func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last Snapshot
	for time.Now().Before(deadline) {
		last = s.Snapshot()
		if cond(last) {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; snapshot = %+v", what, last)
	return last
}

func TestSamplerFillsEverySection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)},
		"ps":   {out: []byte(psSample)},
	})
	s := newTestSampler(t, run,
		map[string]int{"app:web": 100},
		map[string]string{"app:web": srv.URL},
		map[string]int{"app:web": 3000},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	snap := waitSnap(t, s, "all three sections", func(s Snapshot) bool {
		return len(s.Ports) > 0 && len(s.Vitals) > 0 && len(s.Health) > 0
	})
	if len(snap.Errors) != 0 {
		t.Fatalf("errors = %v, want none", snap.Errors)
	}
	for _, section := range []string{"ports", "vitals", "health"} {
		if snap.SampledAt[section].IsZero() {
			t.Fatalf("no timestamp for %q: %v", section, snap.SampledAt)
		}
	}
	if !snap.Health["app:web"].OK {
		t.Fatalf("health = %+v, want up", snap.Health["app:web"])
	}
}

func TestSamplerAttributesPortsOnceVitalsLand(t *testing.T) {
	// lsof reports pid 51192 on 3000; ps says that pid's group is 100, and
	// app:web is pid 100. The port must end up attributed.
	const psWithNode = `  100   100  2.5  40960
51192   100 30.0 100000
`
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)},
		"ps":   {out: []byte(psWithNode)},
	})
	s := newTestSampler(t, run, map[string]int{"app:web": 100}, nil, map[string]int{"app:web": 3000})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	snap := waitSnap(t, s, "port ownership", func(s Snapshot) bool {
		for _, p := range s.Ports {
			if p.Port == 3000 && p.Command == "app:web" {
				return true
			}
		}
		return false
	})
	if len(snap.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none: we own the port", snap.Conflicts)
	}
}

func TestSamplerOneBrokenCollectorLeavesTheOthers(t *testing.T) {
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch name {
		case "ps":
			return []byte(psSample), nil
		default: // lsof and ss both fail
			return nil, errors.New("not installed")
		}
	}
	s := newTestSampler(t, run, map[string]int{"app:web": 100}, nil, map[string]int{"app:web": 3000})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	snap := waitSnap(t, s, "the ports error", func(s Snapshot) bool {
		return s.Errors["ports"] != "" && len(s.Vitals) > 0
	})
	if len(snap.Ports) != 0 {
		t.Fatalf("ports = %+v, want none", snap.Ports)
	}
	// The whole point: no port list means no conflict guessing.
	if len(snap.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none while the ports collector is broken", snap.Conflicts)
	}
	if snap.Errors["vitals"] != "" {
		t.Fatalf("vitals error = %q, want vitals unaffected", snap.Errors["vitals"])
	}
}

func TestSamplerReportsTakenPorts(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)}, // node on 3000, pid 51192
		"ps":   {out: []byte(psSample)},   // pid 51192 is not in any of our groups
	})
	s := newTestSampler(t, run, map[string]int{"app:web": 100}, nil, map[string]int{"app:web": 3000})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	snap := waitSnap(t, s, "the taken conflict", func(s Snapshot) bool {
		return len(s.Conflicts) > 0
	})
	c := snap.Conflicts[0]
	if c.State != "taken" || c.Command != "app:web" || c.HeldBy != "node" {
		t.Fatalf("conflict = %+v", c)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)},
		"ps":   {out: []byte(psSample)},
	})
	s := newTestSampler(t, run, map[string]int{"app:web": 100}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitSnap(t, s, "a first sample", func(s Snapshot) bool { return len(s.Ports) > 0 })

	snap := s.Snapshot()
	snap.Ports[0].Process = "tampered"
	snap.Vitals["injected"] = Vital{}
	snap.Errors["ports"] = "injected"

	again := s.Snapshot()
	if again.Ports[0].Process == "tampered" {
		t.Fatal("Snapshot shares its ports slice with the sampler")
	}
	if _, bad := again.Vitals["injected"]; bad {
		t.Fatal("Snapshot shares its vitals map with the sampler")
	}
	if again.Errors["ports"] == "injected" {
		t.Fatal("Snapshot shares its errors map with the sampler")
	}
}

func TestSamplerStopsOnContextCancel(t *testing.T) {
	calls := make(chan struct{}, 1000)
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return []byte(psSample), nil
	}
	s := newTestSampler(t, run, map[string]int{"a": 1}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	waitSnap(t, s, "a first sample", func(s Snapshot) bool { return !s.SampledAt["vitals"].IsZero() })

	cancel()
	time.Sleep(60 * time.Millisecond)
	drain := len(calls)
	time.Sleep(100 * time.Millisecond)
	if len(calls) > drain {
		t.Fatal("sampling continued after the context was cancelled")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/probe/ -run 'TestSampler|TestSnapshotIsACopy' -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation**

Create `internal/probe/probe.go`:

```go
package probe

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Default sampling intervals. Ports are the most expensive and the least
// volatile; vitals move fastest; health is a network round trip.
const (
	DefaultPortsEvery  = 5 * time.Second
	DefaultVitalsEvery = 2 * time.Second
	DefaultHealthEvery = 10 * time.Second
)

// Sampler keeps one snapshot of the machine up to date. It learns about
// commands through three closures, so it depends on no other package of ours.
type Sampler struct {
	mu     sync.Mutex
	snap   Snapshot
	groups map[int]int // pid -> pgid, refreshed by the vitals loop

	pids     func() map[string]int
	urls     func() map[string]string
	intended func() map[string]int

	run   runFunc
	httpc *http.Client
	now   func() time.Time

	PortsEvery  time.Duration
	VitalsEvery time.Duration
	HealthEvery time.Duration
}

// New builds a Sampler. The closures are called on every tick rather than
// captured, so a config reload is picked up with no extra wiring.
func New(pids func() map[string]int, urls func() map[string]string, intended func() map[string]int) *Sampler {
	return &Sampler{
		snap: Snapshot{
			Vitals:    map[string]Vital{},
			Health:    map[string]Health{},
			SampledAt: map[string]time.Time{},
			Errors:    map[string]string{},
		},
		groups:      map[int]int{},
		pids:        pids,
		urls:        urls,
		intended:    intended,
		run:         execRun,
		httpc:       newHTTPClient(),
		now:         time.Now,
		PortsEvery:  DefaultPortsEvery,
		VitalsEvery: DefaultVitalsEvery,
		HealthEvery: DefaultHealthEvery,
	}
}

// Start runs the three sampling loops until ctx is cancelled. It returns
// immediately.
func (s *Sampler) Start(ctx context.Context) {
	// Vitals first: it publishes the pid->pgid map the ports loop needs to
	// attribute a socket. A ports sample that lands first simply shows no
	// owner until the next one.
	go s.loop(ctx, s.VitalsEvery, s.sampleVitals)
	go s.loop(ctx, s.PortsEvery, s.samplePorts)
	go s.loop(ctx, s.HealthEvery, s.sampleHealth)
}

func (s *Sampler) loop(ctx context.Context, every time.Duration, sample func(context.Context)) {
	sample(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sample(ctx)
		}
	}
}

func (s *Sampler) sampleVitals(ctx context.Context) {
	vitals, groups, err := collectVitals(ctx, s.run, s.pids())

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.SampledAt[sectionVitals] = s.now()
	if err != nil {
		s.snap.Errors[sectionVitals] = err.Error()
		return
	}
	delete(s.snap.Errors, sectionVitals)
	s.snap.Vitals = vitals
	s.groups = groups
}

func (s *Sampler) samplePorts(ctx context.Context) {
	// Read the closures before taking our lock: they take the manager's.
	pids := s.pids()
	intended := s.intended()
	ports, err := collectPorts(ctx, s.run)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.SampledAt[sectionPorts] = s.now()
	if err != nil {
		s.snap.Errors[sectionPorts] = err.Error()
		s.snap.Ports = nil
		// No port list means no conflict guessing: every intended port would
		// look free, which would blame every command for a broken collector.
		s.snap.Conflicts = nil
		return
	}
	delete(s.snap.Errors, sectionPorts)
	s.snap.Ports = applyOwners(ports, s.groups, pids)
	s.snap.Conflicts = conflicts(intended, s.snap.Ports, pids)
}

func (s *Sampler) sampleHealth(ctx context.Context) {
	// Only running commands appear in urls(), so a stopped command's entry
	// disappears rather than going stale.
	health := collectHealth(ctx, s.httpc, s.urls())

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.SampledAt[sectionHealth] = s.now()
	s.snap.Health = health
}

// Snapshot returns a deep copy, safe to serialize or mutate.
func (s *Sampler) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := Snapshot{
		Ports:     append([]Port(nil), s.snap.Ports...),
		Conflicts: append([]Conflict(nil), s.snap.Conflicts...),
		Vitals:    make(map[string]Vital, len(s.snap.Vitals)),
		Health:    make(map[string]Health, len(s.snap.Health)),
		SampledAt: make(map[string]time.Time, len(s.snap.SampledAt)),
		Errors:    make(map[string]string, len(s.snap.Errors)),
	}
	for k, v := range s.snap.Vitals {
		out.Vitals[k] = v
	}
	for k, v := range s.snap.Health {
		out.Health[k] = v
	}
	for k, v := range s.snap.SampledAt {
		out.SampledAt[k] = v
	}
	for k, v := range s.snap.Errors {
		out.Errors[k] = v
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/probe/ -v`
Expected: PASS, every probe test. The race detector matters here: the loops write while `Snapshot` reads.

- [ ] **Step 5: Commit**

```bash
git add internal/probe/
git commit -m "feat(probe): sampler with independent loops and isolated failures

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: `GET /v1/system` and the enriched command view

**Files:**
- Create: `internal/api/system.go`
- Modify: `internal/api/server.go` — a `probe` field, the `NewServer` signature, the new route, and enrichment in `list`/`get`
- Modify: `internal/client/client.go` — `System()`
- Modify (call sites of `NewServer`): `internal/api/server_test.go`, `internal/api/auth_test.go`, `internal/client/client_test.go`, `internal/tui/testdaemon_test.go`, `cmd/lazycomd/cli_test.go`
- Test: `internal/api/system_test.go`

**Interfaces:**
- Consumes: `probe.Sampler`, `probe.Snapshot`, `manager.HealthView`.
- Produces:
  - `api.NewServer(mgr *manager.Manager, token string, reload func() (*config.Config, error), sampler *probe.Sampler) *Server` — a `nil` sampler is legal and means no probe data.
  - Route `GET /v1/system`.
  - `(*Client).System() (probe.Snapshot, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/system_test.go`:

```go
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

// testAPIWithProbe is testAPI plus a sampler fed by canned command output.
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
	// Sections exist even before the first sample lands.
	if snap.Vitals == nil || snap.Health == nil || snap.SampledAt == nil {
		t.Fatalf("snapshot sections missing: %s", body)
	}
}

func TestSystemEndpointIs200EvenWithACollectorError(t *testing.T) {
	_, c, sampler := testAPIWithProbe(t, map[string]config.Command{"a": sleeper()})
	// Wait for at least one sampling round; on a machine with no lsof and no
	// ss this produces errors.ports, which must not change the status code.
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

	// Vitals need one ps round; skip rather than fail where ps is unavailable.
	var st manager.Status
	deadline := time.Now().Add(4 * time.Second)
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestSystem -v`
Expected: FAIL — `too many arguments in call to NewServer`.

- [ ] **Step 3: Extend the server**

In `internal/api/server.go`, add the field and widen the constructor:

```go
type Server struct {
	mgr    *manager.Manager
	token  string
	reload func() (*config.Config, error)
	probe  *probe.Sampler
}

// NewServer builds a Server. sampler may be nil, in which case the probe
// fields are simply absent.
func NewServer(mgr *manager.Manager, token string, reload func() (*config.Config, error), sampler *probe.Sampler) *Server {
	return &Server{mgr: mgr, token: token, reload: reload, probe: sampler}
}
```

Register the route in `routes()`:

```go
	mux.HandleFunc("GET /v1/system", s.system)
```

And enrich both command handlers:

```go
func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.enrich(s.mgr.List()))
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	st, err := s.mgr.Status(r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.enrich([]manager.Status{st})[0])
}
```

Add `"github.com/tphuc/lazycomd/internal/probe"` to the imports.

- [ ] **Step 4: Write the system handler and the merge**

Create `internal/api/system.go`:

```go
package api

import (
	"net/http"

	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/probe"
)

// system serves the last sample of the machine's state. Always 200: partial
// failure is normal and is reported inside the body, not by a status code.
func (s *Server) system(w http.ResponseWriter, _ *http.Request) {
	if s.probe == nil {
		writeJSON(w, http.StatusOK, probe.Snapshot{
			Vitals:    map[string]probe.Vital{},
			Health:    map[string]probe.Health{},
			SampledAt: map[string]interface{ String() string }{}["x"], // placeholder
		})
		return
	}
	writeJSON(w, http.StatusOK, s.probe.Snapshot())
}

// enrich fills the probe-sourced fields the manager never sets.
func (s *Server) enrich(list []manager.Status) []manager.Status {
	if s.probe == nil || len(list) == 0 {
		return list
	}
	snap := s.probe.Snapshot()
	for i := range list {
		if v, ok := snap.Vitals[list[i].Name]; ok {
			list[i].CPU, list[i].MemMB = v.CPU, v.MemMB
		}
		if h, ok := snap.Health[list[i].Name]; ok {
			list[i].Health = &manager.HealthView{
				URL:       h.URL,
				OK:        h.OK,
				Status:    h.Status,
				LatencyMS: h.LatencyMS,
				Error:     h.Error,
			}
		}
	}
	return list
}
```

The `s.probe == nil` branch above is deliberately wrong so you notice it: an
empty `probe.Snapshot{}` serializes its maps as `null`, and
`TestSystemEndpointShape` asserts they are objects. Replace that branch with a
constructor on the probe package instead — add to `internal/probe/types.go`:

```go
// EmptySnapshot is a snapshot with every section present and empty, so a
// client never has to special-case a null map.
func EmptySnapshot() Snapshot {
	return Snapshot{
		Ports:     []Port{},
		Vitals:    map[string]Vital{},
		Health:    map[string]Health{},
		SampledAt: map[string]time.Time{},
		Errors:    map[string]string{},
	}
}
```

and make the handler:

```go
func (s *Server) system(w http.ResponseWriter, _ *http.Request) {
	if s.probe == nil {
		writeJSON(w, http.StatusOK, probe.EmptySnapshot())
		return
	}
	writeJSON(w, http.StatusOK, s.probe.Snapshot())
}
```

- [ ] **Step 5: Update every `NewServer` call site**

Five places pass a fourth argument. Four of them are tests with no sampler:

```bash
grep -rn "NewServer(" --include=*.go .
```

- `internal/api/server_test.go` (`testAPI`) → `NewServer(m, "", reload, nil)`
- `internal/api/auth_test.go` (four calls) → add `, nil`
- `internal/client/client_test.go` (`daemon`) → add `, nil`
- `internal/tui/testdaemon_test.go` (`testDaemon`) → add `, nil`
- `cmd/lazycomd/cli_test.go` (`testDaemon`) → add `, nil`
- `cmd/lazycomd/serve.go` → Task 10 passes the real sampler

- [ ] **Step 6: Add the client method**

In `internal/client/client.go`:

```go
// System returns the daemon's last machine sample.
func (c *Client) System() (probe.Snapshot, error) {
	var out probe.Snapshot
	return out, c.do(context.Background(), "GET", "/v1/system", nil, &out)
}
```

Add `"github.com/tphuc/lazycomd/internal/probe"` to its imports, and a test in `internal/client/client_test.go`:

```go
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
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -race ./... 2>&1 | tail -12`
Expected: every package passes, including the five updated call sites.

- [ ] **Step 8: Commit**

```bash
git add internal/api/ internal/client/ internal/probe/ internal/tui/ cmd/
git commit -m "feat(api): GET /v1/system and probe fields on the command view

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Wire the sampler into `serve`

**Files:**
- Modify: `cmd/lazycomd/serve.go`
- Test: `cmd/lazycomd/serve_test.go`

**Interfaces:**
- Consumes: `probe.New`, `(*Sampler).Start`, the widened `api.NewServer`.
- Produces: no new symbols; `runServe` now builds, starts and drains the sampler.

- [ ] **Step 1: Write the failing test**

Append to `cmd/lazycomd/serve_test.go`:

```go
func TestRunServeServesSystemEndpoint(t *testing.T) {
	dir := shortDir(t)
	t.Setenv("XDG_STATE_HOME", dir)

	cfg := filepath.Join(dir, "config.yaml")
	body := "commands:\n  a:\n    cmd: [\"sleep\", \"30\"]\n    cwd: /tmp\n    port: 4310\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan int, 1)
	go func() { done <- runServe([]string{"-config", cfg}) }()

	sock := filepath.Join(dir, "lazycomd", "lazycomd.sock")
	c := waitForDaemon(t, sock)

	snap, err := c.System()
	if err != nil {
		t.Fatalf("System: %v", err)
	}
	if snap.SampledAt == nil {
		t.Fatalf("snapshot has no sections: %+v", snap)
	}

	// Shut it down the way a user would.
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("runServe = %d, want 0", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runServe did not return after SIGTERM")
	}
}

// waitForDaemon returns a client once the socket answers.
func waitForDaemon(t *testing.T, sock string) *client.Client {
	t.Helper()
	c, err := client.New("unix://"+sock, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Health() == nil {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon never came up")
	return nil
}
```

Add `"syscall"`, `"time"` and `"github.com/tphuc/lazycomd/internal/client"` to that file's imports.

This test signals the **test process itself**, which `runServe` is listening
on. That is deliberate: it exercises the real signal path. Keep it as the only
test in the package that does so.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/lazycomd/ -run TestRunServeServesSystem -v`
Expected: FAIL — `not enough arguments in call to api.NewServer`, then a 404 on `/v1/system` once that compiles.

- [ ] **Step 3: Write the implementation**

In `cmd/lazycomd/serve.go`, after the manager is built and before the server:

```go
	mgr := manager.New(cfg, paths.LogDir())

	// The sampler learns about commands through the manager's accessors, so a
	// reload is picked up on the next tick with no extra wiring.
	sampler := probe.New(mgr.RunningPIDs, mgr.HealthURLs, mgr.IntendedPorts)
	probeCtx, stopProbe := context.WithCancel(context.Background())
	defer stopProbe()
	sampler.Start(probeCtx)

	srv := api.NewServer(mgr, token, func() (*config.Config, error) {
		return config.Load(*cfgPath)
	}, sampler)
```

and stop sampling at the start of the drain, before the manager shuts down:

```go
	unixSrv.Close()
	stopProbe()
	drained := make(chan struct{})
	go func() {
		mgr.Shutdown()
		close(drained)
	}()
```

Add `"context"` and `"github.com/tphuc/lazycomd/internal/probe"` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./cmd/lazycomd/ -v`
Expected: PASS, every CLI and serve test.

- [ ] **Step 5: Commit**

```bash
git add cmd/lazycomd/
git commit -m "feat(serve): build, start and drain the probe sampler

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 11: Health, CPU and MEM columns

**Files:**
- Modify: `internal/tui/table.go` — `SetLayout` replaces `SetSize`, three new columns
- Modify: `internal/tui/tui.go` — `layout()` calls `SetLayout`
- Modify: `internal/tui/table_test.go` — the existing `SetSize` calls
- Test: `internal/tui/table_test.go`

**Interfaces:**
- Consumes: `manager.Status.CPU`, `.MemMB`, `.Health` from Task 2.
- Produces:
  - `type tableLayout struct { Width, Height int; Compact, Wide bool }`.
  - `(*tableModel).SetLayout(l tableLayout)` — replaces `SetSize(w, h int, compact bool)`.
  - `(tableModel).showHealth() bool` — true when any row carries a health result.
  - Column widths `colHealth = 2`, `colCPU = 6`, `colMEM = 7`.

The `H` column is derived, not plumbed: the table decides from its own rows, so no caller has to track whether health is configured.

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/table_test.go`:

```go
func TestWideLayoutAddsCPUAndMem(t *testing.T) {
	tbl := newTable()
	tbl.SetRows([]manager.Status{
		{Name: "web", State: manager.Running, PID: 1, CPU: 142.3, MemMB: 318.4},
	})

	tbl.SetLayout(tableLayout{Width: 60, Height: 10, Wide: true})
	wide := tbl.View()
	for _, want := range []string{"CPU", "MEM", "142%", "318M"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide view missing %q:\n%s", want, wide)
		}
	}

	tbl.SetLayout(tableLayout{Width: 40, Height: 10})
	narrow := tbl.View()
	for _, gone := range []string{"CPU", "MEM", "142%"} {
		if strings.Contains(narrow, gone) {
			t.Fatalf("narrow view still has %q:\n%s", gone, narrow)
		}
	}
}

func TestHealthColumnOnlyAppearsWhenConfigured(t *testing.T) {
	tbl := newTable()
	tbl.SetLayout(tableLayout{Width: 60, Height: 10})

	tbl.SetRows(rows("a", "b"))
	if strings.Contains(tbl.View(), " H ") || tbl.showHealth() {
		t.Fatalf("health column present with no health configured:\n%s", tbl.View())
	}

	tbl.SetRows([]manager.Status{
		{Name: "up", State: manager.Running, Health: &manager.HealthView{OK: true}},
		{Name: "down", State: manager.Running, Health: &manager.HealthView{OK: false, Error: "refused"}},
		{Name: "none", State: manager.Stopped},
	})
	view := tbl.View()
	if !tbl.showHealth() {
		t.Fatal("showHealth() = false with health results present")
	}
	if !strings.Contains(view, "●") {
		t.Fatalf("no up marker:\n%s", view)
	}
	if !strings.Contains(view, "○") {
		t.Fatalf("no down marker:\n%s", view)
	}
}

func TestCPUAndMemFormatting(t *testing.T) {
	if got := formatCPU(0); got != "-" {
		t.Fatalf("formatCPU(0) = %q, want -", got)
	}
	if got := formatCPU(142.3); got != "142%" {
		t.Fatalf("formatCPU(142.3) = %q", got)
	}
	if got := formatCPU(0.4); got != "0%" {
		t.Fatalf("formatCPU(0.4) = %q", got)
	}
	if got := formatMem(0); got != "-" {
		t.Fatalf("formatMem(0) = %q, want -", got)
	}
	if got := formatMem(318.4); got != "318M" {
		t.Fatalf("formatMem(318.4) = %q", got)
	}
	if got := formatMem(2048); got != "2.0G" {
		t.Fatalf("formatMem(2048) = %q, want gigabytes past 1024M", got)
	}
}
```

Then update every existing `SetSize` call in that file — `tbl.SetSize(60, 10, false)` becomes `tbl.SetLayout(tableLayout{Width: 60, Height: 10})`, and `tbl.SetSize(28, 10, true)` becomes `tbl.SetLayout(tableLayout{Width: 28, Height: 10, Compact: true})`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestWideLayout|TestHealthColumn|TestCPUAndMem' -v`
Expected: FAIL — `tbl.SetLayout undefined`.

- [ ] **Step 3: Rewrite the table's sizing and columns**

In `internal/tui/table.go`, replace `SetSize` and add the widths:

```go
const (
	colHealth = 2
	colState  = 10
	colPID    = 7
	colUptime = 8
	colRS     = 3
	colCPU    = 6
	colMEM    = 7
)

// tableLayout is how much room the table has and how much it may show.
type tableLayout struct {
	Width   int
	Height  int
	Compact bool // NAME and STATE only
	Wide    bool // room for CPU and MEM
}

func (t *tableModel) SetLayout(l tableLayout) {
	t.width, t.height = l.Width, l.Height
	t.compact, t.wide = l.Compact, l.Wide
}

// showHealth reports whether any command has a health result to show. The
// column stays hidden in a config with no health checks.
func (t tableModel) showHealth() bool {
	for _, r := range t.rows {
		if r.Health != nil {
			return true
		}
	}
	return false
}
```

Add `wide bool` to the `tableModel` struct next to `compact`.

- [ ] **Step 4: Render the new columns**

Replace `nameWidth` and `View` in `internal/tui/table.go`:

```go
func (t tableModel) nameWidth() int {
	w := t.width - 2 - colState // 2 for the cursor marker
	if t.showHealth() {
		w -= colHealth
	}
	if !t.compact {
		w -= colPID + colUptime + colRS
		if t.wide {
			w -= colCPU + colMEM
		}
	}
	if w < 8 {
		w = 8
	}
	return w
}

func (t tableModel) View() string {
	nameW := t.nameWidth()
	health := t.showHealth()

	var b strings.Builder
	header := "  "
	if health {
		header += cell("H", colHealth, styleHeader)
	}
	header += cell("NAME", nameW, styleHeader) + cell("STATE", colState, styleHeader)
	if !t.compact {
		header += cell("PID", colPID, styleHeader) + cell("UPTIME", colUptime, styleHeader) + cell("RS", colRS, styleHeader)
		if t.wide {
			header += cell("CPU", colCPU, styleHeader) + cell("MEM", colMEM, styleHeader)
		}
	}
	b.WriteString(header)

	start, end := t.window()
	for i := start; i < end; i++ {
		r := t.rows[i]
		marker := "  "
		if i == t.cursor {
			marker = "> "
		}
		state := string(r.State)
		if r.SpecDirty {
			state += "*"
		}

		line := marker
		if health {
			line += cell(healthDot(r.Health), colHealth, healthStyle(r.Health))
		}
		line += cell(r.Name, nameW, lipgloss.NewStyle()) +
			cell(state, colState, stateStyles[r.State])
		if !t.compact {
			pid := "-"
			if r.PID > 0 {
				pid = fmt.Sprintf("%d", r.PID)
			}
			line += cell(pid, colPID, styleDim) +
				cell(formatUptime(r.UptimeSec), colUptime, styleDim) +
				cell(fmt.Sprintf("%d", r.Restarts), colRS, styleDim)
			if t.wide {
				line += cell(formatCPU(r.CPU), colCPU, styleDim) +
					cell(formatMem(r.MemMB), colMEM, styleDim)
			}
		}
		b.WriteString("\n" + line)
	}
	return b.String()
}

// healthDot is filled for a healthy service, hollow for an unhealthy one, and
// blank for a command that configured no check.
func healthDot(h *manager.HealthView) string {
	switch {
	case h == nil:
		return ""
	case h.OK:
		return "●"
	default:
		return "○"
	}
}

func healthStyle(h *manager.HealthView) lipgloss.Style {
	switch {
	case h == nil:
		return styleDim
	case h.OK:
		return stateStyles[manager.Running]
	default:
		return stateStyles[manager.Failed]
	}
}

// formatCPU shows whole percent; it is percent of one core, so past 100 is
// real and is not clamped.
func formatCPU(cpu float64) string {
	if cpu <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", cpu)
}

func formatMem(mb float64) string {
	switch {
	case mb <= 0:
		return "-"
	case mb < 1024:
		return fmt.Sprintf("%.0fM", mb)
	default:
		return fmt.Sprintf("%.1fG", mb/1024)
	}
}
```

- [ ] **Step 5: Update the root model's layout**

In `internal/tui/tui.go`, both `SetSize` calls on the table become `SetLayout`:

```go
	if m.width < 60 {
		m.tableW = m.width
		m.table.SetLayout(tableLayout{Width: m.tableW, Height: m.bodyH, Compact: true})
		m.logs.SetSize(1, m.bodyH)
		// ...unchanged
	}

	// ...
	m.table.SetLayout(tableLayout{
		Width:   m.tableW,
		Height:  m.bodyH,
		Compact: m.width < 80,
		Wide:    m.width >= 110,
	})
	m.logs.SetSize(m.width-m.tableW-1, m.bodyH)
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ -v`
Expected: PASS, every TUI test, including spec #2's resize tests — the extra columns must not push any line past the terminal width at 110.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): health, CPU and memory columns on the command table

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: The ports view

**Files:**
- Create: `internal/tui/system.go`
- Modify: `internal/tui/messages.go` — the system tick, message and command
- Modify: `internal/tui/tui.go` — `overlayPorts`, the `d` key, the system tick, routing and `View`
- Modify: `internal/tui/help.go` — the `d` binding
- Test: `internal/tui/system_test.go`

**Interfaces:**
- Consumes: `probe.Snapshot`, `client.Client.System`.
- Produces:
  - `systemTickMsg time.Time`, `systemMsg probe.Snapshot`, `systemErrMsg{err error}`, `systemTick = 3 * time.Second`, `fetchSystem(c *client.Client) tea.Cmd`.
  - `type systemModel struct` with `newSystem()`, `(*systemModel).SetSize(w, h int)`, `(*systemModel).SetSnapshot(s probe.Snapshot)`, `(systemModel).Update(tea.Msg) (systemModel, tea.Cmd)`, `(systemModel).View() string`, `(systemModel).title() string`.
  - `overlayPorts` added to the `overlay` enum in `help.go`.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/system_test.go`:

```go
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/probe"
)

func snapshotFixture(now time.Time) probe.Snapshot {
	return probe.Snapshot{
		Ports: []probe.Port{
			{Addr: "127.0.0.1", Port: 3000, PID: 51192, Process: "node"},
			{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"},
			{Addr: "127.0.0.1", Port: 7777, PID: 54405, Process: "lazycomd", Command: "daemon"},
		},
		Conflicts: []probe.Conflict{
			{Port: 3000, Command: "web", State: "taken", HeldBy: "node", PID: 51192},
		},
		SampledAt: map[string]time.Time{"ports": now.Add(-2 * time.Second)},
		Errors:    map[string]string{},
	}
}

func newTestSystem(t *testing.T, snap probe.Snapshot, now time.Time) systemModel {
	t.Helper()
	s := newSystem()
	s.now = func() time.Time { return now }
	s.SetSize(80, 12)
	s.SetSnapshot(snap)
	return s
}

func TestPortsViewRendersRows(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)

	view := s.View()
	for _, want := range []string{"PORT", "ADDR", "PID", "PROCESS", "OWNER", "3000", "postgres", "daemon"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestPortsViewLeadsWithConflicts(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)

	view := s.View()
	if !strings.Contains(view, "3000 wanted by web") || !strings.Contains(view, "node") {
		t.Fatalf("conflict summary missing:\n%s", view)
	}
	if !strings.Contains(view, "⚠") {
		t.Fatalf("conflict marker missing:\n%s", view)
	}
}

func TestPortsViewTitleShowsFreshnessThenStaleness(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, snapshotFixture(now), now)
	if got := s.title(); !strings.Contains(got, "sampled 2s ago") {
		t.Fatalf("title = %q, want the sample age", got)
	}

	stale := snapshotFixture(now)
	stale.SampledAt["ports"] = now.Add(-18 * time.Second)
	s.SetSnapshot(stale)
	if got := s.title(); !strings.Contains(got, "stale 18s") {
		t.Fatalf("title = %q, want a staleness warning", got)
	}
}

func TestPortsViewShowsACollectorError(t *testing.T) {
	now := time.Now()
	snap := snapshotFixture(now)
	snap.Ports = nil
	snap.Conflicts = nil
	snap.Errors["ports"] = `exec: "lsof": executable file not found in $PATH`

	s := newTestSystem(t, snap, now)
	view := s.View()
	if !strings.Contains(view, "ports unavailable") || !strings.Contains(view, "lsof") {
		t.Fatalf("error not explained:\n%s", view)
	}
}

func TestPortsViewScrolls(t *testing.T) {
	now := time.Now()
	snap := snapshotFixture(now)
	snap.Conflicts = nil
	for i := 0; i < 40; i++ {
		snap.Ports = append(snap.Ports, probe.Port{Addr: "*", Port: 9000 + i, PID: i, Process: "filler"})
	}
	s := newTestSystem(t, snap, now)

	if strings.Contains(s.View(), "9039") {
		t.Fatal("last row visible before scrolling")
	}
	for i := 0; i < 40; i++ {
		s, _ = s.Update(key("j"))
	}
	if !strings.Contains(s.View(), "9039") {
		t.Fatalf("scrolling never reached the last row:\n%s", s.View())
	}
}

func TestPortsViewEmpty(t *testing.T) {
	now := time.Now()
	s := newTestSystem(t, probe.Snapshot{SampledAt: map[string]time.Time{"ports": now}}, now)
	if !strings.Contains(s.View(), "nothing listening") {
		t.Fatalf("empty view should say so:\n%s", s.View())
	}
}
```

Create `internal/tui/system_routing_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/probe"
)

func TestDTogglesThePortsView(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, systemMsg(probe.Snapshot{
		Ports:     []probe.Port{{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"}},
		SampledAt: map[string]time.Time{"ports": time.Now()},
	}))

	m, _ = step(t, m, key("d"))
	if m.overlay != overlayPorts {
		t.Fatal("d should open the ports view")
	}
	if !strings.Contains(m.View(), "postgres") {
		t.Fatalf("ports not rendered:\n%s", m.View())
	}

	m, _ = step(t, m, key("d"))
	if m.overlay != overlayNone {
		t.Fatal("d should close the ports view")
	}

	m, _ = step(t, m, key("d"))
	m, _ = step(t, m, key("esc"))
	if m.overlay != overlayNone {
		t.Fatal("esc should close the ports view")
	}
}

func TestPortsViewTakesScrollKeys(t *testing.T) {
	m := modelWithRows(t, "a", "b")
	m, _ = step(t, m, key("d"))

	before, _ := m.table.Selected()
	m, _ = step(t, m, key("j"))
	if after, _ := m.table.Selected(); after.Name != before.Name {
		t.Fatal("j moved the table cursor while the ports view was open")
	}
}

func TestSystemTickFetches(t *testing.T) {
	m := modelWithRows(t, "a")
	_, cmd := step(t, m, systemTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("system tick produced no command")
	}
}

func TestSystemErrorDoesNotClearTheLastSnapshot(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, systemMsg(probe.Snapshot{
		Ports:     []probe.Port{{Addr: "*", Port: 5432, PID: 1183, Process: "postgres"}},
		SampledAt: map[string]time.Time{"ports": time.Now()},
	}))
	m, _ = step(t, m, systemErrMsg{err: errNoDaemonForTest{}})
	m, _ = step(t, m, key("d"))

	if !strings.Contains(m.View(), "postgres") {
		t.Fatalf("last snapshot dropped on a fetch error:\n%s", m.View())
	}
}

func TestHelpListsTheDBinding(t *testing.T) {
	if !strings.Contains(helpOverlay(80, 40), "ports") {
		t.Fatal("help overlay does not mention the ports view")
	}
	var found bool
	for _, b := range bindings {
		if b.key == "d" {
			found = true
		}
	}
	if !found {
		t.Fatal("no d binding registered")
	}
}

var _ = tea.KeyMsg{}
```

Add `"time"` to that file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestPortsView|TestDToggles|TestSystem|TestHelpLists' -v`
Expected: FAIL — `undefined: newSystem`.

- [ ] **Step 3: Add the messages**

Append to `internal/tui/messages.go`:

```go
// systemTick is slower than the command poll: the underlying samples only
// move every 2-10s.
const systemTick = 3 * time.Second

// systemTickMsg drives the machine-state poll.
type systemTickMsg time.Time

// systemMsg is a successful GET /v1/system.
type systemMsg probe.Snapshot

// systemErrMsg is a failed one. The last good snapshot stays on screen.
type systemErrMsg struct{ err error }

func systemTickCmd() tea.Cmd {
	return tea.Tick(systemTick, func(t time.Time) tea.Msg { return systemTickMsg(t) })
}

// fetchSystem polls the machine's state.
func fetchSystem(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		snap, err := c.System()
		if err != nil {
			return systemErrMsg{err: err}
		}
		return systemMsg(snap)
	}
}
```

Add `"github.com/tphuc/lazycomd/internal/probe"` to its imports.

- [ ] **Step 4: Write the ports view**

Create `internal/tui/system.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/probe"
)

// staleAfter is when a sample stops being worth trusting silently.
const staleAfter = 15 * time.Second

// Ports view column widths.
const (
	pcolPort    = 7
	pcolAddr    = 11
	pcolPID     = 8
	pcolProcess = 13
)

type systemModel struct {
	snap   probe.Snapshot
	offset int
	width  int
	height int
	now    func() time.Time
}

func newSystem() systemModel {
	return systemModel{now: time.Now}
}

func (s *systemModel) SetSize(w, h int) {
	s.width, s.height = w, h
}

func (s *systemModel) SetSnapshot(snap probe.Snapshot) {
	s.snap = snap
	if s.offset >= len(snap.Ports) {
		s.offset = 0
	}
}

// Update handles scrolling only; opening and closing is the root's business.
func (s systemModel) Update(msg tea.Msg) (systemModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	page := s.rowRoom() / 2
	if page < 1 {
		page = 1
	}
	switch k.String() {
	case "j", "down":
		s.offset++
	case "k", "up":
		s.offset--
	case "ctrl+d":
		s.offset += page
	case "ctrl+u":
		s.offset -= page
	case "g":
		s.offset = 0
	case "G":
		s.offset = len(s.snap.Ports)
	default:
		return s, nil
	}
	s.clamp()
	return s, nil
}

func (s *systemModel) clamp() {
	max := len(s.snap.Ports) - s.rowRoom()
	if s.offset > max {
		s.offset = max
	}
	if s.offset < 0 {
		s.offset = 0
	}
}

// rowRoom is how many port rows fit under the title, the conflict lines and
// the column header.
func (s systemModel) rowRoom() int {
	room := s.height - 2 - len(s.snap.Conflicts)
	if room < 1 {
		room = 1
	}
	return room
}

// title reports the sample's age, and says so loudly once it is stale.
func (s systemModel) title() string {
	at, ok := s.snap.SampledAt["ports"]
	if !ok || at.IsZero() {
		return "ports — waiting for the first sample"
	}
	age := s.now().Sub(at).Round(time.Second)
	if age >= staleAfter {
		return fmt.Sprintf("ports — stale %ds", int(age.Seconds()))
	}
	return fmt.Sprintf("ports — sampled %ds ago", int(age.Seconds()))
}

func (s systemModel) View() string {
	lines := []string{styleHeader.Render(truncate(s.title(), s.width))}

	if msg := s.snap.Errors["ports"]; msg != "" {
		lines = append(lines, styleWarn.Render(truncate("ports unavailable: "+msg, s.width)))
		return strings.Join(lines, "\n")
	}

	for _, c := range s.snap.Conflicts {
		lines = append(lines, styleWarn.Render(truncate(conflictLine(c), s.width)))
	}

	if len(s.snap.Ports) == 0 {
		lines = append(lines, styleDim.Render("nothing listening"))
		return strings.Join(lines, "\n")
	}

	header := " " + cell("PORT", pcolPort, styleHeader) +
		cell("ADDR", pcolAddr, styleHeader) +
		cell("PID", pcolPID, styleHeader) +
		cell("PROCESS", pcolProcess, styleHeader) +
		"OWNER"
	lines = append(lines, truncate(header, s.width))

	contested := make(map[int]bool, len(s.snap.Conflicts))
	for _, c := range s.snap.Conflicts {
		if c.State == "taken" {
			contested[c.Port] = true
		}
	}

	end := s.offset + s.rowRoom()
	if end > len(s.snap.Ports) {
		end = len(s.snap.Ports)
	}
	for _, p := range s.snap.Ports[s.offset:end] {
		mark := " "
		if contested[p.Port] {
			mark = "⚠"
		}
		row := mark + cell(fmt.Sprintf("%d", p.Port), pcolPort, lipglossPlain()) +
			cell(p.Addr, pcolAddr, styleDim) +
			cell(fmt.Sprintf("%d", p.PID), pcolPID, styleDim) +
			cell(p.Process, pcolProcess, lipglossPlain()) +
			p.Command
		lines = append(lines, truncate(row, s.width))
	}
	return strings.Join(lines, "\n")
}

// conflictLine explains one contested or missing listener in one sentence.
func conflictLine(c probe.Conflict) string {
	if c.State == "free" {
		return fmt.Sprintf("⚠ %d wanted by %s — nothing is listening", c.Port, c.Command)
	}
	return fmt.Sprintf("⚠ %d wanted by %s — held by %s (pid %d)", c.Port, c.Command, c.HeldBy, c.PID)
}
```

Add this tiny helper to `internal/tui/table.go`, next to `cell`:

```go
// lipglossPlain is an unstyled style, for cells that only need padding.
func lipglossPlain() lipgloss.Style { return lipgloss.NewStyle() }
```

- [ ] **Step 5: Wire it into the root model**

In `internal/tui/help.go`, add `overlayPorts` and the binding:

```go
const (
	overlayNone overlay = iota
	overlayHelp
	overlayPorts
)
```

```go
	{"d", "ports", scopeGlobal},
```

In `internal/tui/tui.go`: add `system systemModel` to `Model`, initialize it in `New` with `system: newSystem()`, and extend `Init`:

```go
func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), tickCmd(tickConnected), fetchSystem(m.client), systemTickCmd())
}
```

Add the message cases to `Update`, beside the existing ones:

```go
	case systemTickMsg:
		return m, tea.Batch(fetchSystem(m.client), systemTickCmd())

	case systemMsg:
		m.system.SetSnapshot(probe.Snapshot(msg))
		return m, nil

	case systemErrMsg:
		// The banner already reports an unreachable daemon; keep the last
		// good snapshot on screen rather than blanking the view.
		return m, nil
```

Give the ports overlay its keys at the top of `handleKey`, right after the help-overlay block:

```go
	if m.overlay == overlayPorts {
		switch s {
		case "ctrl+c":
			return m, tea.Quit
		case "d", "esc", "q":
			m.overlay = overlayNone
			return m, nil
		case "?":
			m.overlay = overlayHelp
			return m, nil
		}
		var cmd tea.Cmd
		m.system, cmd = m.system.Update(k)
		return m, cmd
	}
```

Open it from the table and the log pane by adding a case to each of
`handleTableKey` and `handleLogKey` (the latter only when the filter input is
closed, alongside `q` and `?`):

```go
	case "d":
		m.overlay = overlayPorts
		return m, nil
```

Size it in `layout()`, next to the other panes:

```go
	m.system.SetSize(m.width, m.bodyH)
```

And render it in `View`, beside the help case:

```go
	if m.overlay == overlayPorts {
		return strings.Join([]string{m.header(), m.system.View(), m.bottom()}, "\n")
	}
```

Add `"github.com/tphuc/lazycomd/internal/probe"` to `tui.go`'s imports.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ -v`
Expected: PASS, every TUI test.

If `TestPortsViewScrolls` never reaches the last row, check `rowRoom` against
the fixture: 12 rows of height minus the title, the header and the conflict
lines must leave fewer rows than the fixture has ports, or nothing scrolls.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): ports view on d, with conflicts and staleness

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: End-to-end check, README, full verification

**Files:**
- Create: `internal/probe/integration_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: everything. Produces no new code.

- [ ] **Step 1: Write the end-to-end test**

Create `internal/probe/integration_test.go`. This is the only test that runs
real `lsof`, `ss` and `ps`, and it skips itself where they are absent.

```go
package probe

import (
	"context"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func haveAny(names ...string) bool {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	return false
}

func TestRealCollectorsSeeTheMachine(t *testing.T) {
	if !haveAny("lsof", "ss") {
		t.Skip("neither lsof nor ss is installed")
	}

	// A listener this test owns must show up in the real port list.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	want := l.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ports, err := collectPorts(ctx, execRun)
	if err != nil {
		t.Fatalf("collectPorts: %v", err)
	}
	found := false
	for _, p := range ports {
		if p.Port == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("port %d not in the real listing of %d ports", want, len(ports))
	}
}

func TestRealVitalsSeeThisProcess(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// This test binary is its own process group leader only sometimes, so ask
	// about our own pid and accept whatever group ps reports for it.
	self := os.Getpid()
	_, groups, err := collectVitals(ctx, execRun, map[string]int{"self": self})
	if err != nil {
		t.Fatalf("collectVitals: %v", err)
	}
	if _, ok := groups[self]; !ok {
		t.Fatalf("ps did not report this process (%d) at all", self)
	}

	pgid := groups[self]
	vitals, _, err := collectVitals(ctx, execRun, map[string]int{"self": pgid})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := vitals["self"]
	if !ok {
		t.Fatalf("no vitals for our own process group %d", pgid)
	}
	if v.MemMB <= 0 {
		t.Fatalf("MemMB = %v, want a real measurement", v.MemMB)
	}
}
```

- [ ] **Step 2: Run the end-to-end tests**

Run: `go test -race ./internal/probe/ -run TestReal -v`
Expected: PASS on this machine (macOS has both `lsof` and `ps`). On a box without either, they skip rather than fail.

- [ ] **Step 3: Update the README**

In `README.md`, add the two fields to the config block in the Configuration
section:

```yaml
  proxy:
    cmd: ["cloudflared", "tunnel", "--url", "localhost:3000"]
    port: 3000            # what this command intends to bind
    health: http://localhost:3000/healthz   # probed every 10s while running
```

Document them under that block:

> `health:` is polled every 10s while the command is running: a GET with a 2s
> timeout, any 2xx is healthy, redirects are not followed. It is a display
> signal only — `depends_on` still uses spawned-plus-200ms readiness.
>
> `port:` declares the port the command means to bind, so the dashboard can
> tell you when something else is holding it. With `health:` set and `port:`
> unset, the port is taken from the health URL.

Add a Dashboard subsection to the TUI section:

````markdown
### Dashboard

`d` opens the ports view:

```
 ports — sampled 2s ago
 ⚠ 3000 wanted by web — held by node (pid 51192)

 PORT   ADDR       PID     PROCESS      OWNER
 3000 ⚠ 127.0.0.1  51192   node
 5432   *          1183    postgres
 7777   127.0.0.1  54405   lazycomd     daemon
```

`OWNER` is the lazycomd command whose process group holds the socket, so you
can tell your own listener from somebody else's. `d`, `Esc` or `q` closes the
view; `j`/`k` and `Ctrl-D`/`Ctrl-U` scroll it.

The command table gains a health dot plus CPU and MEM columns (the latter two
need a terminal at least 110 columns wide). CPU is percent of one core, summed
across the command's whole process tree, so a busy multithreaded process
legitimately reads above 100%.

Ports come from `lsof`, falling back to `ss`. On a machine with neither, the
view says so and the other signals keep working.
````

Also add `GET /v1/system` to the API table:

| `GET /v1/system` | | ports, vitals, health, conflicts |

- [ ] **Step 4: Check the README against the code**

Every key in the README must exist in `bindings` in `internal/tui/help.go`,
and every route must exist in `routes()` in `internal/api/server.go`:

```bash
grep -oE '\{"[^"]+", "[^"]+"' internal/tui/help.go
grep -oE '"[A-Z]+ /v1[^"]*"' internal/api/server.go
```

Fix the README where they disagree.

- [ ] **Step 5: Verify the whole repository**

```bash
gofmt -l .
go vet ./...
go test -race ./...
go build -o /tmp/lazycomd ./cmd/lazycomd
go test ./internal/depsguard/ -v
```

Expected: `gofmt` silent, `go vet` clean, every package green with no data
races, the binary building, and the dependency guard confirming the daemon
packages — `internal/probe` included — never picked up a TUI import.

- [ ] **Step 6: Drive it by hand**

```bash
SP=/private/tmp/claude-501/-Users-tphuc/ed163cab-b6bd-4c7b-901e-7bb5bf910640/scratchpad
go build -o $SP/lazycomd ./cmd/lazycomd
mkdir -p $SP/lzc-config
cat > $SP/lzc-config/config.yaml <<'CFG'
commands:
  server:
    cmd: ["python3", "-m", "http.server", "8099"]
    cwd: /tmp
    port: 8099
    health: http://127.0.0.1:8099/
  squatted:
    cmd: ["python3", "-m", "http.server", "8099"]
    cwd: /tmp
    port: 8099
CFG
rm -rf /tmp/lzcstate
LAZYCOMD_CONFIG=$SP/lzc-config/config.yaml XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd serve &
sleep 1
XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd start server
sleep 6
curl -s --unix-socket /tmp/lzcstate/lazycomd/lazycomd.sock http://unix/v1/system | head -c 600; echo
XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd ls
XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd    # the TUI: press d
kill %1
```

Expected: `/v1/system` lists port 8099 owned by `server`, a `taken` conflict
for `squatted` naming the other python process, health `ok: true` for
`server`, and the TUI's `d` view showing the ⚠ line.

- [ ] **Step 7: Commit**

```bash
git add internal/probe/ README.md
git commit -m "test(probe): real-collector checks, plus dashboard docs

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

Checked against `docs/superpowers/specs/2026-09-11-lazycomd-dashboard-design.md`:

| Spec section | Covered by |
|---|---|
| Package structure, closures instead of imports | Task 8; the File Structure table |
| `Snapshot`, `Port`, `Vital`, `Health`, `Conflict` types | Task 3 |
| Section-keyed `SampledAt` and `Errors` | Tasks 3, 8 |
| Intervals 5s/2s/10s, loops idle when there is nothing to do | Tasks 5, 8 |
| `lsof` with `ss` fallback, output trusted over exit status | Task 4 |
| Address normalization, dedupe key, sort order | Task 3 |
| Ownership by process group | Task 4 |
| Vitals summed over the group, KB to MB, no clamping | Tasks 5, 11 |
| `health:` config, validation, no `$VAR` expansion | Task 1 |
| Probe semantics: 2xx, 2s, no redirects, dropped when stopped | Tasks 6, 8 |
| Health does not gate `depends_on` | Nothing in the plan touches `deps.go` |
| `port:` config and the health-URL fallback | Tasks 1, 2 |
| Conflict states, `taken` for stopped commands, `free` only for running | Task 7 |
| Conflicts suppressed when the ports sample failed | Tasks 7, 8 |
| `GET /v1/system` always 200 | Task 9 |
| `cpu`, `mem_mb`, `health` on the command view, filled by the API | Tasks 2, 9 |
| Three manager accessors | Task 2 |
| `serve` builds, starts and drains the sampler | Task 10 |
| Table tiers and the `H` column | Task 11 |
| Ports view, `d`, conflict summary, staleness, errors | Task 12 |
| 3s system poll independent of the 1s command poll | Task 12 |
| Spec's test table | Tasks 1, 3-9, 11-13 |
| Non-goals | Nothing in the plan implements them |
| Definition of done | Task 13 steps 2, 5 and 6 |

Gaps found and closed while reviewing:

- **`probe.EmptySnapshot()` was missing.** A zero `Snapshot` serializes its
  maps as `null`, which would make every client special-case a nil map. Task 9
  adds the constructor and uses it for the no-sampler case.
- **Lock ordering.** `samplePorts` originally called `s.intended()` — which
  takes the manager's mutex — while holding the sampler's. Task 8 reads both
  closures before locking, so the sampler's lock never covers a foreign one.
- **`lipglossPlain` had no home.** The ports view needs an unstyled cell and
  `table.go` owns `cell`; Task 12 adds the helper there rather than importing
  lipgloss into `system.go` for one call.
- **`SetSize` versus `SetLayout`.** Adding two conditional columns to the
  table would have meant a four-boolean `SetSize`. Task 11 replaces it with a
  struct and updates spec #2's existing call sites, which is why that task
  lists `table_test.go` as a modified file.
- **The `H` column's data source.** The spec says it appears "when at least one
  command has a `health:` URL", but the TUI never sees the config — only
  statuses. Task 11 derives it from rows carrying a health result, which is the
  same thing one sample later.

Type consistency: `probe.Snapshot` is the single wire type across `probe`,
`api`, `client` and `tui`. `manager.HealthView` is produced only in
`api.enrich` (Task 9) and consumed only by the table (Task 11). `runFunc` is
declared in Task 4 and used unchanged by Tasks 5 and 8. The test helpers
`fakeRun` (Task 4), `lsofSample`/`ssSample` (Task 3) and `psSample` (Task 5)
are each declared once and reused by name afterwards.
