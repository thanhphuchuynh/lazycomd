# lazycomd

One user-wide daemon that runs, supervises and exposes your long dev
commands — proxies, tunnels, local service stacks — over an HTTP API.

Docs: https://thanhphuchuynh.github.io/lazycomd/

## Install

```bash
go build -o ~/.local/bin/lazycomd ./cmd/lazycomd
```

Run the daemon in the foreground, or install one of the unit files in
`contrib/`:

```bash
lazycomd serve
```

With no arguments, `lazycomd` opens the TUI (a TTY is required). Quitting
the TUI does not stop the daemon.
