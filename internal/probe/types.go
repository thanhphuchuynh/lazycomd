// Package probe samples what the machine is doing: which ports are listening,
// what each lazycomd command is costing, and whether the services behind them
// answer. It imports neither internal/manager nor internal/api.
package probe

import "time"

// Snapshot is one view of the machine. Sections sample independently, so each
// carries its own timestamp and its own error.
type Snapshot struct {
	Ports     []Port               `json:"ports"`
	Vitals    map[string]Vital     `json:"vitals"` // keyed by command name
	Health    map[string]Health    `json:"health"` // keyed by command name
	Conflicts []Conflict           `json:"conflicts,omitempty"`
	SampledAt map[string]time.Time `json:"sampled_at"`       // "ports", "vitals", "health"
	Errors    map[string]string    `json:"errors,omitempty"` // same keys
}

// Port is one listening socket.
type Port struct {
	Addr    string `json:"addr"` // 127.0.0.1, *, ::1
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Process string `json:"process"`
	Command string `json:"command,omitempty"` // the lazycomd command that owns it
}

// Vital is one command's resource use, summed across its process group.
type Vital struct {
	PID   int     `json:"pid"`
	CPU   float64 `json:"cpu"` // percent of one core; may exceed 100
	MemMB float64 `json:"mem_mb"`
}

// Health is one HTTP probe result.
type Health struct {
	URL       string  `json:"url"`
	OK        bool    `json:"ok"`
	Status    int     `json:"status,omitempty"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// Conflict is a command's intended port not being held by that command.
type Conflict struct {
	Port    int    `json:"port"`
	Command string `json:"command"`           // the command that wants it
	State   string `json:"state"`             // "taken" | "free"
	HeldBy  string `json:"held_by,omitempty"` // process name of the squatter
	PID     int    `json:"pid,omitempty"`
}

// Section keys used by SampledAt and Errors.
const (
	sectionPorts  = "ports"
	sectionVitals = "vitals"
	sectionHealth = "health"
)

// EmptySnapshot is a snapshot with every section present and empty, so a
// client never has to special-case a null map.
func EmptySnapshot() Snapshot {
	return Snapshot{
		Ports:     []Port{},
		Vitals:    map[string]Vital{},
		Health:    map[string]Health{},
		SampledAt: map[string]time.Time{},
		Errors:    map[string]string{},
	}
}
