package probe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestSampler wires a sampler to canned command output and fast tickers.
func newTestSampler(t *testing.T, run runFunc, pids map[string]int, urls map[string]string, intended map[string]int) *Sampler {
	t.Helper()
	s := New(
		func() map[string]int { return pids },
		func() map[string]string { return urls },
		func() map[string]int { return intended },
	)
	s.run = run
	s.PortsEvery = 10 * time.Millisecond
	s.VitalsEvery = 10 * time.Millisecond
	s.HealthEvery = 10 * time.Millisecond
	return s
}

// waitSnap polls until cond holds or the deadline passes.
func waitSnap(t *testing.T, s *Sampler, what string, cond func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last Snapshot
	for time.Now().Before(deadline) {
		last = s.Snapshot()
		if cond(last) {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; snapshot = %+v", what, last)
	return last
}

func TestSamplerFillsEverySection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)},
		"ps":   {out: []byte(psSample)},
	})
	s := newTestSampler(t, run,
		map[string]int{"app:web": 100},
		map[string]string{"app:web": srv.URL},
		map[string]int{"app:web": 3000},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	snap := waitSnap(t, s, "all three sections", func(s Snapshot) bool {
		return len(s.Ports) > 0 && len(s.Vitals) > 0 && len(s.Health) > 0
	})
	if len(snap.Errors) != 0 {
		t.Fatalf("errors = %v, want none", snap.Errors)
	}
	for _, section := range []string{"ports", "vitals", "health"} {
		if snap.SampledAt[section].IsZero() {
			t.Fatalf("no timestamp for %q: %v", section, snap.SampledAt)
		}
	}
	if !snap.Health["app:web"].OK {
		t.Fatalf("health = %+v, want up", snap.Health["app:web"])
	}
}

func TestSamplerAttributesPortsOnceVitalsLand(t *testing.T) {
	const psWithNode = `  100   100  2.5  40960
51192   100 30.0 100000
`
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)},
		"ps":   {out: []byte(psWithNode)},
	})
	s := newTestSampler(t, run, map[string]int{"app:web": 100}, nil, map[string]int{"app:web": 3000})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	snap := waitSnap(t, s, "port ownership", func(s Snapshot) bool {
		for _, p := range s.Ports {
			if p.Port == 3000 && p.Command == "app:web" {
				return true
			}
		}
		return false
	})
	if len(snap.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none: we own the port", snap.Conflicts)
	}
}

func TestSamplerOneBrokenCollectorLeavesTheOthers(t *testing.T) {
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch name {
		case "ps":
			return []byte(psSample), nil
		default: // lsof and ss both fail
			return nil, errors.New("not installed")
		}
	}
	s := newTestSampler(t, run, map[string]int{"app:web": 100}, nil, map[string]int{"app:web": 3000})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	snap := waitSnap(t, s, "the ports error", func(s Snapshot) bool {
		return s.Errors["ports"] != "" && len(s.Vitals) > 0
	})
	if len(snap.Ports) != 0 {
		t.Fatalf("ports = %+v, want none", snap.Ports)
	}
	// The whole point: no port list means no conflict guessing.
	if len(snap.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none while the ports collector is broken", snap.Conflicts)
	}
	if snap.Errors["vitals"] != "" {
		t.Fatalf("vitals error = %q, want vitals unaffected", snap.Errors["vitals"])
	}
}

func TestSamplerReportsTakenPorts(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)}, // node on 3000, pid 51192
		"ps":   {out: []byte(psSample)},   // pid 51192 is not in any of our groups
	})
	s := newTestSampler(t, run, map[string]int{"app:web": 100}, nil, map[string]int{"app:web": 3000})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	snap := waitSnap(t, s, "the taken conflict", func(s Snapshot) bool {
		return len(s.Conflicts) > 0
	})
	c := snap.Conflicts[0]
	if c.State != "taken" || c.Command != "app:web" || c.HeldBy != "node" {
		t.Fatalf("conflict = %+v", c)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)},
		"ps":   {out: []byte(psSample)},
	})
	s := newTestSampler(t, run, map[string]int{"app:web": 100}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitSnap(t, s, "a first sample", func(s Snapshot) bool { return len(s.Ports) > 0 })

	snap := s.Snapshot()
	snap.Ports[0].Process = "tampered"
	snap.Vitals["injected"] = Vital{}
	snap.Errors["ports"] = "injected"

	again := s.Snapshot()
	if again.Ports[0].Process == "tampered" {
		t.Fatal("Snapshot shares its ports slice with the sampler")
	}
	if _, bad := again.Vitals["injected"]; bad {
		t.Fatal("Snapshot shares its vitals map with the sampler")
	}
	if again.Errors["ports"] == "injected" {
		t.Fatal("Snapshot shares its errors map with the sampler")
	}
}

func TestSamplerStopsOnContextCancel(t *testing.T) {
	calls := make(chan struct{}, 1000)
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return []byte(psSample), nil
	}
	s := newTestSampler(t, run, map[string]int{"a": 1}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	waitSnap(t, s, "a first sample", func(s Snapshot) bool { return !s.SampledAt["vitals"].IsZero() })

	cancel()
	time.Sleep(60 * time.Millisecond)
	drain := len(calls)
	time.Sleep(100 * time.Millisecond)
	if len(calls) > drain {
		t.Fatal("sampling continued after the context was cancelled")
	}
}
