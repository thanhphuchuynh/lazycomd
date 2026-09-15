---
layout: default
title: CLI
---

| Command | What it does |
|---|---|
| `lazycomd serve` | Run the daemon in the foreground |
| `lazycomd ls` | Name, state, pid, uptime, restarts |
| `lazycomd start <name> [-d] [--wait 30s]` | Start; `-d` starts dependencies first, `--wait` blocks until ready |
| `lazycomd stop <name>` | SIGTERM the process group, SIGKILL after 10s |
| `lazycomd restart <name>` | Stop then start |
| `lazycomd logs <name> [-n N] [-f]` | Show or follow output |
| `lazycomd reload` | Re-read the config and apply the diff |
| `lazycomd run <name>` | Start with dependencies, follow output, stop on Ctrl-C |
| `lazycomd port [N]` | Who is listening, and which command owns it |
| `lazycomd doctor` | Check the catalog for what will fail to start |
| `lazycomd mcp` | Serve the Model Context Protocol on stdio — see [Agents]({{ '/agents/' | relative_url }}) |
| `lazycomd version` | Print the build version |

A bare name matches a global command, then a unique `project:name`. An
ambiguous name lists the candidates and exits 1. Flags may come before or
after the command name: `logs api -f` and `logs -f api` both work.

`lazycomd run` on a command that is already running attaches to its output and
leaves it running on Ctrl-C — it only stops what it started.

Exit codes: 0 success, 1 daemon or client error, 2 usage error.

## Start and wait for it

`start` returns as soon as the process spawns, which is too early to run
anything against it. `--wait` holds until the command is actually ready:

```bash
lazycomd start app:api --wait 30s && npm test
```

Ready means its `health:` URL answers 2xx; with no health URL, that its
`port:` accepts a connection; with neither, that it is running. A command that
exits while starting fails immediately rather than at the deadline, and the
daemon caps the wait at two minutes.

The daemon does the waiting, not the CLI — the health URL and the port are on
the daemon's machine, which is not always yours.

## Who owns this port

```bash
$ lazycomd port 3000
PORT  ADDR       PID    PROCESS  COMMAND
3000  127.0.0.1  41022  node     app:web
```

The `COMMAND` column is the lazycomd command that owns the listener, by
process group, or `-` for anything else on the machine. With no port, it lists
every listener.

## What is about to break

```bash
$ lazycomd doctor
error port 8080 is claimed by [api web]
error worker: cwd does not exist: /Users/me/old-checkout
warn  project "app" points at a folder that is gone: ~/coding/app
```

It reports the mistakes that only ever show up as a process that will not
start: two commands claiming one port, a `depends_on` naming nothing, a `cwd`
that moved, a project folder that is gone. An `error` exits 1 so a script can
gate on it; a `warn` does not.

## JSON

`ls`, `start`, `stop`, `restart`, `logs`, `port` and `doctor` all take
`--json`, so the CLI is usable from something other than a person:

```bash
$ lazycomd ls --json | jq -r '.[] | select(.state=="failed") | .name'
app:worker

$ lazycomd doctor --json | jq '[.[] | select(.level=="error")] | length'
2
```

Empty results are `[]`, never `null`. `logs --json` returns
`{"name":..., "lines":[...]}`; following and JSON do not mix, so `--json`
wins and returns the buffer as it stands.
