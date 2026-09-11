package tui

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tphuc/lazycomd/internal/api"
	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/configw"
	"github.com/tphuc/lazycomd/internal/manager"
)

// writableModel is a model wired to a daemon over a real config file.
func writableModel(t *testing.T, body string) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := manager.New(cfg, t.TempDir())
	m.Grace = 500 * time.Millisecond
	t.Cleanup(m.Shutdown)

	s := api.New(api.Options{
		Manager:    m,
		Reload:     func() (*config.Config, error) { return config.Load(path) },
		ConfigPath: path,
		Writers:    configw.NewRegistry(),
	})

	sockDir, err := os.MkdirTemp("/tmp", "lzc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s.sock")

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler()}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	c, err := client.New("unix://"+sock, "")
	if err != nil {
		t.Fatal(err)
	}
	sink, _ := captureSink()
	model := New(c, sink)
	next, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = next.(Model)
	t.Cleanup(func() { model.stream.stop() })
	return model, path
}

func TestEndToEndFormWritesTheFile(t *testing.T) {
	m, path := writableModel(t, "# keep me\ncommands:\n  web:\n    cmd: [\"sleep\", \"30\"]\n    cwd: /tmp\n")
	m = drive(t, m, fetchStatus(m.client))

	m, _ = step(t, m, key("a"))
	for _, r := range "extra" {
		m, _ = step(t, m, key(string(r)))
	}
	m, _ = step(t, m, key("tab"))
	for _, r := range "sleep 30" {
		m, _ = step(t, m, key(string(r)))
	}

	m, cmd := step(t, m, key("enter"))
	m = drive(t, m, cmd)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "extra:") {
		t.Fatalf("command not written:\n%s", data)
	}
	if !strings.Contains(string(data), "# keep me") {
		t.Fatalf("the header comment was lost:\n%s", data)
	}
	if m.overlay != overlayNone {
		t.Fatal("the form stayed open after a successful save")
	}
}

func TestEndToEndDeleteRemovesFromTheFile(t *testing.T) {
	m, path := writableModel(t, "commands:\n  web:\n    cmd: [\"sleep\", \"30\"]\n  other:\n    cmd: [\"sleep\", \"30\"]\n")
	m = drive(t, m, fetchStatus(m.client))

	m.table.SelectName("web")

	m, _ = step(t, m, key("d"))
	m, cmd := step(t, m, key("y"))
	m = drive(t, m, cmd)

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "web:") {
		t.Fatalf("web survived:\n%s", data)
	}
	if !strings.Contains(string(data), "other:") {
		t.Fatalf("other went with it:\n%s", data)
	}
}

func TestEndToEndDuplicateNameStaysInTheForm(t *testing.T) {
	m, _ := writableModel(t, "commands:\n  web:\n    cmd: [\"sleep\", \"30\"]\n")
	m = drive(t, m, fetchStatus(m.client))

	m, _ = step(t, m, key("a"))
	for _, r := range "web" {
		m, _ = step(t, m, key(string(r)))
	}
	m, _ = step(t, m, key("tab"))
	for _, r := range "sleep 30" {
		m, _ = step(t, m, key(string(r)))
	}
	m, cmd := step(t, m, key("enter"))
	m = drive(t, m, cmd)

	if m.overlay != overlayForm {
		t.Fatal("the form closed on a rejected save")
	}
	if !strings.Contains(m.View(), "already exists") {
		t.Fatalf("the conflict was not shown:\n%s", m.View())
	}
}

func TestEndToEndEditPreservesUnshownFields(t *testing.T) {
	m, path := writableModel(t, `commands:
  api:
    cmd: ["sleep", "30"]
    env: {LOG: debug}
    health: http://127.0.0.1:9/health
    port: 9
`)
	m = drive(t, m, fetchStatus(m.client))

	m, cmd := step(t, m, key("e"))
	m = drive(t, m, cmd)
	if m.overlay != overlayForm {
		t.Fatalf("the form did not open: overlay = %v", m.overlay)
	}

	// Change only the folder, then save.
	for m.form.field != fieldFolder {
		m, _ = step(t, m, key("tab"))
	}
	for _, r := range "/srv" {
		m, _ = step(t, m, key(string(r)))
	}
	m, cmd = step(t, m, key("enter"))
	m = drive(t, m, cmd)

	data, _ := os.ReadFile(path)
	for _, want := range []string{"env:", "LOG: debug", "health:", "port: 9"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("editing through the form dropped %q:\n%s", want, data)
		}
	}
}
