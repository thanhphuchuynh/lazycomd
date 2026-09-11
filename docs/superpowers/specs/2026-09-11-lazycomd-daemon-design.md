# lazycomd — Daemon and API Design (Spec #1)

Date: 2026-09-11
Status: Approved for planning
Scope: Spec #1 of 3. Core daemon, process manager, HTTP API, CLI client.

## Problem

Developers keep long, awkward commands in shell history: proxies, tunnels, log
tails, port scans, local service stacks. Running them means retyping or
rummaging, and each one owns a terminal tab. Existing tools each solve part of
this. `process-compose` and `mprocs` supervise a fixed process set per project.
`just` and `Taskfile` name commands but do not keep them running or observable.
`overmind` and `tmuxinator` are per-project and terminal-bound. None gives one
long-lived, user-wide control plane that a TUI, a shell script, and a phone can
all drive.

lazycomd is that control plane: one daemon per user, holding every configured
command across every project, controllable through an HTTP API.

## Project decomposition

lazycomd is built in three specs, each with its own plan and implementation
cycle:

1. **Daemon and API** (this spec) — config, process lifecycle, log buffers,
   HTTP API, thin CLI client.
2. **TUI** — a lazygit-style client over the API: fuzzy command palette, log
   panes, keybindings.
3. **Dashboard panels** — read-only awareness: ports in use, container and
   service health, quick actions.

Specs #2 and #3 consume the API defined here and add no process-management
code of their own.

## Approach

Single Go binary. Standard library for HTTP, process control, and CLI parsing;
`gopkg.in/yaml.v3` as the only external dependency.

Two alternatives were considered and rejected:

- **Embed `f1bonacc1/process-compose` as a library.** It already has YAML
  config, restart policies, `depends_on` with readiness probes, and a REST API.
  Rejected because its model is one instance per project and its packages are
  internal rather than a published API. A user-wide daemon would mean running N
  instances or patching internals, and its config schema and API shape would
  become ours to live with.
- **Wrap tmux.** Each command becomes a tmux window; lifecycle and logs go
  through the `tmux` CLI. Rejected for the hard tmux dependency, polling
  `capture-pane` for log streaming, and awkward exit-code propagation.

Owning roughly 150 lines of restart and dependency logic is cheaper than either.

## Repository layout

```
cmd/lazycomd/main.go     flag parsing, subcommand dispatch
internal/config/         YAML load, validate, merge global + project configs
internal/manager/        process lifecycle, state, dependencies, restart
internal/logbuf/         ring buffer, subscribers
internal/api/            HTTP handlers, routes, auth middleware
internal/client/         HTTP client used by CLI subcommands
```

Module path `github.com/tphuc/lazycomd`. `manager` never imports `api`; `api`
never spawns a process.

## Configuration

Global config lives at `~/.config/lazycomd/config.yaml`:

```yaml
listen: ""                 # empty = unix socket only
token_file: ""             # required when listen is set
projects:
  - ~/aviron/api
  - ~/coding/scraper
commands:
  proxy:
    cmd: ["cloudflared", "tunnel", "--url", "localhost:3000"]
    cwd: ~
    env: {LOG: debug}
    shell: false            # true wraps cmd in sh -c
    restart: on-failure     # no | on-failure | always
    autostart: false
    log: true               # also tee output to disk
    size: 262144            # ring buffer bytes, optional
    depends_on: []
```

Each path in `projects` is a directory expected to contain `lazycomd.yaml`,
holding a `commands:` block only. A project file carrying `listen`, `projects`,
or `token_file` is a load error.

Command naming: global commands use their bare name; project commands are
namespaced `<project-dir-basename>:<name>`. A duplicate name within a single
file is a load error. The same name in two different projects is legal — they
have distinct namespaced keys. Two `projects` entries whose directory
basenames collide (`~/a/api` and `~/b/api`) is a load error naming both paths;
resolving it means renaming a directory or dropping one entry.

`cmd` is a string array, not a shell string, so no quoting or word-splitting
bugs and no shell dependency. Setting `shell: true` runs the joined command
through `sh -c` for pipes and redirection.

`~` and `$VAR` in `cwd`, `env`, and `cmd` are expanded at command start time,
not at config load time, so a command started later picks up a changed
environment.

Unknown fields are rejected (`yaml.Decoder.KnownFields(true)`) — a typo in
`restart` must fail loudly, not default silently. A missing or empty `cmd` is
a load error. A `restart` value outside the three allowed strings is a load
error.

## Process lifecycle

State machine per command:

```
stopped ──start──> starting ──spawned──> running ──exit 0────> stopped
                      │                     │
                      │                  exit≠0 ──> failed
                      └─spawn error──> failed

running/starting ──stop──> stopping ──> stopped   (SIGTERM, 10s grace, SIGKILL)
failed ──restart policy──> starting (with backoff)
```

The `stopping` state exists so that a manual stop never triggers the restart
policy. Each process carries an `intentionalStop` flag, checked by the reaper
before it consults `restart:`.

Core types:

```go
type Manager struct {
    mu    sync.Mutex
    procs map[string]*Process   // key = "project:name" or "name"
    cfg   *config.Config
}

type Process struct {
    Name     string
    Spec     config.Command
    State    State
    PID      int
    Started  time.Time
    ExitCode *int
    Restarts int
    Logs     *logbuf.Buffer
    cmd      *exec.Cmd
    cancel   context.CancelFunc
}
```

One mutex on `Manager` rather than one per process. It is held only for map
and field access, never across a spawn or a `Wait`.

Spawning uses `exec.CommandContext` with `SysProcAttr{Setpgid: true}`. Stdout
and stderr both write to the command's ring buffer, interleaved as a terminal
would show them. Killing sends the signal to the negated process group ID so
child trees die with their parent — the case naive supervisors get wrong with
wrapper commands like `npm run`.

One reaper goroutine per process calls `cmd.Wait()`, then under the lock sets
the final state and exit code, then decides whether to restart. Backoff
doubles from 1s to a 30s cap and resets after 60 seconds of continuous uptime.
`always` restarts on a zero exit too; `on-failure` restarts only on a non-zero
exit.

`depends_on` applies at start time only. `StartWithDeps(name)` walks the
dependency list depth-first, starting anything not already running, and waits
200ms after each spawn to let it settle. Dependency cycles are detected at
config load time by a depth-first search with color marking, not at start time.
Stopping a command does not cascade to its dependents: stopping a database
leaves the API that depends on it running.

Readiness is deliberately shallow. A dependency is considered ready once its
process is spawned. The code carries this marker:

```go
// ponytail: readiness = "spawned + 200ms". add port/http probes when a real
// stack races (pg accepting connections lags its pid by ~1s).
```

The daemon does not re-adopt orphaned processes. Children are killed on daemon
shutdown via context cancellation, so there are no PID files and no stale
process reconciliation. Commands marked `autostart: true` bring the stack back
up when the daemon restarts.

## Log buffers

Fixed-size byte ring per command, default 256 KB, overridable with `size:`.
Writes never block and never fail; the oldest bytes are dropped. Reads are
line-aware: `Tail(n int) []string` scans backward for newlines so a wrapped
buffer never returns a partial line.

Streaming uses `Subscribe() (<-chan []byte, func())`. The channel is buffered
to 64 chunks; a subscriber that falls behind loses chunks rather than applying
backpressure to the running process. The cancel function unsubscribes.

When a command sets `log: true`, an `O_APPEND` file at
`$XDG_STATE_HOME/lazycomd/logs/<name>.log` receives the same bytes through an
`io.MultiWriter`. The `:` in a namespaced command name becomes `__` in the
filename, so `scraper:api` logs to `scraper__api.log`. At 10 MB the file is renamed to `<name>.log.1`, replacing any
previous `.1`. One generation, no external rotation config.

## HTTP API

`net/http` with the standard `ServeMux` (Go 1.22 method and wildcard patterns —
no router dependency).

```
GET    /v1/commands              list: name, state, pid, uptime, exit code, restarts
GET    /v1/commands/{name}       single command, same shape
POST   /v1/commands/{name}/start body: {"with_deps": true}
POST   /v1/commands/{name}/stop
POST   /v1/commands/{name}/restart
GET    /v1/commands/{name}/logs?tail=200        JSON array of lines
GET    /v1/commands/{name}/logs/stream          SSE, live chunks
POST   /v1/reload                re-read config, apply the diff
GET    /v1/healthz
```

JSON in, JSON out. Errors return `{"error": "..."}` with these codes:

| Code | Meaning |
|---|---|
| 400 | malformed body, bad query parameter, or reload of an invalid config |
| 401 | missing or wrong token on the TCP listener |
| 404 | unknown command name |
| 409 | operation illegal in the current state (start while running) |
| 500 | spawn failure or internal error |

Log streaming uses SSE rather than websockets: the traffic is one-way, `curl`
can consume it, and `http.Flusher` is standard library.

`POST /v1/reload` re-reads the global config and every project file. Added
commands appear in state `stopped`. Removed commands are stopped and then
dropped. A changed spec on a currently running command is staged, reported as
`spec_dirty: true`, and takes effect on that command's next start — reload
never silently restarts something. An invalid config file leaves the previously
loaded config in place and returns 400.

Every handler runs behind a `recover()` so a client-triggered panic cannot take
down a running stack.

## Listeners and authentication

The daemon always listens on a unix socket at
`$XDG_STATE_HOME/lazycomd/lazycomd.sock` (default
`~/.local/state/lazycomd/lazycomd.sock`), mode `0600`. File permissions are the
authentication for local clients; requests arriving on the socket skip the
token middleware.

When `listen:` is set, a second listener serves the same mux wrapped in token
middleware. The token is read from `token_file` and compared with
`crypto/subtle.ConstantTimeCompare`. The daemon refuses to start if
`token_file` is unset when `listen:` is set, or if the token file's mode is not
`0600`.

A bare port in `listen:` (`":7777"`) binds to `127.0.0.1` only. Binding to all
interfaces requires writing the address out explicitly (`"0.0.0.0:7777"`) and
logs a warning at startup. Remote exposure is always a deliberate act.

## CLI

Standard library `flag` only — eight subcommands do not need a framework.

```
lazycomd serve                 run the daemon in the foreground
lazycomd ls                    table: name, state, pid, uptime, restarts
lazycomd start <name> [-d]     -d starts dependencies first
lazycomd stop <name>
lazycomd restart <name>
lazycomd logs <name> [-n 200] [-f]
lazycomd reload
lazycomd run <name>            start, stream logs, stop on SIGINT
```

`run` on a command that is already running attaches to its log stream and
leaves it running on SIGINT — it only stops what it started.

When no daemon is running, a client prints
`lazycomd: daemon not running (start with: lazycomd serve)` and exits 1. The
client never autospawns a daemon — a daemon that appears by surprise is a
daemon that cannot be reasoned about.

Address resolution: `$LAZYCOMD_ADDR` if set (`unix:///path/to.sock` or
`http://host:port`), otherwise the default socket path. For a TCP address the
token comes from `$LAZYCOMD_TOKEN`.

Name resolution: a bare name matches a global command; failing that, a unique
`*:<name>` across all projects. An ambiguous bare name prints every candidate
and exits 1. A qualified `project:name` is always exact.

Exit codes: 0 success, 1 client or daemon error, 2 usage error. `logs -f` exits
0 on SIGINT.

## Daemon lifecycle

`serve` runs in the foreground only. Daemonization belongs to launchd or
systemd, not to this code. A `contrib/com.tphuc.lazycomd.plist` and a systemd
unit ship in the repository, documented in the README, installed by the user
rather than by the binary.

Startup sequence: load config, validate, check for dependency cycles, open
listeners, start `autostart` commands with their dependencies, serve.

Shutdown on SIGINT or SIGTERM: stop accepting connections, send SIGTERM to
every process group, wait up to 10 seconds, SIGKILL whatever remains, unlink
the socket, exit. A second signal during the drain escalates immediately to
SIGKILL for all processes.

Single-instance enforcement uses the socket path as the lock. If a socket
exists and answers `GET /v1/healthz`, the daemon prints
`lazycomd: already running` and exits 1. If the socket exists but refuses
connections, it is stale: unlink it and continue.

## Testing

`go test ./...` with the standard `testing` package. No assertion libraries.

| Package | Coverage |
|---|---|
| config | Table test: valid file; unknown field; dependency cycle; duplicate name within a file; missing `cmd`; invalid `restart` value; `~` and `$VAR` expansion; project file carrying a global-only key |
| logbuf | Writing past capacity leaves `Tail` returning whole lines only; a slow subscriber drops chunks and never blocks the writer; disk tee rotation at the size threshold |
| manager | Real processes via `sh -c` fakes: `sleep 10` then stop reaches `stopped`; `exit 1` with `on-failure` restarts and the backoff grows; `sh -c 'sleep 1; exit 0'` with `on-failure` stays `stopped`; manually stopping a crash-looping process does not restart it; `depends_on` start order is recorded; the child tree dies (spawn `sh -c 'sleep 30 & wait'` and assert the grandchild is gone) |
| api | `httptest.Server` over a unix socket: full lifecycle; 404 on an unknown name; 409 on a double start; SSE delivers a chunk; a TCP request without a token returns 401; reload of a broken config returns 400 and keeps the old config |

Backoff intervals and the stop grace period are injectable fields on `Manager`
so tests run in milliseconds rather than tens of seconds.

## Non-goals for this spec

The TUI; the ports and container dashboard; readiness probes of any kind; log
search; re-adopting orphaned processes; PTY allocation (commands get pipes, so
anything requiring a TTY is spec #2's concern alongside the TUI); multi-user
operation; packaging beyond the two unit files in `contrib/`.

## Definition of done

- `lazycomd serve` holds a real stack (proxy, API, database) across crashes and
  restarts without manual intervention.
- `lazycomd ls` and `lazycomd logs -f` are good enough for daily use.
- Every endpoint is drivable with `curl --unix-socket`.
- `go test ./...` passes.
- `README.md` documents the full config schema and every route.
