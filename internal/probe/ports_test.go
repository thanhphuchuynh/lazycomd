package probe

import (
	"testing"
)

// Real `lsof -nP -iTCP -sTCP:LISTEN` output, macOS.
const lsofSample = `COMMAND     PID  USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME
node      51192 tphuc   23u  IPv4 0x1a2b3c4d5e6f7890      0t0  TCP 127.0.0.1:3000 (LISTEN)
postgres   1183 tphuc    7u  IPv6 0x0987654321fedcba      0t0  TCP *:5432 (LISTEN)
postgres   1183 tphuc    8u  IPv4 0x1122334455667788      0t0  TCP *:5432 (LISTEN)
lazycomd  54405 tphuc    3u  IPv4 0xaabbccddeeff0011      0t0  TCP 127.0.0.1:7777 (LISTEN)
`

// Real `ss -ltnpH` output, Linux.
const ssSample = `LISTEN 0      4096         0.0.0.0:22         0.0.0.0:*    users:(("sshd",pid=1183,fd=3))
LISTEN 0      511        127.0.0.1:3000       0.0.0.0:*    users:(("node",pid=51192,fd=23))
LISTEN 0      4096            [::]:22            [::]:*    users:(("sshd",pid=1183,fd=4))
`

func TestParseLsof(t *testing.T) {
	got := normalizePorts(parseLsof([]byte(lsofSample)))

	if len(got) != 3 {
		t.Fatalf("got %d ports, want 3 after the v4/v6 pair collapses: %+v", len(got), got)
	}
	if got[0].Port != 3000 || got[0].Addr != "127.0.0.1" || got[0].PID != 51192 || got[0].Process != "node" {
		t.Fatalf("first row = %+v", got[0])
	}
	if got[1].Port != 5432 || got[1].Addr != "*" || got[1].Process != "postgres" {
		t.Fatalf("second row = %+v, want the wildcard postgres listener once", got[1])
	}
	if got[2].Port != 7777 {
		t.Fatalf("third row = %+v, want the sorted 7777 entry", got[2])
	}
}

func TestParseSS(t *testing.T) {
	got := normalizePorts(parseSS([]byte(ssSample)))

	if len(got) != 2 {
		t.Fatalf("got %d ports, want 2 after 0.0.0.0 and [::] collapse: %+v", len(got), got)
	}
	if got[0].Port != 22 || got[0].Addr != "*" || got[0].PID != 1183 || got[0].Process != "sshd" {
		t.Fatalf("first row = %+v", got[0])
	}
	if got[1].Port != 3000 || got[1].Addr != "127.0.0.1" || got[1].Process != "node" {
		t.Fatalf("second row = %+v", got[1])
	}
}

func TestParsersIgnoreGarbage(t *testing.T) {
	if got := parseLsof([]byte("COMMAND PID USER\nnonsense\n")); len(got) != 0 {
		t.Fatalf("parseLsof = %+v, want nothing", got)
	}
	if got := parseSS([]byte("garbage line\n")); len(got) != 0 {
		t.Fatalf("parseSS = %+v, want nothing", got)
	}
	if got := parseLsof(nil); len(got) != 0 {
		t.Fatalf("parseLsof(nil) = %+v", got)
	}
}

func TestNormalizeAddr(t *testing.T) {
	for _, in := range []string{"0.0.0.0", "::", "[::]", "*", ""} {
		if got := normalizeAddr(in); got != "*" {
			t.Fatalf("normalizeAddr(%q) = %q, want *", in, got)
		}
	}
	if got := normalizeAddr("127.0.0.1"); got != "127.0.0.1" {
		t.Fatalf("normalizeAddr(127.0.0.1) = %q", got)
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in   string
		addr string
		port int
		ok   bool
	}{
		{"127.0.0.1:3000", "127.0.0.1", 3000, true},
		{"*:5432", "*", 5432, true},
		{"[::1]:8080", "::1", 8080, true},
		{"[::]:22", "*", 22, true},
		{"nonsense", "", 0, false},
		{"127.0.0.1:notaport", "", 0, false},
	}
	for _, tc := range cases {
		addr, port, ok := splitHostPort(tc.in)
		if ok != tc.ok || addr != tc.addr || port != tc.port {
			t.Fatalf("splitHostPort(%q) = %q, %d, %v; want %q, %d, %v", tc.in, addr, port, ok, tc.addr, tc.port, tc.ok)
		}
	}
}

func TestNormalizePortsKeepsDistinctBinds(t *testing.T) {
	// A real double bind: loopback and wildcard on the same port, different
	// processes. Both must survive.
	in := []Port{
		{Addr: "*", Port: 3000, PID: 2, Process: "b"},
		{Addr: "127.0.0.1", Port: 3000, PID: 1, Process: "a"},
		{Addr: "127.0.0.1", Port: 3000, PID: 1, Process: "a"}, // exact duplicate
	}
	got := normalizePorts(in)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Addr != "*" || got[1].Addr != "127.0.0.1" {
		t.Fatalf("rows not sorted by addr within a port: %+v", got)
	}
}
