package probe

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// parseLsof reads `lsof -nP -iTCP -sTCP:LISTEN` output. Columns are
// COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME, and the address is the
// field before the trailing "(LISTEN)".
func parseLsof(out []byte) []Port {
	var ports []Port
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "COMMAND" {
			continue
		}
		if !strings.HasSuffix(line, "(LISTEN)") {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		addr, port, ok := splitHostPort(fields[len(fields)-2])
		if !ok {
			continue
		}
		ports = append(ports, Port{Addr: addr, Port: port, PID: pid, Process: fields[0]})
	}
	return ports
}

// parseSS reads `ss -ltnpH` output, where the local address is the fourth
// field and the process appears as users:(("name",pid=N,fd=M)).
func parseSS(out []byte) []Port {
	var ports []Port
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "LISTEN" {
			continue
		}
		addr, port, ok := splitHostPort(fields[3])
		if !ok {
			continue
		}
		name, pid, ok := parseSSUsers(line)
		if !ok {
			continue
		}
		ports = append(ports, Port{Addr: addr, Port: port, PID: pid, Process: name})
	}
	return ports
}

// parseSSUsers pulls the first ("name",pid=N) pair out of an ss line.
func parseSSUsers(line string) (string, int, bool) {
	i := strings.Index(line, `(("`)
	if i < 0 {
		return "", 0, false
	}
	rest := line[i+3:]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return "", 0, false
	}
	name := rest[:j]

	k := strings.Index(rest, "pid=")
	if k < 0 {
		return "", 0, false
	}
	digits := rest[k+4:]
	end := 0
	for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
		end++
	}
	pid, err := strconv.Atoi(digits[:end])
	if err != nil {
		return "", 0, false
	}
	return name, pid, true
}

// splitHostPort splits "127.0.0.1:3000", "*:5432" or "[::1]:8080".
func splitHostPort(s string) (string, int, bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(s[i+1:])
	if err != nil || port <= 0 {
		return "", 0, false
	}
	host := strings.TrimSuffix(strings.TrimPrefix(s[:i], "["), "]")
	return normalizeAddr(host), port, true
}

// normalizeAddr folds every spelling of "any address" into "*", so one
// wildcard listener reported once per address family collapses to one row.
func normalizeAddr(a string) string {
	switch a {
	case "", "*", "0.0.0.0", "::", "[::]":
		return "*"
	}
	return a
}

// normalizePorts deduplicates on (addr, port, pid) and sorts by port, then
// address. A genuine double bind — loopback and wildcard on one port — stays
// two rows, because it is two listeners.
func normalizePorts(in []Port) []Port {
	type key struct {
		addr string
		port int
		pid  int
	}
	seen := make(map[key]bool, len(in))
	out := make([]Port, 0, len(in))
	for _, p := range in {
		k := key{p.Addr, p.Port, p.PID}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Addr < out[j].Addr
	})
	return out
}

// runFunc runs a command and returns its stdout. Injectable so tests never
// spawn a subprocess.
type runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// execRun is the real runner.
func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// collectPorts lists listening TCP sockets, preferring lsof and falling back
// to ss. Output is trusted over exit status: lsof exits 1 both when it has
// nothing to report and when it merely could not stat some filesystem.
func collectPorts(ctx context.Context, run runFunc) ([]Port, error) {
	out, lsofErr := run(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN")
	if len(out) > 0 {
		return normalizePorts(parseLsof(out)), nil
	}
	if lsofErr == nil {
		return []Port{}, nil // lsof ran, nothing is listening
	}

	out, ssErr := run(ctx, "ss", "-ltnpH")
	if len(out) > 0 {
		return normalizePorts(parseSS(out)), nil
	}
	if ssErr == nil {
		return []Port{}, nil
	}
	return nil, fmt.Errorf("lsof: %v; ss: %v", lsofErr, ssErr)
}

// ownerOf names the lazycomd command whose process group contains pid, or "".
// Ownership is by group because a command like `sh -c "npm start"` listens
// from a grandchild, and spec #1 gives every command its own group whose id
// is the command's own PID.
func ownerOf(pid int, groups map[int]int, pids map[string]int) string {
	pgid, ok := groups[pid]
	if !ok {
		return ""
	}
	for name, cmdPID := range pids {
		if cmdPID == pgid {
			return name
		}
	}
	return ""
}

// applyOwners fills in the Command field of every port it can attribute.
func applyOwners(ports []Port, groups map[int]int, pids map[string]int) []Port {
	out := make([]Port, len(ports))
	copy(out, ports)
	for i := range out {
		out[i].Command = ownerOf(out[i].PID, groups, pids)
	}
	return out
}
