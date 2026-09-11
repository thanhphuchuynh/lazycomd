package manager

import (
	"reflect"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

// Reload applies a freshly loaded config. It never restarts a running
// command: a changed spec is staged for that command's next start.
func (m *Manager) Reload(cfg *config.Config) error {
	m.mu.Lock()

	var kill []*Process
	for name, p := range m.procs {
		if _, ok := cfg.Commands[name]; ok {
			continue
		}
		delete(m.procs, name)
		if p.State == Running || p.State == Starting {
			m.markStopLocked(p)
			kill = append(kill, p)
		}
	}

	for name, c := range cfg.Commands {
		p, ok := m.procs[name]
		if !ok {
			m.procs[name] = &Process{Name: name, Spec: c, State: Stopped}
			continue
		}
		if reflect.DeepEqual(p.Spec, c) {
			continue
		}
		if p.State == Running || p.State == Starting {
			staged := c
			p.pending = &staged
			p.SpecDirty = true
			continue
		}
		p.Spec = c
		p.pending = nil
		p.SpecDirty = false
		p.Logs = nil
	}

	m.cfg = cfg
	m.mu.Unlock()

	for _, p := range kill {
		m.terminate(p)
	}
	return nil
}
