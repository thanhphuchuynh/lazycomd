package tui

import (
	"fmt"
	"strings"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

// commandDetail is everything known about one command on one screen: what it
// is configured to do, and what it is doing right now. The spec arrives from
// the daemon a moment after the pane opens, so specOK says whether to draw
// the configured half at all rather than drawing it empty.
func commandDetail(st manager.Status, spec config.Command, specOK bool, ports []probe.Port) []string {
	out := []string{st.Name, "  state     " + runtimeLine(st)}

	if specOK {
		out = append(out,
			"  cmd       "+strings.Join(spec.Cmd, " "),
			"  folder    "+orDash(spec.Cwd, "~"),
			"  restart   "+orDash(string(spec.Restart), string(config.RestartNo)),
		)
		if len(spec.DependsOn) > 0 {
			out = append(out, "  depends   "+strings.Join(spec.DependsOn, ", "))
		}
		if spec.Port != 0 {
			out = append(out, fmt.Sprintf("  port      %d", spec.Port))
		}
	}

	if h := st.Health; h != nil {
		out = append(out, "  health    "+h.URL+" · "+healthWord(*h))
	} else if specOK && spec.Health != "" {
		out = append(out, "  health    "+spec.Health+" · not probed yet")
	}

	if listening := ownedPorts(st.Name, ports); listening != "" {
		out = append(out, "  listening "+listening)
	}
	if st.MemMB > 0 || st.CPU > 0 {
		out = append(out, fmt.Sprintf("  mem/cpu   %.1fM · %.1f%%", st.MemMB, st.CPU))
	}
	if st.SpecDirty {
		out = append(out, "", "⚠ the config changed since this started — restart to pick it up")
	}
	return out
}

// runtimeLine folds state, pid, uptime and restarts into one line; each on its
// own row pushed the configured half off a short pane.
func runtimeLine(st manager.Status) string {
	parts := []string{string(st.State)}
	if st.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", st.PID))
	}
	if st.UptimeSec > 0 {
		parts = append(parts, "up "+formatUptime(st.UptimeSec))
	}
	if st.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *st.ExitCode))
	}
	parts = append(parts, fmt.Sprintf("%d restarts", st.Restarts))
	return strings.Join(parts, " · ")
}

func healthWord(h manager.HealthView) string {
	if h.OK {
		return fmt.Sprintf("ok %d · %.0fms", h.Status, h.LatencyMS)
	}
	if h.Error != "" {
		return "failing · " + h.Error
	}
	return fmt.Sprintf("failing · %d", h.Status)
}

// ownedPorts lists what this command is listening on, which is the question
// the ports panel answers from the other direction.
func ownedPorts(name string, ports []probe.Port) string {
	var out []string
	for _, p := range ports {
		if p.Command == name {
			out = append(out, fmt.Sprintf("%d", p.Port))
		}
	}
	return strings.Join(out, ", ")
}

func orDash(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
