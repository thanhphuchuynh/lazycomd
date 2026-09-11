package manager

import (
	"errors"
	"os/exec"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

// reap waits for one spawned process, records its outcome and applies the
// restart policy unless the stop was intentional.
func (m *Manager) reap(p *Process, cmd *exec.Cmd) {
	code := exitCode(cmd.Wait())

	m.mu.Lock()
	defer m.mu.Unlock()

	uptime := time.Since(p.Started)
	p.ExitCode = &code
	p.PID = 0
	if p.intentionalStop || code == 0 {
		p.State = Stopped
	} else {
		p.State = Failed
	}
	close(p.done)

	if p.intentionalStop || !m.shouldRestart(p.Spec.Restart, code) {
		return
	}
	if uptime >= m.UptimeReset {
		p.backoff = 0
	}
	delay := m.nextBackoff(p)
	p.Restarts++
	name := p.Name
	p.restartTimer = time.AfterFunc(delay, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		p.restartTimer = nil
		if p.intentionalStop {
			return
		}
		// A failure to respawn is already in the command's log buffer.
		_ = m.startLocked(name)
	})
}

func (m *Manager) shouldRestart(r config.Restart, code int) bool {
	switch r {
	case config.RestartAlways:
		return true
	case config.RestartOnFailure:
		return code != 0
	default:
		return false
	}
}

// nextBackoff doubles the delay up to BackoffMax. A caller resets p.backoff
// to zero when the process earned a clean slate.
func (m *Manager) nextBackoff(p *Process) time.Duration {
	if p.backoff == 0 {
		p.backoff = m.BackoffMin
		return p.backoff
	}
	p.backoff *= 2
	if p.backoff > m.BackoffMax {
		p.backoff = m.BackoffMax
	}
	return p.backoff
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
