package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shortDir returns a temp dir short enough for a unix socket path on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lzc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestBindAddrKeepsBarePortOnLoopback(t *testing.T) {
	if got, want := bindAddr(":7777"), "127.0.0.1:7777"; got != want {
		t.Fatalf("bindAddr(:7777) = %q, want %q", got, want)
	}
	if got, want := bindAddr("0.0.0.0:7777"), "0.0.0.0:7777"; got != want {
		t.Fatalf("bindAddr = %q, want it left alone", got)
	}
	if got, want := bindAddr("192.168.1.5:7777"), "192.168.1.5:7777"; got != want {
		t.Fatalf("bindAddr = %q, want %q", got, want)
	}
}

func TestReadToken(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "token")
	if err := os.WriteFile(good, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readToken(good)
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cret" {
		t.Fatalf("token = %q, want s3cret", got)
	}

	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(loose, []byte("s3cret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(loose); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("err = %v, want a mode complaint", err)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(empty); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want an empty-token complaint", err)
	}

	if _, err := readToken(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("missing file: err = nil, want an error")
	}
	if _, err := readToken(""); err == nil {
		t.Fatal("empty path: err = nil, want an error")
	}
}

func TestListenUnixClearsStaleSocket(t *testing.T) {
	sock := filepath.Join(shortDir(t), "lazycomd.sock")
	// A leftover file with nothing behind it is stale garbage.
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := listenUnix(sock)
	if err != nil {
		t.Fatalf("listenUnix over a stale socket: %v", err)
	}
	defer l.Close()

	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestListenUnixRefusesWhenDaemonAlive(t *testing.T) {
	sock := filepath.Join(shortDir(t), "lazycomd.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/healthz" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})}
	go srv.Serve(l)
	defer srv.Close()

	if _, err := listenUnix(sock); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v, want an already-running error", err)
	}
}

func TestRunServeRejectsBadConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("listten: \":1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runServe([]string{"-config", cfg}); code != 1 {
		t.Fatalf("runServe = %d, want 1", code)
	}
}

func TestRunServeRejectsListenWithoutToken(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("listen: \":7777\"\ncommands: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runServe([]string{"-config", cfg}); code != 1 {
		t.Fatalf("runServe = %d, want 1", code)
	}
}
