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
- **Whole process trees die.** Each command runs in its own process group, and
  stop signals the group, so wrapper commands do not leak children.
- **No orphan adoption.** Commands die with the daemon. Use `autostart: true`
  to bring a stack back up.
- **Logs are memory-first.** The ring buffer holds the last 256 KB per
  command. `log: true` also appends to disk, rotating once at 10 MB, keeping
  one previous generation as `<name>.log.1`.
- **Commands get pipes, not a TTY.** Anything that needs a terminal will
  behave as though piped.
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
