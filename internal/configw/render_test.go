package configw

import (
	"slices"
	"strings"
	"testing"

	"github.com/tphuc/lazycomd/internal/config"
)

func TestRenderBlockMinimal(t *testing.T) {
	got, err := renderBlock("web", config.Command{Cmd: []string{"npm", "start"}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"  web:",
		"    cmd:",
		"      - npm",
		"      - start",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("renderBlock =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRenderBlockOmitsEmptyFields(t *testing.T) {
	got, err := renderBlock("web", config.Command{
		Cmd:     []string{"npm", "start"},
		Cwd:     "/tmp",
		Restart: config.RestartOnFailure,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"cwd: /tmp", "restart: on-failure"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("block missing %q:\n%s", want, joined)
		}
	}
	for _, gone := range []string{"env", "shell", "autostart", "log", "size", "depends_on", "health", "port"} {
		if strings.Contains(joined, gone+":") {
			t.Fatalf("block carries an unset field %q:\n%s", gone, joined)
		}
	}
}

func TestRenderBlockDropsTheDefaultRestart(t *testing.T) {
	got, _ := renderBlock("web", config.Command{Cmd: []string{"x"}, Restart: config.RestartNo}, 2)
	if strings.Contains(strings.Join(got, "\n"), "restart") {
		t.Fatalf("block writes the default restart:\n%s", strings.Join(got, "\n"))
	}
}

func TestRenderBlockKeepsRicherFields(t *testing.T) {
	got, err := renderBlock("api", config.Command{
		Cmd:       []string{"node", "server.js"},
		Env:       map[string]string{"LOG": "debug"},
		DependsOn: []string{"db"},
		Health:    "http://localhost:3000/healthz",
		Port:      3000,
		Autostart: true,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"env:", "LOG: debug", "depends_on:", "- db", "health: http://localhost:3000/healthz", "port: 3000", "autostart: true"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("block missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderBlockIndent(t *testing.T) {
	got, err := renderBlock("web", config.Command{Cmd: []string{"x"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "    web:" {
		t.Fatalf("first line = %q, want four spaces of indent", got[0])
	}
	for _, l := range got[1:] {
		if !strings.HasPrefix(l, "      ") {
			t.Fatalf("child line not indented under the key: %q", l)
		}
	}
}

func TestRenderBlockRoundTrips(t *testing.T) {
	in := config.Command{
		Cmd:     []string{"sh", "-c", "echo hi && sleep 1"},
		Cwd:     "/tmp",
		Shell:   true,
		Restart: config.RestartAlways,
		Port:    8099,
	}
	lines, err := renderBlock("web", in, 2)
	if err != nil {
		t.Fatal(err)
	}
	doc := "commands:\n" + strings.Join(lines, "\n") + "\n"

	f, err := config.ParseBytes([]byte(doc), "rendered")
	if err != nil {
		t.Fatalf("rendered block does not parse: %v\n%s", err, doc)
	}
	got := f.Commands["web"]
	if !slices.Equal(got.Cmd, in.Cmd) || got.Cwd != in.Cwd || !got.Shell || got.Restart != in.Restart || got.Port != in.Port {
		t.Fatalf("round trip lost fields: %+v", got)
	}
}
