package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/client"
	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

func TestFetchStatusReturnsStatusMsg(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{"a": sleeper()})

	msg := fetchStatus(c)()
	list, ok := msg.(statusMsg)
	if !ok {
		t.Fatalf("msg = %T, want statusMsg", msg)
	}
	if len(list) != 1 || list[0].Name != "a" || list[0].State != manager.Stopped {
		t.Fatalf("statusMsg = %+v", list)
	}
}

func TestFetchStatusReportsNoDaemon(t *testing.T) {
	c, err := client.New("unix:///tmp/lzc-absent-xyz.sock", "")
	if err != nil {
		t.Fatal(err)
	}
	msg := fetchStatus(c)()
	e, ok := msg.(statusErrMsg)
	if !ok {
		t.Fatalf("msg = %T, want statusErrMsg", msg)
	}
	if !errors.Is(e.err, client.ErrNoDaemon) {
		t.Fatalf("err = %v, want ErrNoDaemon", e.err)
	}
}

func TestFetchTailReturnsLines(t *testing.T) {
	c, m := testDaemon(t, map[string]config.Command{"a": echoer()})
	if err := m.Start("a"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool {
		b, err := m.Logs("a")
		return err == nil && len(b.Tail(1)) == 1
	})

	msg := fetchTail(c, "a")()
	tail, ok := msg.(logTailMsg)
	if !ok {
		t.Fatalf("msg = %T, want logTailMsg", msg)
	}
	if tail.name != "a" || len(tail.lines) == 0 || tail.lines[0] != "hi" {
		t.Fatalf("logTailMsg = %+v", tail)
	}
}

func TestFetchTailOnErrorStillNamesTheCommand(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{})
	msg := fetchTail(c, "ghost")()
	tail, ok := msg.(logTailMsg)
	if !ok {
		t.Fatalf("msg = %T, want logTailMsg", msg)
	}
	if tail.name != "ghost" || len(tail.lines) != 1 {
		t.Fatalf("logTailMsg = %+v, want one error line for ghost", tail)
	}
}

func TestDoActionStartsWithDeps(t *testing.T) {
	c, m := testDaemon(t, map[string]config.Command{
		"db":  sleeper(),
		"api": {Cmd: []string{"sleep", "30"}, Cwd: "/tmp", DependsOn: []string{"db"}},
	})

	msg := doAction(c, "start", "api")()
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want actionDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("err = %v", done.err)
	}
	if done.verb != "start" || done.name != "api" || done.st.State != manager.Running {
		t.Fatalf("actionDoneMsg = %+v", done)
	}
	st, err := m.Status("db")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != manager.Running {
		t.Fatalf("db state = %q, want running: start must pass with_deps", st.State)
	}
}

func TestDoActionCarriesTheError(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{"a": sleeper()})
	if msg := doAction(c, "start", "a")(); msg.(actionDoneMsg).err != nil {
		t.Fatal("first start failed")
	}
	done := doAction(c, "start", "a")().(actionDoneMsg)
	if done.err == nil {
		t.Fatal("second start err = nil, want a 409")
	}
	var apiErr *client.APIError
	if !errors.As(done.err, &apiErr) || apiErr.Status != 409 {
		t.Fatalf("err = %v, want a 409 APIError", done.err)
	}
}

func TestDoActionStopAndRestart(t *testing.T) {
	c, _ := testDaemon(t, map[string]config.Command{"a": sleeper()})
	if done := doAction(c, "start", "a")().(actionDoneMsg); done.err != nil {
		t.Fatal(done.err)
	}
	if done := doAction(c, "restart", "a")().(actionDoneMsg); done.err != nil || done.st.State != manager.Running {
		t.Fatalf("restart = %+v", done)
	}
	if done := doAction(c, "stop", "a")().(actionDoneMsg); done.err != nil || done.st.State != manager.Stopped {
		t.Fatalf("stop = %+v", done)
	}
}

func TestTickCmdReturnsTickMsg(t *testing.T) {
	msg := tickCmd(time.Millisecond)()
	if _, ok := msg.(tickMsg); !ok {
		t.Fatalf("msg = %T, want tickMsg", msg)
	}
}
