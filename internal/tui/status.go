package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tphuc/lazycomd/internal/manager"
	"github.com/tphuc/lazycomd/internal/probe"
)

// statusModel is panel 1: how the daemon and its collectors are doing.
type statusModel struct {
	addr      string
	connected bool
	rows      []manager.Status
	snap      probe.Snapshot
	now       func() time.Time
}

func newStatus(addr string) statusModel {
	return statusModel{addr: addr, now: time.Now}
}

func (s *statusModel) SetRows(rows []manager.Status)   { s.rows = rows }
func (s *statusModel) SetSnapshot(snap probe.Snapshot) { s.snap = snap }
func (s *statusModel) SetConnected(ok bool)            { s.connected = ok }

// counts tallies commands by the states worth a number.
func (s statusModel) counts() (running, failed int) {
	for _, r := range s.rows {
		switch r.State {
		case manager.Running:
			running++
		case manager.Failed:
			failed++
		}
	}
	return running, failed
}

// Summary is the one-line subtitle the panel shows in its title bar.
func (s statusModel) Summary() string {
	if !s.connected {
		return "daemon down"
	}
	running, failed := s.counts()
	if failed > 0 {
		return fmt.Sprintf("%d running, %d failed", running, failed)
	}
	return fmt.Sprintf("%d of %d running", running, len(s.rows))
}

// PanelRows is the panel's body: two lines, the second one a warning when
// there is something to warn about.
func (s statusModel) PanelRows() []string {
	dot, note := "●", shortAddr(s.addr)
	if !s.connected {
		dot, note = "○", "retrying every 2s"
	}
	rows := []string{fmt.Sprintf("%s %s", dot, s.Summary()), note}

	if problems := s.problems(); len(problems) > 0 {
		rows[1] = "⚠ " + problems[0]
	}
	return rows
}

// problems lists everything currently wrong, worst first.
func (s statusModel) problems() []string {
	var out []string
	if !s.connected {
		out = append(out, "daemon not running (start with: lazycomd serve)")
	}
	sections := make([]string, 0, len(s.snap.Errors))
	for k := range s.snap.Errors {
		sections = append(sections, k)
	}
	sort.Strings(sections)
	for _, k := range sections {
		out = append(out, k+": "+s.snap.Errors[k])
	}
	return out
}

// Detail is the main pane while this panel has focus.
func (s statusModel) Detail() []string {
	running, failed := s.counts()
	out := []string{
		"  address    " + s.addr,
		"  reachable  " + yesNo(s.connected),
		fmt.Sprintf("  commands   %d (%d running, %d failed)", len(s.rows), running, failed),
		"",
		"collectors",
	}
	for _, section := range []string{"ports", "vitals", "health"} {
		out = append(out, "  "+pad(section, 10)+" "+s.sectionLine(section))
	}

	if problems := s.problems(); len(problems) > 0 {
		out = append(out, "", "problems")
		for _, p := range problems {
			out = append(out, "  ⚠ "+p)
		}
	}
	return out
}

// sectionLine reports one collector's freshness, or why it has none.
func (s statusModel) sectionLine(section string) string {
	if msg := s.snap.Errors[section]; msg != "" {
		return "failing — " + msg
	}
	at, ok := s.snap.SampledAt[section]
	if !ok || at.IsZero() {
		return "no sample yet"
	}
	age := s.now().Sub(at).Round(time.Second)
	if age >= staleAfter {
		return fmt.Sprintf("stale, %ds ago", int(age.Seconds()))
	}
	return fmt.Sprintf("sampled %ds ago", int(age.Seconds()))
}

// shortAddr keeps the informative tail of a socket path: nobody needs to read
// the leading directories in a 34-column panel.
func shortAddr(addr string) string {
	path, ok := strings.CutPrefix(addr, "unix://")
	if !ok {
		return addr
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		return path
	}
	return "…/" + strings.Join(parts[len(parts)-2:], "/")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
