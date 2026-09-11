package manager

import (
	"errors"
	"os/exec"
)

// reap waits for one spawned process and records its outcome.
func (m *Manager) reap(p *Process, cmd *exec.Cmd) {
	code := exitCode(cmd.Wait())

	m.mu.Lock()
	p.ExitCode = &code
	p.PID = 0
	if p.intentionalStop || code == 0 {
		p.State = Stopped
	} else {
		p.State = Failed
	}
	close(p.done)
	m.mu.Unlock()
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
