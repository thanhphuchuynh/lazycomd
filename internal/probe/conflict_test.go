package probe

import "testing"

func TestConflictNoneWhenTheCommandOwnsItsPort(t *testing.T) {
	got := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 3000, PID: 137, Process: "node", Command: "web"}},
		map[string]int{"web": 100},
	)
	if len(got) != 0 {
		t.Fatalf("conflicts = %+v, want none", got)
	}
}

func TestConflictTakenNamesTheSquatter(t *testing.T) {
	got := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 3000, PID: 51192, Process: "node"}}, // no Command: not ours
		map[string]int{"web": 100},
	)
	if len(got) != 1 {
		t.Fatalf("conflicts = %+v, want one", got)
	}
	c := got[0]
	if c.State != "taken" || c.Command != "web" || c.Port != 3000 {
		t.Fatalf("conflict = %+v", c)
	}
	if c.HeldBy != "node" || c.PID != 51192 {
		t.Fatalf("conflict = %+v, want the squatter named", c)
	}
}

func TestConflictTakenIsReportedForAStoppedCommand(t *testing.T) {
	// The most useful case: npm start just died because something else holds
	// the port. The command is not running, and that is exactly when this
	// matters.
	got := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 3000, PID: 51192, Process: "node"}},
		map[string]int{}, // nothing running
	)
	if len(got) != 1 || got[0].State != "taken" {
		t.Fatalf("conflicts = %+v, want taken for a stopped command", got)
	}
}

func TestConflictFreeOnlyForRunningCommands(t *testing.T) {
	running := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 5432, PID: 1183, Process: "postgres"}},
		map[string]int{"web": 100},
	)
	if len(running) != 1 || running[0].State != "free" {
		t.Fatalf("conflicts = %+v, want free for a running command with nothing bound", running)
	}

	stopped := conflicts(
		map[string]int{"web": 3000},
		[]Port{{Port: 5432, PID: 1183, Process: "postgres"}},
		map[string]int{},
	)
	if len(stopped) != 0 {
		t.Fatalf("conflicts = %+v, want nothing: a stopped command not listening is normal", stopped)
	}
}

func TestConflictOwnershipWinsOverAnotherRowOnTheSamePort(t *testing.T) {
	got := conflicts(
		map[string]int{"web": 3000},
		[]Port{
			{Addr: "*", Port: 3000, PID: 999, Process: "other"},
			{Addr: "127.0.0.1", Port: 3000, PID: 137, Process: "node", Command: "web"},
		},
		map[string]int{"web": 100},
	)
	if len(got) != 0 {
		t.Fatalf("conflicts = %+v, want none when one of the rows is ours", got)
	}
}

func TestConflictsAreSorted(t *testing.T) {
	got := conflicts(
		map[string]int{"b": 5000, "a": 5000, "c": 3000},
		[]Port{
			{Port: 3000, PID: 1, Process: "x"},
			{Port: 5000, PID: 2, Process: "y"},
		},
		map[string]int{},
	)
	if len(got) != 3 {
		t.Fatalf("conflicts = %+v, want three", got)
	}
	if got[0].Port != 3000 || got[1].Command != "a" || got[2].Command != "b" {
		t.Fatalf("conflicts not sorted by port then command: %+v", got)
	}
}
