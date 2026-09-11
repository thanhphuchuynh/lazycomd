# lazycomd

One user-wide daemon that runs, supervises and exposes your long dev
commands — proxies, tunnels, local service stacks — over an HTTP API.

Docs: https://thanhphuchuynh.github.io/lazycomd/

## Install

```bash
curl -sSfL https://github.com/thanhphuchuynh/lazycomd/releases/latest/download/install.sh | sh
```

That installs `~/.local/bin/lazycomd`, starts the daemon, and enables it at
login. With no arguments, `lazycomd` opens the TUI (a TTY is required).
Quitting the TUI does not stop the daemon.
