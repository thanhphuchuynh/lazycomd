#!/usr/bin/env python3
"""Render a short terminal GIF that matches the lazycomd TUI layout."""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

W, H = 1080, 560
BG = (24, 24, 37)
FG = (205, 214, 244)
DIM = (108, 112, 134)
GREEN = (166, 227, 161)
RED = (243, 139, 168)
PEACH = (250, 179, 135)
MAUVE = (203, 166, 247)
SURFACE = (49, 50, 68)
FONT_PATH = "/System/Library/Fonts/Menlo.ttc"
OUT = Path(__file__).with_name("demo.gif")

font = ImageFont.truetype(FONT_PATH, 14)


def draw_frame(lines):
    im = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(im)
    d.rectangle((8, 8, W - 9, H - 9), outline=SURFACE, width=2)
    y = 20
    for line in lines:
        if isinstance(line, tuple):
            text, color = line
        else:
            text, color = line, FG
        d.text((20, y), text, font=font, fill=color)
        y += 18
    return im


header_on = (" lazycomd                                              2 of 3 running", GREEN)
header_off = (" lazycomd                                              1 of 3 running", PEACH)

status = [
    "╭─ 1 Status ─────────────────────────╮",
    ("│● 2 of 3 running                    │", GREEN),
    "│ unix://…/lazycomd.sock             │",
    "╰────────────────────────────────────╯",
]
status_one = [
    "╭─ 1 Status ─────────────────────────╮",
    ("│● 1 of 3 running                    │", PEACH),
    "│ unix://…/lazycomd.sock             │",
    "╰────────────────────────────────────╯",
]
cmds_before = [
    "┏━ 2 Commands ━━━━━━━━━━━━━━━━━━━━━━━┓",
    "┃  H NAME           STATE     MEM    ┃",
    "┃    tunnel         stopped   -      ┃",
    ("┃>   worker         stopped   -      ┃", MAUVE),
    ("┃  ● web            running   8M     ┃", GREEN),
    "┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛",
]
cmds_after = [
    "┏━ 2 Commands ━━━━━━━━━━━━━━━━━━━━━━━┓",
    "┃  H NAME           STATE     MEM    ┃",
    "┃    tunnel         stopped   -      ┃",
    ("┃> ● worker         running   1M     ┃", GREEN),
    ("┃  ● web            running   8M     ┃", GREEN),
    "┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛",
]
ports = [
    "╭─ 3 Ports ────────────────── 2, 0 ⚠ ╮",
    "│  18099  Python     web             │",
    "╰────────────────────────────────────╯",
]
keys = (" j/k move  1-3 panel  s start  3 ports  q quit", DIM)

follow_web = [
    "╭─ web — following · pid 2031 ──────────╮",
    "│ Serving HTTP on 0.0.0.0 port 18099    │",
    "│ …                                     │",
    "╰───────────────────────────────────────╯",
]
follow_worker = [
    "╭─ worker — following · pid 4402 ───────╮",
    "│ (no output yet)                       │",
    "│                                       │",
    "╰───────────────────────────────────────╯",
]
follow_port = [
    "╭─ port 18099 ──────────────────────────╮",
    "│  address   *                          │",
    "│  process   Python                     │",
    "│  owner     web                        │",
    "╰───────────────────────────────────────╯",
]


def tui(header, status_block, cmds, follow):
    lines = [header, ""]
    # interleave left column conceptually as sequential lines
    left = status_block + cmds + ports
    # pad follow to left height
    f = follow + [""] * max(0, len(left) - len(follow))
    for a, b in zip(left, f):
        a_t, a_c = (a if isinstance(a, tuple) else (a, DIM if a.startswith("╭") or a.startswith("╰") or a.startswith("│") else FG))
        b_t, b_c = (b if isinstance(b, tuple) else (b, DIM if b.startswith("╭") or b.startswith("╰") or b.startswith("│") else FG))
        # keep colors from original tuples
        if isinstance(a, tuple):
            a_t, a_c = a
        if isinstance(b, tuple):
            b_t, b_c = b
        lines.append((f"{a_t}{b_t}", a_c if a_c != FG else b_c))
    lines.append("")
    lines.append(keys)
    return lines


shell = [
    ("$ lazycomd", FG),
    ("", FG),
    ("$ lazycomd ls", FG),
    ("NAME    STATE    PID   UPTIME  RESTARTS", DIM),
    "tunnel  stopped  -     -       0",
    ("web     running  2031  1m      0", GREEN),
    ("worker  running  4402  8s      0", GREEN),
    ("", FG),
    ("# q quit the TUI. the daemon kept them.", DIM),
]

frames = [
    draw_frame(tui(header_off, status_one, cmds_before, follow_web)),
    draw_frame(tui(header_on, status, cmds_after, follow_worker)),
    draw_frame(tui(header_on, status, cmds_after, follow_port)),
    draw_frame(shell),
]
frames[0].save(
    OUT,
    save_all=True,
    append_images=frames[1:],
    duration=[1800, 1600, 1800, 2200],
    loop=0,
    optimize=True,
)
print("wrote", OUT, "bytes", OUT.stat().st_size)
