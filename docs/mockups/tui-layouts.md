# lazycomd TUI layout mockups

Local scratch. Not published.

## A — lazygit classic: three stacked panels, always visible, numbered

```
╭─ 1 Status ─────────────────────╮╭─ noisy — following ────────────────────────────────────────────────╮
│lazycomd ● 3 of 5 running       ││line 412 level=2                                                    │
│socket ~/.local/state/lazycomd  ││line 413 level=3                                                    │
╰────────────────────────────────╯│line 414 level=4                                                    │
┏━ 2 Commands ━━━━━━━━━━━━━━━━━━━┓│line 415 level=0                                                    │
┃● web      running   24M  :8099 ┃│line 416 level=1                                                    │
┃  noisy    running  1.1M        ┃│line 417 level=2                                                    │
┃  rival    failed         :8099 ┃│line 418 level=3                                                    │
┃○ ghost    running  0.9M        ┃│line 419 level=4                                                    │
┃  flaky    failed    ×3         ┃│line 420 level=0                                                    │
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛│line 421 level=1                                                    │
╭─ 3 Ports ──────────────────────╮│line 422 level=2                                                    │
│⚠ 8099  Python     web          ││line 423 level=3                                                    │
│  5432  postgres                ││line 424 level=4                                                    │
│  7777  lazycomd   daemon       ││line 425 level=0                                                    │
│  5000  ControlCe               ││line 426 level=1                                                    │
╰────────────────────────────────╯╰────────────────────────────────────────────────────────────────────╯
 1-3 panel  s start  S stop  r restart  f follow  / filter  ? help  q quit
```

## B — accordion: the focused panel expands, the others collapse to one line

```
╭─ 1 Status    ● 3 of 5 running ─╮╭─ noisy — following ────────────────────────────────────────────────╮
┏━ 2 Commands ━━━━━━━━━━━━━━━━━━━┓│line 412 level=2                                                    │
┃● web      running   24M  :8099 ┃│line 413 level=3                                                    │
┃  noisy    running  1.1M        ┃│line 414 level=4                                                    │
┃  rival    failed         :8099 ┃│line 415 level=0                                                    │
┃○ ghost    running  0.9M        ┃│line 416 level=1                                                    │
┃  flaky    failed    ×3         ┃│line 417 level=2                                                    │
┃                                ┃│line 418 level=3                                                    │
┃  the focused panel takes the   ┃│line 419 level=4                                                    │
┃  room the others give up       ┃│line 420 level=0                                                    │
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛│line 421 level=1                                                    │
╭─ 3 Ports     4 listening, 1 ⚠ ─╮│line 422 level=2                                                    │
╭─ 4 Health    1 up, 1 down ─────╮│line 423 level=3                                                    │
╰────────────────────────────────╯│line 424 level=4                                                    │
                                  │line 425 level=0                                                    │
                                  │line 426 level=1                                                    │
                                  ╰────────────────────────────────────────────────────────────────────╯
 1-4 panel  tab cycles  s start  S stop  f follow  / filter  ? help  q quit
```

## C — tabbed left column: one panel, three tabs

```
┏━ Commands ▸ Ports ▸ Health ━━━━━━━━━━┓╭─ noisy — following ──────────────────────────────────────────╮
┃                                      ┃│line 412 level=2                                              │
┃ NAME      STATE     CPU    MEM       ┃│line 413 level=3                                              │
┃● web      running     2%    24M      ┃│line 414 level=4                                              │
┃▸ noisy    running    11%   1.1M      ┃│line 415 level=0                                              │
┃  rival    failed       -      -      ┃│line 416 level=1                                              │
┃○ ghost    running     0%   0.9M      ┃│line 417 level=2                                              │
┃  flaky    failed       -      -      ┃│line 418 level=3                                              │
┃                                      ┃│line 419 level=4                                              │
┃ ⚠ 8099 wanted by rival               ┃│line 420 level=0                                              │
┃   held by Python (pid 89179)         ┃│line 421 level=1                                              │
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛│line 422 level=2                                              │
                                        │line 423 level=3                                              │
                                        │line 424 level=4                                              │
                                        ╰──────────────────────────────────────────────────────────────╯
 1-3 tab  s start  S stop  r restart  f follow  / filter  ? help  q quit
```
