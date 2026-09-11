# lazycomd — Dashboard Design (Spec #3)

Date: 2026-09-11
Status: Approved for planning
Scope: Spec #3 of 3. Machine awareness: listening ports, per-command vitals,
HTTP health, and port conflicts.

## Problem

Specs #1 and #2 answer "what are my commands doing". They cannot answer the
questions that actually stall a morning:

- What is already on port 3000, and is it mine?
- Is this command eating a core, or idle?
- The process is up — is the service behind it actually answering?
- `npm start` just died. Was the port already taken?

Each of those currently means leaving the tool: `lsof -i :3000`, `ps aux |
grep`, `curl localhost:3000/healthz`. This spec brings the three signals that
answer them into the daemon, and surfaces them where they are needed.

## Relationship to the other specs

- Spec #1 (shipped) — daemon, process manager, HTTP API, CLI.
- Spec #2 (shipped) — the TUI over that API.
- **Spec #3 (this document) — the dashboard signals.**

This spec adds a collector package, one endpoint, three fields on the command
view, two optional config fields, and a TUI view. It changes no process
lifecycle behavior.

Deliberately excluded after discussion: Docker and any container runtime. The
three signals here need no client library; Docker would.

## Approach

One `internal/probe` package holding three collectors, each on its own ticker,
writing into a snapshot the API serves. Ports come from `lsof` (falling back
to `ss`), vitals from `ps`, health from an HTTP GET.

Two alternatives were rejected:

- **Sample on demand per request.** No goroutines and no cache, but `lsof`
  takes 100-300ms and the TUI polls every second: a permanent subprocess
  treadmill, with the request blocked behind it. Ticker-based sampling makes
  the cost independent of how many clients are watching.
- **Fold sampling into the manager.** No new endpoint, since vitals would ride
  on `Process`. Rejected because the manager's one job is process lifecycle,
  and putting subprocess calls under its mutex is the same shape that produced
  spec #1's Stop-blocking defect.

Shelling out to `lsof` and `ps` was chosen over `gopsutil` and over native
per-platform code. `gopsutil` is a large dependency in the one part of the
repo that has stayed `yaml.v3`-only; native means `/proc` on Linux and cgo
`libproc` on macOS, two implementations to maintain. Parsing two well-known
text formats is about 120 lines and keeps the daemon's dependency tree as it
is.

## Package structure

```
internal/probe/probe.go     Sampler, Snapshot, the three sampling loops
internal/probe/ports.go     lsof/ss invocation and parsers
internal/probe/vitals.go    ps invocation and parser
internal/probe/health.go    HTTP prober
internal/probe/conflict.go  intended ports vs. reality
internal/api/system.go      GET /v1/system, and the merge into /v1/commands
internal/tui/system.go      the ports view
internal/tui/table.go       (modify) health, CPU and MEM columns
internal/config/config.go   (modify) the health: and port: fields
cmd/lazycomd/serve.go       (modify) build, start and drain the sampler
```

`internal/probe` imports neither `manager` nor `api`. It receives what it
needs through closures, so there is no cycle and no coupling:

```go
type Sampler struct {
    pids     func() map[string]int    // command name -> pid, running only
    urls     func() map[string]string // command name -> health URL, running only
    intended func() map[string]int    // command name -> intended port, all commands
    run      func(ctx context.Context, name string, args ...string) ([]byte, error)
    httpc    *http.Client
    // snapshot guarded by a mutex
}

func New(pids func() map[string]int, urls func() map[string]string, intended func() map[string]int) *Sampler
func (s *Sampler) Start(ctx context.Context) // three goroutines, one per signal
func (s *Sampler) Snapshot() Snapshot        // a copy, safe to serialize
```

`run` is injectable, so every parser test feeds captured real command output
and never spawns a subprocess.

## The snapshot

```go
type Snapshot struct {
    Ports     []Port               `json:"ports"`
    Vitals    map[string]Vital     `json:"vitals"`  // keyed by command name
    Health    map[string]Health    `json:"health"`
    Conflicts []Conflict           `json:"conflicts,omitempty"`
    SampledAt map[string]time.Time `json:"sampled_at"`      // "ports", "vitals", "health"
    Errors    map[string]string    `json:"errors,omitempty"` // same keys
}

type Port struct {
    Addr    string `json:"addr"`              // 127.0.0.1, *, ::1
    Port    int    `json:"port"`
    PID     int    `json:"pid"`
    Process string `json:"process"`
    Command string `json:"command,omitempty"` // the lazycomd command that owns it
}

type Vital struct {
    PID   int     `json:"pid"`
    CPU   float64 `json:"cpu"`     // percent of one core; may exceed 100
    MemMB float64 `json:"mem_mb"`
}

type Health struct {
    URL       string  `json:"url"`
    OK        bool    `json:"ok"`
    Status    int     `json:"status,omitempty"`
    LatencyMS float64 `json:"latency_ms,omitempty"`
    Error     string  `json:"error,omitempty"`
}
```

`Errors` shares its keys with `SampledAt`, so a missing `lsof` reads as
`errors.ports = "exec: \"lsof\": executable file not found in $PATH"` while
vitals and health keep sampling. One dead signal never takes the others down.

Intervals: ports 5s, vitals 2s, health 10s. Each loop does nothing when there
is nothing to do — no running commands means no `ps` call, no configured
health URLs means no HTTP.

## Ports

`lsof -nP -iTCP -sTCP:LISTEN` on both platforms, falling back to `ss -ltnpH`
when `lsof` is absent, which is common on minimal Linux images. With neither
present, `errors.ports` says so and the view explains itself rather than
showing an empty list that reads as "nothing is listening".

```go
func parseLsof(out []byte) []Port
func parseSS(out []byte) []Port
```

Both tools emit an IPv4 and an IPv6 row for one wildcard listener. Addresses
are first normalized — `0.0.0.0`, `::` and `*` all become `*` — and rows are
then deduplicated on `(addr, port, pid)`. That collapses the v4/v6 pair of a
single wildcard bind into one row, while keeping a genuine double bind such as
`127.0.0.1:3000` alongside `*:3000` visible as two. Results are sorted by port,
then by address.

A nonzero exit from `lsof` is not treated as failure: it exits 1 when it
simply has nothing to report. An empty parse with output present records an
error; an empty parse with no output is an empty list.

### Ownership is by process group

A command like `sh -c "npm start"` listens from a grandchild, so matching a
listening PID against the command's PID would almost never hit. Spec #1 puts
every command in its own process group with `pgid == the command's PID`, so
ownership is: resolve the listening PID's pgid, and if it equals a running
command's PID, that command owns the port.

```go
// ownerOf maps a listening PID to the lazycomd command whose process group
// contains it, or "" when nothing of ours owns it.
func ownerOf(pid int, groups map[int]int, pids map[string]int) string
```

The pgid map comes free from the vitals collector, which already reads the
whole process table. The vitals loop publishes `map[pid]pgid`; the ports loop
reads it. Before the first vitals sample lands, ports render with an empty
owner column rather than blocking.

## Vitals

One `ps -eo pid=,pgid=,%cpu=,rss=` every 2s, skipped entirely when no command
is running.

```go
func parsePS(out []byte) (rows []psRow, groups map[int]int)
```

A command's vitals are the **sum over its process group**, not its direct PID:
`sh -c "npm start"` burns its CPU in the grandchild, and the whole tree shares
the command's pgid. So the number shown is the one you care about.

`rss` is KB on both platforms, so `MemMB = rss / 1024`. `%cpu` is percent of a
single core on both, so a busy multithreaded process legitimately reads above
100; the column shows `142%` rather than clamping, because clamping would hide
the interesting case.

## Health

A new config field, one line per command:

```yaml
commands:
  api:
    cmd: ["node", "server.js"]
    health: http://localhost:3000/healthz
```

Validated at load: it must parse, and its scheme must be `http` or `https`.
Anything else rejects the file the way every bad field does. No `$VAR`
expansion — unlike `cmd` and `cwd`, a health URL is a literal, and a `$` in one
is far likelier a mistake than an intention.

Probing: every 10s, only for commands currently `running`, `GET` with a 2s
timeout, any 2xx is up. Redirects are not followed, so a 302 to a login page
reads as down rather than up. When a command stops, its health entry is
dropped rather than kept, so the dashboard never shows a stale green dot next
to a dead process.

`Error` carries the transport failure verbatim — `dial tcp 127.0.0.1:3000:
connect: connection refused` — because that message is the whole diagnostic
value.

**Health changes nothing about startup.** `depends_on` keeps spec #1's
spawned-plus-200ms readiness. Making health gate dependency starts is a
deliberate follow-up with its own failure modes (a bad URL blocking an entire
stack, and the start timeout that would then be required), not a side effect
of adding a dashboard.

## Port conflicts

A second optional config field declares intent:

```yaml
commands:
  web:
    cmd: ["npm", "start"]
    port: 3000                        # what this command intends to bind
    health: http://localhost:3000/healthz
```

`port:` is validated as 1-65535. When it is absent but `health:` is set, the
intended port is inferred from the health URL, so most commands get this for
free; `port:` exists for the ones that speak no HTTP.

Every sample, intended ports are compared against what is actually bound:

```go
type Conflict struct {
    Port    int    `json:"port"`
    Command string `json:"command"`            // the command that wants it
    State   string `json:"state"`              // "taken" | "free"
    HeldBy  string `json:"held_by,omitempty"`  // process name of the squatter
    PID     int    `json:"pid,omitempty"`
}
```

Three outcomes per command with an intended port:

| Situation | Result |
|---|---|
| bound by a PID inside that command's process group | healthy, no entry |
| bound by anything else | `taken`, naming the squatter |
| bound by nothing | `free` |

`taken` is reported for **stopped commands too**, because that is precisely
when it matters: `npm start` just died, and the reason is that something else
already holds 3000. `free` is reported only for a `running` command, where
"running but nothing listening" means still booting or quietly broken; for a
stopped command it is simply normal.

Conflicts are computed inside `internal/probe` from data it already holds —
the ports list, the pgid map, and the intended-port closure — so the API and
the TUI only render them.

**Conflicts are not computed at all when the ports sample is missing or
errored.** Without a port list every intended port would look `free`, which
would raise a false alarm about every command precisely when the collector is
the broken thing. In that state `conflicts` is empty and `errors.ports`
already explains why.

## API

```
GET /v1/system → the Snapshot, always 200
```

Always 200 because partial failure is the normal case: `lsof` missing while
health probing works is a real state, expressed inside the body via `errors`,
not by a status code. A client that cannot reach the daemon learns that from
the transport already.

`GET /v1/commands` and `GET /v1/commands/{name}` gain three fields:

```json
{"name":"web","state":"running","pid":54405,
 "cpu":142.3,"mem_mb":318.4,
 "health":{"url":"http://localhost:3000/healthz","ok":true,"status":200,"latency_ms":4.1}}
```

They live on `manager.Status` as `omitempty` additions, with a comment stating
plainly that **the API layer fills them from the sampler and the manager never
sets them**. The alternative — a separate wire type in its own package, or a
duplicated view struct in both `api` and `client` — buys a cleaner boundary in
exchange for a new package and signature churn across `client` and `tui`.
Three optional fields plus a comment is the smaller, less drift-prone trade.
A second manager consumer that must not see them is the moment to split.

Three small additions to the manager, all trivial reads under its existing
mutex:

```go
func (m *Manager) RunningPIDs() map[string]int    // name -> pid, running only
func (m *Manager) HealthURLs() map[string]string  // name -> health URL, running only
func (m *Manager) IntendedPorts() map[string]int  // name -> port: or the health URL's port
```

These are exactly the three closures `probe.New` takes. Because the sampler
calls them every tick rather than capturing once, a `POST /v1/reload` that
adds, removes or re-points a URL or port is picked up on the next sample with
no extra wiring.

`cmd/lazycomd/serve.go` builds the sampler after the manager, starts it with a
context cancelled during the shutdown drain, and hands it to `api.NewServer`,
so `serve` still exits with nothing left running.

## TUI

### Table columns

| Terminal width | Columns |
|---|---|
| ≥ 110 | `H NAME STATE PID UPTIME RS CPU MEM` |
| 80-109 | `H NAME STATE PID UPTIME RS` |
| 60-79 | `H NAME STATE` |
| < 60 | same, log pane hidden |

`H` is a 2-wide health column holding `●` (up), `○` (down) or blank, shown only
when at least one command has a `health:` URL — a config without health checks
gets no mystery column. `CPU` renders `142%`, `MEM` renders `318M`.

### Ports view

`d` swaps the body for the ports list; `d` or `Esc` returns. `j`/`k` and
`Ctrl-D`/`Ctrl-U` scroll it.

```
 ⚠ 3000 wanted by web — held by node (pid 51192)

 PORT   ADDR       PID     PROCESS      OWNER
 3000 ⚠ 127.0.0.1  51192   node
 5432   *          1183    postgres
 7777   127.0.0.1  54405   lazycomd     (daemon)
```

Sorted by port. `OWNER` names the lazycomd command whose process group holds
the socket and is blank for everything else. Conflicts lead the view as
summary lines and mark their rows.

### Polling, staleness, errors

The TUI adds a second, slower tick: `GET /v1/system` every 3s, independent of
the 1s command poll, because the underlying samples move every 2-10s. CPU,
memory and health need no extra fetch — they ride along on `/v1/commands`.

The ports view titles itself `ports — sampled 2s ago`, and past 15s says
`stale 18s`, so a wedged collector looks wedged rather than looking like an
empty machine. A collector error renders as its own line: `ports unavailable:
exec: "lsof": executable file not found in $PATH`. While the daemon is
unreachable the last snapshot stays on screen, dimmed, under spec #2's
existing banner.

## Testing

| Unit | Cases |
|---|---|
| `probe/ports` | parse captured `lsof` output (a macOS and a Linux sample); parse `ss` output; IPv4/IPv6 dedupe while a genuine double bind stays two rows; sort by port; ownership resolved through the pgid map; missing binary becomes `errors.ports`; `lsof` exit 1 with no output is an empty list, not an error |
| `probe/vitals` | parse `ps` output; per-group summing across parent and grandchild; `rss` KB to MB; no running commands means no `ps` invocation |
| `probe/health` | `httptest`: 200 up with latency; 500 down; timeout down; 302 not followed so down; entry dropped when the command stops |
| `probe/conflict` | own group holding the port yields nothing; a foreign PID yields `taken` with the right owner and PID; a running command with nothing bound yields `free`; a stopped command with nothing bound yields nothing; the intended port is inferred from the health URL when `port:` is unset |
| `probe` sampler | injected `run` and intervals: each loop writes its own section; one failing collector leaves the others sampling; `Snapshot()` returns a copy, checked under `-race` |
| `config` | `health:` accepted, rejected for a bad scheme or an unparseable URL; `port:` accepted, rejected outside 1-65535 |
| `manager` | `RunningPIDs`, `HealthURLs` and `IntendedPorts` return the right subsets |
| `api` | `GET /v1/system` shape including a partial-error body; `/v1/commands` carries merged `cpu`, `mem_mb` and `health` |
| `tui` | CPU and MEM appear at width 110 and vanish at 100; the `H` column appears only when a health URL is configured; `d` toggles the ports view and `Esc` returns; ports rows, owner column, conflict summary, error line and stale marker all render |
| integration | against a real daemon with real `lsof`/`ps`: a listener the test opens appears in `/v1/system`, and a running command reports `mem_mb > 0`. Skipped when neither `lsof` nor `ss` exists |

## Non-goals

Docker or any container runtime; disk and network I/O; history, graphs or
sparklines; alerting; killing processes the daemon does not own; health gating
`depends_on`; and aggregating more than one machine.

## Definition of done

- `d` answers "what is on 3000", including which of your commands owns it.
- A command whose port is held by something else says so, by name and PID,
  whether that command is running or dead.
- CPU and memory track a busy command, summed across its process tree.
- A health dot flips within 10s of the service behind it dying.
- A machine without `lsof` degrades to an explained empty pane while vitals and
  health keep working.
- `go test -race ./...` passes on a machine with `lsof` and on one without.
- `README.md` documents `health:`, `port:`, and the ports view.
