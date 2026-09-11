---
layout: default
title: Install
---

```bash
curl -sSfL https://github.com/thanhphuchuynh/lazycomd/releases/latest/download/install.sh | sh
```

That puts `lazycomd` in `~/.local/bin`, starts the daemon now, and enables
it at login (LaunchAgent on macOS, systemd user unit on Linux).

Add `~/.local/bin` to `PATH` if `lazycomd` is not found afterwards.

Pin a release with `LAZYCOMD_VERSION=v0.1.0` in the environment before
running the script.

## From source

```bash
go build -o ~/.local/bin/lazycomd ./cmd/lazycomd
```

The module path is `github.com/thanhphuchuynh/lazycomd`.

`serve` stays in the foreground. Login autostart is launchd or systemd —
copy a unit from `contrib/` or run the curl installer.

```bash
lazycomd serve
```

> A second `serve` prints `already running` and exits 1. A leftover socket
with nothing behind it is cleared.

