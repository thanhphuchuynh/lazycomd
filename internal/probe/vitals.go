package probe

import (
	"context"
	"strconv"
	"strings"
)

// psRow is one line of `ps -eo pid=,pgid=,%cpu=,rss=`.
type psRow struct {
	pid   int
	pgid  int
	cpu   float64
	rssKB float64
}

// parsePS returns every row plus the pid→pgid map, which the ports collector
// uses to attribute a listening socket to a command.
func parsePS(out []byte) ([]psRow, map[int]int) {
	var rows []psRow
	groups := make(map[int]int)

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		pgid, err2 := strconv.Atoi(fields[1])
		cpu, err3 := strconv.ParseFloat(fields[2], 64)
		rss, err4 := strconv.ParseFloat(fields[3], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		rows = append(rows, psRow{pid: pid, pgid: pgid, cpu: cpu, rssKB: rss})
		groups[pid] = pgid
	}
	return rows, groups
}

// collectVitals sums CPU and memory over each command's process group. A
// command's own PID is its process group id, so the whole tree it spawned
// counts — which is the number worth showing for `sh -c "npm start"`.
func collectVitals(ctx context.Context, run runFunc, pids map[string]int) (map[string]Vital, map[int]int, error) {
	if len(pids) == 0 {
		return map[string]Vital{}, map[int]int{}, nil
	}

	out, err := run(ctx, "ps", "-eo", "pid=,pgid=,%cpu=,rss=")
	if len(out) == 0 && err != nil {
		return nil, nil, err
	}
	rows, groups := parsePS(out)

	byGroup := make(map[int]Vital, len(pids))
	for _, r := range rows {
		v := byGroup[r.pgid]
		v.CPU += r.cpu
		v.MemMB += r.rssKB / 1024
		byGroup[r.pgid] = v
	}

	vitals := make(map[string]Vital, len(pids))
	for name, pid := range pids {
		v, ok := byGroup[pid]
		if !ok {
			continue
		}
		v.PID = pid
		vitals[name] = v
	}
	return vitals, groups, nil
}
