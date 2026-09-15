package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

// printJSON writes one value as indented JSON, which is what every --json
// flag in this CLI produces.
func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail(err)
	}
	return 0
}

// runPort answers "who owns this port". With no port it lists every listening
// socket the daemon can see, lazycomd's own commands named.
func runPort(args []string) int {
	flags := flag.NewFlagSet("port", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(hoistFlags(args, nil)); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		return usageErr("port [N] [--json]")
	}

	want := 0
	if flags.NArg() == 1 {
		n, err := strconv.Atoi(flags.Arg(0))
		if err != nil || n < 1 || n > 65535 {
			return usageErr("port [N] [--json]")
		}
		want = n
	}

	c, err := client.Default()
	if err != nil {
		return fail(err)
	}
	snap, err := c.System()
	if err != nil {
		return fail(err)
	}

	ports := snap.Ports
	if want != 0 {
		ports = filterPort(ports, want)
	}
	sortPorts(ports)

	if *asJSON {
		if ports == nil {
			ports = []probe.Port{}
		}
		return printJSON(ports)
	}
	if len(ports) == 0 {
		if want != 0 {
			fmt.Printf("nothing is listening on %d\n", want)
			return 0
		}
		fmt.Println("nothing is listening")
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "PORT\tADDR\tPID\tPROCESS\tCOMMAND")
	for _, p := range ports {
		owner := p.Command
		if owner == "" {
			owner = "-"
		}
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\n", p.Port, p.Addr, p.PID, p.Process, owner)
	}
	tw.Flush()
	return 0
}

func filterPort(ports []probe.Port, want int) []probe.Port {
	var out []probe.Port
	for _, p := range ports {
		if p.Port == want {
			out = append(out, p)
		}
	}
	return out
}

// sortPorts orders by port, then by address, so two runs print the same thing.
func sortPorts(ports []probe.Port) {
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Port != ports[j].Port {
			return ports[i].Port < ports[j].Port
		}
		return ports[i].Addr < ports[j].Addr
	})
}
