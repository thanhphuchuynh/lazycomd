package website

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPagesExist(t *testing.T) {
	want := []string{"index.md", "install.md", "tui.md", "cli.md", "configuration.md", "api.md", "behavior.md"}
	for _, p := range want {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestConfigIsGitHubPages(t *testing.T) {
	b, err := os.ReadFile("_config.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "baseurl: /lazycomd") {
		t.Fatalf("_config.yml missing GitHub Pages baseurl:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join("_layouts", "default.html")); err != nil {
		t.Fatal(err)
	}
}
