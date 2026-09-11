// Package depsguard holds no code. Its test keeps the daemon's dependency
// tree clean: the TUI's terminal libraries must never reach the packages
// that run processes.
package depsguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// daemonPkgs are the packages that must stay free of TUI dependencies.
var daemonPkgs = []string{
	"github.com/thanhphuchuynh/lazycomd/internal/config",
	"github.com/thanhphuchuynh/lazycomd/internal/manager",
	"github.com/thanhphuchuynh/lazycomd/internal/logbuf",
	"github.com/thanhphuchuynh/lazycomd/internal/api",
}

// banned substrings that must not appear in those packages' dependency trees.
var banned = []string{
	"charmbracelet/bubbletea",
	"charmbracelet/bubbles",
	"charmbracelet/lipgloss",
	"muesli/termenv",
}

func TestModulePath(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(b), "\n")
	want := "module github.com/thanhphuchuynh/lazycomd"
	if first != want {
		t.Fatalf("go.mod first line = %q, want %q", first, want)
	}
}

func TestDaemonPackagesHaveNoTUIDependencies(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	out, err := exec.Command("go", append([]string{"list", "-deps"}, daemonPkgs...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")

	// Sanity check: the list really did resolve our packages.
	var sawYAML bool
	for _, d := range deps {
		if strings.Contains(d, "gopkg.in/yaml.v3") {
			sawYAML = true
		}
		for _, b := range banned {
			if strings.Contains(d, b) {
				t.Errorf("daemon packages depend on %s (via %s)", b, d)
			}
		}
	}
	if !sawYAML {
		t.Fatalf("go list -deps returned %d entries but no yaml.v3; did the package list change?", len(deps))
	}
}
