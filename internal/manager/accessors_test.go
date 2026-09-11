package manager

import (
	"testing"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

func TestAccessorsCoverTheRightCommands(t *testing.T) {
	m := testManager(t, map[string]config.Command{
		"up":      {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", Health: "http://localhost:3000/h", Port: 3000},
		"down":    {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", Health: "http://localhost:4310/h"},
		"noprobe": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp"},
	})
	defer m.Shutdown()

	if err := m.Start("up"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "up", Running)

	pids := m.RunningPIDs()
	if len(pids) != 1 || pids["up"] <= 0 {
		t.Fatalf("RunningPIDs = %v, want just up", pids)
	}

	urls := m.HealthURLs()
	if len(urls) != 1 || urls["up"] != "http://localhost:3000/h" {
		t.Fatalf("HealthURLs = %v, want just the running command with a URL", urls)
	}

	// Intended ports cover every command, running or not: a stopped command
	// whose port is taken is exactly the case worth reporting.
	ports := m.IntendedPorts()
	if len(ports) != 2 || ports["up"] != 3000 || ports["down"] != 4310 {
		t.Fatalf("IntendedPorts = %v, want up:3000 and down:4310", ports)
	}
	if _, ok := ports["noprobe"]; ok {
		t.Fatal("a command with neither port: nor health: should have no intended port")
	}
}

func TestStatusCarriesTheProbeFields(t *testing.T) {
	m := testManager(t, map[string]config.Command{"a": sleeper()})
	st, err := m.Status("a")
	if err != nil {
		t.Fatal(err)
	}
	// The manager never fills these; the API layer does. They must exist and
	// stay zero here.
	if st.CPU != 0 || st.MemMB != 0 || st.Health != nil {
		t.Fatalf("manager populated probe fields: %+v", st)
	}
}
