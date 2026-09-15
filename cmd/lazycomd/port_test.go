package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

func TestFilterAndSortPorts(t *testing.T) {
	ports := []probe.Port{
		{Port: 3000, Addr: "::1", PID: 2},
		{Port: 8080, Addr: "*", PID: 3},
		{Port: 3000, Addr: "127.0.0.1", PID: 1},
	}
	sortPorts(ports)
	if ports[0].Port != 3000 || ports[0].Addr != "127.0.0.1" || ports[2].Port != 8080 {
		t.Fatalf("sorted = %+v", ports)
	}
	if got := filterPort(ports, 3000); len(got) != 2 {
		t.Fatalf("filter 3000 = %+v", got)
	}
	if got := filterPort(ports, 9999); got != nil {
		t.Fatalf("filter on a free port = %+v, want nil", got)
	}
}

func TestPortUsage(t *testing.T) {
	for _, args := range [][]string{{"port", "70000"}, {"port", "nope"}, {"port", "1", "2"}} {
		if code := dispatch(args); code != 2 {
			t.Fatalf("%v = %d, want 2", args, code)
		}
	}
}

func TestPortJSONIsAlwaysAList(t *testing.T) {
	testDaemon(t, map[string]config.Command{"api": {Cmd: []string{"sleep", "30"}}})

	out := capture(t, func() {
		if code := dispatch([]string{"port", "65535", "--json"}); code != 0 {
			t.Errorf("port = %d, want 0", code)
		}
	})
	var ports []probe.Port
	if err := json.Unmarshal([]byte(out), &ports); err != nil {
		t.Fatalf("port --json is not JSON: %v\n%s", err, out)
	}
	if len(ports) != 0 {
		t.Fatalf("something is listening on 65535 in this test: %+v", ports)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("empty result should still be a list: %q", out)
	}
}

func TestLsJSON(t *testing.T) {
	testDaemon(t, map[string]config.Command{"api": {Cmd: []string{"sleep", "30"}}})

	out := capture(t, func() {
		if code := dispatch([]string{"ls", "--json"}); code != 0 {
			t.Errorf("ls --json = %d, want 0", code)
		}
	})
	var list []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("ls --json is not JSON: %v\n%s", err, out)
	}
	if len(list) != 1 || list[0].Name != "api" || list[0].State != "stopped" {
		t.Fatalf("list = %+v", list)
	}
}
