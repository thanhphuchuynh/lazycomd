package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

// Finding is one thing doctor noticed. Level is "error" for something that
// will fail on the next start, "warn" for something that only might.
type Finding struct {
	Level   string `json:"level"`
	Command string `json:"command,omitempty"`
	Message string `json:"message"`
}

// runDoctor checks the catalog for the mistakes that only show up as a
// process that will not start: a port two commands both want, a dependency
// that does not exist, a folder that has been moved or deleted.
func runDoctor(args []string) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
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
	findings, err := runDoctorFindings(c)
	if err != nil {
		return fail(err)
	}

	// A warning is not worth a non-zero exit; an error is, so a script — or
	// an agent — can gate on it whichever way it asked for the output.
	code := 0
	for _, f := range findings {
		if f.Level == "error" {
			code = 1
		}
	}

	if *asJSON {
		if findings == nil {
			findings = []Finding{}
		}
		if out := printJSON(findings); out != 0 {
			return out
		}
		return code
	}

	if len(findings) == 0 {
		fmt.Printf("%d commands, nothing to report\n", len(list))
		return 0
	}
	for _, f := range findings {
		where := ""
		if f.Command != "" {
			where = f.Command + ": "
		}
		fmt.Printf("%-5s %s%s\n", f.Level, where, f.Message)
	}
	return code
}

// runDoctorFindings gathers everything doctor looks at and runs the checks,
// so the CLI and the MCP tool see the same findings.
func runDoctorFindings(c *client.Client) ([]Finding, error) {
	list, err := c.List()
	if err != nil {
		return nil, err
	}
	specs := make(map[string]config.Command, len(list))
	for _, s := range list {
		spec, err := c.CommandConfig(s.Name)
		if err != nil {
			return nil, err
		}
		specs[s.Name] = spec
	}
	return check(list, specs, projectsOf(c), dirExists), nil
}

// projectsOf reads the project list, tolerating a daemon too old to serve it.
func projectsOf(c *client.Client) map[string]string {
	p, err := c.Projects()
	if err != nil {
		return nil
	}
	return p
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// check is the whole of doctor's judgement, taking everything it looks at as
// arguments so it can be tested without a daemon or a filesystem.
func check(list []manager.Status, specs map[string]config.Command, projects map[string]string, exists func(string) bool) []Finding {
	var out []Finding

	names := make(map[string]bool, len(list))
	for _, s := range list {
		names[s.Name] = true
	}

	// Two commands that both want one port: whichever starts second fails to
	// bind, and which one that is depends on the day.
	byPort := map[int][]string{}
	for name, spec := range specs {
		if p := spec.IntendedPort(); p != 0 {
			byPort[p] = append(byPort[p], name)
		}
	}
	for _, port := range sortedKeys(byPort) {
		owners := byPort[port]
		if len(owners) < 2 {
			continue
		}
		sort.Strings(owners)
		out = append(out, Finding{
			Level:   "error",
			Message: fmt.Sprintf("port %d is claimed by %v", port, owners),
		})
	}

	for _, name := range sortedNames(specs) {
		spec := specs[name]
		if spec.Cwd != "" && !exists(spec.Cwd) {
			out = append(out, Finding{Level: "error", Command: name, Message: "cwd does not exist: " + spec.Cwd})
		}
		for _, dep := range spec.DependsOn {
			if !names[dep] {
				out = append(out, Finding{Level: "error", Command: name, Message: "depends_on names an unknown command: " + dep})
			}
		}
		if spec.Health != "" && spec.Port == 0 {
			out = append(out, Finding{Level: "warn", Command: name, Message: "has health but no port, so the ports panel cannot tell who owns it"})
		}
	}

	for _, ns := range sortedKeys2(projects) {
		if !exists(projects[ns]) {
			out = append(out, Finding{Level: "warn", Message: fmt.Sprintf("project %q points at a folder that is gone: %s", ns, projects[ns])})
		}
	}
	return out
}

func sortedKeys(m map[int][]string) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func sortedKeys2(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedNames(m map[string]config.Command) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
