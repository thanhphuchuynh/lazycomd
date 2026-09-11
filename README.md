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
 lazycomd                                              3 of 5 running
╭─ 1 Status ─────────────────────────╮╭─ web — following · pid 9663 ──────╮
│● 3 of 5 running                    ││127.0.0.1 - - "GET / HTTP/1.1" 200 │
│…/lazycomd/lazycomd.sock            ││127.0.0.1 - - "GET / HTTP/1.1" 200 │
╰────────────────────────────────────╯│                                   │
┏━ 2 Commands ━━━━━━━━━━━━━━━━━━━━━━━┓│                                   │
┃  H NAME           STATE     MEM    ┃│                                   │
┃    flaky          stopped   -      ┃│                                   │
┃  ○ ghost          running   1M     ┃│                                   │
┃    noisy          running   3M     ┃│                                   │
┃> ● web            running   25M    ┃│                                   │
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛│                                   │
╭─ 3 Ports ────────────────── 4, 1 ⚠ ╮│                                   │
│  3283   ARDAgent                   ││                                   │
│ ⚠8099   Python     web             ││                                   │
│  11434  ollama                     ││                                   │
╰────────────────────────────────────╯╰───────────────────────────────────╯
 j/k move  1-3 panel  tab cycle panels  f follow  / filter  ? help  q quit
```

Three panels in the left column, numbered. The right-hand pane follows
whichever panel has focus: the selected command's logs for **2 Commands**, the
selected listener's detail for **3 Ports**, and daemon and collector state for
**1 Status** — which is where a missing `lsof` or an unreachable daemon
explains itself.

| Key | Where | Action |
|---|---|---|
| `1` `2` `3` | anywhere | focus the Status, Commands or Ports panel |
| `tab` `shift+tab` | anywhere | cycle panels |
| `j` `k` `↓` `↑` | focused panel | move the cursor |
| `g` `G` | focused panel | first / last row |
| `s` | Commands | start, dependencies first |
| `S` | Commands | stop |
| `r` | Commands | restart |
| `p` | anywhere | fuzzy command palette |
| `a` | Commands | add a command, writing it to the config |
| `e` | Commands | edit the selected command |
| `d` | Commands | delete it, after confirming which file changes |
| `ctrl+d` `ctrl+u` | anywhere | scroll the main pane |
| `f` | anywhere | toggle log follow |
| `/` | anywhere | filter the log pane |
| `esc` | anywhere | clear the log filter |
| `?` | anywhere | help overlay |
| `q` `ctrl+c` | anywhere | quit (the daemon keeps running) |

Lifecycle keys act on the Commands panel only; pressed elsewhere they say so
rather than acting on something you cannot see.

The sidebar shows memory from about 34 columns and adds CPU and restart counts
past 46. A command's pid rides in the main pane's title, since a sidebar has no
column to spare for it. Below 90 columns the two columns become two rows: the
focused panel keeps its height, the others collapse to their title bars, and
the main pane sits underneath — nothing becomes unreachable.

The filter is a plain substring with smart case: a lowercase query matches
case-insensitively. A state shown as `running*` means the config changed under
a reload and the new spec applies on that command's next start.

Quitting the TUI stops nothing. If the daemon goes away, the header says so and
the TUI retries every 2s rather than exiting.

With no terminal — `lazycomd | cat`, CI — the bare command prints usage
instead of launching.

### Editing the config from the TUI

`a` opens a four-field form and writes the result into your config:

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

FOLDER is `cwd:` in the file — where the command runs. It opens filled with
the directory you launched the TUI in, and follows the project once the name
is namespaced, as in `app:api`.

The command line splits on spaces. A line containing `|`, `>`, `<`, `&`, `;`,
`$` or `*` goes to `sh -c` whole instead, and the form says so while you type.

`e` edits the selected command. It reads the command's full spec first, so
fields the form never shows — `env`, `depends_on`, `health`, `port` — survive
the edit untouched. Renaming is not offered: that is a file edit.

`d` deletes, after a prompt naming the file that will change.

Only the lines of the command being touched are rewritten. Comments, blank
lines and quoting everywhere else come out byte-identical, so `git diff` shows
the one command you changed. If the file changed on disk since the daemon read
it — because you have it open in an editor — the write is refused and says so
rather than overwriting you.

### Dashboard

The Ports panel lists every listener with the lazycomd command that owns it,
marking the contested ones. Focus it with `3` and the main pane shows the
selected port in full:

```
 port 8099
   address   *
   process   Python
   pid       9663
   owner     web

 conflict
   ⚠ 8099 wanted by rival — held by Python (pid 9663)
```

Ownership is by process group, so a command that listens from a grandchild —
`sh -c "npm start"` — is still credited correctly.

The Commands panel carries a health dot: `●` up, `○` down, blank for a command
with no `health:` URL. CPU is percent of one core, summed across the command's
whole process tree, so a busy multithreaded process legitimately reads above
100%.

Ports come from `lsof`, falling back to `ss`. On a machine with neither, the
panel says so, panel 1 explains why, and the other signals keep working.

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
| `GET /v1/projects` | | registered project basenames to directories |
| `GET /v1/commands/{name}/config` | | the command's full spec |
| `POST /v1/commands` | a command plus `name` | 201 and its status |
| `PUT /v1/commands/{name}` | a command | 200 and its status |
| `DELETE /v1/commands/{name}` | | 204 |

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
