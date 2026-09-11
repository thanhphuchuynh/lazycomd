# lazycomd Config Writes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a command be added, edited and removed from the TUI and the API, with the daemon rewriting the config file safely enough to use on a file you have committed.

**Architecture:** A new `internal/configw` package splices YAML by line range: it parses to a `yaml.Node` for line numbers, replaces only the lines the target command occupies, validates the result in memory, and lands it through a temp file and rename. `internal/config` stays read-only. Four endpoints expose read, create, update and delete; the TUI gets a four-field form on `a`, the same form prefilled on `e`, and a confirmed delete on `d`.

**Tech Stack:** Go 1.22+, `gopkg.in/yaml.v3` (already a dependency), bubbletea/bubbles for the form. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-11-lazycomd-config-writes-design.md`

## Global Constraints

- Specs #1, #2 and #3 are merged on `main`. Read all three design docs in `docs/superpowers/specs/` before starting; this plan assumes their vocabulary (process groups, `spec_dirty`, panels, the probe sampler).
- **No new dependencies.** `internal/configw` uses the standard library plus `yaml.v3`. `internal/depsguard` must keep passing, and `configw` must never import a Charm library.
- `internal/config` stays read-only. Nothing in this plan adds a write path to it beyond `ParseBytes`, which is a parse.
- **Validate before writing, always.** A splice that would produce a file the daemon cannot load is refused and nothing touches disk.
- Writes land through a temp file in the same directory, `fsync`, then `rename`. The original file mode is preserved.
- Before the rename, re-stat the file: if size or modification time moved since the read, abandon with `ErrChanged`. The human editing the file wins.
- Untouched lines must come out byte-identical. Comments, blank lines, quoting and indentation elsewhere in the file are never re-encoded.
- Deleting a command leaves trailing comments and blank lines alone — a comment above the next command belongs to that command.
- A bare name writes to the global config; `app:api` writes to that project's `lazycomd.yaml` under the bare key `api`. An unknown namespace is a 400.
- `PUT` replaces a whole spec, so the form must first read the existing one via `GET /v1/commands/{name}/config` and send back the fields it does not own.
- The form's fields are NAME, COMMAND, FOLDER, RESTART. The YAML key stays `cwd`; only the label says FOLDER.
- FOLDER prefills with the TUI's launch directory, switches to the project's directory once the name is namespaced, and stays blank when the client is pointed at a TCP address.
- A command line containing `|`, `>`, `<`, `&`, `;`, `$` or `*` sets `shell: true` and is passed through whole; otherwise it splits on whitespace.
- Every task ends with `gofmt -l .` silent, `go vet ./...` clean, and `go test -race ./...` passing.
- Commits: `feat(configw):`, `feat(api):`, `feat(tui):`, `feat(config):`, ending with the repo's attribution trailer (`Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`).

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/config/config.go` | (modify) `ParseBytes`, so a spliced buffer can be validated without touching disk |
| `internal/config/load.go` | (modify) `Config.Projects`, basename to directory |
| `internal/configw/render.go` | Rendering one command to YAML lines at a given indent |
| `internal/configw/locate.go` | Finding the `commands` mapping and a command's line range |
| `internal/configw/configw.go` | `File`, `Registry`, `Create`/`Update`/`Delete`, validate-then-write |
| `internal/configw/atomic.go` | Temp-file write, mode preservation, the `ErrChanged` guard |
| `internal/api/options.go` | `Options` and `New`, replacing the widening `NewServer` argument list |
| `internal/api/write.go` | The four command-config endpoints and file resolution |
| `internal/client/client.go` | (modify) `CommandConfig`, `Create`, `Update`, `Delete` |
| `internal/tui/form.go` | The add/edit form: fields, navigation, prefill, shell detection |
| `internal/tui/confirm.go` | The delete confirmation |
| `internal/tui/tui.go` | (modify) `a`, `e`, `d`, the two new overlays, save and delete wiring |
| `internal/tui/messages.go` | (modify) form and delete messages and commands |
| `internal/tui/help.go` | (modify) the three new bindings |
| `cmd/lazycomd/serve.go` | (modify) build the writer registry and pass the config path |
| `README.md` | (modify) the keys and the endpoints |

---

### Task 1: `Config.Projects` and `config.ParseBytes`

**Files:**
- Modify: `internal/config/load.go` — keep the project map `Load` already computes
- Modify: `internal/config/config.go` — `ParseBytes`, with `ParseFile` delegating to it
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: existing `config.Load`, `config.ParseFile`.
- Produces:
  - `Config.Projects map[string]string` — project basename to absolute directory.
  - `func ParseBytes(data []byte, name string) (*File, error)` — `name` is only used in error messages.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/load_test.go`:

```go
func TestLoadKeepsTheProjectMap(t *testing.T) {
	g := tree(t, map[string]string{
		"config.yaml":           "projects:\n  - @ROOT@/scraper\ncommands: {}\n",
		"scraper/lazycomd.yaml": "commands:\n  api:\n    cmd: [\"sleep\", \"1\"]\n",
	})
	cfg, err := Load(g)
	if err != nil {
		t.Fatal(err)
	}
	dir, ok := cfg.Projects["scraper"]
	if !ok {
		t.Fatalf("Projects = %v, want a scraper entry", cfg.Projects)
	}
	if !strings.HasSuffix(dir, "/scraper") {
		t.Fatalf("Projects[scraper] = %q, want the project directory", dir)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("Projects = %v, want exactly one entry", cfg.Projects)
	}
}

func TestParseBytes(t *testing.T) {
	f, err := ParseBytes([]byte("commands:\n  a:\n    cmd: [\"x\"]\n"), "memory")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(f.Commands) != 1 {
		t.Fatalf("commands = %v", f.Commands)
	}

	// The name given is what shows up in the error.
	_, err = ParseBytes([]byte("listten: 1\n"), "spliced.yaml")
	if err == nil || !strings.Contains(err.Error(), "spliced.yaml") {
		t.Fatalf("err = %v, want it to name the buffer", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run 'TestLoadKeepsTheProjectMap|TestParseBytes' -v`
Expected: FAIL — `cfg.Projects undefined` and `undefined: ParseBytes`.

- [ ] **Step 3: Add `ParseBytes`**

In `internal/config/config.go`, split `ParseFile`:

```go
// ParseFile decodes one YAML file, rejecting unknown fields.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data, path)
}

// ParseBytes decodes YAML that is already in memory. name appears in errors,
// so a spliced buffer can be validated before it is written anywhere.
func ParseBytes(data []byte, name string) (*File, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var out File
	if err := dec.Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return &File{Commands: map[string]Command{}}, nil
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if out.Commands == nil {
		out.Commands = map[string]Command{}
	}
	return &out, nil
}
```

Add `"bytes"` to the imports; `os.Open` is no longer needed there.

- [ ] **Step 4: Keep the project map**

In `internal/config/load.go`, add the field:

```go
type Config struct {
	Listen    string
	TokenFile string
	Commands  map[string]Command
	Projects  map[string]string // project basename -> directory
}
```

and assign it in `Load`, where `byBase` is already built — after the project loop, before `resolveDeps`:

```go
	cfg.Projects = byBase
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, every config test.

- [ ] **Step 6: Commit**

```bash
git add internal/config/
git commit -m "feat(config): expose the project map and parse from memory

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Rendering a command block

**Files:**
- Create: `internal/configw/render.go`
- Test: `internal/configw/render_test.go`

**Interfaces:**
- Consumes: `config.Command`.
- Produces: `func renderBlock(name string, c config.Command, indent int) ([]string, error)` — the YAML lines for one command, the key indented by `indent` spaces, no trailing blank line.

- [ ] **Step 1: Write the failing test**

Create `internal/configw/render_test.go`:

```go
package configw

import (
	"slices"
	"strings"
	"testing"

	"github.com/tphuc/lazycomd/internal/config"
)

func TestRenderBlockMinimal(t *testing.T) {
	got, err := renderBlock("web", config.Command{Cmd: []string{"npm", "start"}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"  web:",
		"    cmd:",
		"      - npm",
		"      - start",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("renderBlock =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRenderBlockOmitsEmptyFields(t *testing.T) {
	got, err := renderBlock("web", config.Command{
		Cmd:     []string{"npm", "start"},
		Cwd:     "/tmp",
		Restart: config.RestartOnFailure,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"cwd: /tmp", "restart: on-failure"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("block missing %q:\n%s", want, joined)
		}
	}
	// Everything unset stays out of the file.
	for _, gone := range []string{"env", "shell", "autostart", "log", "size", "depends_on", "health", "port"} {
		if strings.Contains(joined, gone+":") {
			t.Fatalf("block carries an unset field %q:\n%s", gone, joined)
		}
	}
}

func TestRenderBlockDropsTheDefaultRestart(t *testing.T) {
	// restart: no is the default; writing it adds noise to the file.
	got, _ := renderBlock("web", config.Command{Cmd: []string{"x"}, Restart: config.RestartNo}, 2)
	if strings.Contains(strings.Join(got, "\n"), "restart") {
		t.Fatalf("block writes the default restart:\n%s", strings.Join(got, "\n"))
	}
}

func TestRenderBlockKeepsRicherFields(t *testing.T) {
	got, err := renderBlock("api", config.Command{
		Cmd:       []string{"node", "server.js"},
		Env:       map[string]string{"LOG": "debug"},
		DependsOn: []string{"db"},
		Health:    "http://localhost:3000/healthz",
		Port:      3000,
		Autostart: true,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"env:", "LOG: debug", "depends_on:", "- db", "health: http://localhost:3000/healthz", "port: 3000", "autostart: true"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("block missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderBlockIndent(t *testing.T) {
	got, err := renderBlock("web", config.Command{Cmd: []string{"x"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "    web:" {
		t.Fatalf("first line = %q, want four spaces of indent", got[0])
	}
	for _, l := range got[1:] {
		if !strings.HasPrefix(l, "      ") {
			t.Fatalf("child line not indented under the key: %q", l)
		}
	}
}

func TestRenderBlockRoundTrips(t *testing.T) {
	// Whatever we render must parse back into the same command.
	in := config.Command{
		Cmd:     []string{"sh", "-c", "echo hi && sleep 1"},
		Cwd:     "/tmp",
		Shell:   true,
		Restart: config.RestartAlways,
		Port:    8099,
	}
	lines, err := renderBlock("web", in, 2)
	if err != nil {
		t.Fatal(err)
	}
	doc := "commands:\n" + strings.Join(lines, "\n") + "\n"

	f, err := config.ParseBytes([]byte(doc), "rendered")
	if err != nil {
		t.Fatalf("rendered block does not parse: %v\n%s", err, doc)
	}
	got := f.Commands["web"]
	if !slices.Equal(got.Cmd, in.Cmd) || got.Cwd != in.Cwd || !got.Shell || got.Restart != in.Restart || got.Port != in.Port {
		t.Fatalf("round trip lost fields: %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/configw/ -v`
Expected: FAIL — `undefined: renderBlock`.

- [ ] **Step 3: Write the implementation**

Create `internal/configw/render.go`:

```go
// Package configw modifies lazycomd config files in place, changing only the
// lines a command occupies so the rest of the file — comments, quoting,
// blank lines — survives byte-for-byte.
package configw

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tphuc/lazycomd/internal/config"
)

// wire mirrors config.Command with every optional field omitempty, so a
// command with two fields set renders as two lines rather than the whole
// schema padded out with zeros.
type wire struct {
	Cmd       []string          `yaml:"cmd"`
	Cwd       string            `yaml:"cwd,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Shell     bool              `yaml:"shell,omitempty"`
	Restart   string            `yaml:"restart,omitempty"`
	Autostart bool              `yaml:"autostart,omitempty"`
	Log       bool              `yaml:"log,omitempty"`
	Size      int               `yaml:"size,omitempty"`
	DependsOn []string          `yaml:"depends_on,omitempty"`
	Health    string            `yaml:"health,omitempty"`
	Port      int               `yaml:"port,omitempty"`
}

// renderBlock returns the YAML lines for one command, with its key indented by
// indent spaces and its fields one level further in.
func renderBlock(name string, c config.Command, indent int) ([]string, error) {
	w := wire{
		Cmd:       c.Cmd,
		Cwd:       c.Cwd,
		Env:       c.Env,
		Shell:     c.Shell,
		Autostart: c.Autostart,
		Log:       c.Log,
		Size:      c.Size,
		DependsOn: c.DependsOn,
		Health:    c.Health,
		Port:      c.Port,
	}
	// "no" is the default; writing it only adds noise.
	if c.Restart != "" && c.Restart != config.RestartNo {
		w.Restart = string(c.Restart)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]wire{name: w}); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	pad := strings.Repeat(" ", indent)
	var out []string
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		out = append(out, pad+line)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/configw/ -v`
Expected: PASS, six tests.

If `TestRenderBlockMinimal` fails on the list style — `cmd: [npm, start]` on one line instead of a block sequence — that is `yaml.v3` choosing flow style for short lists. Update the expectation to whatever it emits, provided `TestRenderBlockRoundTrips` still passes: the file only has to parse back correctly, not match a particular style.

- [ ] **Step 5: Commit**

```bash
git add internal/configw/
git commit -m "feat(configw): render one command block at a given indent

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Locating a command in the file

**Files:**
- Create: `internal/configw/locate.go`
- Test: `internal/configw/locate_test.go`

**Interfaces:**
- Consumes: `yaml.Node`.
- Produces:
  - `func commandsNode(doc *yaml.Node) (*yaml.Node, bool)` — the `commands` mapping node.
  - `func blockRange(doc *yaml.Node, name string) (start, end, indent int, ok bool)` — 1-based inclusive line range and the key's indentation in spaces.
  - `func commandsEnd(doc *yaml.Node) (line, indent int, ok bool)` — the last line of the `commands` mapping and the indentation its keys use, for appending.

- [ ] **Step 1: Write the failing test**

Create `internal/configw/locate_test.go`:

```go
package configw

import (
	"testing"

	"gopkg.in/yaml.v3"
)

const sample = `# lazycomd config
listen: ""

commands:
  # the http server
  web:
    cmd: ["npm", "start"]
    cwd: /tmp

  # a chatty one
  noisy:
    cmd: ["sh", "-c", "while true; do echo hi; sleep 1; done"]
    restart: always
`

func parse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

func TestBlockRangeFindsACommand(t *testing.T) {
	doc := parse(t, sample)

	start, end, indent, ok := blockRange(doc, "web")
	if !ok {
		t.Fatal("web not found")
	}
	// Line 6 is "  web:", and its block runs through the cwd line.
	if start != 6 || end != 8 {
		t.Fatalf("range = %d..%d, want 6..8", start, end)
	}
	if indent != 2 {
		t.Fatalf("indent = %d, want 2", indent)
	}
}

func TestBlockRangeLastCommand(t *testing.T) {
	doc := parse(t, sample)

	start, end, _, ok := blockRange(doc, "noisy")
	if !ok {
		t.Fatal("noisy not found")
	}
	if start != 11 {
		t.Fatalf("start = %d, want 11", start)
	}
	if end != 13 {
		t.Fatalf("end = %d, want 13 (its last field, not the end of the file)", end)
	}
}

func TestBlockRangeMissing(t *testing.T) {
	if _, _, _, ok := blockRange(parse(t, sample), "ghost"); ok {
		t.Fatal("ok = true for a command that is not there")
	}
}

func TestBlockRangeExcludesTheFollowingComment(t *testing.T) {
	// "# a chatty one" sits between the two commands and belongs to noisy.
	// web's range must stop before it, or deleting web takes it along.
	_, end, _, _ := blockRange(parse(t, sample), "web")
	if end >= 10 {
		t.Fatalf("web's range ends at %d, which swallows the comment on line 10", end)
	}
}

func TestCommandsEnd(t *testing.T) {
	line, indent, ok := commandsEnd(parse(t, sample))
	if !ok {
		t.Fatal("no commands mapping found")
	}
	if line != 13 {
		t.Fatalf("end line = %d, want 13", line)
	}
	if indent != 2 {
		t.Fatalf("indent = %d, want 2", indent)
	}
}

func TestCommandsNodeMissing(t *testing.T) {
	if _, ok := commandsNode(parse(t, "listen: \"\"\n")); ok {
		t.Fatal("ok = true for a file with no commands key")
	}
	if _, _, ok := commandsEnd(parse(t, "listen: \"\"\n")); ok {
		t.Fatal("commandsEnd ok = true for a file with no commands key")
	}
}

func TestBlockRangeFourSpaceIndent(t *testing.T) {
	src := "commands:\n    web:\n        cmd: [\"x\"]\n"
	_, _, indent, ok := blockRange(parse(t, src), "web")
	if !ok {
		t.Fatal("web not found")
	}
	if indent != 4 {
		t.Fatalf("indent = %d, want 4", indent)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/configw/ -run 'TestBlockRange|TestCommands' -v`
Expected: FAIL — `undefined: blockRange`.

- [ ] **Step 3: Write the implementation**

Create `internal/configw/locate.go`:

```go
package configw

import "gopkg.in/yaml.v3"

// commandsNode returns the mapping node under the top-level "commands" key.
func commandsNode(doc *yaml.Node) (*yaml.Node, bool) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, false
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, false
	}
	// A mapping's Content alternates key, value, key, value.
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "commands" && root.Content[i+1].Kind == yaml.MappingNode {
			return root.Content[i+1], true
		}
	}
	return nil, false
}

// blockRange reports the 1-based inclusive line range one command occupies and
// the indentation of its key.
//
// The end is the deepest line of the command's own value, never the line
// before the next key: a comment sitting between two commands belongs to the
// one below it, and including it here would delete somebody else's note.
func blockRange(doc *yaml.Node, name string) (int, int, int, bool) {
	cmds, ok := commandsNode(doc)
	if !ok {
		return 0, 0, 0, false
	}
	for i := 0; i+1 < len(cmds.Content); i += 2 {
		key, val := cmds.Content[i], cmds.Content[i+1]
		if key.Value != name {
			continue
		}
		return key.Line, maxLine(val), key.Column - 1, true
	}
	return 0, 0, 0, false
}

// commandsEnd is the last line of the commands mapping and the indentation its
// keys use, for appending a new command.
func commandsEnd(doc *yaml.Node) (int, int, bool) {
	cmds, ok := commandsNode(doc)
	if !ok {
		return 0, 0, false
	}
	if len(cmds.Content) == 0 {
		// "commands: {}" or an empty mapping: append just below it.
		return cmds.Line, cmds.Column - 1, true
	}
	last := cmds.Content[len(cmds.Content)-1]
	return maxLine(last), cmds.Content[0].Column - 1, true
}

// maxLine is the deepest line any part of a node reaches.
//
// ponytail: a multi-line block scalar reports its start line, so a command
// written with `cmd: |` would under-report its end. lazycomd commands are one
// line each; revisit if that ever stops being true.
func maxLine(n *yaml.Node) int {
	line := n.Line
	for _, c := range n.Content {
		if l := maxLine(c); l > line {
			line = l
		}
	}
	return line
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/configw/ -v`
Expected: PASS, every render and locate test.

If a line number is off by one, print the parsed tree rather than adjusting the expectation — `yaml.Node.Line` is 1-based and the fixture's line numbers in the test comments are the source of truth.

- [ ] **Step 5: Commit**

```bash
git add internal/configw/
git commit -m "feat(configw): locate a command's line range in a config file

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 4: Atomic writes and the change guard

**Files:**
- Create: `internal/configw/atomic.go`
- Test: `internal/configw/atomic_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `var ErrChanged = errors.New("config changed on disk")`.
  - `type snapshot struct { data []byte; size int64; mod time.Time; mode os.FileMode }`.
  - `func read(path string) (snapshot, error)`.
  - `func writeAtomic(path string, data []byte, from snapshot) error`.

- [ ] **Step 1: Write the failing test**

Create `internal/configw/atomic_test.go`:

```go
package configw

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFixture(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteAtomicReplacesContentAndKeepsMode(t *testing.T) {
	p := writeFixture(t, "old\n", 0o640)
	snap, err := read(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeAtomic(p, []byte("new\n"), snap); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Fatalf("file = %q, want new", got)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640 preserved", fi.Mode().Perm())
	}
}

func TestWriteAtomicRefusesAChangedFile(t *testing.T) {
	p := writeFixture(t, "old\n", 0o600)
	snap, err := read(p)
	if err != nil {
		t.Fatal(err)
	}

	// Somebody else — an editor, another client — got there first.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(p, []byte("theirs, longer\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = writeAtomic(p, []byte("ours\n"), snap)
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
	if !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("err = %v, want it to name the file", err)
	}

	got, _ := os.ReadFile(p)
	if string(got) != "theirs, longer\n" {
		t.Fatalf("their write was clobbered: %q", got)
	}
}

func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	p := writeFixture(t, "old\n", 0o600)
	snap, _ := read(p)
	if err := writeAtomic(p, []byte("new\n"), snap); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only config.yaml", names)
	}
}

func TestReadCapturesTheFile(t *testing.T) {
	p := writeFixture(t, "hello\n", 0o600)
	snap, err := read(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(snap.data) != "hello\n" {
		t.Fatalf("data = %q", snap.data)
	}
	if snap.size != 6 || snap.mode != 0o600 || snap.mod.IsZero() {
		t.Fatalf("snapshot = %+v", snap)
	}

	if _, err := read(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("read of a missing file returned no error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/configw/ -run 'TestWriteAtomic|TestRead' -v`
Expected: FAIL — `undefined: read`.

- [ ] **Step 3: Write the implementation**

Create `internal/configw/atomic.go`:

```go
package configw

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrChanged means the file moved under us between read and write: an editor
// or another client got there first, and their version wins.
var ErrChanged = errors.New("config changed on disk")

// snapshot is the file as it was read, and what is checked again before it is
// replaced.
type snapshot struct {
	data []byte
	size int64
	mod  time.Time
	mode os.FileMode
}

func read(path string) (snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshot{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{
		data: data,
		size: fi.Size(),
		mod:  fi.ModTime(),
		mode: fi.Mode().Perm(),
	}, nil
}

// writeAtomic replaces path with data, but only while the file still matches
// the snapshot it was read from. The replacement goes through a temp file in
// the same directory and a rename, so a crash leaves the old file whole rather
// than half of a new one.
func writeAtomic(path string, data []byte, from snapshot) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() != from.size || !fi.ModTime().Equal(from.mod) {
		return fmt.Errorf("%w: %s", ErrChanged, filepath.Base(path))
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".lazycomd-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, from.mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/configw/ -v`
Expected: PASS, every configw test so far.

If `TestWriteAtomicRefusesAChangedFile` is flaky, the cause is modification-time granularity on the filesystem — the test changes the file's size as well, so a size comparison catches it even when the timestamp does not. Do not weaken the check; both comparisons stay.

- [ ] **Step 5: Commit**

```bash
git add internal/configw/
git commit -m "feat(configw): atomic writes that lose to an editor holding the file

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Create, update and delete

**Files:**
- Create: `internal/configw/configw.go`
- Test: `internal/configw/configw_test.go`

**Interfaces:**
- Consumes: `renderBlock`, `blockRange`, `commandsEnd`, `read`, `writeAtomic`, `config.ParseBytes`, `config.Command`.
- Produces:
  - `type File struct` with `(*File).Create(name string, c config.Command) error`, `.Update(...)`, `.Delete(name string) error`.
  - `type Registry struct` with `NewRegistry() *Registry` and `(*Registry).File(path string) *File` — one `File`, and so one mutex, per path.
  - `var ErrExists`, `var ErrNotFound`.

- [ ] **Step 1: Write the failing test**

Create `internal/configw/configw_test.go`:

```go
package configw

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/tphuc/lazycomd/internal/config"
)

const withComments = `# lazycomd config
listen: ""

commands:
  # the http server, do not rename
  web:
    cmd: ["npm", "start"]
    cwd: /tmp

  # a chatty one
  noisy:
    cmd: ["sh", "-c", "echo hi"]
    restart: always
`

// linesOf reads a file back as lines.
func linesOf(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")
}

func TestCreateLeavesEveryOtherLineAlone(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	before := linesOf(t, p)

	f := &File{path: p}
	if err := f.Create("extra", config.Command{Cmd: []string{"sleep", "30"}}); err != nil {
		t.Fatal(err)
	}

	after := linesOf(t, p)
	// Every line that was there before must still be there, unchanged and in
	// order — comments, blank lines and quoting included.
	var kept []string
	for _, l := range after {
		kept = append(kept, l)
	}
	i := 0
	for _, want := range before {
		for i < len(kept) && kept[i] != want {
			i++
		}
		if i == len(kept) {
			t.Fatalf("original line %q is gone:\n%s", want, strings.Join(after, "\n"))
		}
		i++
	}
	if !strings.Contains(strings.Join(after, "\n"), "extra:") {
		t.Fatalf("new command missing:\n%s", strings.Join(after, "\n"))
	}
}

func TestCreateIntoAFileWithNoCommandsKey(t *testing.T) {
	p := writeFixture(t, "listen: \"\"\n", 0o600)

	f := &File{path: p}
	if err := f.Create("web", config.Command{Cmd: []string{"npm", "start"}}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(p)
	parsed, err := config.ParseBytes(data, p)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, data)
	}
	if _, ok := parsed.Commands["web"]; !ok {
		t.Fatalf("web missing:\n%s", data)
	}
}

func TestCreateRejectsADuplicate(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	err := f.Create("web", config.Command{Cmd: []string{"x"}})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func TestUpdateReplacesOnlyItsOwnBlock(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	if err := f.Update("web", config.Command{Cmd: []string{"npm", "run", "dev"}, Cwd: "/srv"}); err != nil {
		t.Fatal(err)
	}

	body := strings.Join(linesOf(t, p), "\n")
	for _, want := range []string{"# the http server, do not rename", "# a chatty one", "noisy:", "restart: always", "dev", "/srv"} {
		if !strings.Contains(body, want) {
			t.Fatalf("result missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "start") {
		t.Fatalf("old command line survived:\n%s", body)
	}
}

func TestUpdateUnknownCommand(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}
	if err := f.Update("ghost", config.Command{Cmd: []string{"x"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteKeepsTheNextCommandsComment(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	if err := f.Delete("web"); err != nil {
		t.Fatal(err)
	}

	body := strings.Join(linesOf(t, p), "\n")
	if strings.Contains(body, "web:") {
		t.Fatalf("web survived:\n%s", body)
	}
	if !strings.Contains(body, "# a chatty one") {
		t.Fatalf("the next command's comment went with it:\n%s", body)
	}
	if !strings.Contains(body, "noisy:") {
		t.Fatalf("noisy went with it:\n%s", body)
	}
	if !strings.Contains(body, "# lazycomd config") {
		t.Fatalf("the header comment went with it:\n%s", body)
	}
}

func TestDeleteTheLastCommand(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	if err := f.Delete("noisy"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	parsed, err := config.ParseBytes(data, p)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, data)
	}
	if _, gone := parsed.Commands["noisy"]; gone {
		t.Fatalf("noisy survived:\n%s", data)
	}
	if _, kept := parsed.Commands["web"]; !kept {
		t.Fatalf("web went with it:\n%s", data)
	}
}

func TestDeleteUnknownCommand(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}
	if err := f.Delete("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRefusesToWriteSomethingInvalid(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	before, _ := os.ReadFile(p)
	f := &File{path: p}

	// An empty cmd fails validation, so this must never reach disk.
	err := f.Create("broken", config.Command{})
	if err == nil {
		t.Fatal("err = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "refusing to write") {
		t.Fatalf("err = %v, want it to say it refused", err)
	}

	after, _ := os.ReadFile(p)
	if string(after) != string(before) {
		t.Fatalf("the file changed despite the refusal:\n%s", after)
	}
}

func TestRegistryGivesOneFilePerPath(t *testing.T) {
	r := NewRegistry()
	a := r.File("/tmp/one.yaml")
	b := r.File("/tmp/one.yaml")
	c := r.File("/tmp/two.yaml")

	if a != b {
		t.Fatal("the same path handed back two Files, so two mutexes")
	}
	if a == c {
		t.Fatal("different paths share a File")
	}
}

func TestConcurrentCreatesAllLand(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := NewRegistry().File(p)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a'+i)) + "cmd"
			if err := f.Create(name, config.Command{Cmd: []string{"sleep", "30"}}); err != nil {
				t.Errorf("create %s: %v", name, err)
			}
		}(i)
	}
	wg.Wait()

	data, _ := os.ReadFile(p)
	parsed, err := config.ParseBytes(data, p)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, data)
	}
	if len(parsed.Commands) != 10 { // web, noisy, and the eight new ones
		t.Fatalf("got %d commands, want 10:\n%s", len(parsed.Commands), data)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/configw/ -run 'TestCreate|TestUpdate|TestDelete|TestRefuses|TestRegistry|TestConcurrent' -v`
Expected: FAIL — `undefined: File`.

- [ ] **Step 3: Write the implementation**

Create `internal/configw/configw.go`:

```go
package configw

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/tphuc/lazycomd/internal/config"
)

var (
	ErrExists   = errors.New("command already exists")
	ErrNotFound = errors.New("command not found")
)

// File is one config file this process may modify. Its mutex serializes the
// read-modify-write cycle, so two clients cannot interleave into one file.
type File struct {
	path string
	mu   sync.Mutex
}

// Registry hands out one File per path, so the same file always shares a
// mutex no matter who asks for it.
type Registry struct {
	mu    sync.Mutex
	files map[string]*File
}

func NewRegistry() *Registry {
	return &Registry{files: make(map[string]*File)}
}

func (r *Registry) File(path string) *File {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.files[path]; ok {
		return f
	}
	f := &File{path: path}
	r.files[path] = f
	return f
}

// Create adds a command, refusing a name the file already has.
func (f *File) Create(name string, c config.Command) error {
	return f.edit(func(lines []string, doc *yaml.Node) ([]string, error) {
		if _, _, _, exists := blockRange(doc, name); exists {
			return nil, fmt.Errorf("%w: %s", ErrExists, name)
		}

		end, indent, ok := commandsEnd(doc)
		if !ok {
			// No commands: key at all; start one at the end of the file.
			block, err := renderBlock(name, c, 2)
			if err != nil {
				return nil, err
			}
			out := trimTrailingBlank(lines)
			out = append(out, "commands:")
			return append(out, block...), nil
		}

		block, err := renderBlock(name, c, indent)
		if err != nil {
			return nil, err
		}
		return insertAt(lines, end, block), nil
	})
}

// Update replaces one command's block, leaving everything around it alone.
func (f *File) Update(name string, c config.Command) error {
	return f.edit(func(lines []string, doc *yaml.Node) ([]string, error) {
		start, end, indent, ok := blockRange(doc, name)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		block, err := renderBlock(name, c, indent)
		if err != nil {
			return nil, err
		}
		return replaceRange(lines, start, end, block), nil
	})
}

// Delete removes one command's block.
func (f *File) Delete(name string) error {
	return f.edit(func(lines []string, doc *yaml.Node) ([]string, error) {
		start, end, _, ok := blockRange(doc, name)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return replaceRange(lines, start, end, nil), nil
	})
}

// edit runs one read-modify-write cycle. The mutated buffer is parsed and
// validated before anything reaches disk, so a bad edit cannot leave the
// daemon unable to load its own config.
func (f *File) edit(mutate func([]string, *yaml.Node) ([]string, error)) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	snap, err := read(f.path)
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(snap.data, &doc); err != nil {
		return fmt.Errorf("%s: %w", f.path, err)
	}

	out, err := mutate(strings.Split(string(snap.data), "\n"), &doc)
	if err != nil {
		return err
	}
	blob := []byte(strings.Join(out, "\n"))

	parsed, err := config.ParseBytes(blob, f.path)
	if err != nil {
		return fmt.Errorf("refusing to write: %w", err)
	}
	if err := parsed.Validate(); err != nil {
		return fmt.Errorf("refusing to write: %w", err)
	}
	return writeAtomic(f.path, blob, snap)
}

// insertAt puts block after the given 1-based line.
func insertAt(lines []string, afterLine int, block []string) []string {
	if afterLine < 0 {
		afterLine = 0
	}
	if afterLine > len(lines) {
		afterLine = len(lines)
	}
	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:afterLine]...)
	out = append(out, block...)
	return append(out, lines[afterLine:]...)
}

// replaceRange swaps the 1-based inclusive line range for block, which may be
// empty to delete it.
func replaceRange(lines []string, start, end int, block []string) []string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[:start-1]...)
	out = append(out, block...)
	return append(out, lines[end:]...)
}

// trimTrailingBlank drops trailing empty lines, so appending does not leave a
// gap in the middle of the file.
func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/configw/ -v`
Expected: PASS, every configw test. The race detector matters for `TestConcurrentCreatesAllLand`.

If that test reports fewer than ten commands, the mutex is not covering the whole read-modify-write cycle — check that `edit` holds it across `read` and `writeAtomic`, not just the mutation.

- [ ] **Step 5: Commit**

```bash
git add internal/configw/
git commit -m "feat(configw): create, update and delete a command in place

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 6: `api.Options`

**Files:**
- Create: `internal/api/options.go`
- Modify: `internal/api/server.go` — the `Server` fields, and `NewServer` gives way to `New`
- Modify (call sites): `internal/api/server_test.go`, `internal/api/auth_test.go`, `internal/api/system_test.go`, `internal/client/client_test.go`, `internal/tui/testdaemon_test.go`, `cmd/lazycomd/cli_test.go`, `cmd/lazycomd/serve.go`
- Test: `internal/api/options_test.go`

**Interfaces:**
- Consumes: `manager.Manager`, `config.Config`, `probe.Sampler`, `configw.Registry`.
- Produces:
  - `type Options struct { Manager *manager.Manager; Token string; Reload func() (*config.Config, error); Sampler *probe.Sampler; ConfigPath string; Writers *configw.Registry }`.
  - `func New(o Options) *Server`. `NewServer` is removed.

`NewServer` has grown an argument per spec — token, then reload, then the sampler, and now two more. A struct stops that, and every future addition becomes a field rather than a change at seven call sites.

- [ ] **Step 1: Write the failing test**

Create `internal/api/options_test.go`:

```go
package api

import (
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

func TestNewWithOnlyAManagerServes(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{"a": sleeper()}}, t.TempDir())
	m.Grace = 500 * time.Millisecond
	t.Cleanup(m.Shutdown)

	// Everything but the manager is optional: no token, no sampler, no
	// writers, no config path.
	s := New(Options{Manager: m, Reload: func() (*config.Config, error) { return nil, nil }})
	c := serveUnix(t, s.Handler())

	if code, body := do(t, c, "GET", "/v1/commands", ""); code != 200 {
		t.Fatalf("commands = %d %s", code, body)
	}
	if code, _ := do(t, c, "GET", "/v1/system", ""); code != 200 {
		t.Fatalf("system with no sampler = %d, want 200", code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestNewWithOnly -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the options type**

Create `internal/api/options.go`:

```go
package api

import (
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/configw"
	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/probe"
)

// Options is everything a Server needs. Only Manager and Reload are required;
// the rest degrade to a smaller API rather than failing.
type Options struct {
	Manager *manager.Manager
	Reload  func() (*config.Config, error)

	// Token is required only when a TCP listener is configured; AuthHandler
	// rejects everything when it is empty.
	Token string

	// Sampler may be nil, in which case the probe fields are absent and
	// GET /v1/system returns an empty snapshot.
	Sampler *probe.Sampler

	// ConfigPath is the global config file, the target for writes to a bare
	// command name. Writers may be nil, which refuses every write with 501.
	ConfigPath string
	Writers    *configw.Registry
}

// New builds a Server from its options.
func New(o Options) *Server {
	return &Server{
		mgr:     o.Manager,
		token:   o.Token,
		reload:  o.Reload,
		probe:   o.Sampler,
		cfgPath: o.ConfigPath,
		writers: o.Writers,
	}
}
```

- [ ] **Step 4: Rework the server struct**

In `internal/api/server.go`, replace the struct and delete `NewServer`:

```go
type Server struct {
	mgr     *manager.Manager
	token   string
	reload  func() (*config.Config, error)
	probe   *probe.Sampler
	cfgPath string
	writers *configw.Registry
}
```

- [ ] **Step 5: Update every call site**

```bash
grep -rn "NewServer(" --include=*.go .
```

Each becomes `New(Options{...})`. The test helpers take the short form:

```go
	s := New(Options{
		Manager: m,
		Reload:  func() (*config.Config, error) { return &config.Config{Commands: cmds}, nil },
	})
```

`internal/api/system_test.go` adds `Sampler: sampler`, `internal/api/auth_test.go` adds `Token: "s3cret"` where it had one, and `cmd/lazycomd/serve.go` becomes:

```go
	srv := api.New(api.Options{
		Manager:    mgr,
		Token:      token,
		Reload:     func() (*config.Config, error) { return config.Load(*cfgPath) },
		Sampler:    sampler,
		ConfigPath: *cfgPath,
		Writers:    configw.NewRegistry(),
	})
```

Add `"github.com/tphuc/lazycomd/internal/configw"` to `serve.go`'s imports.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./... 2>&1 | tail -12`
Expected: every package passes. This task changes no behavior — it is the refactor that makes the next one small.

- [ ] **Step 7: Commit**

```bash
git add internal/ cmd/
git commit -m "refactor(api): build the server from an Options struct

NewServer had grown an argument per spec. A struct makes the next two
additions fields rather than a change at seven call sites.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: The write endpoints

**Files:**
- Create: `internal/api/write.go`
- Modify: `internal/api/server.go` — register four routes, and reuse one reload helper
- Modify: `internal/config/config.go` — JSON tags on `Command`
- Modify: `internal/configw/configw.go` — an `ErrInvalid` sentinel instead of a string prefix
- Test: `internal/api/write_test.go`

**Interfaces:**
- Consumes: `configw.File`, `configw.Registry`, `configw.ErrExists`, `.ErrNotFound`, `.ErrChanged`, `.ErrInvalid`, `config.Config.Projects`.
- Produces:
  - Routes `GET /v1/commands/{name}/config`, `POST /v1/commands`, `PUT /v1/commands/{name}`, `DELETE /v1/commands/{name}`.
  - `(*Server).targetFile(name string) (path, key string, err error)`.
  - `var errBadRequest = errors.New("bad request")` in the api package.
  - `configw.ErrInvalid`, wrapping whatever validation rejected.
  - JSON tags on `config.Command` matching its YAML names.

- [ ] **Step 1: Write the failing test**

Create `internal/api/write_test.go`:

```go
package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/configw"
	"github.com/tphuc/lazycomd/internal/manager"
)

// writableAPI serves a real config file that writes actually land in.
func writableAPI(t *testing.T, body string) (*manager.Manager, *http.Client, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := manager.New(cfg, t.TempDir())
	m.Grace = 500 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := New(Options{
		Manager:    m,
		Reload:     func() (*config.Config, error) { return config.Load(path) },
		ConfigPath: path,
		Writers:    configw.NewRegistry(),
	})
	return m, serveUnix(t, s.Handler()), path
}

const oneCommand = `commands:
  web:
    cmd: ["npm", "start"]
    cwd: /tmp
`

func TestCreateCommandWritesAndReloads(t *testing.T) {
	m, c, path := writableAPI(t, oneCommand)

	code, body := do(t, c, "POST", "/v1/commands", `{"name":"extra","cmd":["sleep","30"],"cwd":"/tmp","restart":"on-failure"}`)
	if code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}
	var st manager.Status
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st.Name != "extra" || st.State != manager.Stopped {
		t.Fatalf("status = %+v", st)
	}

	// It is in the file...
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "extra:") {
		t.Fatalf("file missing the command:\n%s", data)
	}
	// ...and the daemon already knows about it, with no extra reload call.
	if _, err := m.Status("extra"); err != nil {
		t.Fatalf("manager does not have it: %v", err)
	}
}

func TestCreateDuplicateIsAConflict(t *testing.T) {
	_, c, _ := writableAPI(t, oneCommand)
	code, body := do(t, c, "POST", "/v1/commands", `{"name":"web","cmd":["x"]}`)
	if code != 409 {
		t.Fatalf("duplicate = %d %s, want 409", code, body)
	}
}

func TestCreateInvalidIsABadRequest(t *testing.T) {
	_, c, path := writableAPI(t, oneCommand)
	before, _ := os.ReadFile(path)

	code, body := do(t, c, "POST", "/v1/commands", `{"name":"broken","cmd":[]}`)
	if code != 400 {
		t.Fatalf("empty cmd = %d %s, want 400", code, body)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("the file changed despite the rejection:\n%s", after)
	}
}

func TestCreateWithNoNameIsABadRequest(t *testing.T) {
	_, c, _ := writableAPI(t, oneCommand)
	if code, _ := do(t, c, "POST", "/v1/commands", `{"cmd":["x"]}`); code != 400 {
		t.Fatalf("no name = %d, want 400", code)
	}
}

func TestGetCommandConfigReturnsEveryField(t *testing.T) {
	_, c, _ := writableAPI(t, `commands:
  api:
    cmd: ["node", "server.js"]
    env: {LOG: debug}
    health: http://localhost:3000/healthz
    port: 3000
`)

	code, body := do(t, c, "GET", "/v1/commands/api/config", "")
	if code != 200 {
		t.Fatalf("config = %d %s", code, body)
	}
	var got config.Command
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if got.Env["LOG"] != "debug" || got.Health == "" || got.Port != 3000 {
		t.Fatalf("config = %+v, want the fields a form never shows", got)
	}

	if code, _ := do(t, c, "GET", "/v1/commands/ghost/config", ""); code != 404 {
		t.Fatalf("unknown command = %d, want 404", code)
	}
}

func TestUpdateReplacesTheSpec(t *testing.T) {
	_, c, path := writableAPI(t, oneCommand)

	code, body := do(t, c, "PUT", "/v1/commands/web", `{"cmd":["npm","run","dev"],"cwd":"/srv"}`)
	if code != 200 {
		t.Fatalf("update = %d %s", code, body)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "dev") || !strings.Contains(string(data), "/srv") {
		t.Fatalf("file not updated:\n%s", data)
	}

	if code, _ := do(t, c, "PUT", "/v1/commands/ghost", `{"cmd":["x"]}`); code != 404 {
		t.Fatalf("unknown command = %d, want 404", code)
	}
}

func TestDeleteRemovesFromTheFileAndTheDaemon(t *testing.T) {
	m, c, path := writableAPI(t, oneCommand)

	code, body := do(t, c, "DELETE", "/v1/commands/web", "")
	if code != 204 {
		t.Fatalf("delete = %d %s, want 204", code, body)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "web:") {
		t.Fatalf("still in the file:\n%s", data)
	}
	if _, err := m.Status("web"); err == nil {
		t.Fatal("the manager still has it after the reload")
	}

	if code, _ := do(t, c, "DELETE", "/v1/commands/web", ""); code != 404 {
		t.Fatalf("second delete = %d, want 404", code)
	}
}

func TestWritesToAProjectFile(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "app")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "lazycomd.yaml"), []byte("commands: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("projects:\n  - "+proj+"\ncommands: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := manager.New(cfg, t.TempDir())
	t.Cleanup(m.Shutdown)
	s := New(Options{
		Manager:    m,
		Reload:     func() (*config.Config, error) { return config.Load(path) },
		ConfigPath: path,
		Writers:    configw.NewRegistry(),
	})
	c := serveUnix(t, s.Handler())

	if code, body := do(t, c, "POST", "/v1/commands", `{"name":"app:api","cmd":["node","server.js"]}`); code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}

	// The bare key lands in the project file, and the global config is untouched.
	projData, _ := os.ReadFile(filepath.Join(proj, "lazycomd.yaml"))
	if !strings.Contains(string(projData), "api:") {
		t.Fatalf("project file missing the command:\n%s", projData)
	}
	if strings.Contains(string(projData), "app:api") {
		t.Fatalf("namespaced key written into the project file:\n%s", projData)
	}
	globalData, _ := os.ReadFile(path)
	if strings.Contains(string(globalData), "api") {
		t.Fatalf("global config was touched:\n%s", globalData)
	}
}

func TestUnknownProjectIsABadRequest(t *testing.T) {
	_, c, _ := writableAPI(t, oneCommand)
	code, body := do(t, c, "POST", "/v1/commands", `{"name":"ghostproj:api","cmd":["x"]}`)
	if code != 400 {
		t.Fatalf("unknown project = %d %s, want 400", code, body)
	}
	if !strings.Contains(body, "ghostproj") {
		t.Fatalf("error should name the project: %s", body)
	}
}

func TestWritesRefusedWithoutARegistry(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{}}, t.TempDir())
	t.Cleanup(m.Shutdown)
	s := New(Options{Manager: m, Reload: func() (*config.Config, error) { return nil, nil }})
	c := serveUnix(t, s.Handler())

	if code, _ := do(t, c, "POST", "/v1/commands", `{"name":"x","cmd":["y"]}`); code != 501 {
		t.Fatalf("create with no writers = %d, want 501", code)
	}
}
```

Add `"net/http"` to that file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run 'TestCreate|TestGetCommandConfig|TestUpdate|TestDelete|TestWrites|TestUnknownProject' -v`
Expected: FAIL — 404s from unregistered routes.

- [ ] **Step 3: Add JSON tags to `config.Command`**

The API takes a command as JSON, so its fields need JSON names matching the
YAML ones. In `internal/config/config.go`:

```go
type Command struct {
	Cmd       []string          `yaml:"cmd" json:"cmd"`
	Cwd       string            `yaml:"cwd" json:"cwd,omitempty"`
	Env       map[string]string `yaml:"env" json:"env,omitempty"`
	Shell     bool              `yaml:"shell" json:"shell,omitempty"`
	Restart   Restart           `yaml:"restart" json:"restart,omitempty"`
	Autostart bool              `yaml:"autostart" json:"autostart,omitempty"`
	Log       bool              `yaml:"log" json:"log,omitempty"`
	Size      int               `yaml:"size" json:"size,omitempty"`
	DependsOn []string          `yaml:"depends_on" json:"depends_on,omitempty"`
	Health    string            `yaml:"health" json:"health,omitempty"`
	Port      int               `yaml:"port" json:"port,omitempty"`
}
```

- [ ] **Step 4: Give the validation refusal a sentinel**

In `internal/configw/configw.go`, add the error and use it in `edit`, so the
API can map it without matching on message text:

```go
var (
	ErrExists   = errors.New("command already exists")
	ErrNotFound = errors.New("command not found")
	ErrInvalid  = errors.New("refusing to write")
)
```

```go
	parsed, err := config.ParseBytes(blob, f.path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := parsed.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
```

`TestRefusesToWriteSomethingInvalid` from Task 5 still passes: the message
still begins with `refusing to write`.

- [ ] **Step 5: Write the handlers**

Create `internal/api/write.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/configw"
)

// errBadRequest marks an error the client caused, for the 400 mapping.
var errBadRequest = errors.New("bad request")

// commandBody is a command plus the name it goes under.
type commandBody struct {
	Name string `json:"name"`
	config.Command
}

// commandConfig returns one command's full spec, including fields the TUI's
// form never shows — a form that knows four fields must not erase the rest.
func (s *Server) commandConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.reload()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	name := r.PathValue("name")
	cmd, ok := cfg.Commands[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("unknown command: %s", name)))
		return
	}
	writeJSON(w, http.StatusOK, cmd)
}

func (s *Server) createCommand(w http.ResponseWriter, r *http.Request) {
	if s.writers == nil {
		writeJSON(w, http.StatusNotImplemented, errBody(errors.New("this daemon was built without config writing")))
		return
	}
	var body commandBody
	if err := decodeOptional(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, errBody(errors.New("name is required")))
		return
	}

	path, key, err := s.targetFile(body.Name)
	if err != nil {
		s.failWrite(w, err)
		return
	}
	if err := s.writers.File(path).Create(key, body.Command); err != nil {
		s.failWrite(w, err)
		return
	}
	s.afterWrite(w, body.Name, http.StatusCreated)
}

func (s *Server) updateCommand(w http.ResponseWriter, r *http.Request) {
	if s.writers == nil {
		writeJSON(w, http.StatusNotImplemented, errBody(errors.New("this daemon was built without config writing")))
		return
	}
	var body config.Command
	if err := decodeOptional(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	name := r.PathValue("name")

	path, key, err := s.targetFile(name)
	if err != nil {
		s.failWrite(w, err)
		return
	}
	if err := s.writers.File(path).Update(key, body); err != nil {
		s.failWrite(w, err)
		return
	}
	s.afterWrite(w, name, http.StatusOK)
}

func (s *Server) deleteCommand(w http.ResponseWriter, r *http.Request) {
	if s.writers == nil {
		writeJSON(w, http.StatusNotImplemented, errBody(errors.New("this daemon was built without config writing")))
		return
	}
	name := r.PathValue("name")

	path, key, err := s.targetFile(name)
	if err != nil {
		s.failWrite(w, err)
		return
	}
	if err := s.writers.File(path).Delete(key); err != nil {
		s.failWrite(w, err)
		return
	}
	if _, err := s.applyReload(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// afterWrite reloads the daemon and answers with the command's new status.
func (s *Server) afterWrite(w http.ResponseWriter, name string, code int) {
	if _, err := s.applyReload(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	st, err := s.mgr.Status(name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, code, s.enrich([]manager.Status{st})[0])
}

// targetFile resolves which file a write to name belongs in, and the key to
// use inside it: a bare name goes to the global config, and app:api goes to
// that project's lazycomd.yaml under the bare key api.
func (s *Server) targetFile(name string) (string, string, error) {
	ns, key, namespaced := strings.Cut(name, ":")
	if !namespaced {
		if s.cfgPath == "" {
			return "", "", fmt.Errorf("%w: this daemon has no config path", errBadRequest)
		}
		return s.cfgPath, name, nil
	}

	cfg, err := s.reload()
	if err != nil {
		return "", "", err
	}
	dir, ok := cfg.Projects[ns]
	if !ok {
		known := make([]string, 0, len(cfg.Projects))
		for p := range cfg.Projects {
			known = append(known, p)
		}
		sort.Strings(known)
		return "", "", fmt.Errorf("%w: unknown project %q (known: %s)", errBadRequest, ns, strings.Join(known, ", "))
	}
	return filepath.Join(dir, "lazycomd.yaml"), key, nil
}

// failWrite maps a writer error to its status code.
func (s *Server) failWrite(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, configw.ErrExists), errors.Is(err, configw.ErrChanged):
		writeJSON(w, http.StatusConflict, errBody(err))
	case errors.Is(err, configw.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody(err))
	case errors.Is(err, configw.ErrInvalid), errors.Is(err, errBadRequest):
		writeJSON(w, http.StatusBadRequest, errBody(err))
	default:
		writeJSON(w, http.StatusInternalServerError, errBody(err))
	}
}
```

Add `"github.com/tphuc/lazycomd/internal/manager"` to that file's imports.

- [ ] **Step 6: Register the routes and share one reload helper**

In `internal/api/server.go`, add to `routes()`:

```go
	mux.HandleFunc("GET /v1/commands/{name}/config", s.commandConfig)
	mux.HandleFunc("POST /v1/commands", s.createCommand)
	mux.HandleFunc("PUT /v1/commands/{name}", s.updateCommand)
	mux.HandleFunc("DELETE /v1/commands/{name}", s.deleteCommand)
```

And factor the reload both `doReload` and `afterWrite` need, in `auth.go`
beside `doReload`:

```go
// applyReload re-reads the config and applies it to the manager.
func (s *Server) applyReload() (*config.Config, error) {
	cfg, err := s.reload()
	if err != nil {
		return nil, err
	}
	if err := s.mgr.Reload(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
```

with `doReload` becoming:

```go
func (s *Server) doReload(w http.ResponseWriter, _ *http.Request) {
	if _, err := s.applyReload(); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	writeJSON(w, http.StatusOK, s.enrich(s.mgr.List()))
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -race ./internal/api/ ./internal/configw/ -v 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: PASS for both packages.

- [ ] **Step 8: Commit**

```bash
git add internal/api/ internal/config/ internal/configw/
git commit -m "feat(api): read, create, update and delete a command's config

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Client methods

**Files:**
- Modify: `internal/client/client.go`
- Test: `internal/client/client_test.go`

**Interfaces:**
- Consumes: the four endpoints from Task 7.
- Produces:
  - `(*Client).CommandConfig(name string) (config.Command, error)`.
  - `(*Client).Create(name string, cmd config.Command) (manager.Status, error)`.
  - `(*Client).Update(name string, cmd config.Command) (manager.Status, error)`.
  - `(*Client).Delete(name string) error`.

- [ ] **Step 1: Write the failing test**

Append to `internal/client/client_test.go`:

```go
func TestClientWriteRoundTrip(t *testing.T) {
	// A daemon over a real, writable config file.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("commands:\n  web:\n    cmd: [\"sleep\", \"30\"]\n    cwd: /tmp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := writableDaemon(t, path)

	// Create
	st, err := c.Create("extra", config.Command{Cmd: []string{"sleep", "30"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "extra" {
		t.Fatalf("status = %+v", st)
	}

	// Read back every field
	got, err := c.CommandConfig("extra")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cmd) != 2 || got.Cwd != "/tmp" {
		t.Fatalf("config = %+v", got)
	}

	// Update
	got.Cwd = "/srv"
	if _, err := c.Update("extra", got); err != nil {
		t.Fatal(err)
	}
	again, err := c.CommandConfig("extra")
	if err != nil {
		t.Fatal(err)
	}
	if again.Cwd != "/srv" {
		t.Fatalf("cwd = %q, want /srv", again.Cwd)
	}

	// Delete
	if err := c.Delete("extra"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CommandConfig("extra"); err == nil {
		t.Fatal("the command survived the delete")
	}
}

func TestClientCreateDuplicateIsAnAPIError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("commands:\n  web:\n    cmd: [\"sleep\", \"30\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := writableDaemon(t, path)

	_, err := c.Create("web", config.Command{Cmd: []string{"x"}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 409 {
		t.Fatalf("err = %v, want a 409 APIError", err)
	}
}
```

And the helper it needs, beside `daemon` in the same file:

```go
// writableDaemon serves a real config file that writes land in.
func writableDaemon(t *testing.T, path string) *Client {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := manager.New(cfg, t.TempDir())
	m.Grace = 500 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.New(api.Options{
		Manager:    m,
		Reload:     func() (*config.Config, error) { return config.Load(path) },
		ConfigPath: path,
		Writers:    configw.NewRegistry(),
	})

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

	c, err := New("unix://"+sock, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
```

Add `"os"` and `"github.com/tphuc/lazycomd/internal/configw"` to that file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/client/ -run TestClientWrite -v`
Expected: FAIL — `c.Create undefined`.

- [ ] **Step 3: Write the implementation**

Append to `internal/client/client.go`:

```go
// CommandConfig returns a command's full spec, including fields no UI shows.
// PUT replaces, so an editor must read this first and send back what it does
// not change.
func (c *Client) CommandConfig(name string) (config.Command, error) {
	var out config.Command
	path := "/v1/commands/" + url.PathEscape(name) + "/config"
	return out, c.do(context.Background(), "GET", path, nil, &out)
}

// Create adds a command to the config and returns its new status.
func (c *Client) Create(name string, cmd config.Command) (manager.Status, error) {
	var out manager.Status
	body := struct {
		Name string `json:"name"`
		config.Command
	}{Name: name, Command: cmd}
	return out, c.do(context.Background(), "POST", "/v1/commands", body, &out)
}

// Update replaces a command's whole spec.
func (c *Client) Update(name string, cmd config.Command) (manager.Status, error) {
	var out manager.Status
	return out, c.do(context.Background(), "PUT", "/v1/commands/"+url.PathEscape(name), cmd, &out)
}

// Delete removes a command from the config, stopping it if it was running.
func (c *Client) Delete(name string) error {
	return c.do(context.Background(), "DELETE", "/v1/commands/"+url.PathEscape(name), nil, nil)
}
```

Add `"github.com/tphuc/lazycomd/internal/config"` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/client/ -v 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: PASS.

A 204 with no body must not trip the decoder — `do` already skips decoding when `out` is nil, which is why `Delete` passes `nil`.

- [ ] **Step 5: Commit**

```bash
git add internal/client/
git commit -m "feat(client): read and write a command's config

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---
### Task 9: The form model

**Files:**
- Create: `internal/tui/form.go`
- Test: `internal/tui/form_test.go`

**Interfaces:**
- Consumes: `config.Command`, `config.Restart` constants, `bubbles/textinput`, `panelView`, `panelSpec`.
- Produces:
  - `type formModel struct` with `newForm(launchDir string, remote bool) formModel`.
  - `(*formModel).OpenCreate(projects map[string]string)`, `(*formModel).OpenEdit(name string, c config.Command, projects map[string]string)`, `(*formModel).SetError(msg string)`.
  - `(formModel).Update(msg tea.Msg) (formModel, tea.Cmd)`, `(formModel).View(width, height int) string`, `(formModel).Result() (string, config.Command)`, `(formModel).Editing() bool`.
  - `func splitCommand(line string) (cmd []string, shell bool)`.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/form_test.go`:

```go
package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/tphuc/lazycomd/internal/config"
)

func typeForm(f formModel, s string) formModel {
	for _, r := range s {
		f, _ = f.Update(key(string(r)))
	}
	return f
}

func TestSplitCommand(t *testing.T) {
	cmd, shell := splitCommand("npm start")
	if shell || !slices.Equal(cmd, []string{"npm", "start"}) {
		t.Fatalf("plain line = %v, shell=%v", cmd, shell)
	}

	// A shell metacharacter means the line only makes sense to a shell.
	for _, line := range []string{
		"while true; do echo hi; sleep 1; done",
		"tail -f log | grep error",
		"echo $HOME",
		"ls *.go",
		"cmd > out.txt",
	} {
		cmd, shell := splitCommand(line)
		if !shell {
			t.Fatalf("%q should switch to shell mode", line)
		}
		if len(cmd) != 1 || cmd[0] != line {
			t.Fatalf("%q should pass through whole, got %v", line, cmd)
		}
	}
}

func TestFormNavigationWraps(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)

	if f.field != fieldName {
		t.Fatal("should open on the name field")
	}
	for i := 0; i < int(fieldCount); i++ {
		f, _ = f.Update(key("tab"))
	}
	if f.field != fieldName {
		t.Fatalf("field = %v after a full cycle, want back to name", f.field)
	}
	f, _ = f.Update(key("shift+tab"))
	if f.field != fieldRestart {
		t.Fatalf("shift+tab from name went to %v, want restart", f.field)
	}
}

func TestFormRestartCycles(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)
	for f.field != fieldRestart {
		f, _ = f.Update(key("tab"))
	}

	if _, c := f.Result(); c.Restart != config.RestartNo {
		t.Fatalf("restart starts at %q, want no", c.Restart)
	}
	f, _ = f.Update(key("right"))
	if _, c := f.Result(); c.Restart != config.RestartOnFailure {
		t.Fatalf("after right = %q, want on-failure", c.Restart)
	}
	f, _ = f.Update(key("right"))
	if _, c := f.Result(); c.Restart != config.RestartAlways {
		t.Fatalf("after two rights = %q, want always", c.Restart)
	}
	f, _ = f.Update(key("right")) // wraps
	if _, c := f.Result(); c.Restart != config.RestartNo {
		t.Fatalf("restart did not wrap: %q", c.Restart)
	}
	f, _ = f.Update(key("left"))
	if _, c := f.Result(); c.Restart != config.RestartAlways {
		t.Fatalf("left did not wrap backwards: %q", c.Restart)
	}
}

func TestFormPrefillsFolderFromTheLaunchDirectory(t *testing.T) {
	f := newForm("/home/me/coding/app", false)
	f.OpenCreate(nil)

	if _, c := f.Result(); c.Cwd != "/home/me/coding/app" {
		t.Fatalf("folder = %q, want the launch directory", c.Cwd)
	}
}

func TestFormLeavesFolderBlankForARemoteDaemon(t *testing.T) {
	f := newForm("/home/me/coding/app", true)
	f.OpenCreate(nil)

	if _, c := f.Result(); c.Cwd != "" {
		t.Fatalf("folder = %q, want blank: a local path means nothing on another machine", c.Cwd)
	}
}

func TestFormFolderFollowsTheProjectOnceNamespaced(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(map[string]string{"app": "/home/me/coding/app"})

	f = typeForm(f, "app:api")
	if _, c := f.Result(); c.Cwd != "/home/me/coding/app" {
		t.Fatalf("folder = %q, want the project directory", c.Cwd)
	}
}

func TestFormKeepsAFolderYouTyped(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(map[string]string{"app": "/home/me/coding/app"})

	// Type a folder by hand, then a namespaced name: your typing wins.
	for f.field != fieldFolder {
		f, _ = f.Update(key("tab"))
	}
	f, _ = f.Update(key("ctrl+u")) // textinput clears the line
	f = typeForm(f, "/srv")
	for f.field != fieldName {
		f, _ = f.Update(key("tab"))
	}
	f = typeForm(f, "app:api")

	if _, c := f.Result(); c.Cwd != "/srv" {
		t.Fatalf("folder = %q, want the value typed by hand", c.Cwd)
	}
}

func TestFormEditPreservesFieldsItDoesNotShow(t *testing.T) {
	base := config.Command{
		Cmd:       []string{"node", "server.js"},
		Cwd:       "/srv",
		Env:       map[string]string{"LOG": "debug"},
		DependsOn: []string{"db"},
		Health:    "http://localhost:3000/healthz",
		Port:      3000,
		Restart:   config.RestartAlways,
	}
	f := newForm("/home/me", false)
	f.OpenEdit("api", base, nil)

	if !f.Editing() {
		t.Fatal("Editing() = false after OpenEdit")
	}
	name, got := f.Result()
	if name != "api" {
		t.Fatalf("name = %q", name)
	}
	if got.Env["LOG"] != "debug" || got.Health != base.Health || got.Port != 3000 || len(got.DependsOn) != 1 {
		t.Fatalf("edit dropped fields the form never shows: %+v", got)
	}
	if got.Restart != config.RestartAlways {
		t.Fatalf("restart = %q, want the existing value prefilled", got.Restart)
	}
}

func TestFormEditLocksTheName(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenEdit("api", config.Command{Cmd: []string{"x"}}, nil)

	f = typeForm(f, "zzz") // the name field has focus but is read-only
	if name, _ := f.Result(); name != "api" {
		t.Fatalf("name = %q, want it unchanged: renaming is a file edit", name)
	}
}

func TestFormViewShowsFieldsErrorAndHint(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)
	f = typeForm(f, "web")

	view := f.View(60, 14)
	for _, want := range []string{"NAME", "COMMAND", "FOLDER", "RESTART", "web", "tab next"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}

	f.SetError(`command "web" already exists`)
	if v := f.View(60, 14); !strings.Contains(v, "already exists") || !strings.Contains(v, "web") {
		t.Fatalf("error not shown with input kept:\n%s", v)
	}
}

func TestFormViewFitsItsBox(t *testing.T) {
	f := newForm("/home/me", false)
	f.OpenCreate(nil)

	view := f.View(48, 12)
	lines := strings.Split(view, "\n")
	if len(lines) != 12 {
		t.Fatalf("view is %d lines, want 12", len(lines))
	}
	for i, l := range lines {
		if w := runeWidth(l); w != 48 {
			t.Fatalf("line %d is %d wide, want 48: %q", i, w, l)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestSplitCommand|TestForm' -v`
Expected: FAIL — `undefined: newForm`.

- [ ] **Step 3: Write the implementation**

Create `internal/tui/form.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/config"
)

type formField int

const (
	fieldName formField = iota
	fieldCommand
	fieldFolder
	fieldRestart
	fieldCount
)

// restartChoices is the cycle the RESTART field walks; three fixed values do
// not deserve free text.
var restartChoices = []config.Restart{config.RestartNo, config.RestartOnFailure, config.RestartAlways}

type formModel struct {
	editing bool
	base    config.Command // what we started from, so unshown fields survive

	name    textinput.Model
	command textinput.Model
	folder  textinput.Model
	restart int
	field   formField

	folderTyped bool // you edited it, so stop prefilling over you
	err         string

	launchDir string
	remote    bool
	projects  map[string]string
}

func newForm(launchDir string, remote bool) formModel {
	mk := func(limit int) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = limit
		return ti
	}
	return formModel{
		name:      mk(80),
		command:   mk(400),
		folder:    mk(200),
		launchDir: launchDir,
		remote:    remote,
	}
}

// OpenCreate resets the form for a new command.
func (f *formModel) OpenCreate(projects map[string]string) {
	f.editing, f.base, f.err = false, config.Command{}, ""
	f.projects, f.folderTyped = projects, false
	f.restart, f.field = 0, fieldName

	f.name.Reset()
	f.command.Reset()
	f.folder.Reset()
	if !f.remote {
		// You add a command for the project you are sitting in.
		f.folder.SetValue(f.launchDir)
	}
	f.focus()
}

// OpenEdit fills the form from an existing command. The name is locked:
// renaming is a delete plus a create, possibly across two files.
func (f *formModel) OpenEdit(name string, c config.Command, projects map[string]string) {
	f.editing, f.base, f.err = true, c, ""
	f.projects, f.folderTyped = projects, true
	f.field = fieldCommand

	f.name.SetValue(name)
	line := strings.Join(c.Cmd, " ")
	if c.Shell && len(c.Cmd) == 1 {
		line = c.Cmd[0]
	}
	f.command.SetValue(line)
	f.folder.SetValue(c.Cwd)

	f.restart = 0
	for i, r := range restartChoices {
		if r == c.Restart {
			f.restart = i
		}
	}
	f.focus()
}

func (f *formModel) SetError(msg string) { f.err = msg }
func (f formModel) Editing() bool        { return f.editing }

// focus points the cursor at the active field.
func (f *formModel) focus() {
	f.name.Blur()
	f.command.Blur()
	f.folder.Blur()
	switch f.field {
	case fieldName:
		if !f.editing {
			f.name.Focus()
		}
	case fieldCommand:
		f.command.Focus()
	case fieldFolder:
		f.folder.Focus()
	}
}

func (f formModel) Update(msg tea.Msg) (formModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, nil
	}
	switch k.String() {
	case "tab", "down":
		f.field = (f.field + 1) % fieldCount
		f.focus()
		return f, nil
	case "shift+tab", "up":
		f.field = (f.field + fieldCount - 1) % fieldCount
		f.focus()
		return f, nil
	}

	if f.field == fieldRestart {
		switch k.String() {
		case "right", "l", " ":
			f.restart = (f.restart + 1) % len(restartChoices)
		case "left", "h":
			f.restart = (f.restart + len(restartChoices) - 1) % len(restartChoices)
		}
		return f, nil
	}

	var cmd tea.Cmd
	switch f.field {
	case fieldName:
		if f.editing {
			return f, nil // locked
		}
		f.name, cmd = f.name.Update(msg)
		f.syncFolderToProject()
	case fieldCommand:
		f.command, cmd = f.command.Update(msg)
	case fieldFolder:
		f.folder, cmd = f.folder.Update(msg)
		f.folderTyped = true
	}
	return f, cmd
}

// syncFolderToProject repoints a prefilled folder at the project a namespaced
// name belongs to. A folder you typed yourself is never overwritten.
func (f *formModel) syncFolderToProject() {
	if f.folderTyped || f.remote {
		return
	}
	ns, _, namespaced := strings.Cut(f.name.Value(), ":")
	if !namespaced {
		f.folder.SetValue(f.launchDir)
		return
	}
	if dir, ok := f.projects[ns]; ok {
		f.folder.SetValue(dir)
	}
}

// Result is the name and the command the form describes. It starts from the
// spec it opened with, so env, depends_on, health and port survive an edit.
func (f formModel) Result() (string, config.Command) {
	c := f.base
	cmd, shell := splitCommand(strings.TrimSpace(f.command.Value()))
	c.Cmd, c.Shell = cmd, shell
	c.Cwd = strings.TrimSpace(f.folder.Value())
	c.Restart = restartChoices[f.restart]
	return strings.TrimSpace(f.name.Value()), c
}

// splitCommand reads the one-line command field. A shell metacharacter means
// the line only makes sense to a shell, so it is passed through whole rather
// than split into nonsense.
func splitCommand(line string) ([]string, bool) {
	if strings.ContainsAny(line, "|><&;$*") {
		return []string{line}, true
	}
	return strings.Fields(line), false
}

// hint explains the focused field.
func (f formModel) hint() string {
	switch f.field {
	case fieldName:
		return "app:name puts it in that project"
	case fieldCommand:
		if _, shell := splitCommand(f.command.Value()); shell {
			return "shell mode: the whole line goes to sh -c"
		}
		return "split on spaces · pipes and $ switch to shell mode"
	case fieldFolder:
		return "where the command runs · blank = ~"
	default:
		return "no · on-failure · always"
	}
}

func (f formModel) View(width, height int) string {
	title := "New command"
	if f.editing {
		title = "Edit " + f.name.Value()
	}

	rows := []string{"", f.row("NAME", f.nameView()), f.row("COMMAND", f.command.View()), f.row("FOLDER", f.folder.View()), f.row("RESTART", f.restartView()), ""}
	if f.err != "" {
		rows = append(rows, styleWarn.Render("  ⚠ "+f.err))
	} else {
		rows = append(rows, styleDim.Render("  "+f.hint()))
	}
	rows = append(rows, styleDim.Render("  tab next · enter save · esc cancel"))

	return panelView(panelSpec{Title: title, Width: width, Height: height, Rows: rows, Focused: true})
}

func (f formModel) row(label, value string) string {
	return fmt.Sprintf("  %-9s %s", label, value)
}

func (f formModel) nameView() string {
	if f.editing {
		return styleDim.Render(f.name.Value() + "  (rename is a file edit)")
	}
	return f.name.View()
}

func (f formModel) restartView() string {
	v := string(restartChoices[f.restart])
	if f.field == fieldRestart {
		return "‹ " + v + " ›"
	}
	return "  " + v
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/ -run 'TestSplitCommand|TestForm' -v`
Expected: PASS, eleven tests.

If `TestFormKeepsAFolderYouTyped` fails, `ctrl+u` may not clear the line in your `bubbles` version — replace it with enough `backspace` keys to empty the field; the assertion is about `folderTyped`, not about that particular key.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): the add and edit form model

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Wiring the form to the API

**Files:**
- Modify: `internal/tui/messages.go` — the form messages and commands
- Modify: `internal/tui/tui.go` — `overlayForm`, the `a` and `e` keys, save handling
- Modify: `internal/tui/help.go` — `overlayForm` in the enum
- Modify: `internal/api/write.go` — `GET /v1/projects`
- Modify: `internal/api/server.go` — register that route
- Modify: `internal/client/client.go` — `Projects()`
- Test: `internal/tui/form_routing_test.go`

**Interfaces:**
- Consumes: `formModel`, `client.Create`, `client.Update`, `client.CommandConfig`.
- Produces:
  - `GET /v1/projects` → `map[string]string`, and `(*Client).Projects() (map[string]string, error)`.
  - `commandConfigMsg{name string; cmd config.Command}`, `formSavedMsg{status manager.Status}`, `formErrMsg{err error}`, `projectsMsg map[string]string`.
  - `fetchCommandConfig(c *client.Client, name string) tea.Cmd`, `saveCommand(c *client.Client, editing bool, name string, cmd config.Command) tea.Cmd`, `fetchProjects(c *client.Client) tea.Cmd`.
  - `overlayForm` in the `overlay` enum.

The TUI needs the project map to prefill FOLDER for a namespaced name, and nothing exposes it yet — hence the small read endpoint.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/form_routing_test.go`:

```go
package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
)

func TestAOpensAnEmptyForm(t *testing.T) {
	m := modelWithRows(t, "web")

	m, _ = step(t, m, key("a"))
	if m.overlay != overlayForm {
		t.Fatal("a should open the form")
	}
	if m.form.Editing() {
		t.Fatal("a opens a create, not an edit")
	}
	if !strings.Contains(m.View(), "New command") {
		t.Fatalf("form not rendered:\n%s", m.View())
	}
}

func TestEFetchesTheSpecThenOpensTheForm(t *testing.T) {
	m := modelWithRows(t, "web")

	// e asks for the full spec first, because PUT replaces.
	_, cmd := step(t, m, key("e"))
	if cmd == nil {
		t.Fatal("e produced no command: it must fetch the spec first")
	}

	// The reply opens the form filled in.
	m, _ = step(t, m, commandConfigMsg{
		name: "web",
		cmd:  config.Command{Cmd: []string{"npm", "start"}, Health: "http://localhost:3000/h"},
	})
	if m.overlay != overlayForm || !m.form.Editing() {
		t.Fatal("the config reply should open the form in edit mode")
	}
	if _, got := m.form.Result(); got.Health == "" {
		t.Fatal("the fetched spec was not carried into the form")
	}
}

func TestFormEnterSaves(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("a"))
	for _, r := range "extra" {
		m, _ = step(t, m, key(string(r)))
	}

	_, cmd := step(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no save command")
	}
}

func TestFormEscCancels(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("a"))
	m, _ = step(t, m, key("esc"))

	if m.overlay != overlayNone {
		t.Fatal("esc should close the form")
	}
}

func TestFormErrorKeepsTheFormOpen(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("a"))
	for _, r := range "web" {
		m, _ = step(t, m, key(string(r)))
	}

	m, _ = step(t, m, formErrMsg{err: errors.New(`command already exists: web`)})
	if m.overlay != overlayForm {
		t.Fatal("an error must not close the form")
	}
	view := m.View()
	if !strings.Contains(view, "already exists") {
		t.Fatalf("error not shown:\n%s", view)
	}
	if !strings.Contains(view, "web") {
		t.Fatalf("input was lost:\n%s", view)
	}
}

func TestFormSaveClosesAndSelects(t *testing.T) {
	m := modelWithRows(t, "web", "extra")
	m, _ = step(t, m, key("a"))

	m, _ = step(t, m, formSavedMsg{status: manager.Status{Name: "extra", State: manager.Stopped}})
	if m.overlay != overlayNone {
		t.Fatal("a save should close the form")
	}
	if got, _ := m.table.Selected(); got.Name != "extra" {
		t.Fatalf("selected = %q, want the command just saved", got.Name)
	}
}

func TestFormKeysDoNotLeakToPanels(t *testing.T) {
	m := modelWithRows(t, "web", "other")
	before, _ := m.table.Selected()

	m, _ = step(t, m, key("a"))
	m, _ = step(t, m, key("j")) // a letter, not navigation, while the form is open

	if after, _ := m.table.Selected(); after.Name != before.Name {
		t.Fatal("j moved the table cursor while the form was open")
	}
	if m.overlay != overlayForm {
		t.Fatal("the form closed on a plain keystroke")
	}
}

func TestProjectsMsgFeedsTheForm(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, projectsMsg(map[string]string{"app": "/home/me/coding/app"}))
	m, _ = step(t, m, key("a"))

	for _, r := range "app:api" {
		m, _ = step(t, m, key(string(r)))
	}
	if _, got := m.form.Result(); got.Cwd != "/home/me/coding/app" {
		t.Fatalf("folder = %q, want the project directory from projectsMsg", got.Cwd)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestAOpens|TestEFetches|TestForm|TestProjectsMsg' -v`
Expected: FAIL — `undefined: overlayForm`.

- [ ] **Step 3: Add the projects endpoint and client method**

In `internal/api/write.go`:

```go
// projects lists the registered projects, so a client can resolve a
// namespaced name to a directory without reading the config file itself.
func (s *Server) projects(w http.ResponseWriter, _ *http.Request) {
	cfg, err := s.reload()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	out := cfg.Projects
	if out == nil {
		out = map[string]string{}
	}
	writeJSON(w, http.StatusOK, out)
}
```

Register it in `routes()`:

```go
	mux.HandleFunc("GET /v1/projects", s.projects)
```

And in `internal/client/client.go`:

```go
// Projects maps each registered project's basename to its directory.
func (c *Client) Projects() (map[string]string, error) {
	var out map[string]string
	return out, c.do(context.Background(), "GET", "/v1/projects", nil, &out)
}
```

- [ ] **Step 4: Add the messages and commands**

Append to `internal/tui/messages.go`:

```go
// commandConfigMsg carries a command's full spec, fetched before an edit.
type commandConfigMsg struct {
	name string
	cmd  config.Command
}

// formSavedMsg is a successful create or update.
type formSavedMsg struct{ status manager.Status }

// formErrMsg is a rejected one. The form stays open with its input.
type formErrMsg struct{ err error }

// projectsMsg carries the registered projects, for the folder prefill.
type projectsMsg map[string]string

// fetchCommandConfig reads a command's whole spec. PUT replaces, so an edit
// must send back the fields the form does not show.
func fetchCommandConfig(c *client.Client, name string) tea.Cmd {
	return func() tea.Msg {
		cmd, err := c.CommandConfig(name)
		if err != nil {
			return formErrMsg{err: err}
		}
		return commandConfigMsg{name: name, cmd: cmd}
	}
}

// saveCommand creates or replaces a command in the config.
func saveCommand(c *client.Client, editing bool, name string, cmd config.Command) tea.Cmd {
	return func() tea.Msg {
		var (
			st  manager.Status
			err error
		)
		if editing {
			st, err = c.Update(name, cmd)
		} else {
			st, err = c.Create(name, cmd)
		}
		if err != nil {
			return formErrMsg{err: err}
		}
		return formSavedMsg{status: st}
	}
}

func fetchProjects(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		p, err := c.Projects()
		if err != nil {
			return projectsMsg(nil) // not worth a banner; the prefill just stays local
		}
		return projectsMsg(p)
	}
}
```

Add `"github.com/tphuc/lazycomd/internal/config"` to that file's imports.

- [ ] **Step 5: Wire the model**

In `internal/tui/help.go`, extend the overlay enum and the bindings:

```go
const (
	overlayNone overlay = iota
	overlayHelp
	overlayPalette
	overlayPorts0 // removed in the panel layout; kept out of the iota order
	overlayForm
)
```

Use this instead — the enum has no stale member:

```go
const (
	overlayNone overlay = iota
	overlayHelp
	overlayPalette
	overlayForm
)
```

```go
	{"a", "add", scopeCommands},
	{"e", "edit", scopeCommands},
```

In `internal/tui/tui.go`, add to the `Model` struct and `New`:

```go
	form     formModel
	projects map[string]string
```

```go
		form: newForm(launchDir(), remoteClient(c)),
```

with the two helpers at the end of the file:

```go
// launchDir is where the TUI was started, which is almost always the project
// you are adding a command for.
func launchDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// remoteClient reports whether the daemon is reached over TCP, where a local
// path means nothing.
func remoteClient(c *client.Client) bool {
	return !strings.HasPrefix(c.Addr(), "unix://")
}
```

Fetch the projects once, in `Init`:

```go
	return tea.Batch(fetchStatus(m.client), tickCmd(tickConnected), fetchSystem(m.client), systemTickCmd(), fetchProjects(m.client))
```

Handle the new messages in `Update`:

```go
	case projectsMsg:
		m.projects = map[string]string(msg)
		return m, nil

	case commandConfigMsg:
		m.form.OpenEdit(msg.name, msg.cmd, m.projects)
		m.overlay = overlayForm
		return m, textinput.Blink

	case formSavedMsg:
		m.overlay = overlayNone
		m.table.MergeStatus(msg.status)
		m.table.SelectName(msg.status.Name)
		m.setStatus(msg.status.Name + " saved")
		return m, tea.Batch(fetchStatus(m.client), m.syncStream())

	case formErrMsg:
		if m.overlay == overlayForm {
			m.form.SetError(msg.err.Error())
			return m, nil
		}
		m.setStatus(msg.err.Error())
		return m, nil
```

Give the form its keys, at the top of `handleKey` beside the other overlays:

```go
	if m.overlay == overlayForm {
		switch s {
		case "esc":
			m.overlay = overlayNone
			return m, nil
		case "enter":
			name, cmd := m.form.Result()
			if name == "" {
				m.form.SetError("name is required")
				return m, nil
			}
			return m, saveCommand(m.client, m.form.Editing(), name, cmd)
		}
		var cmd tea.Cmd
		m.form, cmd = m.form.Update(k)
		return m, cmd
	}
```

Open it from the Commands panel, in `handleTableKey`:

```go
	case "a":
		m.form.OpenCreate(m.projects)
		m.overlay = overlayForm
		return m, textinput.Blink
	case "e":
		sel, ok := m.table.Selected()
		if !ok {
			return m, nil
		}
		return m, fetchCommandConfig(m.client, sel.Name)
```

And render it in `View`, beside the help case:

```go
	if m.overlay == overlayForm {
		body := m.form.View(minInt(m.width, 56), minInt(m.bodyH, 12))
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
	}
```

with:

```go
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

Add `"os"` to `tui.go`'s imports.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ ./internal/api/ ./internal/client/ 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: PASS for all three.

- [ ] **Step 7: Commit**

```bash
git add internal/
git commit -m "feat(tui): add and edit a command from the form

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Deleting with a confirmation

**Files:**
- Create: `internal/tui/confirm.go`
- Modify: `internal/tui/tui.go` — `overlayConfirm`, the `d` key, the delete result
- Modify: `internal/tui/messages.go` — the delete command and messages
- Modify: `internal/tui/help.go` — `overlayConfirm` and the `d` binding
- Test: `internal/tui/confirm_test.go`

**Interfaces:**
- Consumes: `client.Delete`, `panelView`.
- Produces:
  - `type confirmModel struct` with `newConfirm()`, `(*confirmModel).Open(name, file string)`, `(confirmModel).View(width, height int) string`, `(confirmModel).Name() string`.
  - `deletedMsg{name string}`, `deleteCommand(c *client.Client, name string) tea.Cmd`.
  - `func targetFileLabel(name string) string` — `config.yaml`, or `app/lazycomd.yaml` for `app:api`.
  - `overlayConfirm`.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/confirm_test.go`:

```go
package tui

import (
	"strings"
	"testing"
)

func TestTargetFileLabel(t *testing.T) {
	if got := targetFileLabel("web"); got != "config.yaml" {
		t.Fatalf("bare name = %q, want config.yaml", got)
	}
	if got := targetFileLabel("app:api"); got != "app/lazycomd.yaml" {
		t.Fatalf("namespaced = %q, want app/lazycomd.yaml", got)
	}
}

func TestConfirmViewNamesTheCommandAndFile(t *testing.T) {
	c := newConfirm()
	c.Open("app:api", targetFileLabel("app:api"))

	view := c.View(50, 7)
	for _, want := range []string{"app:api", "app/lazycomd.yaml", "y", "n"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestDAsksBeforeDeleting(t *testing.T) {
	m := modelWithRows(t, "web")

	m, cmd := step(t, m, key("d"))
	if m.overlay != overlayConfirm {
		t.Fatal("d should ask first")
	}
	if cmd != nil {
		t.Fatal("d fired a delete without asking")
	}
	if !strings.Contains(m.View(), "delete web") {
		t.Fatalf("prompt not rendered:\n%s", m.View())
	}
}

func TestConfirmNCancels(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("d"))

	m, cmd := step(t, m, key("n"))
	if m.overlay != overlayNone {
		t.Fatal("n should close the prompt")
	}
	if cmd != nil {
		t.Fatal("n fired a delete")
	}

	m, _ = step(t, m, key("d"))
	m, cmd = step(t, m, key("esc"))
	if m.overlay != overlayNone || cmd != nil {
		t.Fatal("esc should cancel too")
	}
}

func TestConfirmYDeletes(t *testing.T) {
	m := modelWithRows(t, "web")
	m, _ = step(t, m, key("d"))

	m, cmd := step(t, m, key("y"))
	if m.overlay != overlayNone {
		t.Fatal("y should close the prompt")
	}
	if cmd == nil {
		t.Fatal("y produced no delete command")
	}
}

func TestDeletedMsgRefreshes(t *testing.T) {
	m := modelWithRows(t, "web", "other")
	m, cmd := step(t, m, deletedMsg{name: "web"})
	if cmd == nil {
		t.Fatal("a delete should refresh the list")
	}
	if !strings.Contains(m.bottom(), "web") {
		t.Fatalf("status line = %q, want it to mention the deleted command", m.bottom())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run 'TestTargetFileLabel|TestConfirm|TestD|TestDeleted' -v`
Expected: FAIL — `undefined: targetFileLabel`.

- [ ] **Step 3: Write the confirmation**

Create `internal/tui/confirm.go`:

```go
package tui

import (
	"fmt"
	"strings"
)

// confirmModel asks before an irreversible edit to a file.
type confirmModel struct {
	name string
	file string
}

func newConfirm() confirmModel { return confirmModel{} }

func (c *confirmModel) Open(name, file string) {
	c.name, c.file = name, file
}

func (c confirmModel) Name() string { return c.name }

func (c confirmModel) View(width, height int) string {
	rows := []string{
		"",
		fmt.Sprintf("  delete %s from %s?", c.name, c.file),
		"",
		styleDim.Render("  this edits a file you may have committed"),
		"",
		styleDim.Render("  y delete · n or esc cancel"),
	}
	return panelView(panelSpec{Title: "Confirm", Width: width, Height: height, Rows: rows, Focused: true})
}

// targetFileLabel names the file a write to this command will change, so the
// prompt never surprises somebody by editing a project's shared config.
func targetFileLabel(name string) string {
	ns, _, namespaced := strings.Cut(name, ":")
	if !namespaced {
		return "config.yaml"
	}
	return ns + "/lazycomd.yaml"
}
```

- [ ] **Step 4: Wire it up**

In `internal/tui/messages.go`:

```go
// deletedMsg is a command removed from the config.
type deletedMsg struct{ name string }

// deleteCommand removes a command from the config file.
func deleteCommand(c *client.Client, name string) tea.Cmd {
	return func() tea.Msg {
		if err := c.Delete(name); err != nil {
			return formErrMsg{err: err}
		}
		return deletedMsg{name: name}
	}
}
```

In `internal/tui/help.go`, add `overlayConfirm` to the enum after `overlayForm`, and the binding:

```go
	{"d", "delete", scopeCommands},
```

In `internal/tui/tui.go`, add `confirm confirmModel` to the model and `confirm: newConfirm()` to `New`, then the key handling beside the other overlays:

```go
	if m.overlay == overlayConfirm {
		switch s {
		case "y":
			m.overlay = overlayNone
			return m, deleteCommand(m.client, m.confirm.Name())
		case "n", "esc", "q":
			m.overlay = overlayNone
			return m, nil
		}
		return m, nil
	}
```

the `d` key in `handleTableKey`:

```go
	case "d":
		sel, ok := m.table.Selected()
		if !ok {
			return m, nil
		}
		m.confirm.Open(sel.Name, targetFileLabel(sel.Name))
		m.overlay = overlayConfirm
		return m, nil
```

the result in `Update`:

```go
	case deletedMsg:
		m.setStatus(msg.name + " deleted from the config")
		return m, fetchStatus(m.client)
```

and the render in `View`:

```go
	if m.overlay == overlayConfirm {
		body := m.confirm.View(minInt(m.width, 56), minInt(m.bodyH, 8))
		return strings.Join([]string{m.header(), body, m.bottom()}, "\n")
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/tui/ -v 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): delete a command after confirming which file changes

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: End-to-end check, README, full verification

**Files:**
- Create: `internal/tui/writes_integration_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: everything. Produces no new code.

- [ ] **Step 1: Write the end-to-end test**

Create `internal/tui/writes_integration_test.go`:

```go
package tui

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/api"
	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/configw"
	"github.com/tphuc/lazycomd/internal/manager"
)

// writableModel is a model wired to a daemon over a real config file.
func writableModel(t *testing.T, body string) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := manager.New(cfg, t.TempDir())
	m.Grace = 500 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.New(api.Options{
		Manager:    m,
		Reload:     func() (*config.Config, error) { return config.Load(path) },
		ConfigPath: path,
		Writers:    configw.NewRegistry(),
	})

	sockDir, err := os.MkdirTemp("/tmp", "lzc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s.sock")

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
	sink, _ := captureSink()
	model := New(c, sink)
	next, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = next.(Model)
	t.Cleanup(func() { model.stream.stop() })
	return model, path
}

func TestEndToEndFormWritesTheFile(t *testing.T) {
	m, path := writableModel(t, "# keep me\ncommands:\n  web:\n    cmd: [\"sleep\", \"30\"]\n    cwd: /tmp\n")
	m = drive(t, m, fetchStatus(m.client))

	m, _ = step(t, m, key("a"))
	for _, r := range "extra" {
		m, _ = step(t, m, key(string(r)))
	}
	m, _ = step(t, m, key("tab"))
	for _, r := range "sleep 30" {
		m, _ = step(t, m, key(string(r)))
	}

	m, cmd := step(t, m, key("enter"))
	m = drive(t, m, cmd)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "extra:") {
		t.Fatalf("command not written:\n%s", data)
	}
	if !strings.Contains(string(data), "# keep me") {
		t.Fatalf("the header comment was lost:\n%s", data)
	}
	if m.overlay != overlayNone {
		t.Fatal("the form stayed open after a successful save")
	}
}

func TestEndToEndDeleteRemovesFromTheFile(t *testing.T) {
	m, path := writableModel(t, "commands:\n  web:\n    cmd: [\"sleep\", \"30\"]\n  other:\n    cmd: [\"sleep\", \"30\"]\n")
	m = drive(t, m, fetchStatus(m.client))

	// The table is sorted, so "other" is first; select "web".
	m.table.SelectName("web")

	m, _ = step(t, m, key("d"))
	m, cmd := step(t, m, key("y"))
	m = drive(t, m, cmd)

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "web:") {
		t.Fatalf("web survived:\n%s", data)
	}
	if !strings.Contains(string(data), "other:") {
		t.Fatalf("other went with it:\n%s", data)
	}
}

func TestEndToEndDuplicateNameStaysInTheForm(t *testing.T) {
	m, _ := writableModel(t, "commands:\n  web:\n    cmd: [\"sleep\", \"30\"]\n")
	m = drive(t, m, fetchStatus(m.client))

	m, _ = step(t, m, key("a"))
	for _, r := range "web" {
		m, _ = step(t, m, key(string(r)))
	}
	m, _ = step(t, m, key("tab"))
	for _, r := range "sleep 30" {
		m, _ = step(t, m, key(string(r)))
	}
	m, cmd := step(t, m, key("enter"))
	m = drive(t, m, cmd)

	if m.overlay != overlayForm {
		t.Fatal("the form closed on a rejected save")
	}
	if !strings.Contains(m.View(), "already exists") {
		t.Fatalf("the conflict was not shown:\n%s", m.View())
	}
}
```

- [ ] **Step 2: Run the end-to-end tests**

Run: `go test -race ./internal/tui/ -run TestEndToEnd -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: PASS, including the three from spec #2 and #3.

- [ ] **Step 3: Update the README**

In the TUI keymap table, add three rows after the `r` row:

```markdown
| `a` | Commands | add a command, writing it to the config |
| `e` | Commands | edit the selected command |
| `d` | Commands | delete it, after confirming which file changes |
```

And a subsection after the keymap:

````markdown
### Editing the config from the TUI

`a` opens a four-field form — name, command, folder, restart — and writes the
result into your config:

```
╭─ New command ──────────────────────────────────╮
│                                                │
│  NAME     web                                  │
│  COMMAND  npm start                            │
│  FOLDER   ~/coding/app                         │
│  RESTART  ‹ on-failure ›                       │
│                                                │
│  where the command runs · blank = ~            │
│  tab next · enter save · esc cancel            │
╰────────────────────────────────────────────────╯
```

FOLDER is `cwd:` in the file — where the command runs. It starts filled with
the directory you launched the TUI in, and follows the project once the name
is namespaced, as in `app:api`.

The command line splits on spaces. A line containing `|`, `>`, `<`, `&`, `;`,
`$` or `*` is passed to `sh -c` whole instead, and the form says so while you
type.

`e` edits the selected command. It reads the command's full spec first, so
fields the form never shows — `env`, `depends_on`, `health`, `port` — survive
the edit untouched. Renaming is not offered: that is a file edit.

`d` deletes, after a prompt naming the file that will change.

Only the lines of the command being touched are rewritten. Comments, blank
lines and quoting everywhere else in the file come out byte-identical, so
`git diff` shows the one command you changed. If the file has changed on disk
since the daemon read it — because you have it open in an editor — the write
is refused and says so rather than overwriting you.
````

Add the endpoints to the API table:

| `GET /v1/commands/{name}/config` | | the command's full spec |
| `POST /v1/commands` | a command plus `name` | 201 and its status |
| `PUT /v1/commands/{name}` | a command | 200 and its status |
| `DELETE /v1/commands/{name}` | | 204 |
| `GET /v1/projects` | | registered project basenames to directories |

- [ ] **Step 4: Check the README against the code**

```bash
grep -oE '\{"[^"]+", "[^"]+"' internal/tui/help.go | sed 's/[{"]//g' | awk -F, '{print $1}' | sort -u
grep -oE '"[A-Z]+ /v1[^"]*"' internal/api/server.go | tr -d '"'
```

Every key and route in the README must appear in those two lists, and every
entry in them must appear in the README.

- [ ] **Step 5: Verify the whole repository**

```bash
gofmt -l .
go vet ./...
go test -race ./...
go build -o /tmp/lazycomd ./cmd/lazycomd
go test ./internal/depsguard/ -v
```

Expected: `gofmt` silent, `go vet` clean, every package green with no races,
the binary building, and `internal/configw` carrying no TUI dependency.

- [ ] **Step 6: Drive it by hand**

```bash
SP=/private/tmp/claude-501/-Users-tphuc/ed163cab-b6bd-4c7b-901e-7bb5bf910640/scratchpad
go build -o $SP/lazycomd ./cmd/lazycomd
mkdir -p $SP/lzc-config
cat > $SP/lzc-config/config.yaml <<'CFG'
# hand-written, with comments that must survive
commands:
  # the http server
  web:
    cmd: ["python3", "-m", "http.server", "8099"]
    cwd: /tmp
CFG
cp $SP/lzc-config/config.yaml $SP/before.yaml
rm -rf /tmp/lzcstate
LAZYCOMD_CONFIG=$SP/lzc-config/config.yaml XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd serve &
sleep 1
XDG_STATE_HOME=/tmp/lzcstate $SP/lazycomd      # press a, fill it in, enter; then e; then d
diff -u $SP/before.yaml $SP/lzc-config/config.yaml
kill %1
```

Expected: the `diff` shows only the block you added or changed — the header
comment, the `# the http server` comment and `web`'s own lines untouched.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/ README.md
git commit -m "test(tui): end-to-end config writes, plus docs

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

Checked against `docs/superpowers/specs/2026-09-11-lazycomd-config-writes-design.md`:

| Spec section | Covered by |
|---|---|
| `internal/configw` owns writing; `config` stays read-only | Tasks 2-5; `config` gains only `ParseBytes` and `Projects` |
| Five-step read, parse, locate, splice, validate-then-write | Task 5 (`edit`) |
| Validation before the write, never after | Task 5, with `TestRefusesToWriteSomethingInvalid` |
| Line range ends at the command's own deepest line | Task 3 (`blockRange`, `maxLine`) |
| Deleting keeps the next command's comment | Tasks 3 and 5 |
| `omitempty` rendering, re-indented | Task 2 |
| Temp file, `fsync`, rename, mode preserved | Task 4 |
| `ErrChanged` on a modification-time or size change | Task 4 |
| Four endpoints, `config.Command` plus `name` as the body | Task 7 |
| Bare name to the global config, `app:api` to the project file under a bare key | Task 7 (`targetFile`), with a test |
| `Config.Projects` | Task 1 |
| Reload after the write through the existing path | Task 7 (`applyReload`) |
| Status codes 400, 404, 409, 500 | Task 7 (`failWrite`) |
| Client methods | Task 8 |
| The form: four fields, tab, enter, esc, restart cycling | Task 9 |
| FOLDER labelled, hinted, prefilled, project-aware, blank when remote | Task 9 |
| Shell metacharacter detection | Task 9 (`splitCommand`) |
| Edit does not rename; the name field is locked | Task 9 |
| Edit preserves unshown fields | Tasks 9 and 10 (`fetchCommandConfig` first) |
| Errors stay in the form with input intact | Tasks 9 and 10 |
| Delete asks, naming the file | Task 11 |
| Spec's test table | Tasks 2-5, 7-9, 11, 12 |
| Non-goals | Nothing in the plan implements them |
| Definition of done | Task 12 steps 2, 5 and 6 |

Gaps found and closed while reviewing:

- **The TUI had no way to learn the project map.** The spec says FOLDER follows
  the project once a name is namespaced, but nothing exposed `Config.Projects`
  to a client. Task 10 adds `GET /v1/projects` and `Client.Projects()`.
- **`config.Command` had no JSON tags.** The API takes a command as JSON, and
  without tags the wire format would have been `{"Cmd":...}` with Go's field
  names. Task 7 adds them, matching the YAML names.
- **The validation refusal was only a message prefix.** Mapping it to a 400
  would have meant matching on error text; Task 7 introduces
  `configw.ErrInvalid` and maps on it.
- **`NewServer` was about to take six arguments.** Task 6 converts it to an
  `Options` struct first, which is why that task exists at all and why it
  changes no behavior.
- **A daemon with no writer registry** — every test helper, since they pass no
  `Writers` — needed a defined answer. Writes return 501 rather than panicking
  on a nil registry, and there is a test for it.
- **`overlay` gained two members**, and the panel layout had already removed
  `overlayPorts`. Tasks 10 and 11 spell the enum out in full so nobody
  reintroduces a stale member by appending.

Type consistency: `config.Command` is the single shape across `configw`, `api`,
`client` and the form. `configw.File` is created only through `Registry.File`,
so one path always means one mutex. `formModel.Result()` returns the same
`(string, config.Command)` pair `saveCommand` takes. The test helpers `key`,
`step`, `modelWithRows`, `drive`, `captureSink` and `runeWidth` already exist
from specs #2 and #3 and are reused by name; `writeFixture` is declared once in
Task 4 and reused by Task 5.
