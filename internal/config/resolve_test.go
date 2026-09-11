package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolveExpandsVars(t *testing.T) {
	t.Setenv("LZC_PORT", "8080")
	dir := t.TempDir()
	c := Command{
		Cmd: []string{"curl", "http://localhost:$LZC_PORT"},
		Cwd: dir,
		Env: map[string]string{"TOKEN": "t-$LZC_PORT", "PLAIN": "x"},
	}
	r, err := c.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if r.Argv[1] != "http://localhost:8080" {
		t.Fatalf("argv[1] = %q", r.Argv[1])
	}
	if !slices.Contains(r.Env, "TOKEN=t-8080") {
		t.Fatalf("env missing expanded TOKEN: %v", tail(r.Env, 3))
	}
	if !slices.Contains(r.Env, "PLAIN=x") {
		t.Fatalf("env missing PLAIN: %v", tail(r.Env, 3))
	}
}

func TestResolveExpandsHomeInCwd(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Command{Cmd: []string{"true"}, Cwd: "~"}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if r.Cwd != home {
		t.Fatalf("cwd = %q, want %q", r.Cwd, home)
	}
}

func TestResolveDefaultsCwdToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Command{Cmd: []string{"true"}}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if r.Cwd != home {
		t.Fatalf("cwd = %q, want %q", r.Cwd, home)
	}
}

func TestResolveDollarEscape(t *testing.T) {
	t.Setenv("LZC_X", "boom")
	r, err := Command{Cmd: []string{"sh", "-c", "echo $$LZC_X and $LZC_X"}, Cwd: t.TempDir()}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := "echo $LZC_X and boom"
	if r.Argv[2] != want {
		t.Fatalf("argv[2] = %q, want %q", r.Argv[2], want)
	}
}

func TestResolveShellWraps(t *testing.T) {
	r, err := Command{Cmd: []string{"echo", "hi", "|", "wc", "-l"}, Shell: true, Cwd: t.TempDir()}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sh", "-c", "echo hi | wc -l"}
	if !slices.Equal(r.Argv, want) {
		t.Fatalf("argv = %v, want %v", r.Argv, want)
	}
}

func TestResolveEnvIsSorted(t *testing.T) {
	r, err := Command{
		Cmd: []string{"true"},
		Cwd: t.TempDir(),
		Env: map[string]string{"B": "2", "A": "1", "C": "3"},
	}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	got := tail(r.Env, 3)
	want := []string{"A=1", "B=2", "C=3"}
	if !slices.Equal(got, want) {
		t.Fatalf("last three env entries = %v, want %v", got, want)
	}
}

func TestResolveErrors(t *testing.T) {
	if _, err := (Command{Cmd: nil}).Resolve(); err == nil {
		t.Fatal("empty cmd: err = nil, want an error")
	}
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := Command{Cmd: []string{"true"}, Cwd: missing}.Resolve()
	if err == nil || !strings.Contains(err.Error(), "cwd") {
		t.Fatalf("err = %v, want a cwd error", err)
	}
}

func tail(s []string, n int) []string {
	if len(s) < n {
		return s
	}
	return s[len(s)-n:]
}
