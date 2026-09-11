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
