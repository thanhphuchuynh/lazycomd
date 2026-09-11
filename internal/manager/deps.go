package manager

import (
	"fmt"
	"time"
)

// StartWithDeps starts a command's dependencies depth-first, in config order,
// before the command itself. Already-running dependencies are left alone.
func (m *Manager) StartWithDeps(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startTreeLocked(name, make(map[string]bool))
}

// ponytail: the settle sleep holds the manager lock, so a three-deep chain
// blocks the API for ~600ms. Move to a per-process sync.Cond if the TUI
// stutters.
func (m *Manager) startTreeLocked(name string, seen map[string]bool) error {
	if seen[name] {
		return nil
	}
	seen[name] = true

	p, ok := m.procs[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	for _, dep := range p.Spec.DependsOn {
		if err := m.startTreeLocked(dep, seen); err != nil {
			return err
		}
	}
	if p.State == Running || p.State == Starting {
		return nil
	}
	if err := m.startLocked(name); err != nil {
		return err
	}
	time.Sleep(m.SettleDelay)
	return nil
}
