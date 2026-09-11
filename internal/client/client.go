// Package client talks to the lazycomd daemon over a unix socket or TCP.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
	"github.com/thanhphuchuynh/lazycomd/internal/paths"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

// ErrNoDaemon means nothing is listening at the configured address.
var ErrNoDaemon = errors.New("daemon not running")

// APIError is a non-2xx response carrying the daemon's error message.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// Client is a lazycomd API client.
type Client struct {
	http  *http.Client
	base  string
	addr  string // as given, for display
	token string
}

// New builds a client for addr: "unix:///path/to.sock" or "http://host:port".
func New(addr, token string) (*Client, error) {
	if sock, ok := strings.CutPrefix(addr, "unix://"); ok {
		return &Client{
			base:  "http://unix",
			addr:  addr,
			token: token,
			http: &http.Client{Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", sock)
				},
			}},
		}, nil
	}
	if u, err := url.Parse(addr); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return &Client{
			base:  strings.TrimSuffix(addr, "/"),
			addr:  addr,
			token: token,
			http:  &http.Client{Timeout: 30 * time.Second},
		}, nil
	}
	return nil, fmt.Errorf("bad address %q: want unix:///path/to.sock or http://host:port", addr)
}

// Default builds a client from LAZYCOMD_ADDR and LAZYCOMD_TOKEN, falling back
// to the default unix socket.
func Default() (*Client, error) {
	addr := os.Getenv("LAZYCOMD_ADDR")
	if addr == "" {
		addr = "unix://" + paths.SocketPath()
	}
	return New(addr, os.Getenv("LAZYCOMD_TOKEN"))
}

// Health reports whether a daemon is answering.
func (c *Client) Health() error {
	return c.do(context.Background(), "GET", "/v1/healthz", nil, nil)
}

// List returns every command's status.
func (c *Client) List() ([]manager.Status, error) {
	var out []manager.Status
	return out, c.do(context.Background(), "GET", "/v1/commands", nil, &out)
}

// Get returns one command's status.
func (c *Client) Get(name string) (manager.Status, error) {
	var out manager.Status
	return out, c.do(context.Background(), "GET", "/v1/commands/"+url.PathEscape(name), nil, &out)
}

// Start starts a command, optionally starting its dependencies first.
func (c *Client) Start(name string, withDeps bool) (manager.Status, error) {
	var out manager.Status
	body := map[string]bool{"with_deps": withDeps}
	return out, c.do(context.Background(), "POST", "/v1/commands/"+url.PathEscape(name)+"/start", body, &out)
}

// Stop stops a command.
func (c *Client) Stop(name string) (manager.Status, error) {
	var out manager.Status
	return out, c.do(context.Background(), "POST", "/v1/commands/"+url.PathEscape(name)+"/stop", nil, &out)
}

// Restart restarts a command.
func (c *Client) Restart(name string) (manager.Status, error) {
	var out manager.Status
	return out, c.do(context.Background(), "POST", "/v1/commands/"+url.PathEscape(name)+"/restart", nil, &out)
}

// Logs returns the last tail lines of a command's output.
func (c *Client) Logs(name string, tail int) ([]string, error) {
	var out []string
	path := "/v1/commands/" + url.PathEscape(name) + "/logs?tail=" + strconv.Itoa(tail)
	return out, c.do(context.Background(), "GET", path, nil, &out)
}

// Reload asks the daemon to re-read its config.
func (c *Client) Reload() ([]manager.Status, error) {
	var out []manager.Status
	return out, c.do(context.Background(), "POST", "/v1/reload", nil, &out)
}

// Stream copies live output lines to w until ctx is done or the stream ends.
func (c *Client) Stream(ctx context.Context, name string, w io.Writer) error {
	path := "/v1/commands/" + url.PathEscape(name) + "/logs/stream"
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if isDown(err) {
			return ErrNoDaemon
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &APIError{Status: resp.StatusCode, Msg: apiMessage(resp.Body, resp.StatusCode)}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue // blank separator or ": ping"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if isDown(err) {
			return ErrNoDaemon
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return &APIError{Status: resp.StatusCode, Msg: apiMessage(resp.Body, resp.StatusCode)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func apiMessage(r io.Reader, status int) string {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(r).Decode(&body); err == nil && body.Error != "" {
		return body.Error
	}
	return fmt.Sprintf("http %d", status)
}

// isDown distinguishes "no daemon there" from a real transport failure.
func isDown(err error) bool {
	return errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET)
}

// Addr is the address this client talks to, for display.
func (c *Client) Addr() string { return c.addr }

// System returns the daemon's last machine sample: listening ports, per
// command vitals and health, and any port conflicts.
func (c *Client) System() (probe.Snapshot, error) {
	var out probe.Snapshot
	return out, c.do(context.Background(), "GET", "/v1/system", nil, &out)
}

// CommandConfig returns a command's full spec, including fields no UI shows.
// PUT replaces, so an editor must read this first and send back what it does
// not change.
func (c *Client) CommandConfig(name string) (config.Command, error) {
	var out config.Command
	path := "/v1/commands/" + url.PathEscape(name) + "/config"
	return out, c.do(context.Background(), "GET", path, nil, &out)
}

// Projects maps each registered project's basename to its directory.
func (c *Client) Projects() (map[string]string, error) {
	var out map[string]string
	return out, c.do(context.Background(), "GET", "/v1/projects", nil, &out)
}

// Create adds a command to the config and returns its new status.
func (c *Client) Create(name string, cmd config.Command) (manager.Status, error) {
	var out manager.Status
	body := struct {
		Name string `json:"name"`
		config.Command
	}{Name: name, Command: cmd}
	return out, c.do(context.Background(), "POST", "/v1/commands", body, &out)
}

// Update replaces a command's whole spec.
func (c *Client) Update(name string, cmd config.Command) (manager.Status, error) {
	var out manager.Status
	return out, c.do(context.Background(), "PUT", "/v1/commands/"+url.PathEscape(name), cmd, &out)
}

// Delete removes a command from the config, stopping it if it was running.
func (c *Client) Delete(name string) error {
	return c.do(context.Background(), "DELETE", "/v1/commands/"+url.PathEscape(name), nil, nil)
}
