package probe

import (
	"context"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func haveAny(names ...string) bool {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	return false
}

func TestRealCollectorsSeeTheMachine(t *testing.T) {
	if !haveAny("lsof", "ss") {
		t.Skip("neither lsof nor ss is installed")
	}

	// A listener this test owns must show up in the real port list.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	want := l.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ports, err := collectPorts(ctx, execRun)
	if err != nil {
		t.Fatalf("collectPorts: %v", err)
	}
	found := false
	for _, p := range ports {
		if p.Port == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("port %d not in the real listing of %d ports", want, len(ports))
	}
}

func TestRealVitalsSeeThisProcess(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Ask about our own pid first, to learn the group ps puts us in.
	self := os.Getpid()
	_, groups, err := collectVitals(ctx, execRun, map[string]int{"self": self})
	if err != nil {
		t.Fatalf("collectVitals: %v", err)
	}
	pgid, ok := groups[self]
	if !ok {
		t.Fatalf("ps did not report this process (%d) at all", self)
	}

	vitals, _, err := collectVitals(ctx, execRun, map[string]int{"self": pgid})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := vitals["self"]
	if !ok {
		t.Fatalf("no vitals for our own process group %d", pgid)
	}
	if v.MemMB <= 0 {
		t.Fatalf("MemMB = %v, want a real measurement", v.MemMB)
	}
}
