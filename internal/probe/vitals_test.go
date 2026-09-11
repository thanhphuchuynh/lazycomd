package probe

import (
	"context"
	"errors"
	"testing"
)

// Real `ps -eo pid=,pgid=,%cpu=,rss=` output: a command at pid 100 with a
// grandchild at 137 in the same group, plus unrelated processes.
const psSample = `    1     1   0.0   12345
  100   100   2.5   40960
  137   100 142.3  326000
  200   200   0.1    2048
`

func TestParsePS(t *testing.T) {
	rows, groups := parsePS([]byte(psSample))

	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4: %+v", len(rows), rows)
	}
	if rows[2].pid != 137 || rows[2].pgid != 100 || rows[2].cpu != 142.3 || rows[2].rssKB != 326000 {
		t.Fatalf("row 2 = %+v", rows[2])
	}
	if groups[137] != 100 || groups[200] != 200 {
		t.Fatalf("groups = %v", groups)
	}
}

func TestParsePSIgnoresGarbage(t *testing.T) {
	rows, groups := parsePS([]byte("PID PGID %CPU RSS\nnonsense\n  7\n"))
	if len(rows) != 0 || len(groups) != 0 {
		t.Fatalf("rows = %+v groups = %v, want nothing", rows, groups)
	}
}

func TestCollectVitalsSumsTheProcessGroup(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"ps": {out: []byte(psSample)},
	})

	vitals, groups, err := collectVitals(context.Background(), run, map[string]int{"app:web": 100})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := vitals["app:web"]
	if !ok {
		t.Fatalf("vitals = %+v, want app:web", vitals)
	}
	// 2.5 from the parent plus 142.3 from the grandchild.
	if v.CPU < 144.7 || v.CPU > 144.9 {
		t.Fatalf("CPU = %v, want ~144.8 summed across the group", v.CPU)
	}
	// (40960 + 326000) KB / 1024.
	if v.MemMB < 358.3 || v.MemMB > 358.5 {
		t.Fatalf("MemMB = %v, want ~358.4", v.MemMB)
	}
	if v.PID != 100 {
		t.Fatalf("PID = %d, want the command's own pid", v.PID)
	}
	if groups[137] != 100 {
		t.Fatalf("groups not returned for the ports collector: %v", groups)
	}
}

func TestCollectVitalsSkipsWorkWithNoRunningCommands(t *testing.T) {
	called := false
	run := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}

	vitals, groups, err := collectVitals(context.Background(), run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("ps was run with no commands running")
	}
	if len(vitals) != 0 || len(groups) != 0 {
		t.Fatalf("vitals = %+v groups = %v, want empty", vitals, groups)
	}
}

func TestCollectVitalsReportsAFailure(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"ps": {err: errors.New("boom")},
	})

	if _, _, err := collectVitals(context.Background(), run, map[string]int{"a": 1}); err == nil {
		t.Fatal("err = nil, want the ps failure")
	}
}

func TestCollectVitalsOmitsCommandsWithNoProcesses(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"ps": {out: []byte(psSample)},
	})

	vitals, _, err := collectVitals(context.Background(), run, map[string]int{"ghost": 4242})
	if err != nil {
		t.Fatal(err)
	}
	if len(vitals) != 0 {
		t.Fatalf("vitals = %+v, want nothing for a pid ps never saw", vitals)
	}
}
