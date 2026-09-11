package probe

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeRun answers by command name, so a test can make lsof missing and ss
// present, or both fail.
func fakeRun(answers map[string]struct {
	out []byte
	err error
}) runFunc {
	return func(_ context.Context, name string, _ ...string) ([]byte, error) {
		a, ok := answers[name]
		if !ok {
			return nil, exec.ErrNotFound
		}
		return a.out, a.err
	}
}

func TestCollectPortsUsesLsof(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample)},
	})

	got, err := collectPorts(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Port != 3000 {
		t.Fatalf("ports = %+v", got)
	}
}

func TestCollectPortsTrustsOutputOverExitStatus(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: []byte(lsofSample), err: errors.New("exit status 1")},
	})

	got, err := collectPorts(context.Background(), run)
	if err != nil {
		t.Fatalf("err = %v, want the parsed output instead", err)
	}
	if len(got) != 3 {
		t.Fatalf("ports = %+v", got)
	}
}

func TestCollectPortsFallsBackToSS(t *testing.T) {
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"ss": {out: []byte(ssSample)}, // lsof is absent: fakeRun returns ErrNotFound
	})

	got, err := collectPorts(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Process != "node" {
		t.Fatalf("ports = %+v, want the ss rows", got)
	}
}

func TestCollectPortsBothMissing(t *testing.T) {
	got, err := collectPorts(context.Background(), fakeRun(nil))
	if err == nil {
		t.Fatal("err = nil, want an error naming both tools")
	}
	if !strings.Contains(err.Error(), "lsof") || !strings.Contains(err.Error(), "ss") {
		t.Fatalf("err = %v, want both tools named", err)
	}
	if got != nil {
		t.Fatalf("ports = %+v, want nil", got)
	}
}

func TestCollectPortsNothingListeningIsNotAnError(t *testing.T) {
	// lsof ran cleanly and printed nothing: the machine has no listeners.
	run := fakeRun(map[string]struct {
		out []byte
		err error
	}{
		"lsof": {out: nil, err: nil},
	})

	got, err := collectPorts(context.Background(), run)
	if err != nil {
		t.Fatalf("err = %v, want an empty list", err)
	}
	if len(got) != 0 {
		t.Fatalf("ports = %+v, want none", got)
	}
}

func TestOwnerOfUsesTheProcessGroup(t *testing.T) {
	// app:web is pid 100; its grandchild 137 holds the socket.
	pids := map[string]int{"app:web": 100, "other": 200}
	groups := map[int]int{100: 100, 137: 100, 200: 200, 999: 999}

	if got := ownerOf(137, groups, pids); got != "app:web" {
		t.Fatalf("ownerOf(grandchild) = %q, want app:web", got)
	}
	if got := ownerOf(100, groups, pids); got != "app:web" {
		t.Fatalf("ownerOf(leader) = %q, want app:web", got)
	}
	if got := ownerOf(999, groups, pids); got != "" {
		t.Fatalf("ownerOf(stranger) = %q, want empty", got)
	}
	if got := ownerOf(42, groups, pids); got != "" {
		t.Fatalf("ownerOf(unknown pid) = %q, want empty", got)
	}
}

func TestApplyOwners(t *testing.T) {
	ports := []Port{
		{Port: 3000, PID: 137, Process: "node"},
		{Port: 5432, PID: 999, Process: "postgres"},
	}
	got := applyOwners(ports, map[int]int{137: 100, 999: 999}, map[string]int{"app:web": 100})

	if got[0].Command != "app:web" {
		t.Fatalf("row 0 = %+v, want owned by app:web", got[0])
	}
	if got[1].Command != "" {
		t.Fatalf("row 1 = %+v, want no owner", got[1])
	}
}
