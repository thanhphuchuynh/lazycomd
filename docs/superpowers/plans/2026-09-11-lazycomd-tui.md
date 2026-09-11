# lazycomd TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the interactive terminal UI for lazycomd: a command table beside a live log pane, lifecycle actions on single keys, a fuzzy palette, a log filter, help, and reconnection — all over the daemon's existing HTTP API.

**Architecture:** One `bubbletea` root model in a new `internal/tui` package, composed of three sub-models (hand-written table, `bubbles/viewport` log pane, `bubbles/textinput` palette). Status arrives from a 1s `tea.Tick` that fetches `GET /v1/commands`; log lines arrive from one long-lived SSE goroutine for the selected command that calls `program.Send`. No daemon code changes and no new endpoints.

**Tech Stack:** Go 1.22+, `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles`, `github.com/charmbracelet/lipgloss`, `golang.org/x/term`. Existing: `internal/client`, `internal/manager` (for `Status`).

**Spec:** `docs/superpowers/specs/2026-09-11-lazycomd-tui-design.md`

## Global Constraints

- Spec #1 is already merged on `main`: `internal/{paths,config,logbuf,manager,api,client}` and `cmd/lazycomd` all exist and pass. Read `docs/superpowers/specs/2026-09-11-lazycomd-daemon-design.md` for the API this consumes.
- **Dependency confinement:** `bubbletea`, `bubbles`, `lipgloss` may be imported ONLY from `internal/tui` and `cmd/lazycomd`. Nothing under `internal/config`, `internal/manager`, `internal/logbuf` or `internal/api` may import them, directly or transitively. Task 1 adds the test that enforces this.
- Use the **v1 API** of each Charm library. The bare module paths (`github.com/charmbracelet/bubbletea`, no `/v2`) always resolve to v1.x, so `go get <path>@latest` is correct. `tea.Model` is `Init() tea.Cmd`, `Update(tea.Msg) (tea.Model, tea.Cmd)`, `View() string`.
- `internal/tui` imports `internal/client` and `internal/manager` and nothing else of ours. It never spawns or signals a process — every action is an HTTP call.
- `tui.Run(c *client.Client) error` is the package's only exported function. Everything else is unexported.
- Every test must pass with **no terminal attached** (`go test ./...` in CI): `Update` is a pure function, `View` returns a string. No test may call `tea.NewProgram(...).Run()`.
- Tick cadence: 1s connected, 2s disconnected. Log tail on selection change: 500 lines. Client-side log cap: 5000 lines. Status line lifetime: 4s.
- Layout: table is 40% of width, minimum 32 columns. Below 80 columns the table shows `NAME STATE` only. Below 60 columns the log pane is hidden, focus falls back to the table, and any open filter input closes.
- Keys: `j` `k` `↓` `↑` move, `g`/`G` first/last, `s` start (always with dependencies), `S` stop, `r` restart, `p` palette, `f` follow, `/` filter, `Tab` focus, `?` help, `q`/`Ctrl-C` quit. While an input line is open, `q` types a literal `q`.
- Every task ends with `gofmt -l .` printing nothing, `go vet ./...` clean, and `go test -race ./...` passing.
- Commit messages: imperative subject, `feat(tui):` or `test(tui):` prefix, and the attribution trailer this repo already uses (`Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/depsguard/depsguard_test.go` | Test-only package asserting the daemon packages never gain a TUI dependency |
| `internal/tui/fuzzy.go` | `match` (subsequence scorer) and `rank` (sorted candidates) |
| `internal/tui/messages.go` | Message types and the `tea.Cmd`s that wrap `internal/client` |
| `internal/tui/table.go` | Command table: rows, cursor, selection-by-name, column layout |
| `internal/tui/logs.go` | Log pane: viewport, follow mode, line cap |
| `internal/tui/filter.go` | Log filter state, smart-case matching, pane title |
| `internal/tui/palette.go` | Fuzzy palette overlay |
| `internal/tui/help.go` | Binding table, key bar, help overlay |
| `internal/tui/stream.go` | `sink`, `lineWriter`, SSE goroutine lifecycle |
| `internal/tui/tui.go` | Root `Model`, `Init`/`Update`/`View`, focus, layout, `Run` |
| `internal/tui/testdaemon_test.go` | Shared test helper: real manager + API on a unix socket, real client |
| `cmd/lazycomd/main.go` | Bare invocation launches the TUI when stdout is a terminal |
| `README.md` | Layout block and full keymap |

---

### Task 1: Dependencies and the confinement guard

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/depsguard/depsguard_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: the three Charm modules in `go.mod`, and a test that fails if a daemon package ever imports one.

- [ ] **Step 1: Add the dependencies**

```bash
cd ~/coding/lazycomd
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/bubbles@latest
go get github.com/charmbracelet/lipgloss@latest
go get golang.org/x/term@latest
```

Confirm the major versions are v1 — `grep charmbracelet go.mod` must show no `/v2` suffixes. If a `/v2` path appears, you fetched the wrong module; the bare path is the v1 line.

- [ ] **Step 2: Write the failing test**

Create `internal/depsguard/depsguard_test.go`:

```go
// Package depsguard holds no code. Its test keeps the daemon's dependency
// tree clean: the TUI's terminal libraries must never reach the packages
// that run processes.
package depsguard

import (
	"os/exec"
	"strings"
	"testing"
)

// daemonPkgs are the packages that must stay free of TUI dependencies.
var daemonPkgs = []string{
	"github.com/tphuc/lazycomd/internal/config",
	"github.com/tphuc/lazycomd/internal/manager",
	"github.com/tphuc/lazycomd/internal/logbuf",
	"github.com/tphuc/lazycomd/internal/api",
}

// banned substrings that must not appear in those packages' dependency trees.
var banned = []string{
	"charmbracelet/bubbletea",
	"charmbracelet/bubbles",
	"charmbracelet/lipgloss",
	"muesli/termenv",
}

func TestDaemonPackagesHaveNoTUIDependencies(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	out, err := exec.Command("go", append([]string{"list", "-deps"}, daemonPkgs...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")

	// Sanity check: the list really did resolve our packages.
	var sawYAML bool
	for _, d := range deps {
		if strings.Contains(d, "gopkg.in/yaml.v3") {
			sawYAML = true
		}
		for _, b := range banned {
			if strings.Contains(d, b) {
				t.Errorf("daemon packages depend on %s (via %s)", b, d)
			}
		}
	}
	if !sawYAML {
		t.Fatalf("go list -deps returned %d entries but no yaml.v3; did the package list change?", len(deps))
	}
}
```

- [ ] **Step 3: Run the test to verify it passes for the right reason**

Run: `go test ./internal/depsguard/ -v`
Expected: PASS. It passes now because `internal/tui` does not exist yet — the point is that it keeps passing as the TUI grows. To prove the test actually bites, temporarily add `import _ "github.com/charmbracelet/lipgloss"` to `internal/api/server.go`, re-run, and confirm it FAILS with `daemon packages depend on charmbracelet/lipgloss`. Then remove that import.

- [ ] **Step 4: Confirm nothing else broke**

Run: `go build ./... && go test ./... 2>&1 | tail -8`
Expected: every existing package still passes.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/depsguard/
git commit -m "test(tui): add Charm dependencies and a confinement guard

The daemon packages must keep a yaml.v3-only dependency tree. The guard
runs go list -deps over internal/{config,manager,logbuf,api} and fails if
bubbletea, bubbles, lipgloss or termenv appears.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Fuzzy matcher

**Files:**
- Create: `internal/tui/fuzzy.go`
- Test: `internal/tui/fuzzy_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type candidate struct { name string; score int; positions []int }`
  - `func match(query, target string) (score int, positions []int, ok bool)` — subsequence match, smart-case, greedy left to right.
  - `func rank(query string, names []string) []candidate` — matching names only, sorted by score descending then name ascending. An empty query returns every name in input order with score 0.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/fuzzy_test.go`:

```go
package tui

import (
	"slices"
	"testing"
)

func TestMatchSubsequence(t *testing.T) {
	if _, _, ok := match("api", "app:api"); !ok {
		t.Fatal("api should match app:api")
	}
	if _, _, ok := match("xyz", "app:api"); ok {
		t.Fatal("xyz should not match app:api")
	}
	if _, _, ok := match("ipa", "app:api"); ok {
		t.Fatal("out-of-order runes should not match")
	}
}

func TestMatchPositions(t *testing.T) {
	_, pos, ok := match("app", "app:api")
	if !ok {
		t.Fatal("no match")
	}
	if !slices.Equal(pos, []int{0, 1, 2}) {
		t.Fatalf("positions = %v, want [0 1 2]", pos)
	}
}

func TestMatchSmartCase(t *testing.T) {
	if _, _, ok := match("api", "APP:API"); !ok {
		t.Fatal("lowercase query should match case-insensitively")
	}
	if _, _, ok := match("API", "app:api"); ok {
		t.Fatal("uppercase query should be case-sensitive")
	}
}

func TestMatchEmptyQuery(t *testing.T) {
	score, pos, ok := match("", "anything")
	if !ok || score != 0 || len(pos) != 0 {
		t.Fatalf("empty query = %d, %v, %v; want 0, [], true", score, pos, ok)
	}
}

func TestRankOrdersBestFirst(t *testing.T) {
	got := rank("api", []string{"scraper:api", "app:api", "api", "proxy"})

	var names []string
	for _, c := range got {
		names = append(names, c.name)
	}
	want := []string{"api", "app:api", "scraper:api"}
	if !slices.Equal(names, want) {
		t.Fatalf("rank = %v, want %v", names, want)
	}
}

func TestRankEmptyQueryKeepsInputOrder(t *testing.T) {
	got := rank("", []string{"b", "a"})
	if len(got) != 2 || got[0].name != "b" || got[1].name != "a" {
		t.Fatalf("rank = %+v, want input order", got)
	}
}

func TestRankTiesBreakByName(t *testing.T) {
	got := rank("x", []string{"bx", "ax"})
	if len(got) != 2 {
		t.Fatalf("rank = %+v, want 2 matches", got)
	}
	if got[0].score == got[1].score && got[0].name != "ax" {
		t.Fatalf("equal scores should sort by name, got %v then %v", got[0].name, got[1].name)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -v`
Expected: FAIL — `undefined: match`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/fuzzy.go`:

```go
package tui

import (
	"sort"
	"strings"
	"unicode"
)

// candidate is one palette entry that matched the query.
type candidate struct {
	name      string
	score     int
	positions []int
}

// Scoring weights. Consecutive runs and segment starts are what make a
// match feel right; everything else is a tiebreak.
const (
	consecutiveBonus = 15
	segmentBonus     = 10
)

// match reports whether query is a subsequence of target and scores the
// match. Smart-case: an all-lowercase query matches case-insensitively.
//
// ponytail: greedy left-to-right, not an optimal alignment. For command
// names a few dozen runes long nobody can tell the difference.
func match(query, target string) (int, []int, bool) {
	if query == "" {
		return 0, nil, true
	}
	cased := query
	hay := target
	if strings.ToLower(query) == query {
		hay = strings.ToLower(target)
	}

	q := []rune(cased)
	h := []rune(hay)
	positions := make([]int, 0, len(q))

	score, qi := 0, 0
	for hi := 0; hi < len(h) && qi < len(q); hi++ {
		if h[hi] != q[qi] {
			continue
		}
		if len(positions) > 0 && positions[len(positions)-1] == hi-1 {
			score += consecutiveBonus
		}
		if hi == 0 || isSegmentBreak(h[hi-1]) {
			score += segmentBonus
		}
		positions = append(positions, hi)
		qi++
	}
	if qi < len(q) {
		return 0, nil, false
	}

	// Prefer an earlier first match and a shorter target.
	score -= positions[0]
	score -= len(h) - len(q)
	return score, positions, true
}

func isSegmentBreak(r rune) bool {
	switch r {
	case ':', '-', '_', '.', '/', ' ':
		return true
	}
	return unicode.IsDigit(r)
}

// rank returns the matching names, best first. An empty query keeps the
// input order so the palette opens as a plain list.
func rank(query string, names []string) []candidate {
	out := make([]candidate, 0, len(names))
	for _, n := range names {
		score, pos, ok := match(query, n)
		if !ok {
			continue
		}
		out = append(out, candidate{name: n, score: score, positions: pos})
	}
	if query == "" {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})
	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/tui/ -v`
Expected: PASS, seven tests. If `TestRankOrdersBestFirst` fails, print the three scores and check the weights before changing the expectation — the intended order is exact match, then segment-start match, then mid-word match.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): subsequence matcher and ranking for the palette

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Messages, commands and the shared test daemon

**Files:**
- Create: `internal/tui/messages.go`, `internal/tui/testdaemon_test.go`
- Test: `internal/tui/messages_test.go`

**Interfaces:**
- Consumes: `client.Client` methods `List`, `Logs`, `Start`, `Stop`, `Restart`; `manager.Status`.
- Produces:
  - Message types: `tickMsg time.Time`; `statusMsg []manager.Status`; `statusErrMsg{err error}`; `logTailMsg{name string; lines []string}`; `logLineMsg{name, line string}`; `streamEndedMsg{name string; err error}`; `actionDoneMsg{verb, name string; st manager.Status; err error}`.
  - Constants `tickConnected = time.Second`, `tickDisconnected = 2 * time.Second`, `tailLines = 500`, `maxLogLines = 5000`, `statusLife = 4 * time.Second`.
  - Commands: `tickCmd(d time.Duration) tea.Cmd`, `fetchStatus(c *client.Client) tea.Cmd`, `fetchTail(c *client.Client, name string) tea.Cmd`, `doAction(c *client.Client, verb, name string) tea.Cmd`.
  - Test helper (test files only): `testDaemon(t *testing.T, cmds map[string]config.Command) (*client.Client, *manager.Manager)`.

- [ ] **Step 1: Write the shared test helper**

Create `internal/tui/testdaemon_test.go`. This is the same pattern the client tests already use — a real manager and API over a unix socket, driven by the real client, so no mocks anywhere in this package.

```go
package tui

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/api"
	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// testDaemon runs a real manager and API on a unix socket and returns a
// client for it plus the manager, so a test can assert daemon-side state.
func testDaemon(t *testing.T, cmds map[string]config.Command) (*client.Client, *manager.Manager) {
	t.Helper()
	m := manager.New(&config.Config{Commands: cmds}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	m.SettleDelay = 5 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.NewServer(m, "", func() (*config.Config, error) {
		return &config.Config{Commands: cmds}, nil
	})

	// Not t.TempDir(): macOS caps a unix socket path at 104 bytes and the
	// per-test temp path plus a long test name overruns it.
	dir, err := os.MkdirTemp("/tmp", "lzc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	c, err := client.New("unix://"+sock, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, m
}

// sleeper is a command that stays up until stopped.
func sleeper() config.Command {
	return config.Command{Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}
}

// echoer prints one line and stays up.
func echoer() config.Command {
	return config.Command{Cmd: []string{"sh", "-c", "echo hi; sleep 30"}, Cwd: "/tmp"}
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/tui/messages_test.go`:

```go
package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

func TestFetchStatusReturnsStatusMsg(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{"a": sleeper()})

	msg := fetchStatus(c)()
	list, ok := msg.(statusMsg)
	if !ok {
		t.Fatalf("msg = %T, want statusMsg", msg)
	}
	if len(list) != 1 || list[0].Name != "a" || list[0].State != manager.Stopped {
		t.Fatalf("statusMsg = %+v", list)
	}
}

func TestFetchStatusReportsNoDaemon(t *testing.T) {
	c, err := client.New("unix:///tmp/lzc-absent-xyz.sock", "")
	if err != nil {
		t.Fatal(err)
	}
	msg := fetchStatus(c)()
	e, ok := msg.(statusErrMsg)
	if !ok {
		t.Fatalf("msg = %T, want statusErrMsg", msg)
	}
	if !errors.Is(e.err, client.ErrNoDaemon) {
		t.Fatalf("err = %v, want ErrNoDaemon", e.err)
	}
}

func TestFetchTailReturnsLines(t *testing.T) {
	c, m := testDaemon(t, map[string]config.Command{"a": echoer()})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool {
		b, err := m.Logs("a")
		return err == nil && len(b.Tail(1)) == 1
	})

	msg := fetchTail(c, "a")()
	tail, ok := msg.(logTailMsg)
	if !ok {
		t.Fatalf("msg = %T, want logTailMsg", msg)
	}
	if tail.name != "a" || len(tail.lines) == 0 || tail.lines[0] != "hi" {
		t.Fatalf("logTailMsg = %+v", tail)
	}
}

func TestFetchTailOnErrorStillNamesTheCommand(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{})
	msg := fetchTail(c, "ghost")()
	tail, ok := msg.(logTailMsg)
	if !ok {
		t.Fatalf("msg = %T, want logTailMsg", msg)
	}
	if tail.name != "ghost" || len(tail.lines) != 1 {
		t.Fatalf("logTailMsg = %+v, want one error line for ghost", tail)
	}
}

func TestDoActionStartsWithDeps(t *testing.T) {
	c, m := testDaemon(t, map[string]config.Command{
		"db":  sleeper(),
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}},
	})

	msg := doAction(c, "start", "api")()
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want actionDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("err = %v", done.err)
	}
	if done.verb != "start" || done.name != "api" || done.st.State != manager.Running {
		t.Fatalf("actionDoneMsg = %+v", done)
	}
	st, err := m.Status("db")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != manager.Running {
		t.Fatalf("db state = %q, want running: start must pass with_deps", st.State)
	}
}

func TestDoActionCarriesTheError(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{"a": sleeper()})
	if msg := doAction(c, "start", "a")(); msg.(actionDoneMsg).err != nil {
		t.Fatal("first start failed")
	}
	done := doAction(c, "start", "a")().(actionDoneMsg)
	if done.err == nil {
		t.Fatal("second start err = nil, want a 409")
	}
	var apiErr *client.APIError
	if !errors.As(done.err, &apiErr) || apiErr.Status != 409 {
		t.Fatalf("err = %v, want a 409 APIError", done.err)
	}
}

func TestDoActionStopAndRestart(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{"a": sleeper()})
	if done := doAction(c, "start", "a")().(actionDoneMsg); done.err != nil {
		t.Fatal(done.err)
	}
	if done := doAction(c, "restart", "a")().(actionDoneMsg); done.err != nil || done.st.State != manager.Running {
		t.Fatalf("restart = %+v", done)
	}
	if done := doAction(c, "stop", "a")().(actionDoneMsg); done.err != nil || done.st.State != manager.Stopped {
		t.Fatalf("stop = %+v", done)
	}
}

func TestTickCmdReturnsTickMsg(t *testing.T) {
	msg := tickCmd(time.Millisecond)()
	if _, ok := msg.(tickMsg); !ok {
		t.Fatalf("msg = %T, want tickMsg", msg)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestFetch|TestDoAction|TestTick' -v`
Expected: FAIL — `undefined: fetchStatus`.

- [ ] **Step 4: Write the implementation**

Create `internal/tui/messages.go`:

```go
// Package tui is lazycomd's terminal UI. It drives the daemon entirely
// through internal/client; it never spawns or signals a process.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/manager"
)

// Timings and limits. All of them are spec values, not taste.
const (
	tickConnected    = time.Second
	tickDisconnected = 2 * time.Second
	tailLines        = 500
	maxLogLines      = 5000
	statusLife       = 4 * time.Second
)

// tickMsg drives the status poll.
type tickMsg time.Time

// statusMsg is a successful GET /v1/commands.
type statusMsg []manager.Status

// statusErrMsg is a failed status poll: the daemon is unreachable or broken.
type statusErrMsg struct{ err error }

// logTailMsg seeds the log pane when the selection changes.
type logTailMsg struct {
	name  string
	lines []string
}

// logLineMsg is one live line from the SSE stream. It carries the command
// name because lines can arrive after the selection has moved on.
type logLineMsg struct {
	name string
	line string
}

// streamEndedMsg says the SSE stream closed on its own.
type streamEndedMsg struct {
	name string
	err  error
}

// actionDoneMsg is the result of a lifecycle key.
type actionDoneMsg struct {
	verb string
	name string
	st   manager.Status
	err  error
}

// tickCmd schedules the next poll.
func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// fetchStatus polls every command's state.
func fetchStatus(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		list, err := c.List()
		if err != nil {
			return statusErrMsg{err: err}
		}
		return statusMsg(list)
	}
}

// fetchTail loads the scrollback for one command. A failure becomes a single
// visible line rather than an error path: the pane always has content.
func fetchTail(c *client.Client, name string) tea.Cmd {
	return func() tea.Msg {
		lines, err := c.Logs(name, tailLines)
		if err != nil {
			return logTailMsg{name: name, lines: []string{"lazycomd: " + err.Error()}}
		}
		return logTailMsg{name: name, lines: lines}
	}
}

// doAction runs one lifecycle verb. start always passes with_deps: in a TUI
// that is what the keystroke means.
func doAction(c *client.Client, verb, name string) tea.Cmd {
	return func() tea.Msg {
		var (
			st  manager.Status
			err error
		)
		switch verb {
		case "start":
			st, err = c.Start(name, true)
		case "stop":
			st, err = c.Stop(name)
		case "restart":
			st, err = c.Restart(name)
		}
		return actionDoneMsg{verb: verb, name: name, st: st, err: err}
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ -v`
Expected: PASS, all fuzzy and message tests.

- [ ] **Step 6: Confirm the guard still holds**

Run: `go test ./internal/depsguard/ -v`
Expected: PASS — `internal/tui` now imports bubbletea, and the daemon packages must still be clean.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): message types and client-backed commands

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 4: Command table

**Files:**
- Create: `internal/tui/table.go`
- Test: `internal/tui/table_test.go`

**Interfaces:**
- Consumes: `manager.Status`, `manager.State` constants.
- Produces:
  - `type tableModel struct` with `newTable() tableModel`.
  - `(*tableModel).SetSize(w, h int, compact bool)`, `(*tableModel).SetRows(rows []manager.Status)`, `(*tableModel).SelectName(name string)`, `(*tableModel).MergeStatus(st manager.Status)`.
  - `(tableModel).Selected() (manager.Status, bool)`, `(tableModel).Names() []string`, `(tableModel).Update(msg tea.Msg) (tableModel, tea.Cmd)`, `(tableModel).View() string`.
  - Shared helpers other files use: `styleHeader`, `styleDim`, `stateStyles`, `cell(s string, w int, st lipgloss.Style) string`, `truncate(s string, w int) string`, `formatUptime(sec float64) string`.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/table_test.go`:

```go
package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/manager"
)

func rows(names ...string) []manager.Status {
	out := make([]manager.Status, 0, len(names))
	for _, n := range names {
		out = append(out, manager.Status{Name: n, State: manager.Stopped})
	}
	return out
}

func key(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestSetRowsKeepsSelectionByName(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b", "c"))

	tbl, _ = tbl.Update(key("j")) // select b
	if got, _ := tbl.Selected(); got.Name != "b" {
		t.Fatalf("selected = %q, want b", got.Name)
	}

	// A new command sorts in ahead of b; the cursor must follow b, not index.
	tbl.SetRows(rows("a", "aa", "b", "c"))
	got, ok := tbl.Selected()
	if !ok || got.Name != "b" {
		t.Fatalf("selected = %q, want b after rows changed", got.Name)
	}
}

func TestSetRowsClampsWhenSelectionDisappears(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b", "c"))
	tbl, _ = tbl.Update(key("G")) // select c

	tbl.SetRows(rows("a", "b"))
	got, ok := tbl.Selected()
	if !ok || got.Name != "b" {
		t.Fatalf("selected = %q, want b (clamped)", got.Name)
	}
}

func TestSetRowsEmpty(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a"))
	tbl.SetRows(nil)
	if _, ok := tbl.Selected(); ok {
		t.Fatal("Selected() ok = true on an empty table")
	}
}

func TestCursorRespectsBounds(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b"))

	for i := 0; i < 5; i++ {
		tbl, _ = tbl.Update(key("k"))
	}
	if got, _ := tbl.Selected(); got.Name != "a" {
		t.Fatalf("selected = %q after 5 k, want a", got.Name)
	}
	for i := 0; i < 5; i++ {
		tbl, _ = tbl.Update(key("j"))
	}
	if got, _ := tbl.Selected(); got.Name != "b" {
		t.Fatalf("selected = %q after 5 j, want b", got.Name)
	}
}

func TestArrowKeysMoveToo(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b"))
	tbl, _ = tbl.Update(key("down"))
	if got, _ := tbl.Selected(); got.Name != "b" {
		t.Fatalf("selected = %q, want b", got.Name)
	}
	tbl, _ = tbl.Update(key("up"))
	if got, _ := tbl.Selected(); got.Name != "a" {
		t.Fatalf("selected = %q, want a", got.Name)
	}
}

func TestSelectName(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b", "c"))
	tbl.SelectName("c")
	if got, _ := tbl.Selected(); got.Name != "c" {
		t.Fatalf("selected = %q, want c", got.Name)
	}
	tbl.SelectName("ghost") // no such command: selection unchanged
	if got, _ := tbl.Selected(); got.Name != "c" {
		t.Fatalf("selected = %q, want c", got.Name)
	}
}

func TestMergeStatus(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(60, 10, false)
	tbl.SetRows(rows("a", "b"))
	tbl.MergeStatus(manager.Status{Name: "b", State: manager.Running, PID: 42})

	if !strings.Contains(tbl.View(), "running") {
		t.Fatalf("View missing merged state:\n%s", tbl.View())
	}
	tbl.MergeStatus(manager.Status{Name: "ghost", State: manager.Running}) // must not panic
}

func TestNames(t *testing.T) {
	tbl := newTable()
	tbl.SetRows(rows("a", "b"))
	if got := tbl.Names(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Names = %v", got)
	}
}

func TestViewColumnsFullAndCompact(t *testing.T) {
	tbl := newTable()
	tbl.SetRows([]manager.Status{{Name: "tick", State: manager.Running, PID: 54405, UptimeSec: 133, Restarts: 2}})

	tbl.SetSize(40, 10, false) // 100-col terminal
	full := tbl.View()
	for _, want := range []string{"NAME", "STATE", "PID", "UPTIME", "RS", "tick", "running", "54405", "2m13s"} {
		if !strings.Contains(full, want) {
			t.Fatalf("full view missing %q:\n%s", want, full)
		}
	}

	tbl.SetSize(28, 10, true) // 70-col terminal
	compact := tbl.View()
	if !strings.Contains(compact, "NAME") || !strings.Contains(compact, "STATE") {
		t.Fatalf("compact view missing NAME/STATE:\n%s", compact)
	}
	for _, gone := range []string{"PID", "UPTIME", "RS"} {
		if strings.Contains(compact, gone) {
			t.Fatalf("compact view still has %q:\n%s", gone, compact)
		}
	}
}

func TestViewMarksCursorAndDirtySpec(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(40, 10, false)
	tbl.SetRows([]manager.Status{
		{Name: "a", State: manager.Running, SpecDirty: true},
		{Name: "b", State: manager.Stopped},
	})

	view := tbl.View()
	if !strings.Contains(view, "> a") {
		t.Fatalf("cursor marker missing:\n%s", view)
	}
	if !strings.Contains(view, "running*") {
		t.Fatalf("spec-dirty asterisk missing:\n%s", view)
	}
}

func TestViewScrollsToKeepCursorVisible(t *testing.T) {
	tbl := newTable()
	tbl.SetSize(40, 4, false) // header plus 3 rows
	tbl.SetRows(rows("r0", "r1", "r2", "r3", "r4", "r5"))
	tbl, _ = tbl.Update(key("G"))

	view := tbl.View()
	if !strings.Contains(view, "r5") {
		t.Fatalf("last row not visible after G:\n%s", view)
	}
	if strings.Contains(view, "r0") {
		t.Fatalf("window did not scroll:\n%s", view)
	}
}

func TestFormatUptime(t *testing.T) {
	cases := map[float64]string{
		0:    "-",
		45:   "45s",
		133:  "2m13s",
		3840: "1h4m",
	}
	for in, want := range cases {
		if got := formatUptime(in); got != want {
			t.Fatalf("formatUptime(%v) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestSetRows|TestCursor|TestView|TestFormat' -v`
Expected: FAIL — `undefined: newTable`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/table.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tphuc/lazycomd/internal/manager"
)

// Styles shared across the package.
var (
	styleHeader = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	stateStyles = map[manager.State]lipgloss.Style{
		manager.Running:  lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		manager.Failed:   lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		manager.Starting: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		manager.Stopping: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		manager.Stopped:  lipgloss.NewStyle().Faint(true),
	}
)

// Fixed column widths; the name column takes whatever is left.
const (
	colState  = 10
	colPID    = 7
	colUptime = 8
	colRS     = 3
)

type tableModel struct {
	rows     []manager.Status
	selected string // command name, never an index
	cursor   int
	width    int
	height   int
	compact  bool
}

func newTable() tableModel { return tableModel{} }

func (t *tableModel) SetSize(w, h int, compact bool) {
	t.width, t.height, t.compact = w, h, compact
}

// SetRows replaces every row, keeping the cursor on the same command name.
// A selection that no longer exists clamps to the nearest index.
func (t *tableModel) SetRows(rows []manager.Status) {
	t.rows = rows
	if t.selected != "" {
		for i, r := range rows {
			if r.Name == t.selected {
				t.cursor = i
				return
			}
		}
	}
	t.clampCursor()
}

func (t *tableModel) clampCursor() {
	if len(t.rows) == 0 {
		t.cursor, t.selected = 0, ""
		return
	}
	if t.cursor >= len(t.rows) {
		t.cursor = len(t.rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	t.selected = t.rows[t.cursor].Name
}

func (t tableModel) Selected() (manager.Status, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return manager.Status{}, false
	}
	return t.rows[t.cursor], true
}

// SelectName moves the cursor to name when that command exists.
func (t *tableModel) SelectName(name string) {
	for i, r := range t.rows {
		if r.Name == name {
			t.cursor, t.selected = i, name
			return
		}
	}
}

// MergeStatus updates one row in place, ahead of the next poll.
func (t *tableModel) MergeStatus(st manager.Status) {
	for i, r := range t.rows {
		if r.Name == st.Name {
			t.rows[i] = st
			return
		}
	}
}

func (t tableModel) Names() []string {
	out := make([]string, 0, len(t.rows))
	for _, r := range t.rows {
		out = append(out, r.Name)
	}
	return out
}

// Update handles navigation only. Lifecycle keys live in the root model.
func (t tableModel) Update(msg tea.Msg) (tableModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return t, nil
	}
	switch k.String() {
	case "j", "down":
		t.cursor++
	case "k", "up":
		t.cursor--
	case "g":
		t.cursor = 0
	case "G":
		t.cursor = len(t.rows) - 1
	default:
		return t, nil
	}
	t.clampCursor()
	return t, nil
}

// window returns the visible row range, scrolled to keep the cursor in view.
func (t tableModel) window() (int, int) {
	h := t.height - 1 // the header takes a line
	if h < 1 {
		h = 1
	}
	start := 0
	if t.cursor >= h {
		start = t.cursor - h + 1
	}
	end := start + h
	if end > len(t.rows) {
		end = len(t.rows)
	}
	return start, end
}

func (t tableModel) nameWidth() int {
	w := t.width - 2 - colState // 2 for the cursor marker
	if !t.compact {
		w -= colPID + colUptime + colRS
	}
	if w < 8 {
		w = 8
	}
	return w
}

func (t tableModel) View() string {
	nameW := t.nameWidth()

	var b strings.Builder
	header := "  " + cell("NAME", nameW, styleHeader) + cell("STATE", colState, styleHeader)
	if !t.compact {
		header += cell("PID", colPID, styleHeader) + cell("UPTIME", colUptime, styleHeader) + cell("RS", colRS, styleHeader)
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
		line := marker +
			cell(r.Name, nameW, lipgloss.NewStyle()) +
			cell(state, colState, stateStyles[r.State])
		if !t.compact {
			pid := "-"
			if r.PID > 0 {
				pid = fmt.Sprintf("%d", r.PID)
			}
			line += cell(pid, colPID, styleDim) +
				cell(formatUptime(r.UptimeSec), colUptime, styleDim) +
				cell(fmt.Sprintf("%d", r.Restarts), colRS, styleDim)
		}
		b.WriteString("\n" + line)
	}
	return b.String()
}

// cell renders one fixed-width column: padded when short, cut when long.
func cell(s string, w int, st lipgloss.Style) string {
	return st.Width(w).MaxWidth(w).Render(s)
}

// truncate cuts s to w runes, marking a cut with an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

func formatUptime(sec float64) string {
	if sec <= 0 {
		return "-"
	}
	d := time.Duration(sec * float64(time.Second)).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS. If `TestViewColumnsFullAndCompact` fails because `2m13s` is cut, widen `colUptime` — the column must fit `1h4m` and `2m13s`, not the other way around.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): command table with selection preserved by name

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Log pane with follow mode

**Files:**
- Create: `internal/tui/logs.go`
- Test: `internal/tui/logs_test.go`

**Interfaces:**
- Consumes: `maxLogLines`, `styleHeader`, `truncate`, `bubbles/viewport`.
- Produces:
  - `type logsModel struct` with `newLogs() logsModel`.
  - `(*logsModel).SetSize(w, h int)`, `(*logsModel).Reset(name string, lines []string)`, `(*logsModel).Append(line string)`.
  - `(logsModel).Name() string`, `(logsModel).Following() bool`, `(logsModel).LineCount() int`, `(logsModel).Title() string`, `(logsModel).AtBottom() bool`, `(logsModel).Update(msg tea.Msg) (logsModel, tea.Cmd)`, `(logsModel).View() string`.

Task 6 extends this file with the filter; `visible()` is the single seam it hooks into.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/logs_test.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"testing"
)

func newTestLogs(name string, lines ...string) logsModel {
	l := newLogs()
	l.SetSize(40, 5)
	l.Reset(name, lines)
	return l
}

func TestResetReplacesContentAndFollows(t *testing.T) {
	l := newTestLogs("tick", "one", "two")
	if l.Name() != "tick" {
		t.Fatalf("Name = %q, want tick", l.Name())
	}
	if !l.Following() {
		t.Fatal("Reset must turn follow on")
	}
	if !strings.Contains(l.View(), "two") {
		t.Fatalf("View missing content:\n%s", l.View())
	}

	l.Reset("greet", []string{"other"})
	if strings.Contains(l.View(), "two") {
		t.Fatalf("View still shows the previous command's lines:\n%s", l.View())
	}
}

func TestAppendFollowsBottom(t *testing.T) {
	l := newTestLogs("tick")
	for i := 0; i < 50; i++ {
		l.Append(fmt.Sprintf("line %d", i))
	}
	if !l.AtBottom() {
		t.Fatal("following pane should stay at the bottom")
	}
	if !strings.Contains(l.View(), "line 49") {
		t.Fatalf("newest line not visible:\n%s", l.View())
	}
}

func TestManualScrollStopsFollow(t *testing.T) {
	l := newTestLogs("tick")
	for i := 0; i < 50; i++ {
		l.Append(fmt.Sprintf("line %d", i))
	}

	l, _ = l.Update(key("k"))
	if l.Following() {
		t.Fatal("scrolling up must clear follow")
	}
	l.Append("line 50")
	if strings.Contains(l.View(), "line 50") {
		t.Fatalf("paused pane jumped to the new line:\n%s", l.View())
	}

	l, _ = l.Update(key("f"))
	if !l.Following() {
		t.Fatal("f must restore follow")
	}
	if !strings.Contains(l.View(), "line 50") {
		t.Fatalf("f must jump to the bottom:\n%s", l.View())
	}
}

func TestHalfPageAndJumpKeysClearFollow(t *testing.T) {
	for _, k := range []string{"ctrl+u", "ctrl+d", "g", "G", "j"} {
		l := newTestLogs("tick")
		for i := 0; i < 50; i++ {
			l.Append(fmt.Sprintf("line %d", i))
		}
		l, _ = l.Update(key(k))
		if l.Following() {
			t.Fatalf("%s must clear follow", k)
		}
	}
}

func TestLineCapDropsOldest(t *testing.T) {
	l := newTestLogs("tick")
	for i := 0; i < maxLogLines+10; i++ {
		l.Append(fmt.Sprintf("line %d", i))
	}
	if got := l.LineCount(); got != maxLogLines {
		t.Fatalf("LineCount = %d, want %d", got, maxLogLines)
	}
	if !strings.Contains(l.View(), fmt.Sprintf("line %d", maxLogLines+9)) {
		t.Fatal("newest line missing after the cap kicked in")
	}
}

func TestTitle(t *testing.T) {
	l := newLogs()
	l.SetSize(40, 5)
	if got := l.Title(); !strings.Contains(got, "no command") {
		t.Fatalf("empty Title = %q", got)
	}

	l.Reset("tick", []string{"a", "b"})
	if got := l.Title(); got != "tick — following" {
		t.Fatalf("Title = %q, want \"tick — following\"", got)
	}

	l, _ = l.Update(key("k"))
	if got := l.Title(); !strings.Contains(got, "paused") || !strings.Contains(got, "2 lines") {
		t.Fatalf("paused Title = %q", got)
	}
}

func TestLongLinesAreTruncatedNotWrapped(t *testing.T) {
	l := newLogs()
	l.SetSize(20, 4)
	l.Reset("tick", []string{strings.Repeat("x", 200)})

	for _, line := range strings.Split(l.View(), "\n") {
		if len([]rune(line)) > 20 {
			t.Fatalf("line is %d runes wide, want <= 20: %q", len([]rune(line)), line)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestReset|TestAppend|TestManual|TestHalf|TestLineCap|TestTitle|TestLong' -v`
Expected: FAIL — `undefined: newLogs`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/logs.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type logsModel struct {
	vp     viewport.Model
	name   string
	lines  []string
	follow bool
}

func newLogs() logsModel {
	return logsModel{vp: viewport.New(0, 0), follow: true}
}

func (l *logsModel) SetSize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 2 {
		h = 2
	}
	l.vp.Width = w
	l.vp.Height = h - 1 // the title takes a line
	l.refresh()
}

// Reset points the pane at a different command: new scrollback, follow on.
func (l *logsModel) Reset(name string, lines []string) {
	l.name = name
	l.lines = append([]string(nil), lines...)
	l.capLines()
	l.follow = true
	l.refresh()
	l.vp.GotoBottom()
}

// Append adds one live line, scrolling only while following.
func (l *logsModel) Append(line string) {
	l.lines = append(l.lines, line)
	l.capLines()
	l.refresh()
	if l.follow {
		l.vp.GotoBottom()
	}
}

func (l *logsModel) capLines() {
	if len(l.lines) <= maxLogLines {
		return
	}
	l.lines = append([]string(nil), l.lines[len(l.lines)-maxLogLines:]...)
}

// visible is the seam the filter hooks into; without a filter it is every
// line.
func (l logsModel) visible() []string { return l.lines }

// refresh rebuilds the viewport content, truncating each line to the pane
// width.
//
// ponytail: truncate, don't wrap. Keeps one line == one row, so filter and
// scroll math stay trivial. Wrap when reading long JSON lines actually hurts.
func (l *logsModel) refresh() {
	src := l.visible()
	out := make([]string, 0, len(src))
	for _, line := range src {
		out = append(out, truncate(line, l.vp.Width))
	}
	l.vp.SetContent(strings.Join(out, "\n"))
}

func (l logsModel) Name() string     { return l.name }
func (l logsModel) Following() bool  { return l.follow }
func (l logsModel) LineCount() int   { return len(l.lines) }
func (l logsModel) AtBottom() bool   { return l.vp.AtBottom() }

func (l logsModel) Update(msg tea.Msg) (logsModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return l, nil
	}
	switch k.String() {
	case "f":
		l.follow = !l.follow
		if l.follow {
			l.vp.GotoBottom()
		}
	case "j", "down":
		l.follow = false
		l.vp.LineDown(1)
	case "k", "up":
		l.follow = false
		l.vp.LineUp(1)
	case "ctrl+d":
		l.follow = false
		l.vp.HalfViewDown()
	case "ctrl+u":
		l.follow = false
		l.vp.HalfViewUp()
	case "g":
		l.follow = false
		l.vp.GotoTop()
	case "G":
		l.follow = false
		l.vp.GotoBottom()
	}
	return l, nil
}

func (l logsModel) Title() string {
	if l.name == "" {
		return "no command selected"
	}
	if l.follow {
		return l.name + " — following"
	}
	return fmt.Sprintf("%s — paused (%d lines)", l.name, len(l.lines))
}

func (l logsModel) View() string {
	title := styleHeader.Render(truncate(l.Title(), l.vp.Width))
	return lipgloss.JoinVertical(lipgloss.Left, title, l.vp.View())
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS, every fuzzy, message, table and logs test.

If `TestManualScrollStopsFollow` fails because the pane was never scrollable, the content is shorter than the pane: check that `SetSize(40, 5)` leaves `vp.Height == 4` and that 50 lines were appended.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): log pane with follow mode and a line cap

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Log filter

**Files:**
- Create: `internal/tui/filter.go`
- Modify: `internal/tui/logs.go` — add the `filter` field, hook `visible()`, handle filter keys, extend `Title` and `View`
- Test: `internal/tui/filter_test.go`

**Interfaces:**
- Consumes: `logsModel`, `bubbles/textinput`.
- Produces:
  - `type filterState struct { editing bool; input textinput.Model; query string; matches int }`, `newFilter() filterState`.
  - `func smartContains(line, query string) bool` — case-insensitive while the query is all lowercase.
  - `(logsModel).FilterEditing() bool`, `(*logsModel).CancelFilterEdit()`, `(logsModel).Query() string`.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/filter_test.go`:

```go
package tui

import (
	"strings"
	"testing"
)

func typeInto(l logsModel, s string) logsModel {
	for _, r := range s {
		l, _ = l.Update(key(string(r)))
	}
	return l
}

func TestSmartContains(t *testing.T) {
	if !smartContains("ERROR: boom", "error") {
		t.Fatal("lowercase query must match case-insensitively")
	}
	if smartContains("error: boom", "Error") {
		t.Fatal("mixed-case query must be case-sensitive")
	}
	if !smartContains("Error: boom", "Error") {
		t.Fatal("exact case must match")
	}
}

func TestFilterShowsOnlyMatches(t *testing.T) {
	l := newTestLogs("tick", "starting up", "error: boom", "still fine", "error: again")

	l, _ = l.Update(key("/"))
	if !l.FilterEditing() {
		t.Fatal("/ must open the filter input")
	}
	l = typeInto(l, "error")
	l, _ = l.Update(key("enter"))

	if l.FilterEditing() {
		t.Fatal("enter must close the input")
	}
	if l.Query() != "error" {
		t.Fatalf("Query = %q, want error", l.Query())
	}

	view := l.View()
	if strings.Contains(view, "still fine") || strings.Contains(view, "starting up") {
		t.Fatalf("non-matching lines still visible:\n%s", view)
	}
	if !strings.Contains(view, "error: boom") || !strings.Contains(view, "error: again") {
		t.Fatalf("matching lines missing:\n%s", view)
	}
	if title := l.Title(); !strings.Contains(title, `filter "error"`) || !strings.Contains(title, "2 of 4") {
		t.Fatalf("Title = %q, want the filter and the counts", title)
	}
}

func TestFilterKeepsFollowingNewMatches(t *testing.T) {
	l := newTestLogs("tick", "error: one")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "error")
	l, _ = l.Update(key("enter"))

	l.Append("quiet line")
	l.Append("error: two")

	view := l.View()
	if strings.Contains(view, "quiet line") {
		t.Fatalf("non-matching new line leaked in:\n%s", view)
	}
	if !strings.Contains(view, "error: two") {
		t.Fatalf("matching new line missing:\n%s", view)
	}
}

func TestEscWhileEditingClearsTheFilter(t *testing.T) {
	l := newTestLogs("tick", "a", "b")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "a")
	l, _ = l.Update(key("esc"))

	if l.FilterEditing() || l.Query() != "" {
		t.Fatalf("esc must cancel: editing=%v query=%q", l.FilterEditing(), l.Query())
	}
	if !strings.Contains(l.View(), "b") {
		t.Fatalf("all lines should be back:\n%s", l.View())
	}
}

func TestEscAfterApplyingClearsTheFilter(t *testing.T) {
	l := newTestLogs("tick", "a", "b")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "a")
	l, _ = l.Update(key("enter"))
	l, _ = l.Update(key("esc"))

	if l.Query() != "" {
		t.Fatalf("Query = %q, want empty", l.Query())
	}
	if !strings.Contains(l.View(), "b") {
		t.Fatalf("all lines should be back:\n%s", l.View())
	}
}

func TestScrollKeysAreLiteralWhileEditing(t *testing.T) {
	l := newTestLogs("tick", "one", "two")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "j")

	if !l.FilterEditing() {
		t.Fatal("j must not close the input")
	}
	l, _ = l.Update(key("enter"))
	if l.Query() != "j" {
		t.Fatalf("Query = %q, want j typed as text", l.Query())
	}
}

func TestResetClearsTheFilter(t *testing.T) {
	l := newTestLogs("tick", "error: boom")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "error")
	l, _ = l.Update(key("enter"))

	l.Reset("greet", []string{"hello"})
	if l.Query() != "" {
		t.Fatalf("Query = %q after Reset, want empty", l.Query())
	}
	if !strings.Contains(l.View(), "hello") {
		t.Fatalf("new content missing:\n%s", l.View())
	}
}

func TestCancelFilterEdit(t *testing.T) {
	l := newTestLogs("tick", "a")
	l, _ = l.Update(key("/"))
	l.CancelFilterEdit()
	if l.FilterEditing() {
		t.Fatal("CancelFilterEdit must close the input")
	}
}

func TestFilterInputIsVisibleWhileEditing(t *testing.T) {
	l := newTestLogs("tick", "a")
	l, _ = l.Update(key("/"))
	l = typeInto(l, "err")
	if !strings.Contains(l.View(), "err") {
		t.Fatalf("input line not rendered:\n%s", l.View())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestSmartContains|TestFilter|TestEsc|TestScrollKeys|TestResetClears|TestCancelFilter' -v`
Expected: FAIL — `undefined: smartContains`.

- [ ] **Step 3: Write the filter state**

Create `internal/tui/filter.go`:

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// filterState is the log pane's substring filter.
type filterState struct {
	editing bool
	input   textinput.Model
	query   string
	matches int
}

func newFilter() filterState {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 120
	return filterState{input: ti}
}

// smartContains matches case-insensitively while the query is all lowercase,
// and exactly once the query carries an uppercase rune.
//
// ponytail: substring, not regex. No pattern-error state, no per-line regex
// cost. Add regex when a filter people actually want cannot be expressed.
func smartContains(line, query string) bool {
	if strings.ToLower(query) == query {
		return strings.Contains(strings.ToLower(line), query)
	}
	return strings.Contains(line, query)
}
```

- [ ] **Step 4: Wire the filter into the log pane**

Four edits to `internal/tui/logs.go`.

Add the field and initialize it:

```go
type logsModel struct {
	vp     viewport.Model
	name   string
	lines  []string
	follow bool
	filter filterState
}

func newLogs() logsModel {
	return logsModel{vp: viewport.New(0, 0), follow: true, filter: newFilter()}
}
```

Replace `visible` and add the accessors:

```go
// visible is every line, or only the matching lines while a filter is set.
func (l logsModel) visible() []string {
	if l.filter.query == "" {
		return l.lines
	}
	out := make([]string, 0, len(l.lines))
	for _, line := range l.lines {
		if smartContains(line, l.filter.query) {
			out = append(out, line)
		}
	}
	return out
}

func (l logsModel) FilterEditing() bool { return l.filter.editing }
func (l logsModel) Query() string       { return l.filter.query }

// CancelFilterEdit closes the input without touching the applied query.
func (l *logsModel) CancelFilterEdit() {
	l.filter.editing = false
	l.filter.input.Blur()
}
```

Track the match count in `refresh`, and clear the filter in `Reset`:

```go
func (l *logsModel) refresh() {
	src := l.visible()
	l.filter.matches = len(src)
	out := make([]string, 0, len(src))
	for _, line := range src {
		out = append(out, truncate(line, l.vp.Width))
	}
	l.vp.SetContent(strings.Join(out, "\n"))
}
```

In `Reset`, before `l.refresh()`:

```go
	l.filter.query = ""
	l.filter.editing = false
	l.filter.input.Reset()
	l.filter.input.Blur()
```

Handle the filter keys at the top of `Update`, ahead of the scroll keys:

```go
func (l logsModel) Update(msg tea.Msg) (logsModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return l, nil
	}
	if l.filter.editing {
		switch k.String() {
		case "enter":
			l.filter.query = l.filter.input.Value()
			l.filter.editing = false
			l.filter.input.Blur()
			l.refresh()
			if l.follow {
				l.vp.GotoBottom()
			}
		case "esc":
			l.filter.editing = false
			l.filter.input.Blur()
			l.filter.query = ""
			l.refresh()
		default:
			var cmd tea.Cmd
			l.filter.input, cmd = l.filter.input.Update(msg)
			return l, cmd
		}
		return l, nil
	}

	switch k.String() {
	case "/":
		l.filter.editing = true
		l.filter.input.Reset()
		l.filter.input.Focus()
		return l, textinput.Blink
	case "esc":
		if l.filter.query != "" {
			l.filter.query = ""
			l.refresh()
		}
		return l, nil
	case "f":
	// ... the existing scroll cases follow unchanged
```

Extend `Title` and `View`:

```go
func (l logsModel) Title() string {
	if l.name == "" {
		return "no command selected"
	}
	base := l.name + " — following"
	if !l.follow {
		base = fmt.Sprintf("%s — paused (%d lines)", l.name, len(l.lines))
	}
	if l.filter.query != "" {
		base = fmt.Sprintf("%s — filter %q · %d of %d", l.name, l.filter.query, l.filter.matches, len(l.lines))
	}
	return base
}

func (l logsModel) View() string {
	title := styleHeader.Render(truncate(l.Title(), l.vp.Width))
	body := lipgloss.JoinVertical(lipgloss.Left, title, l.vp.View())
	if l.filter.editing {
		return lipgloss.JoinVertical(lipgloss.Left, body, l.filter.input.View())
	}
	return body
}
```

Add `"github.com/charmbracelet/bubbles/textinput"` to the imports of `logs.go`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ -v`
Expected: PASS, every test in the package.

Note that `Title` now reports the filter instead of follow state while a
filter is on — `TestTitle` from Task 5 must still pass, because it never sets
a filter.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): smart-case substring filter for the log pane

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 7: Fuzzy palette

**Files:**
- Create: `internal/tui/palette.go`
- Test: `internal/tui/palette_test.go`

**Interfaces:**
- Consumes: `rank`, `candidate`, `manager.Status`, `bubbles/textinput`.
- Produces:
  - `type paletteModel struct` with `newPalette() paletteModel`.
  - `(*paletteModel).Open(rows []manager.Status)`, `(*paletteModel).Close()`.
  - `(paletteModel).Update(msg tea.Msg) (paletteModel, tea.Cmd)`, `(paletteModel).Highlighted() (manager.Status, bool)`, `(paletteModel).View(width, height int) string`.

`Enter` and `Esc` are the root model's business — it intercepts them before delegating to the palette, because starting a command and closing the overlay are decisions about the whole app.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/palette_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/tphuc/lazycomd/internal/manager"
)

func openPalette(names ...string) paletteModel {
	p := newPalette()
	p.Open(rows(names...))
	return p
}

func typePalette(p paletteModel, s string) paletteModel {
	for _, r := range s {
		p, _ = p.Update(key(string(r)))
	}
	return p
}

func TestPaletteOpenListsEverything(t *testing.T) {
	p := openPalette("proxy", "app:api", "scraper:api")
	if got, ok := p.Highlighted(); !ok || got.Name != "proxy" {
		t.Fatalf("highlighted = %q, want the first row", got.Name)
	}
	view := p.View(60, 10)
	for _, want := range []string{"proxy", "app:api", "scraper:api"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestPaletteTypingNarrows(t *testing.T) {
	p := openPalette("proxy", "app:api", "scraper:api")
	p = typePalette(p, "api")

	view := p.View(60, 10)
	if strings.Contains(view, "proxy") {
		t.Fatalf("non-matching name still listed:\n%s", view)
	}
	got, ok := p.Highlighted()
	if !ok || got.Name != "app:api" {
		t.Fatalf("highlighted = %q, want app:api (best match)", got.Name)
	}
}

func TestPaletteNavigation(t *testing.T) {
	p := openPalette("a", "b", "c")

	p, _ = p.Update(key("down"))
	if got, _ := p.Highlighted(); got.Name != "b" {
		t.Fatalf("after down: %q, want b", got.Name)
	}
	p, _ = p.Update(key("ctrl+n"))
	if got, _ := p.Highlighted(); got.Name != "c" {
		t.Fatalf("after ctrl+n: %q, want c", got.Name)
	}
	p, _ = p.Update(key("ctrl+n")) // past the end: clamps
	if got, _ := p.Highlighted(); got.Name != "c" {
		t.Fatalf("clamp failed: %q, want c", got.Name)
	}
	p, _ = p.Update(key("up"))
	if got, _ := p.Highlighted(); got.Name != "b" {
		t.Fatalf("after up: %q, want b", got.Name)
	}
	p, _ = p.Update(key("ctrl+p"))
	p, _ = p.Update(key("ctrl+p")) // past the start: clamps
	if got, _ := p.Highlighted(); got.Name != "a" {
		t.Fatalf("clamp failed: %q, want a", got.Name)
	}
}

func TestPaletteCursorClampsWhenMatchesShrink(t *testing.T) {
	p := openPalette("aa", "ab", "ac")
	p, _ = p.Update(key("down"))
	p, _ = p.Update(key("down")) // on "ac"
	p = typePalette(p, "aa")     // only one match left

	got, ok := p.Highlighted()
	if !ok || got.Name != "aa" {
		t.Fatalf("highlighted = %q, ok = %v; want aa", got.Name, ok)
	}
}

func TestPaletteNoMatches(t *testing.T) {
	p := openPalette("a", "b")
	p = typePalette(p, "zzz")
	if _, ok := p.Highlighted(); ok {
		t.Fatal("Highlighted ok = true with no matches")
	}
	if view := p.View(60, 10); !strings.Contains(view, "no match") {
		t.Fatalf("view should say there are no matches:\n%s", view)
	}
}

func TestPaletteCarriesState(t *testing.T) {
	p := newPalette()
	p.Open([]manager.Status{{Name: "tick", State: manager.Running}})
	got, ok := p.Highlighted()
	if !ok || got.State != manager.Running {
		t.Fatalf("highlighted = %+v, want the running status", got)
	}
}

func TestPaletteShowsTypedQuery(t *testing.T) {
	p := openPalette("app:api")
	p = typePalette(p, "api")
	if view := p.View(60, 10); !strings.Contains(view, "api") {
		t.Fatalf("query not rendered:\n%s", view)
	}
}

func TestPaletteViewRespectsHeight(t *testing.T) {
	names := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		names = append(names, string(rune('a'+i%26))+"cmd")
	}
	p := openPalette(names...)
	view := p.View(60, 6)
	if got := len(strings.Split(view, "\n")); got > 6 {
		t.Fatalf("view is %d lines, want <= 6", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run TestPalette -v`
Expected: FAIL — `undefined: newPalette`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/palette.go`:

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tphuc/lazycomd/internal/manager"
)

var styleHit = lipgloss.NewStyle().Bold(true).Underline(true)

type paletteModel struct {
	input   textinput.Model
	rows    []manager.Status
	matches []candidate
	cursor  int
}

func newPalette() paletteModel {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 120
	return paletteModel{input: ti}
}

// Open shows the palette over the command list as it stands now.
func (p *paletteModel) Open(rows []manager.Status) {
	p.rows = rows
	p.cursor = 0
	p.input.Reset()
	p.input.Focus()
	p.refilter()
}

func (p *paletteModel) Close() { p.input.Blur() }

func (p *paletteModel) refilter() {
	names := make([]string, 0, len(p.rows))
	for _, r := range p.rows {
		names = append(names, r.Name)
	}
	p.matches = rank(p.input.Value(), names)
	p.clampCursor()
}

func (p *paletteModel) clampCursor() {
	if p.cursor >= len(p.matches) {
		p.cursor = len(p.matches) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// Update handles typing and navigation. The root model intercepts enter and
// esc before this is called.
func (p paletteModel) Update(msg tea.Msg) (paletteModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "down", "ctrl+n":
		p.cursor++
		p.clampCursor()
		return p, nil
	case "up", "ctrl+p":
		p.cursor--
		p.clampCursor()
		return p, nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.refilter()
	return p, cmd
}

// Highlighted is the status of the command under the palette cursor.
func (p paletteModel) Highlighted() (manager.Status, bool) {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return manager.Status{}, false
	}
	name := p.matches[p.cursor].name
	for _, r := range p.rows {
		if r.Name == name {
			return r, true
		}
	}
	return manager.Status{}, false
}

func (p paletteModel) View(width, height int) string {
	lines := []string{styleHeader.Render(truncate(p.input.View(), width))}

	if len(p.matches) == 0 {
		return strings.Join(append(lines, styleDim.Render("no match")), "\n")
	}
	room := height - 1
	for i, m := range p.matches {
		if i >= room {
			break
		}
		marker := "  "
		if i == p.cursor {
			marker = "> "
		}
		lines = append(lines, truncate(marker, width)+highlightMatch(m.name, m.positions))
	}
	return strings.Join(lines, "\n")
}

// highlightMatch emphasizes the runes the query matched.
func highlightMatch(name string, positions []int) string {
	hit := make(map[int]bool, len(positions))
	for _, p := range positions {
		hit[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(name) {
		if hit[i] {
			b.WriteString(styleHit.Render(string(r)))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS, every test in the package.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): fuzzy command palette overlay

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Bindings, key bar and help overlay

**Files:**
- Create: `internal/tui/help.go`
- Test: `internal/tui/help_test.go`

**Interfaces:**
- Consumes: `styleHeader`, `styleDim`, `truncate`.
- Produces:
  - `type focus int` with `focusTable`, `focusLogs`, `focusPalette` — declared here and used by the root model in Task 10.
  - `type overlay int` with `overlayNone`, `overlayHelp`.
  - `type binding struct { key, desc string; scope scope }` and `var bindings []binding`.
  - `func keyBar(width int, f focus) string`, `func helpOverlay(width, height int) string`.

One `bindings` slice feeds both the bar and the overlay, so they cannot drift apart.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/help_test.go`:

```go
package tui

import (
	"strings"
	"testing"
)

func TestEveryScopeHasBindings(t *testing.T) {
	seen := map[scope]int{}
	for _, b := range bindings {
		if b.key == "" || b.desc == "" {
			t.Fatalf("incomplete binding: %+v", b)
		}
		seen[b.scope]++
	}
	for _, s := range []scope{scopeGlobal, scopeTable, scopeLogs, scopePalette} {
		if seen[s] == 0 {
			t.Fatalf("scope %d has no bindings", s)
		}
	}
}

func TestKeyBarShowsTheFocusedScopePlusGlobal(t *testing.T) {
	bar := keyBar(200, focusTable)
	for _, want := range []string{"start", "stop", "restart", "palette", "quit"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("table key bar missing %q:\n%s", want, bar)
		}
	}
	if strings.Contains(bar, "half page") {
		t.Fatalf("table key bar shows a log-pane binding:\n%s", bar)
	}

	bar = keyBar(200, focusLogs)
	if !strings.Contains(bar, "half page") {
		t.Fatalf("log key bar missing the half-page binding:\n%s", bar)
	}
	if strings.Contains(bar, "restart") {
		t.Fatalf("log key bar shows a table binding:\n%s", bar)
	}
}

func TestKeyBarFitsTheWidth(t *testing.T) {
	bar := keyBar(30, focusTable)
	if got := len([]rune(bar)); got > 30 {
		t.Fatalf("key bar is %d runes wide, want <= 30: %q", got, bar)
	}
}

func TestHelpOverlayListsEveryBinding(t *testing.T) {
	help := helpOverlay(80, 40)
	for _, b := range bindings {
		if !strings.Contains(help, b.desc) {
			t.Fatalf("help overlay missing %q:\n%s", b.desc, help)
		}
	}
	if !strings.Contains(help, "running*") {
		t.Fatalf("help overlay should explain the spec-changed asterisk:\n%s", help)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestEveryScope|TestKeyBar|TestHelpOverlay' -v`
Expected: FAIL — `undefined: bindings`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/help.go`:

```go
package tui

import "strings"

// focus is which pane takes keys.
type focus int

const (
	focusTable focus = iota
	focusLogs
	focusPalette
)

// overlay is a full-pane layer drawn over the body.
type overlay int

const (
	overlayNone overlay = iota
	overlayHelp
)

// scope is where a binding applies.
type scope int

const (
	scopeGlobal scope = iota
	scopeTable
	scopeLogs
	scopePalette
)

type binding struct {
	key   string
	desc  string
	scope scope
}

// bindings is the single source of truth for the key bar and the help
// overlay. Adding a key here shows it in both.
var bindings = []binding{
	{"j/k", "move", scopeTable},
	{"g/G", "first/last", scopeTable},
	{"s", "start", scopeTable},
	{"S", "stop", scopeTable},
	{"r", "restart", scopeTable},
	{"p", "palette", scopeTable},
	{"j/k", "scroll", scopeLogs},
	{"ctrl+d/u", "half page", scopeLogs},
	{"g/G", "top/bottom", scopeLogs},
	{"esc", "clear filter", scopeLogs},
	{"enter", "start and select", scopePalette},
	{"esc", "close palette", scopePalette},
	{"f", "follow", scopeGlobal},
	{"/", "filter", scopeGlobal},
	{"tab", "switch pane", scopeGlobal},
	{"?", "help", scopeGlobal},
	{"q", "quit", scopeGlobal},
}

func scopeFor(f focus) scope {
	switch f {
	case focusLogs:
		return scopeLogs
	case focusPalette:
		return scopePalette
	default:
		return scopeTable
	}
}

// keyBar renders the bottom hint line for the focused pane.
func keyBar(width int, f focus) string {
	want := scopeFor(f)
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		if b.scope == want || b.scope == scopeGlobal {
			parts = append(parts, b.key+" "+b.desc)
		}
	}
	return styleDim.Render(truncate(" "+strings.Join(parts, "  "), width))
}

// helpOverlay renders the full keymap, grouped by scope.
func helpOverlay(width, height int) string {
	groups := []struct {
		title string
		scope scope
	}{
		{"global", scopeGlobal},
		{"command table", scopeTable},
		{"log pane", scopeLogs},
		{"palette", scopePalette},
	}

	lines := []string{styleHeader.Render("lazycomd — keys")}
	for _, g := range groups {
		lines = append(lines, "", styleHeader.Render(g.title))
		for _, b := range bindings {
			if b.scope != g.scope {
				continue
			}
			lines = append(lines, "  "+cellPlain(b.key, 10)+b.desc)
		}
	}
	lines = append(lines,
		"",
		styleDim.Render("  a state shown as running* means the config changed;"),
		styleDim.Render("  the new spec applies on that command's next start"),
		"",
		styleDim.Render("  ?, esc or q closes this help"),
	)

	if len(lines) > height {
		lines = lines[:height]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, truncate(l, width))
	}
	return strings.Join(out, "\n")
}

// cellPlain pads without styling, for the help columns.
func cellPlain(s string, w int) string {
	if len([]rune(s)) >= w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-len([]rune(s)))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -v`
Expected: PASS. If `TestKeyBarFitsTheWidth` fails, the culprit is `styleDim.Render` wrapping the string in escape codes — `truncate` must run *before* the style is applied, which is how the code above orders it. Count runes on the unstyled string in the test if lipgloss is emitting color codes in your terminal-less test environment.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): one binding table driving the key bar and help overlay

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: SSE stream lifecycle

**Files:**
- Create: `internal/tui/stream.go`
- Test: `internal/tui/stream_test.go`

**Interfaces:**
- Consumes: `client.Client.Stream`, `logLineMsg`, `streamEndedMsg`.
- Produces:
  - `type sink struct { fn func(tea.Msg) }` with `(*sink).send(msg tea.Msg)` — nil-safe in both the receiver and the function.
  - `type streamHandle struct { name string; ... }` with `(*streamHandle).stop()` — nil-safe, cancels and waits.
  - `func startStream(c *client.Client, s *sink, name string) *streamHandle`.
  - `type lineWriter struct { name string; sink *sink; buf []byte }` implementing `io.Writer`.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/stream_test.go`:

```go
package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/config"
)

// captureSink collects everything sent to it.
func captureSink() (*sink, chan tea.Msg) {
	ch := make(chan tea.Msg, 256)
	return &sink{fn: func(m tea.Msg) {
		select {
		case ch <- m:
		default: // a test that stops draining must not wedge a goroutine
		}
	}}, ch
}

// nextMsg waits for the next message of any type.
func nextMsg(t *testing.T, ch chan tea.Msg, within time.Duration) tea.Msg {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(within):
		t.Fatal("timed out waiting for a message")
		return nil
	}
}

func TestLineWriterSplitsAcrossWrites(t *testing.T) {
	s, ch := captureSink()
	w := &lineWriter{name: "tick", sink: s}

	for _, chunk := range []string{"hel", "lo\nwor", "ld\n"} {
		n, err := w.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}

	for _, want := range []string{"hello", "world"} {
		msg := nextMsg(t, ch, time.Second).(logLineMsg)
		if msg.name != "tick" || msg.line != want {
			t.Fatalf("msg = %+v, want line %q", msg, want)
		}
	}
	select {
	case extra := <-ch:
		t.Fatalf("unexpected extra message %+v", extra)
	default:
	}
}

func TestLineWriterHoldsAnIncompleteLine(t *testing.T) {
	s, ch := captureSink()
	w := &lineWriter{name: "tick", sink: s}
	w.Write([]byte("no newline yet"))

	select {
	case m := <-ch:
		t.Fatalf("sent %+v before a newline arrived", m)
	default:
	}
	w.Write([]byte("\n"))
	if got := nextMsg(t, ch, time.Second).(logLineMsg).line; got != "no newline yet" {
		t.Fatalf("line = %q", got)
	}
}

func TestSinkIsNilSafe(t *testing.T) {
	var s *sink
	s.send(logLineMsg{}) // must not panic

	empty := &sink{}
	empty.send(logLineMsg{}) // no fn set: also fine
}

func TestStreamHandleStopIsNilSafe(t *testing.T) {
	var h *streamHandle
	h.stop() // must not panic
}

func TestStartStreamDeliversLinesAndStopsCleanly(t *testing.T) {
	c, m := testDaemon(t, map[string]config.Command{
		"tick": {Cmd: []string{"sh", "-c", "while true; do echo tick; sleep 0.05; done"}, Cwd: "/tmp"},
	})
	if err := m.Start("tick"); err != nil {
		t.Fatal(err)
	}

	s, ch := captureSink()
	h := startStream(c, s, "tick")

	msg := nextMsg(t, ch, 3*time.Second)
	line, ok := msg.(logLineMsg)
	if !ok || line.name != "tick" || line.line != "tick" {
		t.Fatalf("msg = %+v, want a logLineMsg for tick", msg)
	}

	done := make(chan struct{})
	go func() {
		h.stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stop() did not return: the stream goroutine is stuck")
	}

	// A stream we cancelled ourselves must not report that it ended.
	drained := time.After(200 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			if _, bad := msg.(streamEndedMsg); bad {
				t.Fatal("cancelled stream sent streamEndedMsg")
			}
		case <-drained:
			return
		}
	}
}

func TestStartStreamReportsAnUnknownCommand(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{})

	s, ch := captureSink()
	h := startStream(c, s, "ghost")
	defer h.stop()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			if ended, ok := msg.(streamEndedMsg); ok {
				if ended.name != "ghost" || ended.err == nil {
					t.Fatalf("streamEndedMsg = %+v, want an error for ghost", ended)
				}
				return
			}
		case <-deadline:
			t.Fatal("no streamEndedMsg for an unknown command")
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestLineWriter|TestSink|TestStreamHandle|TestStartStream' -v`
Expected: FAIL — `undefined: sink`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/stream.go`:

```go
package tui

import (
	"bytes"
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/client"
)

// sink delivers messages from a goroutine into the bubbletea loop. Run sets
// fn to the program's Send before any goroutine starts.
type sink struct{ fn func(tea.Msg) }

func (s *sink) send(msg tea.Msg) {
	if s == nil || s.fn == nil {
		return
	}
	s.fn(msg)
}

// streamHandle owns one SSE stream goroutine.
type streamHandle struct {
	name   string
	cancel context.CancelFunc
	done   chan struct{}
}

// stop cancels the stream and waits for its goroutine to exit, so no
// goroutine outlives the program.
//
// ponytail: this blocks the update loop until the HTTP request unwinds — a
// few milliseconds over a unix socket. Make it fire-and-forget if switching
// selection ever feels sticky.
func (h *streamHandle) stop() {
	if h == nil {
		return
	}
	h.cancel()
	<-h.done
}

// startStream opens the log stream for name, sending one logLineMsg per line.
func startStream(c *client.Client, s *sink, name string) *streamHandle {
	ctx, cancel := context.WithCancel(context.Background())
	h := &streamHandle{name: name, cancel: cancel, done: make(chan struct{})}

	go func() {
		defer close(h.done)
		err := c.Stream(ctx, name, &lineWriter{name: name, sink: s})
		if ctx.Err() != nil {
			return // we cancelled it; nobody needs to hear about that
		}
		s.send(streamEndedMsg{name: name, err: err})
	}()
	return h
}

// lineWriter turns a byte stream into one logLineMsg per complete line.
type lineWriter struct {
	name string
	sink *sink
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		w.sink.send(logLineMsg{name: w.name, line: line})
	}
	return len(p), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ -v`
Expected: PASS. The race detector matters here — this is the only place in the package where a goroutine touches shared state.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): SSE stream goroutine with a line-splitting writer

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 10: Root model — polling, status merge, banner, render skeleton

**Files:**
- Create: `internal/tui/tui.go`
- Test: `internal/tui/tui_test.go`

**Interfaces:**
- Consumes: everything from Tasks 2-9.
- Produces:
  - `type Model struct` (fields listed in the code below), `func New(c *client.Client, s *sink) Model`.
  - `(Model).Init() tea.Cmd`, `(Model).Update(tea.Msg) (tea.Model, tea.Cmd)`, `(Model).View() string`.
  - `(Model).tickInterval() time.Duration`, `(*Model).setStatus(s string)`, `(*Model).layout()`, `(Model).header() string`, `(Model).bottom() string`.
  - `func Run(c *client.Client) error` — the package's only exported function besides `New`.

Task 11 replaces `handleKey` and `layout` with their full versions and adds `syncStream`. This task deliberately routes every non-quit key to the table so it has a working deliverable on its own.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/tui_test.go`:

```go
package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// newTestModel returns a sized model plus the channel its sink writes to.
func newTestModel(t *testing.T, cmds map[string]config.Command) (Model, *manager.Manager, chan tea.Msg) {
	t.Helper()
	c, mgr := testDaemon(t, cmds)
	s, ch := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return next.(Model), mgr, ch
}

// step applies one message and returns the model plus the command it emitted.
func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	out, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return out, cmd
}

func TestStatusMsgPopulatesTheTable(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})

	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Running, PID: 42, UptimeSec: 5}})
	view := m.View()
	for _, want := range []string{"tick", "running", "42"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(m.header(), "1 commands · 1 running") {
		t.Fatalf("header = %q", m.header())
	}
}

func TestStatusErrShowsTheBannerAndSlowsTheTick(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Stopped}})

	if m.tickInterval() != tickConnected {
		t.Fatalf("connected tick = %v, want %v", m.tickInterval(), tickConnected)
	}

	m, _ = step(t, m, statusErrMsg{err: client.ErrNoDaemon})
	if m.tickInterval() != tickDisconnected {
		t.Fatalf("disconnected tick = %v, want %v", m.tickInterval(), tickDisconnected)
	}
	if !strings.Contains(m.header(), "daemon not running") {
		t.Fatalf("header = %q, want the reconnect banner", m.header())
	}
	if !strings.Contains(m.View(), "tick") {
		t.Fatal("last known rows should stay visible while disconnected")
	}
}

func TestTickEmitsAFetchAndAnotherTick(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	_, cmd := step(t, m, tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("tick produced no command")
	}
}

func TestActionDoneMergesTheRow(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Stopped}})

	m, _ = step(t, m, actionDoneMsg{verb: "start", name: "tick", st: manager.Status{Name: "tick", State: manager.Running, PID: 7}})
	if !strings.Contains(m.View(), "running") {
		t.Fatalf("merged row not rendered:\n%s", m.View())
	}
	if !strings.Contains(m.bottom(), "start tick") {
		t.Fatalf("status line = %q", m.bottom())
	}
}

func TestActionErrorGoesToTheStatusLine(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, actionDoneMsg{verb: "start", name: "tick", err: errors.New("illegal in current state: tick is running")})
	if !strings.Contains(m.bottom(), "illegal in current state") {
		t.Fatalf("status line = %q, want the daemon's message", m.bottom())
	}
}

func TestStatusLineExpiresBackToTheKeyBar(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	now := time.Now()
	m.now = func() time.Time { return now }

	m, _ = step(t, m, actionDoneMsg{verb: "stop", name: "tick", st: manager.Status{Name: "tick", State: manager.Stopped}})
	if !strings.Contains(m.bottom(), "stop tick") {
		t.Fatalf("status line = %q", m.bottom())
	}

	now = now.Add(statusLife + time.Second)
	if strings.Contains(m.bottom(), "stop tick") {
		t.Fatalf("status line outlived %v: %q", statusLife, m.bottom())
	}
	if !strings.Contains(m.bottom(), "quit") {
		t.Fatalf("key bar should be back: %q", m.bottom())
	}
}

func TestLogLinesOnlyLandForTheSelectedCommand(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"a": sleeper(), "b": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "a", State: manager.Running}, {Name: "b", State: manager.Running}})
	m, _ = step(t, m, logTailMsg{name: "a", lines: []string{"from a"}})

	m, _ = step(t, m, logLineMsg{name: "a", line: "live a"})
	m, _ = step(t, m, logLineMsg{name: "b", line: "live b"})

	view := m.View()
	if !strings.Contains(view, "live a") {
		t.Fatalf("selected command's line missing:\n%s", view)
	}
	if strings.Contains(view, "live b") {
		t.Fatalf("another command's line leaked in:\n%s", view)
	}
}

func TestLogTailForAStaleSelectionIsIgnored(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"a": sleeper(), "b": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "a", State: manager.Running}, {Name: "b", State: manager.Running}})

	m, _ = step(t, m, logTailMsg{name: "b", lines: []string{"stale tail"}})
	if strings.Contains(m.View(), "stale tail") {
		t.Fatalf("tail for an unselected command was applied:\n%s", m.View())
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m, _, _ := newTestModel(t, map[string]config.Command{})
		_, cmd := step(t, m, key(k))
		if cmd == nil {
			t.Fatalf("%s produced no command, want tea.Quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s did not quit", k)
		}
	}
}

func TestResizeKeepsTheViewWithinWidth(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Running, PID: 42}})

	m, _ = step(t, m, tea.WindowSizeMsg{Width: 70, Height: 20})
	for _, line := range strings.Split(m.View(), "\n") {
		if got := len([]rune(line)); got > 70 {
			t.Fatalf("line is %d runes wide at width 70: %q", got, line)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestStatusMsg|TestStatusErr|TestTickEmits|TestAction|TestStatusLine|TestLog|TestQuit|TestResize' -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/tui.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/manager"
)

var styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

// Model is the whole TUI. Update is pure; every side effect is a tea.Cmd or
// the one stream goroutine.
type Model struct {
	client *client.Client
	sink   *sink

	table   tableModel
	logs    logsModel
	palette paletteModel

	focus   focus
	overlay overlay

	width  int
	height int
	tableW int
	bodyH  int

	connected bool
	status    string
	statusAt  time.Time

	stream *streamHandle
	now    func() time.Time
}

// New builds the model. s must be the same sink Run wires to the program.
func New(c *client.Client, s *sink) Model {
	return Model{
		client:  c,
		sink:    s,
		table:   newTable(),
		logs:    newLogs(),
		palette: newPalette(),
		focus:   focusTable,
		now:     time.Now,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(m.client), tickCmd(tickConnected))
}

// tickInterval polls faster while the daemon is answering.
func (m Model) tickInterval() time.Duration {
	if m.connected {
		return tickConnected
	}
	return tickDisconnected
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tickMsg:
		return m, tea.Batch(fetchStatus(m.client), tickCmd(m.tickInterval()))

	case statusMsg:
		m.connected = true
		m.table.SetRows([]manager.Status(msg))
		return m, nil

	case statusErrMsg:
		m.connected = false
		m.setStatus(msg.err.Error())
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error())
			return m, nil
		}
		m.table.MergeStatus(msg.st)
		m.setStatus(fmt.Sprintf("%s %s: %s", msg.verb, msg.name, msg.st.State))
		return m, nil

	case logTailMsg:
		if sel, ok := m.table.Selected(); ok && sel.Name == msg.name {
			m.logs.Reset(msg.name, msg.lines)
		}
		return m, nil

	case logLineMsg:
		if msg.name == m.logs.Name() {
			m.logs.Append(msg.line)
		}
		return m, nil

	case streamEndedMsg:
		if m.stream != nil && m.stream.name == msg.name {
			m.stream = nil // the next selection sync reopens it
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey is replaced with full focus routing in Task 11.
func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(k)
	return m, cmd
}

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusAt = m.now()
}

// layout splits the width between the panes. Task 11 adds the narrow rules.
func (m *Model) layout() {
	m.bodyH = m.height - 2 // header and bottom line
	if m.bodyH < 3 {
		m.bodyH = 3
	}
	m.tableW = m.width * 40 / 100
	if m.tableW < 32 {
		m.tableW = 32
	}
	if m.tableW > m.width-20 {
		m.tableW = m.width - 20
	}
	m.table.SetSize(m.tableW, m.bodyH, m.width < 80)
	m.logs.SetSize(m.width-m.tableW-1, m.bodyH)
}

func (m Model) header() string {
	if !m.connected {
		return styleWarn.Render(truncate(" lazycomd — daemon not running, retrying (start with: lazycomd serve)", m.width))
	}
	running := 0
	for _, r := range m.table.rows {
		if r.State == manager.Running {
			running++
		}
	}
	return styleHeader.Render(truncate(fmt.Sprintf(" lazycomd — %d commands · %d running", len(m.table.rows), running), m.width))
}

// bottom shows a transient status line, then falls back to the key bar.
func (m Model) bottom() string {
	if m.status != "" && m.now().Sub(m.statusAt) < statusLife {
		return truncate(" "+m.status, m.width)
	}
	return keyBar(m.width, m.focus)
}

func (m Model) View() string {
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		m.table.View(),
		styleDim.Render("│"),
		m.logs.View(),
	)
	return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
}

// Run starts the TUI and blocks until the user quits.
func Run(c *client.Client) error {
	s := &sink{}
	p := tea.NewProgram(New(c, s), tea.WithAltScreen())
	s.fn = p.Send

	final, err := p.Run()
	if fm, ok := final.(Model); ok {
		fm.stream.stop()
	}
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ -v`
Expected: PASS, every test in the package.

If `TestResizeKeepsTheViewWithinWidth` fails with lines a few runes too wide, the cause is `lipgloss.JoinHorizontal` adding the separator: the table width plus 1 plus the log width must equal `m.width` exactly. Check the arithmetic in `layout`, not the test.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): root model with status polling and reconnect banner

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Focus routing, lifecycle keys, overlays, stream sync

**Files:**
- Modify: `internal/tui/tui.go` — replace `handleKey` and `layout`, add `handlePaletteKey`, `syncStream`, `logPaneVisible`, and extend `View`; call `syncStream` from the `statusMsg` case
- Test: `internal/tui/routing_test.go`

**Interfaces:**
- Consumes: `tableModel`, `logsModel`, `paletteModel`, `startStream`, `fetchTail`, `doAction`, `helpOverlay`, `focus`, `overlay`.
- Produces: `(*Model).syncStream() tea.Cmd`, `(Model).logPaneVisible() bool`, `(Model).handlePaletteKey(k tea.KeyMsg) (tea.Model, tea.Cmd)`.

**One spec ambiguity this task settles:** the spec gives `Esc` two jobs in the log pane — clear the filter, and go back to the table. Resolution: with a filter applied or being edited, `Esc` clears it and focus stays; with no filter, `Esc` returns focus to the table.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/routing_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

func modelWithRows(t *testing.T, names ...string) Model {
	t.Helper()
	cmds := map[string]config.Command{}
	for _, n := range names {
		cmds[n] = sleeper()
	}
	m, _, _ := newTestModel(t, cmds)

	list := make([]manager.Status, 0, len(names))
	for _, n := range names {
		list = append(list, manager.Status{Name: n, State: manager.Stopped})
	}
	m, _ = step(t, m, statusMsg(list))
	return m
}

func TestTabSwitchesFocus(t *testing.T) {
	m := modelWithRows(t, "a")
	if m.focus != focusTable {
		t.Fatal("should start on the table")
	}
	m, _ = step(t, m, key("tab"))
	if m.focus != focusLogs {
		t.Fatal("tab should focus the log pane")
	}
	m, _ = step(t, m, key("tab"))
	if m.focus != focusTable {
		t.Fatal("tab should come back to the table")
	}
}

func TestLifecycleKeysIssueActions(t *testing.T) {
	m := modelWithRows(t, "tick")
	for _, tc := range []struct{ k, verb string }{{"s", "start"}, {"S", "stop"}, {"r", "restart"}} {
		_, cmd := step(t, m, key(tc.k))
		if cmd == nil {
			t.Fatalf("%s produced no command", tc.k)
		}
		done, ok := cmd().(actionDoneMsg)
		if !ok {
			t.Fatalf("%s produced %T, want actionDoneMsg", tc.k, cmd())
		}
		if done.verb != tc.verb || done.name != "tick" {
			t.Fatalf("%s ran %+v, want %s tick", tc.k, done, tc.verb)
		}
	}
}

func TestLifecycleKeysAreInertWhileDisconnected(t *testing.T) {
	m := modelWithRows(t, "tick")
	m, _ = step(t, m, statusErrMsg{err: errNoDaemonForTest{}})

	m2, cmd := step(t, m, key("s"))
	if cmd != nil {
		t.Fatal("s should not fire an action while disconnected")
	}
	if !strings.Contains(m2.bottom(), "daemon not running") {
		t.Fatalf("status line = %q", m2.bottom())
	}
}

// errNoDaemonForTest stands in for a transport failure.
type errNoDaemonForTest struct{}

func (errNoDaemonForTest) Error() string { return "daemon not running" }

func TestHelpOverlaySwallowsKeys(t *testing.T) {
	m := modelWithRows(t, "a", "b")
	m, _ = step(t, m, key("?"))
	if m.overlay != overlayHelp {
		t.Fatal("? should open help")
	}
	if !strings.Contains(m.View(), "lazycomd — keys") {
		t.Fatalf("help not rendered:\n%s", m.View())
	}

	before, _ := m.table.Selected()
	m, cmd := step(t, m, key("j"))
	if cmd != nil {
		t.Fatal("keys should be ignored while help is open")
	}
	if after, _ := m.table.Selected(); after.Name != before.Name {
		t.Fatal("help must not pass navigation through")
	}

	m, _ = step(t, m, key("esc"))
	if m.overlay != overlayNone {
		t.Fatal("esc should close help")
	}

	m, _ = step(t, m, key("?"))
	_, cmd = step(t, m, key("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c must quit even with help open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c did not quit")
	}
}

func TestPaletteOpensStartsAndSelects(t *testing.T) {
	m := modelWithRows(t, "proxy", "app:api")

	m, _ = step(t, m, key("p"))
	if m.focus != focusPalette {
		t.Fatal("p should focus the palette")
	}
	for _, r := range "api" {
		m, _ = step(t, m, key(string(r)))
	}
	if !strings.Contains(m.View(), "app:api") {
		t.Fatalf("palette not rendered:\n%s", m.View())
	}

	m, cmd := step(t, m, key("enter"))
	if m.focus != focusTable {
		t.Fatal("enter should return focus to the table")
	}
	if got, _ := m.table.Selected(); got.Name != "app:api" {
		t.Fatalf("selected = %q, want app:api", got.Name)
	}
	if cmd == nil {
		t.Fatal("enter on a stopped command should start it")
	}
}

func TestPaletteEnterOnARunningCommandOnlySelects(t *testing.T) {
	m, _, _ := newTestModel(t, map[string]config.Command{"tick": sleeper()})
	m, _ = step(t, m, statusMsg{{Name: "tick", State: manager.Running, PID: 5}})

	m, _ = step(t, m, key("p"))
	m, cmd := step(t, m, key("enter"))

	if got, _ := m.table.Selected(); got.Name != "tick" {
		t.Fatalf("selected = %q", got.Name)
	}
	if cmd != nil {
		if _, isAction := cmd().(actionDoneMsg); isAction {
			t.Fatal("enter on a running command must not start it again")
		}
	}
}

func TestPaletteEscCloses(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("p"))
	m, _ = step(t, m, key("esc"))
	if m.focus != focusTable || m.overlay != overlayNone {
		t.Fatal("esc should close the palette")
	}
}

func TestEscInLogPaneClearsFilterThenReturnsFocus(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, logTailMsg{name: "a", lines: []string{"one", "two"}})
	m, _ = step(t, m, key("tab"))

	m, _ = step(t, m, key("/"))
	m, _ = step(t, m, key("o"))
	m, _ = step(t, m, key("enter"))
	if m.logs.Query() != "o" {
		t.Fatalf("query = %q, want o", m.logs.Query())
	}

	m, _ = step(t, m, key("esc")) // clears the filter, keeps focus
	if m.logs.Query() != "" {
		t.Fatalf("query = %q, want cleared", m.logs.Query())
	}
	if m.focus != focusLogs {
		t.Fatal("focus should stay on the log pane while a filter was set")
	}

	m, _ = step(t, m, key("esc")) // no filter left: back to the table
	if m.focus != focusTable {
		t.Fatal("esc with no filter should return to the table")
	}
}

func TestSlashFromTheTableFocusesTheLogFilter(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("/"))
	if m.focus != focusLogs {
		t.Fatal("/ should focus the log pane")
	}
	if !m.logs.FilterEditing() {
		t.Fatal("/ should open the filter input")
	}
}

func TestQIsLiteralWhileAnInputIsOpen(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("/"))
	m, cmd := step(t, m, key("q"))
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q must be literal text while the filter input is open")
		}
	}
	m, _ = step(t, m, key("enter"))
	if m.logs.Query() != "q" {
		t.Fatalf("query = %q, want q", m.logs.Query())
	}
}

func TestSelectionMoveFetchesTheNewTail(t *testing.T) {
	m := modelWithRows(t, "a", "b")
	_, cmd := step(t, m, key("j"))
	if cmd == nil {
		t.Fatal("moving the selection should fetch the new command's tail")
	}
}

func TestNarrowTerminalHidesTheLogPane(t *testing.T) {
	m := modelWithRows(t, "a")
	m, _ = step(t, m, key("tab"))
	if m.focus != focusLogs {
		t.Fatal("precondition: focus on the log pane")
	}
	m, _ = step(t, m, key("/"))

	m, _ = step(t, m, tea.WindowSizeMsg{Width: 50, Height: 20})
	if m.logPaneVisible() {
		t.Fatal("log pane should be hidden below 60 columns")
	}
	if m.focus != focusTable {
		t.Fatal("focus should fall back to the table")
	}
	if m.logs.FilterEditing() {
		t.Fatal("the filter input should close")
	}
	if strings.Contains(m.View(), "│") {
		t.Fatalf("the pane separator should be gone:\n%s", m.View())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestTab|TestLifecycle|TestHelpOverlay|TestPalette|TestEscInLog|TestSlash|TestQIsLiteral|TestSelectionMove|TestNarrow' -v`
Expected: FAIL — `m.logPaneVisible undefined`, plus routing failures.

- [ ] **Step 3: Sync the stream when the selection changes**

In `internal/tui/tui.go`, add the stream sync and the visibility helper:

```go
// syncStream points the single SSE stream at the selected command and returns
// the command that seeds the pane's scrollback.
func (m *Model) syncStream() tea.Cmd {
	sel, ok := m.table.Selected()
	if !ok {
		m.stream.stop()
		m.stream = nil
		return nil
	}
	if m.stream != nil && m.stream.name == sel.Name {
		return nil
	}
	m.stream.stop()
	m.stream = startStream(m.client, m.sink, sel.Name)
	return fetchTail(m.client, sel.Name)
}

// logPaneVisible is false on a terminal too narrow to split.
func (m Model) logPaneVisible() bool { return m.width >= 60 }
```

Then change the `statusMsg` case of `Update` to keep the stream in step with a
selection that moved because rows appeared or disappeared:

```go
	case statusMsg:
		m.connected = true
		m.table.SetRows([]manager.Status(msg))
		return m, m.syncStream()
```

- [ ] **Step 4: Replace `handleKey` with full routing**

Replace the placeholder `handleKey` in `internal/tui/tui.go` with:

```go
func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()

	// Help swallows every key but its own dismissal; Ctrl-C always quits.
	if m.overlay == overlayHelp {
		switch s {
		case "ctrl+c":
			return m, tea.Quit
		case "?", "esc", "q":
			m.overlay = overlayNone
		}
		return m, nil
	}
	if s == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.focus {
	case focusPalette:
		return m.handlePaletteKey(k)
	case focusLogs:
		return m.handleLogKey(k)
	default:
		return m.handleTableKey(k)
	}
}

func (m Model) handleTableKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.overlay = overlayHelp
		return m, nil
	case "tab":
		if m.logPaneVisible() {
			m.focus = focusLogs
		}
		return m, nil
	case "p":
		m.palette.Open(m.table.rows)
		m.focus = focusPalette
		return m, textinput.Blink
	case "s", "S", "r":
		return m.runAction(k.String())
	case "f":
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(k)
		return m, cmd
	case "/":
		if !m.logPaneVisible() {
			return m, nil
		}
		m.focus = focusLogs
		var cmd tea.Cmd
		m.logs, cmd = m.logs.Update(k)
		return m, cmd
	}

	before, _ := m.table.Selected()
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(k)
	if after, _ := m.table.Selected(); after.Name != before.Name {
		return m, tea.Batch(cmd, m.syncStream())
	}
	return m, cmd
}

// runAction fires one lifecycle verb for the selected command.
func (m Model) runAction(key string) (tea.Model, tea.Cmd) {
	sel, ok := m.table.Selected()
	if !ok {
		return m, nil
	}
	if !m.connected {
		m.setStatus("daemon not running (start with: lazycomd serve)")
		return m, nil
	}
	verb := map[string]string{"s": "start", "S": "stop", "r": "restart"}[key]
	return m, doAction(m.client, verb, sel.Name)
}

func (m Model) handleLogKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.logs.FilterEditing() {
		switch k.String() {
		case "q":
			return m, tea.Quit
		case "?":
			m.overlay = overlayHelp
			return m, nil
		case "tab":
			m.focus = focusTable
			return m, nil
		case "esc":
			// Esc wears two hats: clear the filter if there is one, else
			// hand focus back to the table.
			if m.logs.Query() == "" {
				m.focus = focusTable
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.logs, cmd = m.logs.Update(k)
	return m, cmd
}

func (m Model) handlePaletteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.palette.Close()
		m.focus = focusTable
		return m, nil
	case "enter":
		sel, ok := m.palette.Highlighted()
		m.palette.Close()
		m.focus = focusTable
		if !ok {
			return m, nil
		}
		m.table.SelectName(sel.Name)
		cmds := []tea.Cmd{m.syncStream()}
		if sel.State != manager.Running && sel.State != manager.Starting {
			cmds = append(cmds, doAction(m.client, "start", sel.Name))
		}
		return m, tea.Batch(cmds...)
	}
	var cmd tea.Cmd
	m.palette, cmd = m.palette.Update(k)
	return m, cmd
}
```

Add `"github.com/charmbracelet/bubbles/textinput"` to the imports of `tui.go`.

- [ ] **Step 5: Replace `layout` with the narrow rules and extend `View`**

```go
func (m *Model) layout() {
	m.bodyH = m.height - 2
	if m.bodyH < 3 {
		m.bodyH = 3
	}

	// Too narrow to split: the table takes everything.
	if m.width < 60 {
		m.tableW = m.width
		m.table.SetSize(m.tableW, m.bodyH, true)
		m.logs.SetSize(1, m.bodyH)
		if m.focus == focusLogs {
			m.focus = focusTable
		}
		m.logs.CancelFilterEdit()
		return
	}

	m.tableW = m.width * 40 / 100
	if m.tableW < 32 {
		m.tableW = 32
	}
	if m.tableW > m.width-20 {
		m.tableW = m.width - 20
	}
	m.table.SetSize(m.tableW, m.bodyH, m.width < 80)
	m.logs.SetSize(m.width-m.tableW-1, m.bodyH)
}

func (m Model) View() string {
	if m.overlay == overlayHelp {
		return strings.Join([]string{m.header(), helpOverlay(m.width, m.bodyH), m.bottom()}, "\n")
	}

	left := m.table.View()
	if m.focus == focusPalette {
		left = m.palette.View(m.tableW, m.bodyH)
	}
	if !m.logPaneVisible() {
		return strings.Join([]string{m.header(), left, m.bottom()}, "\n")
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, styleDim.Render("│"), m.logs.View())
	return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ -v`
Expected: PASS, every test in the package.

`TestSelectionMoveFetchesTheNewTail` depends on `syncStream` actually opening a stream against the test daemon — that daemon is real, so the command it returns is a real `fetchTail`. If it hangs, check that `stop()` is not being called on a handle whose goroutine already exited (the `done` channel is closed exactly once, in the goroutine's defer).

- [ ] **Step 7: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): focus routing, lifecycle keys, overlays and stream sync

Esc in the log pane clears an active filter and otherwise returns focus to
the table; the spec gave it both jobs without ordering them.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Launch the TUI from a bare `lazycomd`

**Files:**
- Modify: `cmd/lazycomd/main.go` — bare invocation launches the TUI; extend `usage`
- Modify: `cmd/lazycomd/cli_test.go` — add the no-TTY case
- Test: `cmd/lazycomd/cli_test.go`

**Interfaces:**
- Consumes: `tui.Run(c *client.Client) error`, `client.Default()`, `term.IsTerminal`.
- Produces: `func runTUI() int` in `package main`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/lazycomd/cli_test.go`:

```go
func TestBareInvocationWithoutATTYPrintsUsage(t *testing.T) {
	// go test never gives us a terminal, so this exercises the guard: the
	// TUI must not launch, and the exit code stays 2 as before.
	testDaemon(t, map[string]config.Command{"a": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"}})
	if code := dispatch(nil); code != 2 {
		t.Fatalf("bare dispatch = %d, want 2 without a TTY", code)
	}
}

func TestUsageMentionsTheTUI(t *testing.T) {
	if !strings.Contains(usage, "lazycomd") || !strings.Contains(usage, "TUI") {
		t.Fatalf("usage should say the bare command opens the TUI:\n%s", usage)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/lazycomd/ -run 'TestBareInvocation|TestUsageMentions' -v`
Expected: `TestUsageMentions` FAILS (`usage` has no TUI line). `TestBareInvocation` passes for the wrong reason right now — bare `dispatch(nil)` already returns 2 — and must keep passing after the change.

- [ ] **Step 3: Write the implementation**

In `cmd/lazycomd/main.go`, extend the usage banner:

```go
const usage = `lazycomd - run and supervise long dev commands

usage: lazycomd [command] [flags]

  (no command)             open the TUI
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
```

Replace the empty-args branch of `dispatch`:

```go
func dispatch(args []string) int {
	if len(args) == 0 {
		return runTUI()
	}
	// ... the switch stays exactly as it is
```

And add the launcher, in `main.go` below `dispatch`:

```go
// runTUI opens the interactive UI. Without a terminal — a pipe, CI, a test
// binary — it prints usage instead, so `lazycomd | cat` stays sane.
func runTUI() int {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	if err := tui.Run(c); err != nil {
		return fail(err)
	}
	return 0
}
```

Add to `main.go`'s imports:

```go
import (
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/tui"
)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/lazycomd/ -v`
Expected: PASS, every CLI and serve test, including the pre-existing
`TestDispatchUsageErrors` — bare `dispatch(nil)` still exits 2 because the
test binary has no terminal.

- [ ] **Step 5: Build and launch it by hand**

```bash
SP=/private/tmp/claude-501/-Users-tphuc/ed163cab-b6bd-4c7b-901e-7bb5bf910640/scratchpad
go build -o $SP/lazycomd ./cmd/lazycomd
mkdir -p $SP/lzc-config
cat > $SP/lzc-config/config.yaml <<'CFG'
commands:
  tick:
    cmd: ["sh", "-c", "while true; do date +%s; sleep 1; done"]
    cwd: /tmp
    restart: on-failure
  noisy:
    cmd: ["sh", "-c", "i=0; while true; do i=$((i+1)); echo \"line $i error=$((i % 5))\"; sleep 0.2; done"]
    cwd: /tmp
  greet:
    cmd: ["sh", "-c", "echo hello from lazycomd"]
    cwd: /tmp
CFG
LAZYCOMD_CONFIG=$SP/lzc-config/config.yaml XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd serve &
sleep 1
XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd    # the TUI
```

Walk through it: `s` on `tick`, watch the log pane fill, `j` to `noisy` and
confirm the pane switches, `/error=0` then Enter to filter, `Esc`, `p` then
`gre` then Enter, `?` for help, `q` to quit. Then kill the daemon while the
TUI is open and confirm the banner appears and recovers when you restart it.
Finally `XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd | cat` must print usage,
not garbage.

- [ ] **Step 6: Commit**

```bash
git add cmd/lazycomd/
git commit -m "feat(cli): bare lazycomd opens the TUI on a terminal

Without a TTY it prints usage and exits 2, so pipes and CI behave as before.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: End-to-end test, README, full verification

**Files:**
- Create: `internal/tui/integration_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: everything.
- Produces: no new code.

- [ ] **Step 1: Write the end-to-end test**

Create `internal/tui/integration_test.go`. This drives the model the way the
runtime does — messages in, commands out — against a real daemon, and asserts
the daemon's own state changed.

```go
package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

// drive runs cmd and feeds every resulting message back into the model,
// stopping at a tick so the loop terminates.
func drive(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for depth := 0; cmd != nil && depth < 20; depth++ {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			cmd = nil
			for _, c := range batch {
				if c == nil {
					continue
				}
				m = drive(t, m, c)
			}
			continue
		}
		if _, isTick := msg.(tickMsg); isTick {
			return m
		}
		next, nextCmd := m.Update(msg)
		m = next.(Model)
		cmd = nextCmd
	}
	return m
}

func TestEndToEndStartAndWatch(t *testing.T) {
	c, mgr := testDaemon(t, map[string]config.Command{
		"tick":  {Cmd: []string{"sh", "-c", "while true; do echo tick; sleep 0.05; done"}, Cwd: "/tmp"},
		"greet": {Cmd: []string{"sh", "-c", "echo hello; sleep 30"}, Cwd: "/tmp"},
	})
	s, _ := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)

	// First poll: rows appear and the stream syncs to the selection.
	m = drive(t, m, fetchStatus(m.client))
	if !strings.Contains(m.View(), "greet") || !strings.Contains(m.View(), "tick") {
		t.Fatalf("view missing commands:\n%s", m.View())
	}

	// `s` starts the selected command; the daemon must really run it.
	m, cmd := step(t, m, key("s"))
	m = drive(t, m, cmd)

	sel, _ := m.table.Selected()
	waitFor(t, "the selected command to run", func() bool {
		st, err := mgr.Status(sel.Name)
		return err == nil && st.State == manager.Running
	})
	if !strings.Contains(m.View(), "running") {
		t.Fatalf("view does not show the started command:\n%s", m.View())
	}
}

func TestEndToEndPaletteStartsAnotherCommand(t *testing.T) {
	c, mgr := testDaemon(t, map[string]config.Command{
		"tick":  {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
		"greet": {Cmd: []string{"sh", "-c", "echo hello; sleep 30"}, Cwd: "/tmp"},
	})
	s, _ := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = drive(t, next.(Model), fetchStatus(c))

	m, _ = step(t, m, key("p"))
	for _, r := range "greet" {
		m, _ = step(t, m, key(string(r)))
	}
	m, cmd := step(t, m, key("enter"))
	m = drive(t, m, cmd)

	if got, _ := m.table.Selected(); got.Name != "greet" {
		t.Fatalf("selected = %q, want greet", got.Name)
	}
	waitFor(t, "greet to run", func() bool {
		st, err := mgr.Status("greet")
		return err == nil && st.State == manager.Running
	})
}

func TestEndToEndLiveLinesReachThePane(t *testing.T) {
	c, mgr := testDaemon(t, map[string]config.Command{
		"tick": {Cmd: []string{"sh", "-c", "while true; do echo tick; sleep 0.05; done"}, Cwd: "/tmp"},
	})
	if err := mgr.Start("tick"); err != nil {
		t.Fatal(err)
	}

	s, ch := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = drive(t, next.(Model), fetchStatus(c))
	defer func() { m.stream.stop() }()

	// A live line arrives through the sink; feed it in like the runtime does.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-ch:
			if line, ok := msg.(logLineMsg); ok {
				m, _ = step(t, m, line)
				if strings.Contains(m.View(), "tick") {
					return
				}
			}
		case <-deadline:
			t.Fatalf("no live line reached the pane:\n%s", m.View())
		}
	}
}

func TestEndToEndSurvivesADeadDaemon(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{"tick": sleeper()})
	s, _ := captureSink()
	m := New(c, s)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = drive(t, next.(Model), fetchStatus(c))

	// Simulate the daemon going away, then coming back.
	m, _ = step(t, m, statusErrMsg{err: errNoDaemonForTest{}})
	if !strings.Contains(m.header(), "daemon not running") {
		t.Fatalf("header = %q", m.header())
	}
	m = drive(t, m, fetchStatus(c))
	if strings.Contains(m.header(), "daemon not running") {
		t.Fatalf("banner did not clear after recovery: %q", m.header())
	}
}
```

If `tea.BatchMsg` does not exist in the version you resolved, replace the
batch branch of `drive` with an explicit sequence: run `fetchStatus`, feed its
message, then run whatever command that returns. The assertions do not change.

- [ ] **Step 2: Run the end-to-end tests**

Run: `go test -race ./internal/tui/ -run TestEndToEnd -v`
Expected: PASS, four tests.

- [ ] **Step 3: Update the README**

In `README.md`, insert a TUI section directly after the `## Install` section:

````markdown
## The TUI

Run `lazycomd` with no arguments:

```
┌ lazycomd — 5 commands · 2 running ────────────────────────────────┐
│ NAME        STATE    PID    UPTIME  RS │ tick — following         │
│>tick        running  54405  2m13s    0 │ 1789114081               │
│ greet       stopped  -      -        0 │ 1789114082               │
│ app:api     failed   -      -        3 │ 1789114083               │
└────────────────────────────────────────┴──────────────────────────┘
 j/k move  s start  S stop  r restart  p palette  f follow  / filter  ? help
```

| Key | Where | Action |
|---|---|---|
| `j` `k` `↓` `↑` | table | move the cursor |
| `g` `G` | table | first / last command |
| `s` | table | start, dependencies first |
| `S` | table | stop |
| `r` | table | restart |
| `p` | table | fuzzy command palette |
| `Tab` | either pane | switch panes |
| `j` `k` | log pane | scroll (turns follow off) |
| `Ctrl-D` `Ctrl-U` | log pane | half page |
| `g` `G` | log pane | top / bottom |
| `f` | either pane | toggle follow |
| `/` | either pane | filter the log pane |
| `Esc` | log pane | clear the filter, else back to the table |
| `Enter` | palette | start the command and select it |
| `?` | anywhere | help overlay |
| `q` `Ctrl-C` | anywhere | quit (the daemon keeps running) |

The filter is a plain substring with smart case: a lowercase query matches
case-insensitively. A state shown as `running*` means the config changed under
a reload and the new spec applies on that command's next start.

Quitting the TUI stops nothing. If the daemon goes away, the TUI shows a
banner and retries every 2s rather than exiting.

With no terminal — `lazycomd | cat`, CI — the bare command prints usage
instead of launching.
````

- [ ] **Step 4: Check the README against the code**

Open `README.md` beside `internal/tui/help.go`. Every key in the README table
must exist in `bindings`, and every binding must appear in the README. Fix the
README where they disagree.

- [ ] **Step 5: Verify the whole repository**

```bash
gofmt -l .
go vet ./...
go test -race ./...
go build -o /tmp/lazycomd ./cmd/lazycomd
go test ./internal/depsguard/ -v
```

Expected: `gofmt` silent, `go vet` clean, every package passing with no data
races, the binary building, and the dependency guard confirming the daemon
packages never picked up a TUI import.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/ README.md
git commit -m "test(tui): end-to-end coverage against a real daemon, plus README

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

Checked against `docs/superpowers/specs/2026-09-11-lazycomd-tui-design.md`:

| Spec section | Covered by |
|---|---|
| Approach: bubbletea + lipgloss, own table, bubbles primitives | Tasks 1, 4, 5, 7 |
| Dependency confinement and its enforcing test | Task 1 |
| Layout, 40% table, 80- and 60-column rules | Tasks 10, 11 |
| Package structure | File Structure table; Tasks 2-11 create exactly those files |
| Entry point: bare command, no-TTY fallback | Task 12 |
| `Model` fields, every message type, tick cadence | Tasks 3, 10 |
| Selection preserved by name | Task 4 |
| Stream lifecycle, one stream, late-line drop, no goroutine leak | Tasks 9, 10, 11 |
| Table columns, colors, `running*`, uptime format, keys | Task 4, keys in Task 11 |
| `s` always starts with dependencies; no confirmations | Tasks 3, 11 |
| Action results merged ahead of the tick; 409 into the status line | Tasks 10, 11 |
| Log pane: own line store, 5000 cap, 500-line seed, follow rules | Task 5 |
| Filter: substring, smart-case, counts in the title, no regex, no n/N | Task 6 |
| Truncate rather than wrap, with the ponytail marker | Task 5 |
| Palette: rank, highlight, Enter semantics for running vs stopped | Tasks 2, 7, 11 |
| Help from one binding slice, dismissal keys, swallows other keys | Tasks 8, 11 |
| Reconnection: banner, dimmed rows, 2s tick, inert lifecycle keys | Tasks 10, 11 |
| Never exits on an API error | Tasks 10, 11, 13 |
| Every test runs without a terminal | All tasks; no test calls `Program.Run` |
| Test table from the spec | Tasks 2, 4, 5, 6, 7, 10, 11, 13 |
| Non-goals | Nothing in the plan implements them |
| Definition of done | Task 12 Step 5 (hand walkthrough), Task 13 Steps 2 and 5 |

Gaps found and closed while reviewing:

- **The spec gave `Esc` two jobs in the log pane** — clear the filter, and
  return to the table — without ordering them. Task 11 settles it: clear the
  filter when there is one, otherwise move focus, and tests both.
- **`focus` and `overlay` had no home.** The spec lists them as root-model
  fields, but `keyBar` needs `focus` and is built one task earlier, so Task 8
  declares both types and Task 10 consumes them.
- **The spec never said what `f` and `/` do while the table has focus.** The
  key bar lists them as global. Task 11 routes both into the log pane, and `/`
  also moves focus there.
- **Below 60 columns the spec hides the log pane but says nothing about `/`
  and `Tab`.** Task 11 makes both inert while the pane is hidden.
- **The daemon's status line and the disconnect banner needed distinct
  homes**: the status line replaces the key bar for 4s (`statusLife`,
  injectable `now` for the test), the banner sits in the header.
- **`Reset` clearing the filter** is in the spec's message table but not its
  prose; Task 6 implements and tests it.

Type consistency: `manager.Status` is the only wire type across tasks;
`candidate` (Task 2) is consumed unchanged by the palette (Task 7); `focus`
and `overlay` (Task 8) are consumed by the root model (Tasks 10, 11);
`logsModel` gains the filter in Task 6 without changing a signature from
Task 5; `sink` and `streamHandle` (Task 9) are used exactly as declared in
Tasks 10 and 11. The test helpers `key`, `rows`, `step`, `newTestModel`,
`captureSink`, `testDaemon`, `waitFor`, `sleeper` and `echoer` are each
declared once — `key`/`rows` in Task 4, `testDaemon`/`sleeper`/`echoer`/
`waitFor` in Task 3, `captureSink` in Task 9, `step`/`newTestModel` in
Task 10 — and reused by name afterwards.
