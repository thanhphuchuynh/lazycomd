// Command lazycomd runs and supervises long dev commands.
package main

import (
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
	"github.com/thanhphuchuynh/lazycomd/internal/tui"
)

// version is set at release build time via -ldflags -X main.version=.
var version = "dev"

const usage = `lazycomd - run and supervise long dev commands

usage: lazycomd [command] [flags]

  (no command)             open the TUI
  serve                    run the daemon in the foreground
  ls [--json]              list commands and their state
  start <name> [-d]        start a command (-d starts dependencies first)
  stop <name>              stop a command
  restart <name>           restart a command
  logs <name> [-n N] [-f]  show, or follow, a command's output
  reload                   re-read the config and apply the diff
  port [N] [--json]        who is listening, and which command owns it
  doctor [--json]          check the catalog for what will fail to start
  run <name>               start with dependencies, then follow output
  version                  print the build version

environment:
  LAZYCOMD_ADDR    unix:///path/to.sock or http://host:port
  LAZYCOMD_TOKEN   bearer token, for a TCP address
  LAZYCOMD_CONFIG  override the config path
`

func main() { os.Exit(dispatch(os.Args[1:])) }

// runTUI opens the interactive UI. Without a terminal — a pipe, CI, a test
// binary — it prints usage instead, so `lazycomd | cat` stays sane.
func runTUI() int {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	if err := tui.Run(c); err != nil {
		return fail(err)
	}
	return 0
}

func dispatch(args []string) int {
	if len(args) == 0 {
		return runTUI()
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "ls":
		return runLs(args[1:])
	case "port":
		return runPort(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	case "start":
		return runStart(args[1:])
	case "stop":
		return runSimple("stop", args[1:])
	case "restart":
		return runSimple("restart", args[1:])
	case "logs":
		return runLogs(args[1:])
	case "reload":
		return runReload(args[1:])
	case "run":
		return runRun(args[1:])
	case "version", "-v", "--version":
		fmt.Println(version)
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "lazycomd: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
