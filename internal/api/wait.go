package api

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/manager"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

// maxWait caps how long a client can hold a request open waiting for a
// command to come up. A start that has not made it in two minutes is a
// problem to read logs about, not to keep waiting on.
const maxWait = 2 * time.Minute

// waitPoll is how often readiness is re-checked. The sampler's own health
// pass runs every ten seconds, which is far too coarse to start against.
const waitPoll = 250 * time.Millisecond

// waitReady blocks until the command is ready, it dies, or the deadline
// passes. Ready means: its health URL answers 2xx, or — with no health URL —
// its port accepts a connection, or — with neither — it is simply running.
//
// The daemon does this rather than the client, because the health URL and the
// port are on the daemon's machine, which is not always the caller's.
func (s *Server) waitReady(ctx context.Context, name string, wait time.Duration) (manager.Status, error) {
	if wait > maxWait {
		wait = maxWait
	}
	deadline := time.Now().Add(wait)

	health, port := s.readySignal(name)
	for {
		st, err := s.mgr.Status(name)
		if err != nil {
			return st, err
		}
		// A command that has already left the building is never going to be
		// ready, so say so now rather than at the deadline.
		if st.State == manager.Failed || st.State == manager.Stopped {
			return st, fmt.Errorf("%s exited while starting (state %s)", name, st.State)
		}
		if st.State == manager.Running && ready(ctx, health, port) {
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("%s was not ready within %s (state %s)", name, wait, st.State)
		}

		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(waitPoll):
		}
	}
}

// readySignal is what this command offers to prove it is up.
func (s *Server) readySignal(name string) (health string, port int) {
	cfg, err := s.reload()
	if err != nil {
		return "", 0
	}
	spec, ok := cfg.Commands[name]
	if !ok {
		return "", 0
	}
	return spec.Health, spec.IntendedPort()
}

func ready(ctx context.Context, health string, port int) bool {
	if health != "" {
		return probe.Check(ctx, health).OK
	}
	if port != 0 {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), waitPoll)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	// Nothing to probe: running is the only signal this command has.
	return true
}
