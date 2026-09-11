package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes a global config plus project files and returns the global path.
// files maps a relative path to its contents.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.ReplaceAll(body, "@ROOT@", root)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "config.yaml")
}

func TestLoadNamespacesProjectCommands(t *testing.T) {
	g := tree(t, map[string]string{
		"config.yaml":           "projects:\n  - @ROOT@/scraper\ncommands:\n  proxy:\n    cmd: [\"sleep\", \"1\"]\n",
		"scraper/lazycomd.yaml": "commands:\n  api:\n    cmd: [\"sleep\", \"1\"]\n",
	})
	cfg, err := Load(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Commands["proxy"]; !ok {
		t.Fatalf("global command missing: %v", keys(cfg))
	}
	c, ok := cfg.Commands["scraper:api"]
	if !ok {
		t.Fatalf("namespaced command missing: %v", keys(cfg))
	}
	if !strings.HasSuffix(c.Cwd, "/scraper") {
		t.Fatalf("cwd = %q, want the project dir", c.Cwd)
	}
}

func TestLoadResolvesDepsProjectFirst(t *testing.T) {
	g := tree(t, map[string]string{
		"config.yaml":       "projects:\n  - @ROOT@/app\ncommands:\n  db:\n    cmd: [\"sleep\", \"1\"]\n",
		"app/lazycomd.yaml": "commands:\n  db:\n    cmd: [\"sleep\", \"1\"]\n  api:\n    cmd: [\"sleep\", \"1\"]\n    depends_on: [\"db\"]\n",
	})
	cfg, err := Load(g)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Commands["app:api"].DependsOn
	if len(got) != 1 || got[0] != "app:db" {
		t.Fatalf("depends_on = %v, want [app:db]", got)
	}
}

func TestLoadResolvesDepsFallsBackToGlobal(t *testing.T) {
	g := tree(t, map[string]string{
		"config.yaml":       "projects:\n  - @ROOT@/app\ncommands:\n  db:\n    cmd: [\"sleep\", \"1\"]\n",
		"app/lazycomd.yaml": "commands:\n  api:\n    cmd: [\"sleep\", \"1\"]\n    depends_on: [\"db\"]\n",
	})
	cfg, err := Load(g)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Commands["app:api"].DependsOn
	if len(got) != 1 || got[0] != "db" {
		t.Fatalf("depends_on = %v, want [db]", got)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("basename collision", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml":         "projects:\n  - @ROOT@/a/api\n  - @ROOT@/b/api\ncommands: {}\n",
			"a/api/lazycomd.yaml": "commands: {}\n",
			"b/api/lazycomd.yaml": "commands: {}\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "basename") {
			t.Fatalf("err = %v, want a basename collision error", err)
		}
	})
	t.Run("global key in project file", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml":       "projects:\n  - @ROOT@/app\ncommands: {}\n",
			"app/lazycomd.yaml": "listen: \":1\"\ncommands: {}\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "global-only") {
			t.Fatalf("err = %v, want a global-only error", err)
		}
	})
	t.Run("unknown dependency", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml": "commands:\n  a:\n    cmd: [\"x\"]\n    depends_on: [\"ghost\"]\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "unknown dependency") {
			t.Fatalf("err = %v, want an unknown dependency error", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml": "commands:\n  a:\n    cmd: [\"x\"]\n    depends_on: [\"b\"]\n  b:\n    cmd: [\"x\"]\n    depends_on: [\"a\"]\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("err = %v, want a cycle error", err)
		}
	})
	t.Run("self cycle", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml": "commands:\n  a:\n    cmd: [\"x\"]\n    depends_on: [\"a\"]\n",
		})
		if _, err := Load(g); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("err = %v, want a cycle error", err)
		}
	})
	t.Run("missing project file", func(t *testing.T) {
		g := tree(t, map[string]string{
			"config.yaml": "projects:\n  - @ROOT@/gone\ncommands: {}\n",
		})
		if _, err := Load(g); err == nil {
			t.Fatal("err = nil, want a missing file error")
		}
	})
}

func TestExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ExpandUser("~/x"), filepath.Join(home, "x"); got != want {
		t.Fatalf("ExpandUser(~/x) = %q, want %q", got, want)
	}
	if got := ExpandUser("~x"); got != "~x" {
		t.Fatalf("ExpandUser(~x) = %q, want it untouched", got)
	}
}

func keys(c *Config) []string {
	out := make([]string, 0, len(c.Commands))
	for k := range c.Commands {
		out = append(out, k)
	}
	return out
}

func TestLoadKeepsTheProjectMap(t *testing.T) {
	g := tree(t, map[string]string{
		"config.yaml":           "projects:\n  - @ROOT@/scraper\ncommands: {}\n",
		"scraper/lazycomd.yaml": "commands:\n  api:\n    cmd: [\"sleep\", \"1\"]\n",
	})
	cfg, err := Load(g)
	if err != nil {
		t.Fatal(err)
	}
	dir, ok := cfg.Projects["scraper"]
	if !ok {
		t.Fatalf("Projects = %v, want a scraper entry", cfg.Projects)
	}
	if !strings.HasSuffix(dir, "/scraper") {
		t.Fatalf("Projects[scraper] = %q, want the project directory", dir)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("Projects = %v, want exactly one entry", cfg.Projects)
	}
}

func TestParseBytes(t *testing.T) {
	f, err := ParseBytes([]byte("commands:\n  a:\n    cmd: [\"x\"]\n"), "memory")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(f.Commands) != 1 {
		t.Fatalf("commands = %v", f.Commands)
	}

	_, err = ParseBytes([]byte("listten: 1\n"), "spliced.yaml")
	if err == nil || !strings.Contains(err.Error(), "spliced.yaml") {
		t.Fatalf("err = %v, want it to name the buffer", err)
	}
}
