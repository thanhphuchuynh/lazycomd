// Package manager owns the lifecycle of every configured command. It never
// imports internal/api.
package manager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/logbuf"
)

// Sentinel errors the API layer maps to HTTP status codes.
var (
	ErrNotFound   = errors.New("unknown command")
	ErrWrongState = errors.New("illegal in current state")
)

// State is a command's lifecycle state.
type State string

const (
	Stopped  State = "stopped"
	Starting State = "starting"
	Running  State = "running"
	Stopping State = "stopping"
	Failed   State = "failed"
)

// Process is one configured command and its running instance, if any.
type Process struct {
	Name      string
	Spec      config.Command
	State     State
	PID       int
	Started   time.Time
	ExitCode  *int
	Restarts  int
	SpecDirty bool
	Logs      *logbuf.Buffer

	cmd             *exec.Cmd
	cancel          context.CancelFunc
	done            chan struct{}
	pending         *config.Command
	intentionalStop bool
	backoff         time.Duration
	restartTimer    *time.Timer
}

// Status is the API view of a Process.
type Status struct {
	Name      string   `json:"name"`
	State     State    `json:"state"`
	PID       int      `json:"pid,omitempty"`
	UptimeSec float64  `json:"uptime_sec,omitempty"`
	ExitCode  *int     `json:"exit_code,omitempty"`
	Restarts  int      `json:"restarts"`
	SpecDirty bool     `json:"spec_dirty,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// Manager holds every command. One mutex guards the whole map; it is never
// held across a spawn's Wait or a terminate.
type Manager struct {
	mu     sync.Mutex
	procs  map[string]*Process
	cfg    *config.Config
	logDir string

	// Tunables, injectable so tests run in milliseconds.
	Grace       time.Duration
	BackoffMin  time.Duration
	BackoffMax  time.Duration
	UptimeReset time.Duration
	SettleDelay time.Duration

	startOrder []string
}

// New builds a Manager with every configured command in state stopped.
func New(cfg *config.Config, logDir string) *Manager {
	m := &Manager{
		procs:       make(map[string]*Process, len(cfg.Commands)),
		cfg:         cfg,
		logDir:      logDir,
		Grace:       10 * time.Second,
		BackoffMin:  time.Second,
		BackoffMax:  30 * time.Second,
		UptimeReset: 60 * time.Second,
		SettleDelay: 200 * time.Millisecond,
	}
	for name, c := range cfg.Commands {
		m.procs[name] = &Process{Name: name, Spec: c, State: Stopped}
	}
	return m
}

// List returns every command's status, sorted by name.
func (m *Manager) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.procs))
	for n := range m.procs {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Status, 0, len(names))
	for _, n := range names {
		out = append(out, m.procs[n].status())
	}
	return out
}

// Status returns one command's status.
func (m *Manager) Status(name string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[name]
	if !ok {
		return Status{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return p.status(), nil
}

// Logs returns a command's ring buffer, creating it if the command has never
// started.
func (m *Manager) Logs(name string) (*logbuf.Buffer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err := p.ensureLogs(m.logDir); err != nil {
		return nil, err
	}
	return p.Logs, nil
}

func (p *Process) status() Status {
	s := Status{
		Name:      p.Name,
		State:     p.State,
		PID:       p.PID,
		ExitCode:  p.ExitCode,
		Restarts:  p.Restarts,
		SpecDirty: p.SpecDirty,
		DependsOn: p.Spec.DependsOn,
	}
	if p.State == Running && !p.Started.IsZero() {
		s.UptimeSec = time.Since(p.Started).Seconds()
	}
	return s
}

// ensureLogs creates the ring buffer, attaching the disk tee when log: true.
func (p *Process) ensureLogs(logDir string) error {
	if p.Logs != nil {
		return nil
	}
	p.Logs = logbuf.New(p.Spec.Size)
	if !p.Spec.Log {
		return nil
	}
	return p.Logs.AttachFile(filepath.Join(logDir, logbuf.FileName(p.Name)))
}
