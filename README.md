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

## The TUI

Run `lazycomd` with no arguments:

```
 lazycomd — 5 commands · 2 running
  NAME        STATE    PID    UPTIME  RS │ tick — following
> tick        running  54405  2m13s    0 │ 1789114081
  greet       stopped  -      -        0 │ 1789114082
  app:api     failed   -      -        3 │ 1789114083
 ? help  q quit  tab switch pane  f follow  / filter  j/k move  s start  S stop
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
| `Esc` | palette | close the palette |
| `d` | anywhere | ports view |
| `?` | anywhere | help overlay |
| `q` `Ctrl-C` | anywhere | quit (the daemon keeps running) |

The filter is a plain substring with smart case: a lowercase query matches
case-insensitively. A state shown as `running*` means the config changed under
a reload and the new spec applies on that command's next start.

Quitting the TUI stops nothing. If the daemon goes away, the TUI shows a
banner and retries every 2s rather than exiting.

With no terminal — `lazycomd | cat`, CI — the bare command prints usage
instead of launching.

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
    port: 3000             # the port this command means to bind
    health: http://localhost:3000/healthz   # probed every 10s while running
```

`health:` is polled every 10s while the command is running: a GET with a 2s
timeout, any 2xx is healthy, redirects are not followed. It is a display
signal only — `depends_on` still uses spawned-plus-200ms readiness.

`port:` declares the port the command means to bind, so the dashboard can tell
you when something else is holding it. With `health:` set and `port:` unset,
the port is taken from the health URL.

A project's `lazycomd.yaml` holds a `commands:` block only. Its commands are
namespaced by the project directory's basename: `scraper:api`. Two project
directories with the same basename is a config error, as is `listen:`,
`token_file:` or `projects:` inside a project file.

A project command's `depends_on` entry resolves inside its own project first,
then against global commands. A project command with no `cwd` defaults to its
project directory.

`~` and `$VAR` expand when a command starts, not when the config loads, so a
restart picks up a changed environment. Write `$$` for a literal dollar sign —
without it, shell constructs like `$!` would be expanded away. The fields
above are the complete set; an unknown field is a config error.

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
ambiguous name lists the candidates and exits 1. Flags may come before or
after the command name: `logs api -f` and `logs -f api` both work.

`lazycomd run` on a command that is already running attaches to its output and
leaves it running on Ctrl-C — it only stops what it started.

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
| `GET /v1/system` | | ports, vitals, health and port conflicts |

A status object: `name`, `state` (`stopped`, `starting`, `running`,
`stopping`, `failed`), `pid`, `uptime_sec`, `exit_code`, `restarts`,
`spec_dirty`, `depends_on`, plus `cpu`, `mem_mb` and `health` when the probe
sampler has measured them.

Errors are `{"error":"..."}` with 400 (malformed request or broken config),
401 (bad token), 404 (unknown command), 409 (illegal in the current state) or
500.

```bash
curl --unix-socket ~/.local/state/lazycomd/lazycomd.sock http://unix/v1/commands
```

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

## Behavior worth knowing

- **Reload never restarts anything.** A changed spec on a running command is
  staged and reported as `spec_dirty`; it takes effect on that command's next
  start.
- **Stopping is not recursive.** Stopping a dependency leaves its dependents
  running.
- **Dependencies are ordered, not probed.** A dependency counts as ready 200ms
  after it spawns. There are no port or HTTP readiness checks.
- **Whole process trees die.** Each command runs in its own process group, and
  stop signals the group, so wrapper commands do not leak children.
- **No orphan adoption.** Commands die with the daemon. Use `autostart: true`
  to bring a stack back up.
- **Logs are memory-first.** The ring buffer holds the last 256 KB per
  command. `log: true` also appends to disk, rotating once at 10 MB, keeping
  one previous generation as `<name>.log.1`.
- **Commands get pipes, not a TTY.** Anything that needs a terminal will
  behave as though piped.
- **A contested port is reported either way.** A command whose port is held by
  something else is flagged whether that command is running or dead — the dead
  case is usually why it died.
- **One daemon at a time.** The socket is the lock: a second `serve` prints
  `already running` and exits 1, while a stale socket is cleared.

## Environment

| Variable | Meaning |
|---|---|
| `LAZYCOMD_ADDR` | `unix:///path/to.sock` or `http://host:port` |
| `LAZYCOMD_TOKEN` | Bearer token for a TCP address |
| `LAZYCOMD_CONFIG` | Override the config path |
| `XDG_CONFIG_HOME`, `XDG_STATE_HOME` | Standard overrides |

## Not in this version

A TUI, a ports and container dashboard, readiness probes, log search, and PTY
allocation. Those are later specs — see
`docs/superpowers/specs/2026-09-11-lazycomd-daemon-design.md`.
