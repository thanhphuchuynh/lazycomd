---
layout: default
title: lazycomd
---

One daemon for every long-running command. Quit the TUI, nothing dies.
Ports tell you who owns 3000.

![lazycomd TUI]({{ '/demo.gif' | relative_url }})

```bash
curl -sSfL https://github.com/thanhphuchuynh/lazycomd/releases/latest/download/install.sh | sh
```

That installs the binary, starts the daemon, and enables it at login.
`lazycomd` with no args opens the TUI. `q` leaves everything running.

```yaml
commands:
  tunnel:
    cmd: ["cloudflared", "tunnel", "--url", "localhost:3000"]
    autostart: true
    restart: on-failure
    port: 3000
projects:
  - ~/coding/app
```

| | lazycomd | mprocs | process-compose | overmind |
|---|---|---|---|---|
| Quit the UI, processes stay | yes | no | only if you ran headless | with `-D` |
| One catalog for every project | yes | per folder | per folder | per Procfile |
| HTTP API | yes | no | yes | no |
| Who owns this port? | yes | no | no | no |

Source: [github.com/thanhphuchuynh/lazycomd](https://github.com/thanhphuchuynh/lazycomd).
