package probe

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Default sampling intervals. Ports are the most expensive and the least
// volatile; vitals move fastest; health is a network round trip.
const (
	DefaultPortsEvery  = 5 * time.Second
	DefaultVitalsEvery = 2 * time.Second
	DefaultHealthEvery = 10 * time.Second
)

// Sampler keeps one snapshot of the machine up to date. It learns about
// commands through three closures, so it depends on no other package of ours.
type Sampler struct {
	mu     sync.Mutex
	snap   Snapshot
	groups map[int]int // pid -> pgid, refreshed by the vitals loop

	pids     func() map[string]int
	urls     func() map[string]string
	intended func() map[string]int

	run   runFunc
	httpc *http.Client
	now   func() time.Time

	PortsEvery  time.Duration
	VitalsEvery time.Duration
	HealthEvery time.Duration
}

// New builds a Sampler. The closures are called on every tick rather than
// captured, so a config reload is picked up with no extra wiring.
func New(pids func() map[string]int, urls func() map[string]string, intended func() map[string]int) *Sampler {
	return &Sampler{
		snap:        EmptySnapshot(),
		groups:      map[int]int{},
		pids:        pids,
		urls:        urls,
		intended:    intended,
		run:         execRun,
		httpc:       newHTTPClient(),
		now:         time.Now,
		PortsEvery:  DefaultPortsEvery,
		VitalsEvery: DefaultVitalsEvery,
		HealthEvery: DefaultHealthEvery,
	}
}

// Start runs the three sampling loops until ctx is cancelled. It returns
// immediately.
func (s *Sampler) Start(ctx context.Context) {
	// Vitals first: it publishes the pid->pgid map the ports loop needs to
	// attribute a socket. A ports sample that lands first simply shows no
	// owner until the next one.
	go s.loop(ctx, s.VitalsEvery, s.sampleVitals)
	go s.loop(ctx, s.PortsEvery, s.samplePorts)
	go s.loop(ctx, s.HealthEvery, s.sampleHealth)
}

func (s *Sampler) loop(ctx context.Context, every time.Duration, sample func(context.Context)) {
	sample(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sample(ctx)
		}
	}
}

func (s *Sampler) sampleVitals(ctx context.Context) {
	vitals, groups, err := collectVitals(ctx, s.run, s.pids())

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.SampledAt[sectionVitals] = s.now()
	if err != nil {
		s.snap.Errors[sectionVitals] = err.Error()
		return
	}
	delete(s.snap.Errors, sectionVitals)
	s.snap.Vitals = vitals
	s.groups = groups
}

func (s *Sampler) samplePorts(ctx context.Context) {
	// Read the closures before taking our lock: they take the manager's.
	pids := s.pids()
	intended := s.intended()
	ports, err := collectPorts(ctx, s.run)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.SampledAt[sectionPorts] = s.now()
	if err != nil {
		s.snap.Errors[sectionPorts] = err.Error()
		s.snap.Ports = nil
		// No port list means no conflict guessing: every intended port would
		// look free, which would blame every command for a broken collector.
		s.snap.Conflicts = nil
		return
	}
	delete(s.snap.Errors, sectionPorts)
	s.snap.Ports = applyOwners(ports, s.groups, pids)
	s.snap.Conflicts = conflicts(intended, s.snap.Ports, pids)
}

func (s *Sampler) sampleHealth(ctx context.Context) {
	// Only running commands appear in urls(), so a stopped command's entry
	// disappears rather than going stale.
	health := collectHealth(ctx, s.httpc, s.urls())

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.SampledAt[sectionHealth] = s.now()
	s.snap.Health = health
}

// Snapshot returns a deep copy, safe to serialize or mutate.
func (s *Sampler) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := Snapshot{
		Ports:     append([]Port{}, s.snap.Ports...),
		Conflicts: append([]Conflict{}, s.snap.Conflicts...),
		Vitals:    make(map[string]Vital, len(s.snap.Vitals)),
		Health:    make(map[string]Health, len(s.snap.Health)),
		SampledAt: make(map[string]time.Time, len(s.snap.SampledAt)),
		Errors:    make(map[string]string, len(s.snap.Errors)),
	}
	for k, v := range s.snap.Vitals {
		out.Vitals[k] = v
	}
	for k, v := range s.snap.Health {
		out.Health[k] = v
	}
	for k, v := range s.snap.SampledAt {
		out.SampledAt[k] = v
	}
	for k, v := range s.snap.Errors {
		out.Errors[k] = v
	}
	return out
}
