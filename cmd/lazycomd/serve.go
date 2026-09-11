package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tphuc/lazycomd/internal/api"
	"github.com/tphuc/lazycomd/internal/client"
	"github.com/tphuc/lazycomd/internal/config"
	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/paths"
)

// runServe runs the daemon in the foreground. Daemonization belongs to
// launchd or systemd.
func runServe(args []string) int {
	// Named flags, not fs: the io/fs import is used below in listenUnix.
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := flags.String("config", paths.ConfigPath(), "path to config.yaml")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
		return 1
	}

	token := ""
	if cfg.Listen != "" {
		if token, err = readToken(cfg.TokenFile); err != nil {
			fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
			return 1
		}
	}

	mgr := manager.New(cfg, paths.LogDir())
	srv := api.NewServer(mgr, token, func() (*config.Config, error) {
		return config.Load(*cfgPath)
	})

	sock := paths.SocketPath()
	ul, err := listenUnix(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
		return 1
	}
	defer os.Remove(sock)

	unixSrv := &http.Server{Handler: srv.Handler()}
	go serveLogged(unixSrv, ul)
	log.Printf("lazycomd: listening on %s", sock)

	if cfg.Listen != "" {
		addr := bindAddr(cfg.Listen)
		tl, err := net.Listen("tcp", addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lazycomd: %v\n", err)
			return 1
		}
		tcpSrv := &http.Server{Handler: srv.AuthHandler()}
		go serveLogged(tcpSrv, tl)
		defer tcpSrv.Close()
		log.Printf("lazycomd: listening on %s (token required)", addr)
	}

	mgr.StartAutostart()

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Print("lazycomd: draining, interrupt again to kill immediately")

	unixSrv.Close()
	drained := make(chan struct{})
	go func() {
		mgr.Shutdown()
		close(drained)
	}()
	select {
	case <-drained:
	case <-sigs:
		mgr.KillAll()
		<-drained
	}
	log.Print("lazycomd: stopped")
	return 0
}

// listenUnix claims the socket. A live daemon is fatal; a leftover socket
// with nothing behind it is cleared.
func listenUnix(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if daemonAlive(path) {
		return nil, errors.New("already running")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// daemonAlive reports whether something is answering healthz on the socket.
func daemonAlive(sock string) bool {
	if _, err := os.Stat(sock); err != nil {
		return false
	}
	c, err := client.New("unix://"+sock, "")
	if err != nil {
		return false
	}
	return c.Health() == nil
}

// bindAddr keeps a bare port on loopback. Exposing every interface has to be
// written out, and says so in the log.
func bindAddr(listen string) string {
	if strings.HasPrefix(listen, ":") {
		return "127.0.0.1" + listen
	}
	if strings.HasPrefix(listen, "0.0.0.0:") {
		log.Printf("lazycomd: WARNING %s exposes the API on every interface", listen)
	}
	return listen
}

// readToken reads and sanity-checks the bearer token file.
func readToken(path string) (string, error) {
	if path == "" {
		return "", errors.New("listen requires token_file")
	}
	path = config.ExpandUser(path)

	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("%s: mode is %v, want 0600", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("%s: empty token", path)
	}
	return token, nil
}

func serveLogged(s *http.Server, l net.Listener) {
	if err := s.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("lazycomd: serve: %v", err)
	}
}
