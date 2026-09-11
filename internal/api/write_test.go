package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/configw"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

// writableAPI serves a real config file that writes actually land in.
func writableAPI(t *testing.T, body string) (*manager.Manager, *http.Client, string) {
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

	s := New(Options{
		Manager:    m,
		Reload:     func() (*config.Config, error) { return config.Load(path) },
		ConfigPath: path,
		Writers:    configw.NewRegistry(),
	})
	return m, serveUnix(t, s.Handler()), path
}

const oneCommand = `commands:
  web:
    cmd: ["npm", "start"]
    cwd: /tmp
`

func TestCreateCommandWritesAndReloads(t *testing.T) {
	m, c, path := writableAPI(t, oneCommand)

	code, body := do(t, c, "POST", "/v1/commands", `{"name":"extra","cmd":["sleep","30"],"cwd":"/tmp","restart":"on-failure"}`)
	if code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}
	var st manager.Status
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st.Name != "extra" || st.State != manager.Stopped {
		t.Fatalf("status = %+v", st)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "extra:") {
		t.Fatalf("file missing the command:\n%s", data)
	}
	if _, err := m.Status("extra"); err != nil {
		t.Fatalf("manager does not have it: %v", err)
	}
}

func TestCreateDuplicateIsAConflict(t *testing.T) {
	_, c, _ := writableAPI(t, oneCommand)
	if code, body := do(t, c, "POST", "/v1/commands", `{"name":"web","cmd":["x"]}`); code != 409 {
		t.Fatalf("duplicate = %d %s, want 409", code, body)
	}
}

func TestCreateInvalidIsABadRequest(t *testing.T) {
	_, c, path := writableAPI(t, oneCommand)
	before, _ := os.ReadFile(path)

	code, body := do(t, c, "POST", "/v1/commands", `{"name":"broken","cmd":[]}`)
	if code != 400 {
		t.Fatalf("empty cmd = %d %s, want 400", code, body)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("the file changed despite the rejection:\n%s", after)
	}
}

func TestCreateWithNoNameIsABadRequest(t *testing.T) {
	_, c, _ := writableAPI(t, oneCommand)
	if code, _ := do(t, c, "POST", "/v1/commands", `{"cmd":["x"]}`); code != 400 {
		t.Fatalf("no name = %d, want 400", code)
	}
}

func TestGetCommandConfigReturnsEveryField(t *testing.T) {
	_, c, _ := writableAPI(t, `commands:
  api:
    cmd: ["node", "server.js"]
    env: {LOG: debug}
    health: http://localhost:3000/healthz
    port: 3000
`)

	code, body := do(t, c, "GET", "/v1/commands/api/config", "")
	if code != 200 {
		t.Fatalf("config = %d %s", code, body)
	}
	var got config.Command
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if got.Env["LOG"] != "debug" || got.Health == "" || got.Port != 3000 {
		t.Fatalf("config = %+v, want the fields a form never shows", got)
	}

	if code, _ := do(t, c, "GET", "/v1/commands/ghost/config", ""); code != 404 {
		t.Fatalf("unknown command = %d, want 404", code)
	}
}

func TestUpdateReplacesTheSpec(t *testing.T) {
	_, c, path := writableAPI(t, oneCommand)

	code, body := do(t, c, "PUT", "/v1/commands/web", `{"cmd":["npm","run","dev"],"cwd":"/srv"}`)
	if code != 200 {
		t.Fatalf("update = %d %s", code, body)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "dev") || !strings.Contains(string(data), "/srv") {
		t.Fatalf("file not updated:\n%s", data)
	}

	if code, _ := do(t, c, "PUT", "/v1/commands/ghost", `{"cmd":["x"]}`); code != 404 {
		t.Fatalf("unknown command = %d, want 404", code)
	}
}

func TestDeleteRemovesFromTheFileAndTheDaemon(t *testing.T) {
	m, c, path := writableAPI(t, oneCommand)

	code, body := do(t, c, "DELETE", "/v1/commands/web", "")
	if code != 204 {
		t.Fatalf("delete = %d %s, want 204", code, body)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "web:") {
		t.Fatalf("still in the file:\n%s", data)
	}
	if _, err := m.Status("web"); err == nil {
		t.Fatal("the manager still has it after the reload")
	}

	if code, _ := do(t, c, "DELETE", "/v1/commands/web", ""); code != 404 {
		t.Fatalf("second delete = %d, want 404", code)
	}
}

func TestWritesToAProjectFile(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "app")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "lazycomd.yaml"), []byte("commands: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("projects:\n  - "+proj+"\ncommands: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := manager.New(cfg, t.TempDir())
	t.Cleanup(m.Shutdown)
	s := New(Options{
		Manager:    m,
		Reload:     func() (*config.Config, error) { return config.Load(path) },
		ConfigPath: path,
		Writers:    configw.NewRegistry(),
	})
	c := serveUnix(t, s.Handler())

	if code, body := do(t, c, "POST", "/v1/commands", `{"name":"app:api","cmd":["node","server.js"]}`); code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}

	projData, _ := os.ReadFile(filepath.Join(proj, "lazycomd.yaml"))
	if !strings.Contains(string(projData), "api:") {
		t.Fatalf("project file missing the command:\n%s", projData)
	}
	if strings.Contains(string(projData), "app:api") {
		t.Fatalf("namespaced key written into the project file:\n%s", projData)
	}
	globalData, _ := os.ReadFile(path)
	if strings.Contains(string(globalData), "api") {
		t.Fatalf("global config was touched:\n%s", globalData)
	}
}

func TestUnknownProjectIsABadRequest(t *testing.T) {
	_, c, _ := writableAPI(t, oneCommand)
	code, body := do(t, c, "POST", "/v1/commands", `{"name":"ghostproj:api","cmd":["x"]}`)
	if code != 400 {
		t.Fatalf("unknown project = %d %s, want 400", code, body)
	}
	if !strings.Contains(body, "ghostproj") {
		t.Fatalf("error should name the project: %s", body)
	}
}

func TestWritesRefusedWithoutARegistry(t *testing.T) {
	m := manager.New(&config.Config{Commands: map[string]config.Command{}}, t.TempDir())
	t.Cleanup(m.Shutdown)
	s := New(Options{Manager: m, Reload: func() (*config.Config, error) { return nil, nil }})
	c := serveUnix(t, s.Handler())

	if code, _ := do(t, c, "POST", "/v1/commands", `{"name":"x","cmd":["y"]}`); code != 501 {
		t.Fatalf("create with no writers = %d, want 501", code)
	}
}

func TestProjectsEndpoint(t *testing.T) {
	_, c, _ := writableAPI(t, oneCommand)
	code, body := do(t, c, "GET", "/v1/projects", "")
	if code != 200 {
		t.Fatalf("projects = %d %s", code, body)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if got == nil {
		t.Fatal("projects = null, want an object even when there are none")
	}
}
