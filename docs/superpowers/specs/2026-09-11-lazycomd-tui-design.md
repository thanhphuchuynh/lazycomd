# lazycomd — TUI Design (Spec #2)

Date: 2026-09-11
Status: Approved for planning
Scope: Spec #2 of 3. A terminal UI client over the daemon's HTTP API.

## Problem

Spec #1 shipped a daemon that holds every configured command and an HTTP API
to drive it, plus a thin CLI. The CLI answers one question per invocation:
`lazycomd ls`, then `lazycomd logs api -f`, then `lazycomd restart api`. That
is fine in a script and tedious by hand — watching a stack means retyping
commands and losing the last one's output.

The TUI is the interactive face of the same API: one screen showing every
command's state alongside the selected command's live output, with lifecycle
actions one keystroke away.

## Relationship to the other specs

- Spec #1 (shipped) — daemon, process manager, HTTP API, CLI client.
- **Spec #2 (this document) — the TUI.**
- Spec #3 — the read-only dashboard: ports in use, container and service
  health.

This spec adds no daemon code and no API endpoints. It consumes spec #1's API
exactly as built: `GET /v1/commands`, `GET /v1/commands/{name}/logs`,
`GET /v1/commands/{name}/logs/stream`, and the three lifecycle POSTs.

## Approach

`bubbletea` for the event loop with `lipgloss` for layout, one root model
composed of three sub-models: a hand-written command table, a log pane built
on `bubbles/viewport`, and a palette overlay built on `bubbles/textinput`.

Two alternatives were considered and rejected:

- **All `bubbles` widgets, `bubbles/table` included.** Less code, but that
  widget owns its rows and cursor, so the 1s refresh would rebuild rows and
  re-derive the cursor through its API every tick. The table is precisely the
  part whose refresh behavior we care about; 80 lines of our own beats
  fighting a widget's state model on every tick.
- **No `bubbles` at all**, rendering the log viewport and input line by hand.
  One fewer dependency in exchange for roughly 200 lines of scroll and cursor
  math that `bubbles` already has right.

Hand-rolling the whole terminal layer over `golang.org/x/term` was rejected
earlier still: 600-900 lines of key parsing, `SIGWINCH` handling and redraw
before any feature exists.

### Dependency rule

This spec breaks spec #1's "`yaml.v3` is the only dependency" rule, and
confines the break: it adds `bubbletea`, `bubbles`, `lipgloss` and their
transitive dependencies (`termenv`, `ansi`, `golang.org/x/term`,
`golang.org/x/sync`).

Nothing under `internal/config`, `internal/manager`, `internal/logbuf` or
`internal/api` may import any of them. The daemon's dependency tree stays
`yaml.v3` only, enforced by a test that runs `go list -deps` over those four
packages and fails if a TUI dependency appears.

## Layout

```
┌ lazycomd ───────────────────────────────── 5 commands · 2 running ┐
│ NAME        STATE    PID    UPTIME  RS │ tick — following         │
│>tick        running  54405  2m13s    0 │ 1789114081               │
│ greet       stopped  -      -        0 │ 1789114082               │
│ app:api     failed   -      -        3 │ 1789114083               │
│ app:web     running  54410  2m10s    0 │                          │
└────────────────────────────────────────┴──────────────────────────┘
 j/k move  s start  S stop  r restart  p palette  f follow  / search  ? help
```

The table takes 40% of the width, minimum 32 columns; the log pane takes the
rest. Below 80 columns the table collapses to `NAME STATE`. Below 60 columns
the log pane is hidden and the table fills the width; if it had focus, focus
falls back to the table, and an open filter input closes.

## Package structure

```
internal/tui/tui.go        Run(), root Model, Update/View dispatch, focus
internal/tui/table.go      command table: rows, cursor, selection by name
internal/tui/logs.go       log pane: viewport, follow mode, filter
internal/tui/palette.go    fuzzy palette overlay
internal/tui/fuzzy.go      subsequence matcher and scorer
internal/tui/stream.go     SSE stream goroutine lifecycle
internal/tui/messages.go   msg types and the tea.Cmds wrapping the client
internal/tui/help.go       binding table, help overlay, key bar
cmd/lazycomd/main.go       bare invocation calls tui.Run()
```

`internal/tui` imports `internal/client` and `internal/manager` (for the
`Status` type) and nothing else of ours. It never touches a process: every
action is an HTTP call, the same as the CLI.

`tui.Run(c *client.Client) error` is the entire public surface.
`cmd/lazycomd` builds the client the way every other subcommand does, so
`LAZYCOMD_ADDR` and `LAZYCOMD_TOKEN` work unchanged.

## Entry point

Bare `lazycomd` opens the TUI, the way `lazygit` and `lazydocker` do.
`lazycomd help` still prints usage and every existing subcommand is
unchanged.

When stdout is not a terminal (`lazycomd | cat`, CI, a test binary), bare
`lazycomd` prints usage and exits 2 — today's behavior — instead of launching.
The check is `term.IsTerminal(int(os.Stdout.Fd()))`.

## State and message flow

```go
type Model struct {
    client  *client.Client
    sink    *sink          // holds *tea.Program, for Send from goroutines
    table   tableModel
    logs    logsModel
    palette paletteModel

    focus   focus          // focusTable | focusLogs | focusPalette
    overlay overlay        // overlayNone | overlayHelp
    width, height int

    connected bool
    status    string       // transient: replaces the key bar for 4s
    statusAt  time.Time    // when it was set
    stream    *streamHandle
}
```

`sink` is a small struct holding the `*tea.Program`, created before the model
and assigned after `tea.NewProgram`, so every copy of the model shares one
pointer and a goroutine can deliver messages.

Messages, all declared in `messages.go`:

| Message | Source | Effect |
|---|---|---|
| `tickMsg` | `tea.Tick` | fire the status fetch, schedule the next tick |
| `statusMsg []manager.Status` | status fetch | rebuild table rows, `connected = true` |
| `statusErrMsg{err}` | status fetch | `connected = false`, banner, slower tick |
| `logTailMsg{name, lines}` | tail fetch on selection change | replace contents, reset follow to on, clear any filter |
| `logLineMsg{name, line}` | SSE goroutine via `sink` | append when `name` is still selected, else drop |
| `streamEndedMsg{name, err}` | SSE goroutine | clear the handle; reopen on the next tick if still selected |
| `actionDoneMsg{verb, name, st, err}` | start, stop, restart | merge one row, set the status line |

The tick is 1s while connected and 2s while not, so a down daemon retries on
its own tick instead of in a second loop.

The transient status line — an action result or an API error — replaces the
bottom key bar for 4 seconds, after which the key bar returns. The
disconnected banner is separate, sits at the top, and stays until the daemon
answers again.

### Selection is a name, not an index

A refresh that adds or removes commands keeps the cursor on the same command.
If the selected command is gone from the config, the cursor clamps to the
nearest index.

### Stream lifecycle

One stream at a time, for the selected command only. Moving the selection
cancels the previous context, fetches `client.Logs(name, 500)` to seed the
pane, then starts a goroutine running `client.Stream(ctx, name, w)` where `w`
is a line-splitting writer calling `sink.p.Send(logLineMsg{...})`. `Run`
cancels the stream on quit and waits on its `done` channel, so no goroutine
outlives the program.

Lines from a cancelled stream can still arrive in flight. That is why
`logLineMsg` carries the command name and is dropped when it no longer
matches the selection.

## Command table

```go
type tableModel struct {
    rows     []manager.Status
    selected string // command name, not index
    cursor   int
    width, height int
}

func (t *tableModel) SetRows(rows []manager.Status) // keeps selected if present
func (t tableModel) Selected() (manager.Status, bool)
func (t tableModel) Update(msg tea.Msg) (tableModel, tea.Cmd)
func (t tableModel) View() string
```

Columns `NAME STATE PID UPTIME RS`, in the API's own name order. State is
colored: `running` green, `stopped` dim, `failed` red, `starting` and
`stopping` yellow. A command whose spec changed under a reload shows
`running*` — spec #1 only stages a change on a running or starting command,
so the asterisk never appears on a stopped one. The help overlay explains it. Uptime renders
as `45s`, `2m13s`, `1h4m`.

Keys while the table has focus:

| Key | Action |
|---|---|
| `j` `k` `↓` `↑` | move the cursor |
| `g` `G` | first / last row |
| `s` | start, dependencies first |
| `S` | stop |
| `r` | restart |
| `p` | open the palette |
| `f` | toggle log follow |
| `/` | focus the log pane and open its filter input |
| `Tab` | focus the log pane |
| `?` | help overlay |
| `q` `Ctrl-C` | quit |

`s` always starts with dependencies: in a TUI that is what the keystroke
means, and the CLI keeps `-d` for the scripted case. There is no
dependency-less start key.

`q` and `Ctrl-C` quit from either pane. While an input line is open — the
filter or the palette — `q` types a literal `q` and `Esc` closes the input;
`Ctrl-C` still quits.

No confirmation prompts. Stop is a single keystroke because it is
recoverable, and quitting is harmless — the daemon and everything it runs
survive the TUI exiting.

Every action is a `tea.Cmd` returning `actionDoneMsg`, so the UI never blocks
on HTTP. The returned status merges into its row immediately, ahead of the
next tick. A 409 lands in the status line as the daemon's own message —
`illegal in current state: tick is running` — and changes nothing else.

## Log pane

```go
type logsModel struct {
    vp     viewport.Model
    name   string
    lines  []string      // own store, capped at 5000
    follow bool
    search searchState
}

type searchState struct {
    editing bool            // input line open
    input   textinput.Model
    query   string          // applied
    matches int
}
```

The pane keeps its own line slice rather than reading back the viewport's
rendered text, because filtering needs the unfiltered lines. It is capped at
5000 lines, oldest dropped; the daemon's ring buffer is the real history, and
every selection change seeds the pane with the last 500 lines.

**Follow** is on by default: new lines append and scroll to the bottom. Any
manual scroll turns follow off, so scrolling up never yanks you back down.
`f` re-enables it and jumps to the bottom. The pane title reads
`tick — following` or `tick — paused (1204 lines)`.

**Filter, not jump.** `/` opens an input line at the pane's foot; `Enter`
applies it and the pane shows only matching lines, titled
`tick — filter "error" · 12 of 1204`. `Esc` clears it. Matching new lines
keep appending, so a filtered view still follows.

Two deliberate limits. Matching is plain substring with smart-case (a
lowercase query matches case-insensitively) — no regex, which would add a
pattern-error state and per-line cost for a need that has not come up. And
there is no `n`/`N` match navigation, because filtering already answers "show
me the errors" without scroll-position math.

Long lines are truncated to the pane width rather than wrapped, so one line
is always one row and the filter and scroll math stay trivial. The code
carries this marker:

```go
// ponytail: truncate, don't wrap. Keeps one line == one row, so filter and
// scroll math stay trivial. Wrap when reading long JSON lines actually hurts.
```

Keys while the log pane has focus: `j`/`k` scroll, `Ctrl-D`/`Ctrl-U` half
page, `g`/`G` top and bottom, `f` follow, `/` filter, `Esc` or `Tab` back to
the table.

## Palette

`p` opens an overlay over the table: an input line plus the matching commands,
filtered as you type.

```go
func Match(query, target string) (score int, positions []int, ok bool)
```

Subsequence matching — every query rune appears in order — with smart-case,
scored by consecutive runs, a start-of-segment bonus (line start or after
`:`, `-` or `_`), an earlier first match, and a shorter target. Candidates
sort by score then name, and matched runes are highlighted. Roughly 40 lines
in `fuzzy.go`, no dependency.

`Enter` on a stopped command starts it with dependencies and selects it in the
table. `Enter` on one already running only selects it, avoiding a pointless
409. `Esc` closes the overlay.

## Help

`?` renders an overlay from a single `[]binding{key, desc, scope}` slice in
`help.go`, and the bottom key bar renders from that same slice, so the bar and
the overlay cannot drift apart. `?`, `Esc` or `q` closes it, and while it is
open every other key is ignored.

## Errors and reconnection

A `statusErrMsg` carrying `client.ErrNoDaemon` sets `connected = false`, shows
`daemon not running — retrying (start with: lazycomd serve)` as a top banner,
keeps the last known rows visible but dimmed, and slows the tick to 2s.
Lifecycle keys while disconnected print that same message instead of firing.
On recovery the banner clears and the log stream reopens for the current
selection.

The TUI never exits because of an API error. `tui.Run` returns an error only
when the terminal itself cannot be set up.

## Testing

Every case below runs without a terminal: `Update` is a pure function and
`View` returns a string.

| Unit | Cases |
|---|---|
| `fuzzy` | candidate ordering, returned match positions, smart-case, a non-subsequence rejected |
| `table` | `SetRows` keeps the selection by name; a removed selection clamps the cursor; `j`/`k` respect bounds; `View` shows all columns at 100 columns and collapses at 70 |
| `logs` | appending while following scrolls to the bottom; a manual scroll clears follow; `f` restores it; a filter shows only matches with the right count; the 5000-line cap drops oldest; a `logLineMsg` for a non-selected command is dropped |
| `palette` | typing narrows the matches; `Enter` emits a start for the highlighted name; an already-running command is selected without being started |
| root `Model` | `statusMsg` populates rows; `statusErrMsg` sets the banner and the 2s tick; a resize re-lays out; `q` quits |
| integration | a real manager and API over a unix socket (the helper from the client tests) with the real client: feed a `tickMsg` and assert the rows, drive the palette path and assert the daemon actually started the command |
| dependency guard | `go list -deps` over `internal/{config,manager,logbuf,api}` must not mention `bubbletea`, `bubbles` or `lipgloss` |

## Non-goals

The ports and container dashboard (spec #3), mouse support, editing config
from the TUI, regex matching or `n`/`N` search navigation, PTY panes,
user-configurable keybindings or themes, and talking to more than one daemon.

## Definition of done

- `lazycomd` opens on a real stack and is usable daily.
- Moving the selection shows that command's logs within about 200ms.
- The filter works on a busy log.
- Killing and restarting `lazycomd serve` recovers without the TUI exiting.
- `go test ./...` passes with no terminal attached.
- `README.md` gains the layout block and the full keymap.
