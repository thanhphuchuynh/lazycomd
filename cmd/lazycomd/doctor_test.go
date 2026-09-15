package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

func TestDoctorFindings(t *testing.T) {
	list := []manager.Status{{Name: "api"}, {Name: "web"}, {Name: "lonely"}}
	specs := map[string]config.Command{
		"api":    {Cmd: []string{"go", "run", "."}, Port: 8080, Cwd: "/gone"},
		"web":    {Cmd: []string{"npm", "start"}, Port: 8080, DependsOn: []string{"api"}},
		"lonely": {Cmd: []string{"sleep", "1"}, DependsOn: []string{"ghost"}, Health: "http://localhost:9/x"},
	}
	exists := func(p string) bool { return p != "/gone" && p != "/moved" }

	got := check(list, specs, map[string]string{"app": "/moved"}, exists)
	want := []string{
		"port 8080 is claimed by [api web]",
		"cwd does not exist: /gone",
		"depends_on names an unknown command: ghost",
		"has health but no port",
		`project "app" points at a folder that is gone`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d findings, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if !strings.Contains(got[i].Message, w) {
			t.Fatalf("finding %d = %q, want it to mention %q", i, got[i].Message, w)
		}
	}
	// The port clash and the missing cwd break a start; the rest only might.
	if got[0].Level != "error" || got[3].Level != "warn" {
		t.Fatalf("levels = %q, %q", got[0].Level, got[3].Level)
	}
}

func TestDoctorIsQuietOnAHealthyCatalog(t *testing.T) {
	list := []manager.Status{{Name: "api"}}
	specs := map[string]config.Command{"api": {Cmd: []string{"go", "run", "."}, Port: 8080, Cwd: "/here"}}
	if got := check(list, specs, nil, func(string) bool { return true }); len(got) != 0 {
		t.Fatalf("healthy catalog reported %+v", got)
	}
}

func TestDoctorJSONAndExitCode(t *testing.T) {
	testDaemon(t, map[string]config.Command{
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/definitely/not/here"},
	})

	out := capture(t, func() {
		// An error-level finding exits 1, so a script can gate on it.
		if code := dispatch([]string{"doctor", "--json"}); code != 1 {
			t.Errorf("doctor = %d, want 1", code)
		}
	})
	var findings []Finding
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	if len(findings) != 1 || findings[0].Command != "api" || findings[0].Level != "error" {
		t.Fatalf("findings = %+v", findings)
	}
}
