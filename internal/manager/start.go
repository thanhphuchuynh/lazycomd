package manager

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// Start spawns one command.
func (m *Manager) Start(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(name)
}

func (m *Manager) startLocked(name string) error {
	p, ok := m.procs[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if p.State == Running || p.State == Starting {
		return fmt.Errorf("%w: %s is %s", ErrWrongState, name, p.State)
	}
	if err := p.ensureLogs(m.logDir); err != nil {
		return err
	}

	r, err := p.Spec.Resolve()
	if err != nil {
		p.State = Failed
		fmt.Fprintf(p.Logs, "lazycomd: %v\n", err)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, r.Argv[0], r.Argv[1:]...)
	cmd.Dir = r.Cwd
	cmd.Env = r.Env
	cmd.Stdout, cmd.Stderr = p.Logs, p.Logs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Escalation path: SIGKILL the whole group, not just the parent.
	cmd.Cancel = func() error { return killGroup(cmd.Process.Pid, syscall.SIGKILL) }

	p.State = Starting
	if err := cmd.Start(); err != nil {
		cancel()
		p.State = Failed
		fmt.Fprintf(p.Logs, "lazycomd: spawn failed: %v\n", err)
		return err
	}

	p.cmd, p.cancel = cmd, cancel
	p.PID, p.Started = cmd.Process.Pid, time.Now()
	p.ExitCode, p.intentionalStop = nil, false
	p.done = make(chan struct{})
	p.State = Running
	m.startOrder = append(m.startOrder, name)

	go m.reap(p, cmd)
	return nil
}

// Stop terminates a command and suppresses its restart policy.
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	p, ok := m.procs[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	m.markStopLocked(p)
	live := p.State == Stopping
	m.mu.Unlock()

	if live {
		m.terminate(p)
	}
	return nil
}

// Restart stops then starts, keeping the manual-stop suppression out of the
// way of the restart policy.
func (m *Manager) Restart(name string) error {
	if err := m.Stop(name); err != nil {
		return err
	}
	return m.Start(name)
}

// markStopLocked records an intentional stop and cancels a pending restart.
func (m *Manager) markStopLocked(p *Process) {
	p.intentionalStop = true
	if p.restartTimer != nil {
		p.restartTimer.Stop()
		p.restartTimer = nil
	}
	switch p.State {
	case Running, Starting:
		p.State = Stopping
	default:
		p.State = Stopped
	}
}

// terminate SIGTERMs the process group, then SIGKILLs it after Grace. Call it
// with the lock released.
func (m *Manager) terminate(p *Process) {
	m.mu.Lock()
	pid, done, cancel := p.PID, p.done, p.cancel
	m.mu.Unlock()

	if done == nil {
		return
	}
	_ = killGroup(pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(m.Grace):
		if cancel != nil {
			cancel()
		}
		<-done
	}
}

// killGroup signals the whole process group so child trees die with their
// parent.
func killGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, sig)
}
