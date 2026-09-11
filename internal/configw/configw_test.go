package configw

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
)

const withComments = `# lazycomd config
listen: ""

commands:
  # the http server, do not rename
  web:
    cmd: ["npm", "start"]
    cwd: /tmp

  # a chatty one
  noisy:
    cmd: ["sh", "-c", "echo hi"]
    restart: always
`

func linesOf(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")
}

func TestCreateLeavesEveryOtherLineAlone(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	before := linesOf(t, p)

	f := &File{path: p}
	if err := f.Create("extra", config.Command{Cmd: []string{"sleep", "30"}}); err != nil {
		t.Fatal(err)
	}

	after := linesOf(t, p)
	i := 0
	for _, want := range before {
		for i < len(after) && after[i] != want {
			i++
		}
		if i == len(after) {
			t.Fatalf("original line %q is gone:\n%s", want, strings.Join(after, "\n"))
		}
		i++
	}
	if !strings.Contains(strings.Join(after, "\n"), "extra:") {
		t.Fatalf("new command missing:\n%s", strings.Join(after, "\n"))
	}
}

func TestCreateIntoAFileWithNoCommandsKey(t *testing.T) {
	p := writeFixture(t, "listen: \"\"\n", 0o600)

	f := &File{path: p}
	if err := f.Create("web", config.Command{Cmd: []string{"npm", "start"}}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(p)
	parsed, err := config.ParseBytes(data, p)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, data)
	}
	if _, ok := parsed.Commands["web"]; !ok {
		t.Fatalf("web missing:\n%s", data)
	}
}

func TestCreateRejectsADuplicate(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	if err := f.Create("web", config.Command{Cmd: []string{"x"}}); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func TestUpdateReplacesOnlyItsOwnBlock(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	if err := f.Update("web", config.Command{Cmd: []string{"npm", "run", "dev"}, Cwd: "/srv"}); err != nil {
		t.Fatal(err)
	}

	body := strings.Join(linesOf(t, p), "\n")
	for _, want := range []string{"# the http server, do not rename", "# a chatty one", "noisy:", "restart: always", "dev", "/srv"} {
		if !strings.Contains(body, want) {
			t.Fatalf("result missing %q:\n%s", want, body)
		}
	}
	// Assert on the parsed result, not a substring: "restart: always" on the
	// neighbouring command contains "start" too.
	parsed, err := config.ParseBytes([]byte(body), p)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, body)
	}
	if got := parsed.Commands["web"].Cmd; len(got) != 3 || got[2] != "dev" {
		t.Fatalf("web.cmd = %v, want the replacement", got)
	}
	if parsed.Commands["noisy"].Restart != config.RestartAlways {
		t.Fatalf("the neighbour changed: %+v", parsed.Commands["noisy"])
	}
}

func TestUpdateUnknownCommand(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}
	if err := f.Update("ghost", config.Command{Cmd: []string{"x"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteKeepsTheNextCommandsComment(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	if err := f.Delete("web"); err != nil {
		t.Fatal(err)
	}

	body := strings.Join(linesOf(t, p), "\n")
	if strings.Contains(body, "web:") {
		t.Fatalf("web survived:\n%s", body)
	}
	if !strings.Contains(body, "# a chatty one") {
		t.Fatalf("the next command's comment went with it:\n%s", body)
	}
	// web's own comment belongs to web, so it goes too — otherwise it would
	// end up labelling whatever command lands there next.
	if strings.Contains(body, "# the http server, do not rename") {
		t.Fatalf("web's own comment was left behind to mislabel its neighbour:\n%s", body)
	}
	if !strings.Contains(body, "noisy:") {
		t.Fatalf("noisy went with it:\n%s", body)
	}
	if !strings.Contains(body, "# lazycomd config") {
		t.Fatalf("the header comment went with it:\n%s", body)
	}
}

func TestDeleteTheLastCommand(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	if err := f.Delete("noisy"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	parsed, err := config.ParseBytes(data, p)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, data)
	}
	if _, gone := parsed.Commands["noisy"]; gone {
		t.Fatalf("noisy survived:\n%s", data)
	}
	if _, kept := parsed.Commands["web"]; !kept {
		t.Fatalf("web went with it:\n%s", data)
	}
}

func TestDeleteUnknownCommand(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}
	if err := f.Delete("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRefusesToWriteSomethingInvalid(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	before, _ := os.ReadFile(p)
	f := &File{path: p}

	err := f.Create("broken", config.Command{})
	if err == nil {
		t.Fatal("err = nil, want a refusal")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}

	after, _ := os.ReadFile(p)
	if string(after) != string(before) {
		t.Fatalf("the file changed despite the refusal:\n%s", after)
	}
}

func TestRegistryGivesOneFilePerPath(t *testing.T) {
	r := NewRegistry()
	a := r.File("/tmp/one.yaml")
	b := r.File("/tmp/one.yaml")
	c := r.File("/tmp/two.yaml")

	if a != b {
		t.Fatal("the same path handed back two Files, so two mutexes")
	}
	if a == c {
		t.Fatal("different paths share a File")
	}
}

func TestConcurrentCreatesAllLand(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := NewRegistry().File(p)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a'+i)) + "cmd"
			if err := f.Create(name, config.Command{Cmd: []string{"sleep", "30"}}); err != nil {
				t.Errorf("create %s: %v", name, err)
			}
		}(i)
	}
	wg.Wait()

	data, _ := os.ReadFile(p)
	parsed, err := config.ParseBytes(data, p)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, data)
	}
	if len(parsed.Commands) != 10 {
		t.Fatalf("got %d commands, want 10:\n%s", len(parsed.Commands), data)
	}
}

func TestCreateIntoAnEmptyFlowMapping(t *testing.T) {
	// "commands: {}" is a flow mapping: block entries cannot be spliced under
	// it, so the whole line has to be rewritten.
	p := writeFixture(t, "# keep me\nprojects: []\ncommands: {}\n", 0o600)

	f := &File{path: p}
	if err := f.Create("web", config.Command{Cmd: []string{"npm", "start"}}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(p)
	parsed, err := config.ParseBytes(data, p)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, data)
	}
	if _, ok := parsed.Commands["web"]; !ok {
		t.Fatalf("web missing:\n%s", data)
	}
	if !strings.Contains(string(data), "# keep me") {
		t.Fatalf("the header comment was lost:\n%s", data)
	}
	if strings.Contains(string(data), "{}") {
		t.Fatalf("the empty flow mapping survived alongside the block:\n%s", data)
	}
}

func TestRenderedCommandKeepsFlowStyleAndNoSizeDefault(t *testing.T) {
	p := writeFixture(t, withComments, 0o600)
	f := &File{path: p}

	// A spec read back from a running daemon carries the size default.
	if err := f.Update("web", config.Command{
		Cmd:  []string{"python3", "-m", "http.server", "8099"},
		Cwd:  "/srv",
		Size: config.DefaultBufSize,
	}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(p)
	body := string(data)
	if strings.Contains(body, "size:") {
		t.Fatalf("the buffer-size default was persisted:\n%s", body)
	}
	if !strings.Contains(body, `cmd: [python3, -m, http.server, "8099"]`) {
		t.Fatalf("cmd was not written in flow style:\n%s", body)
	}
}
