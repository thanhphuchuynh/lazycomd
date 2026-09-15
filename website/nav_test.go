package website

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPagesExist(t *testing.T) {
	want := []string{"index.md", "install.md", "tui.md", "cli.md", "configuration.md", "api.md", "agents.md", "behavior.md"}
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

// The page GIFs are checked in, since GitHub Pages serves them straight from
// the repo. record.sh regenerates all three.
func TestRecordingsArePresent(t *testing.T) {
	for _, gif := range []string{"demo.gif", "cli.gif", "mcp.gif"} {
		fi, err := os.Stat(gif)
		if err != nil {
			t.Errorf("missing %s: %v", gif, err)
			continue
		}
		if fi.Size() < 10*1024 {
			t.Errorf("%s is %d bytes, which is too small to be a real recording", gif, fi.Size())
		}
	}
	if _, err := os.Stat("record.sh"); err != nil {
		t.Errorf("record.sh should be checked in so the GIFs can be remade: %v", err)
	}
}
