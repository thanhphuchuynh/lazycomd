// Command lazycomd runs and supervises long dev commands.
package main

import (
	"fmt"
	"os"
)

const usage = `lazycomd - run and supervise long dev commands

usage: lazycomd <command> [flags]

  serve                    run the daemon in the foreground
  ls                       list commands and their state
  start <name> [-d]        start a command (-d starts dependencies first)
  stop <name>              stop a command
  restart <name>           restart a command
  logs <name> [-n N] [-f]  show, or follow, a command's output
  reload                   re-read the config and apply the diff
  run <name>               start with dependencies, then follow output

environment:
  LAZYCOMD_ADDR    unix:///path/to.sock or http://host:port
  LAZYCOMD_TOKEN   bearer token, for a TCP address
  LAZYCOMD_CONFIG  override the config path
`

func main() { os.Exit(dispatch(os.Args[1:])) }

func dispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "ls":
		return runLs(args[1:])
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
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "lazycomd: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
