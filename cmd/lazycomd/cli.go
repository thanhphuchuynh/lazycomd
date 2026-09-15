package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

// hoistFlags moves flags ahead of positional arguments so both orders work:
// "logs api -f" and "logs -f api". valueFlags names the flags that consume
// the next argument.
func hoistFlags(args []string, valueFlags map[string]bool) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		if valueFlags[a] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, rest...)
}

// fail prints an error and returns the process exit code for it.
func fail(err error) int {
	if errors.Is(err, client.ErrNoDaemon) {
		fmt.Fprintln(os.Stderr, "lazycomd: daemon not running (start with: lazycomd serve)")
		return 1
	}
	fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
	return 1
}

func usageErr(line string) int {
	fmt.Fprintf(os.Stderr, "usage: lazycomd %s\n", line)
	return 2
}

func runLs(args []string) int {
	flags := flag.NewFlagSet("ls", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(hoistFlags(args, nil)); err != nil {
		return 2
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	list, err := c.List()
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		if list == nil {
			list = []manager.Status{}
		}
		return printJSON(list)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tPID\tUPTIME\tRESTARTS")
	for _, s := range list {
		state := string(s.State)
		if s.SpecDirty {
			state += " (spec changed)"
		}
		pid := "-"
		if s.PID > 0 {
			pid = strconv.Itoa(s.PID)
		}
		up := "-"
		if s.UptimeSec > 0 {
			up = time.Duration(s.UptimeSec * float64(time.Second)).Round(time.Second).String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", s.Name, state, pid, up, s.Restarts)
	}
	tw.Flush()
	return 0
}

func runStart(args []string) int {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	deps := flags.Bool("d", false, "start dependencies first")
	wait := flags.Duration("wait", 0, "block until the command is ready, e.g. 30s")
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(hoistFlags(args, map[string]bool{"-wait": true, "--wait": true})); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return usageErr("start <name> [-d] [--wait 30s] [--json]")
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	name, err := c.Resolve(flags.Arg(0))
	if err != nil {
		return fail(err)
	}
	st, err := c.StartWait(name, *deps, *wait)
	if err != nil {
		return fail(err)
	}
	return reportStatus(st, *asJSON)
}

// reportStatus is how every lifecycle verb prints its one result: a line for
// a person, the whole manager.Status for anything parsing it.
func reportStatus(st manager.Status, asJSON bool) int {
	if asJSON {
		return printJSON(st)
	}
	fmt.Printf("%s %s\n", st.Name, st.State)
	return 0
}

// runSimple handles stop and restart, which take a name and nothing else.
func runSimple(verb string, args []string) int {
	flags := flag.NewFlagSet(verb, flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(hoistFlags(args, nil)); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return usageErr(verb + " <name> [--json]")
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	name, err := c.Resolve(flags.Arg(0))
	if err != nil {
		return fail(err)
	}

	var st manager.Status
	if verb == "stop" {
		st, err = c.Stop(name)
	} else {
		st, err = c.Restart(name)
	}
	if err != nil {
		return fail(err)
	}
	return reportStatus(st, *asJSON)
}

func runLogs(args []string) int {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	n := flags.Int("n", 200, "lines of scrollback")
	follow := flags.Bool("f", false, "follow output")
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(hoistFlags(args, map[string]bool{"-n": true})); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return usageErr("logs <name> [-n N] [-f] [--json]")
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	name, err := c.Resolve(flags.Arg(0))
	if err != nil {
		return fail(err)
	}
	lines, err := c.Logs(name, *n)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		if lines == nil {
			lines = []string{}
		}
		// Following and JSON do not mix: one is a stream, the other a
		// document. --json wins and returns what there is.
		return printJSON(map[string]any{"name": name, "lines": lines})
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	if !*follow {
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := c.Stream(ctx, name, os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
		return fail(err)
	}
	return 0
}

func runReload(args []string) int {
	flags := flag.NewFlagSet("reload", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	list, err := c.Reload()
	if err != nil {
		return fail(err)
	}
	for _, s := range list {
		suffix := ""
		if s.SpecDirty {
			suffix = " (spec changed, applies on next start)"
		}
		fmt.Printf("%s %s%s\n", s.Name, s.State, suffix)
	}
	return 0
}

// runRun starts a command with its dependencies and follows its output.
// SIGINT stops only what this invocation started.
func runRun(args []string) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		return usageErr("run <name>")
	}
	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	name, err := c.Resolve(flags.Arg(0))
	if err != nil {
		return fail(err)
	}

	st, err := c.Get(name)
	if err != nil {
		return fail(err)
	}
	weStarted := false
	if st.State != manager.Running && st.State != manager.Starting {
		if _, err := c.Start(name, true); err != nil {
			return fail(err)
		}
		weStarted = true
	} else {
		fmt.Fprintf(os.Stderr, "lazycomd: %s already running, attaching\n", name)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	streamErr := c.Stream(ctx, name, os.Stdout)

	if weStarted {
		if _, err := c.Stop(name); err != nil {
			return fail(err)
		}
	}
	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		return fail(streamErr)
	}
	return 0
}
