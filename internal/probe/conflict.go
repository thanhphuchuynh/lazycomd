package probe

import "sort"

// conflicts compares what each command means to bind against what is actually
// bound. pids doubles as the running set.
//
// Callers must not invoke this when the ports sample failed: with no port
// list every intended port would look free, raising a false alarm about every
// command exactly when the collector is the broken thing.
func conflicts(intended map[string]int, ports []Port, pids map[string]int) []Conflict {
	byPort := make(map[int][]Port, len(ports))
	for _, p := range ports {
		byPort[p.Port] = append(byPort[p.Port], p)
	}

	out := make([]Conflict, 0, len(intended))
	for name, port := range intended {
		rows := byPort[port]

		if len(rows) == 0 {
			// Nothing listening. Only interesting while the command is up.
			if _, running := pids[name]; running {
				out = append(out, Conflict{Port: port, Command: name, State: "free"})
			}
			continue
		}

		owned := false
		for _, r := range rows {
			if r.Command == name {
				owned = true
				break
			}
		}
		if owned {
			continue
		}

		squatter := rows[0]
		out = append(out, Conflict{
			Port:    port,
			Command: name,
			State:   "taken",
			HeldBy:  squatter.Process,
			PID:     squatter.PID,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Command < out[j].Command
	})
	return out
}
