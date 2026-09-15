---
layout: default
title: API
---

Always on `~/.local/state/lazycomd/lazycomd.sock` (mode 0600 — file
permissions are the authentication). Set `listen:` plus `token_file:` for a
TCP listener that requires `Authorization: Bearer <token>`.

| Method and path | Body / query | Returns |
|---|---|---|
| `GET /v1/healthz` | | `{"status":"ok"}` |
| `GET /v1/commands` | | array of status objects |
| `GET /v1/commands/{name}` | | one status object |
| `POST /v1/commands/{name}/start` | `{"with_deps":true,"wait_sec":30}` optional | status |
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

`wait_sec` holds the response until the command is ready — its `health:` URL
answers 2xx, or its `port:` accepts a connection, or, with neither, it is
running. A command that exits while starting fails at once; one that never
becomes ready returns 504 with `{"error":..., "status":{...}}` so the caller
can see the state it reached. The daemon caps the wait at two minutes.

Errors are `{"error":"..."}` with 400 (malformed request or broken config),
401 (bad token), 404 (unknown command), 409 (illegal in the current state) or
500.

```bash
curl --unix-socket ~/.local/state/lazycomd/lazycomd.sock http://unix/v1/commands
```
