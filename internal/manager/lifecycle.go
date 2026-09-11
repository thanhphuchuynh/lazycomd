package manager

import (
	"log"
	"sort"
	"sync"
	"syscall"
)

// StartAutostart starts every command marked autostart, dependencies first,
// in name order. A failure is logged, never fatal.
func (m *Manager) StartAutostart() {
	m.mu.Lock()
	names := make([]string, 0, len(m.procs))
	for name, p := range m.procs {
		if p.Spec.Autostart {
			names = append(names, name)
		}
	}
	m.mu.Unlock()
	sort.Strings(names)

	for _, name := range names {
		if err := m.StartWithDeps(name); err != nil {
			log.Printf("lazycomd: autostart %s: %v", name, err)
		}
	}
}

// Shutdown stops every running command: SIGTERM, then SIGKILL after Grace.
// It also cancels pending restarts and closes disk log tees.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	var kill []*Process
	for _, p := range m.procs {
		m.markStopLocked(p)
		if p.State == Stopping {
			kill = append(kill, p)
		}
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range kill {
		wg.Add(1)
		go func(p *Process) {
			defer wg.Done()
			m.terminate(p)
		}(p)
	}
	wg.Wait()

	m.mu.Lock()
	for _, p := range m.procs {
		if p.Logs != nil {
			_ = p.Logs.Close()
		}
	}
	m.mu.Unlock()
}

// KillAll SIGKILLs every process group immediately. Used when a second
// interrupt arrives while Shutdown is still draining.
func (m *Manager) KillAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.procs {
		p.intentionalStop = true
		if p.restartTimer != nil {
			p.restartTimer.Stop()
			p.restartTimer = nil
		}
		// Direct group signal, not p.cancel: the context watcher is gone
		// once the direct child exits.
		_ = killGroup(p.PID, syscall.SIGKILL)
		if p.cancel != nil {
			p.cancel()
		}
	}
}
