---
layout: default
title: TUI
---

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
| `P` | Commands | act on the whole project: `P` then `s`, `S` or `r` |
| `/` `p` | anywhere | search box: commands, listening ports and the buffered log |
| `i` | Commands | detail for the selected command: spec, state, ports, health |
| `o` | Commands | open the full log, which is where the log filter lives |
| `a` | Commands | add a command, writing it to the config |
| `e` | Commands | edit the selected command |
| `d` | Commands | delete it, after confirming which file changes |
| `ctrl+d` `ctrl+u` | anywhere | scroll the main pane |
| `f` | anywhere | toggle log follow |
| `/` | log view | filter the lines, `esc` clears it |
| `esc` | anywhere | close the search box, the log view or the detail pane |
| `?` | anywhere | help overlay |
| `q` `ctrl+c` | anywhere | quit (the daemon keeps running) |

The Ports panel is view only: it lists every listener on the machine, not
just lazycomd's, and start and stop act on commands in the Commands panel.

The mouse works as well: clicking a panel focuses it, clicking a row selects
it, clicking a match in the search box goes to it, and the wheel scrolls
whatever is under the pointer.

Enter in the search box goes to the match and nothing else: a command is
selected, a port is selected in the Ports panel, and a log line opens the
full log view with your query already applied as its filter. The pane beside
the panels is a preview — it follows the selection and scrolls, and the
filter lives in the full view where there is room to read the result.

Lifecycle keys act on the Commands panel only; pressed elsewhere they say so
rather than acting on something you cannot see.

`P` acts on a whole project instead of one command: it names the selected
command's project and waits for a verb, and `s`, `S` or `r` then applies to
every command in it. Any other key cancels, so a stray `s` two moves later
still acts on one command. Start pulls dependencies in per command, so a
project comes up in the order its `depends_on` describes.

The sidebar shows memory from about 34 columns and adds CPU and restart counts
past 46. A command's pid rides in the main pane's title, since a sidebar has no
column to spare for it. Below 90 columns the two columns become two rows: the
focused panel keeps its height, the others collapse to their title bars, and
the main pane sits underneath — nothing becomes unreachable.

The filter is a plain substring with smart case: a lowercase query matches
case-insensitively. A state shown as `running*` means the config changed under
a reload and the new spec applies on that command's next start.

Quitting the TUI stops nothing. If the daemon is down when you open the TUI
(unix socket only), it asks once: `y` starts it (user service if installed,
otherwise a background `serve`), `n` or `esc` skips. After that it retries
every 2s rather than exiting. A TCP `LAZYCOMD_ADDR` never offers to start
a local daemon.

With no terminal — `lazycomd | cat`, CI — the bare command prints usage
instead of launching.

## Editing the config from the TUI

`a` opens a form, centred over the panels, and writes the result into your
config:

```
┏━ New command ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
┃  NAME      app:api                                         ┃
┃› COMMAND   go run .                                        ┃
┃  FOLDER    ~/coding/app                                    ┃
┃            ▸ app                                           ┃
┃              app-worker                                    ┃
┃  ENV       LOG=debug PGPASSWORD=hunter2                    ┃
┃  PORT      8080                                            ┃
┃  HEALTH    http://localhost:8080/healthz                   ┃
┃  RESTART   no · on-failure · always                        ┃
┃  AUTOSTART yes · no                                        ┃
┃                                                            ┃
┃  ↑↓ pick · tab completes                                   ┃
┃  tab next · enter save · esc cancel                        ┃
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
```

FOLDER is `cwd:` in the file — where the command runs. It opens filled with
the directory you launched the TUI in, follows the project once the name is
namespaced (`app:api`), and completes real directories as you type: `tab`
finishes the path, the dropdown lists the matches, `↑↓` pick one.

COMMAND wraps rather than scrolling sideways, so a long line is readable in
full. It splits on spaces; a line containing `|`, `>`, `<`, `&`, `;`, `$` or
`*` goes to `sh -c` whole instead, and the form says so while you type.

ENV is one line of `KEY=VALUE` pairs. A value holding a space cannot survive
that shape, so a command with one keeps its env and the field goes read-only
rather than writing back half of it.

RESTART and AUTOSTART show every choice with the current one marked: `←` `→`
or space changes it.

The form checks its own work before saving — the port parses, the env is well
formed, the health URL has an http scheme — so a typo is caught in the field
rather than by the daemon's reparse. A failing write shows the daemon's whole
error, wrapped over as many lines as it needs.

`e` edits the selected command. It reads the command's full spec first, so
fields the form still does not show — `depends_on`, `size`, `log` — survive
the edit untouched. Renaming is not offered: that is a file edit.

`d` deletes, after a prompt naming the file that will change.

Only the lines of the command being touched are rewritten. Comments, blank
lines and quoting everywhere else come out byte-identical, so `git diff` shows
the one command you changed. If the file changed on disk since the daemon read
it — because you have it open in an editor — the write is refused and says so
rather than overwriting you.

## Dashboard

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
