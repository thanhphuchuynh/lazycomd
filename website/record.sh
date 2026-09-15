#!/bin/zsh
# Records the GIFs on this page from a real terminal: tmux drives the session,
# asciinema captures it, agg renders the GIF.
#
#   brew install tmux asciinema agg jq
#   ./record.sh            # all three
#   ./record.sh cli        # just one
#
# The scenes run against a throwaway daemon in /tmp/lzc-demo, never your own.
set -e

here=${0:A:h}
demo=/tmp/lzc-demo
bin=${LAZYCOMD_BIN:-$here/../bin/lazycomd}
export LAZYCOMD_CONFIG=$demo/config.yaml
export XDG_STATE_HOME=$demo/state
export LAZYCOMD_ADDR="unix://$demo/state/lazycomd/lazycomd.sock"

# --- the throwaway daemon -----------------------------------------------------

start_daemon() {
  pkill -f "$demo" 2>/dev/null || true
  # A python from an earlier run keeps 18099, which would make the scene
  # open on a failed command and credit the port to nobody.
  lsof -ti :18099 2>/dev/null | xargs -r kill 2>/dev/null || true
  sleep 0.5
  rm -rf $demo
  mkdir -p $demo/state
  cp $here/demo.yaml $demo/config.yaml
  (cd $demo && $bin serve >$demo/daemon.log 2>&1 &)
  for i in {1..40}; do
    $bin ls >/dev/null 2>&1 && return
    sleep 0.25
  done
  echo "daemon never came up; see $demo/daemon.log" >&2
  exit 1
}

# --- the recorder -------------------------------------------------------------

# rec_start <cols> <rows> — a recorded shell, ready at a clean prompt.
rec_start() {
  tmux kill-session -t rec 2>/dev/null || true
  rm -f $demo/out.cast
  tmux new-session -d -s rec -x $1 -y $2 \
    "asciinema rec --overwrite $demo/out.cast -c 'zsh -f' >/dev/null 2>&1"
  sleep 2
  send "export PATH=$(dirname $bin:A):/opt/homebrew/bin:/usr/bin:/bin"
  send "export LAZYCOMD_ADDR='$LAZYCOMD_ADDR' LAZYCOMD_CONFIG='$LAZYCOMD_CONFIG' XDG_STATE_HOME='$XDG_STATE_HOME'"
  send "export PS1='%F{6}❯%f ' TERM=xterm-256color"
  send "setopt interactivecomments"
  send "source $demo/mcp.zsh"
  send "cd $demo; clear"
  sleep 1
}

# mcp_helpers writes the two shorthands the MCP scene types, so the scene
# shows the tool call rather than a screenful of JSON-RPC envelope.
mcp_helpers() {
  cat > $demo/mcp.zsh <<'ZSH'
tools() {
  echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' |
    lazycomd mcp | jq -r '.result.tools[].name'
}
call() {
  printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"%s","arguments":%s}}' "$1" "$2" |
    lazycomd mcp | jq -r '.result.content[0].text'
}
ZSH
}

# send <line> — type a line and press enter.
send() { tmux send-keys -t rec "$1" Enter; sleep 0.4 }

# keys <key>... — raw keystrokes, for driving the TUI.
keys() { tmux send-keys -t rec "$@"; sleep 0.4 }

# rec_stop <name> <font-size> — end the shell and render name.gif.
rec_stop() {
  # C-d rather than typing exit, which would leave "exit" as the last line.
  tmux send-keys -t rec C-d
  sleep 2
  tmux kill-session -t rec 2>/dev/null || true
  agg --font-size ${2:-15} --theme asciinema --idle-time-limit 2 \
    $demo/out.cast $here/$1.gif
  echo "wrote $here/$1.gif ($(du -h $here/$1.gif | cut -f1))"
}

# --- scenes -------------------------------------------------------------------

scene_cli() {
  rec_start 100 22
  send "lazycomd ls"; sleep 2
  send "lazycomd start web --wait 20s && echo READY"; sleep 3
  send "lazycomd port 18099"; sleep 3
  send "lazycomd doctor"; sleep 3
  send "lazycomd ls --json | jq -c '.[] | {name, state}'"; sleep 3
  rec_stop cli
}

scene_mcp() {
  rec_start 100 26
  send "# claude mcp add lazycomd -- lazycomd mcp"; sleep 1.5
  send "# tools() and call() wrap the JSON-RPC envelope; the agent sends it itself"; sleep 2
  send "tools"; sleep 4
  send "# bring the API up and wait until it answers"; sleep 1.5
  send "call start_command '{\"name\":\"web\",\"wait_sec\":20}'"; sleep 4
  send "# who has the port now?"; sleep 1.5
  send "call who_owns_port '{\"port\":18099}'"; sleep 4
  rec_stop mcp
}

scene_tui() {
  rec_start 104 30
  send "lazycomd"; sleep 3
  keys j; sleep 0.5           # legacy → tunnel
  keys j; sleep 0.5           # tunnel → web, the one that binds a port
  keys s; sleep 2.5           # start it
  keys 3; sleep 2.5           # ports: who owns what
  keys 2; sleep 1.5
  keys a; sleep 1.5           # the add form
  tmux send-keys -t rec "api"; sleep 1
  keys Tab; tmux send-keys -t rec "go run ."; sleep 1.2
  keys Tab; sleep 1            # → FOLDER, prefilled
  keys Tab; sleep 1            # completes the path
  keys Tab; tmux send-keys -t rec "LOG=debug"; sleep 1
  keys Tab; tmux send-keys -t rec "8080"; sleep 1
  keys Tab; tmux send-keys -t rec "http://localhost:8080/healthz"; sleep 1.5
  keys Tab; sleep 0.8
  keys Right; sleep 1.2       # restart: on-failure
  keys Tab; keys Space; sleep 1.5
  keys Escape; sleep 1
  keys q; sleep 1
  send "lazycomd ls"; sleep 2.5
  rec_stop demo 14
}

start_daemon
mcp_helpers
case ${1:-all} in
  cli) scene_cli ;;
  mcp) scene_mcp ;;
  tui) scene_tui ;;
  all) scene_cli; scene_mcp; scene_tui ;;
  *) echo "usage: record.sh [cli|mcp|tui|all]" >&2; exit 2 ;;
esac
pkill -f "$demo" 2>/dev/null || true
