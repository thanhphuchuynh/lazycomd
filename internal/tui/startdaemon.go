package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thanhphuchuynh/lazycomd/internal/paths"
)

const launchdLabel = "com.tphuc.lazycomd"

// startDaemonView is the overlay that offers to bring the local daemon up.
func startDaemonView(width, height int) string {
	rows := []string{
		"",
		"  daemon is down. start it?",
		"",
		styleDim.Render("  y start · n or esc skip"),
	}
	return panelView(panelSpec{Title: "Daemon", Width: width, Height: height, Rows: rows, Focused: true})
}

func startDaemonCmd(fn func() error) tea.Cmd {
	if fn == nil {
		fn = startLocalDaemon
	}
	return func() tea.Msg {
		return daemonStartMsg{err: fn()}
	}
}

// startLocalDaemon prefers the user service (login autostart), then a
// detached `serve` of this same binary.
func startLocalDaemon() error {
	if err := startUserService(); err == nil {
		return nil
	}
	return spawnServe()
}

func startUserService() error {
	if path, err := exec.LookPath("systemctl"); err == nil {
		if err := exec.Command(path, "--user", "start", "lazycomd").Run(); err == nil {
			return nil
		}
	}
	if path, err := exec.LookPath("launchctl"); err == nil {
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)
		if err := exec.Command(path, "kickstart", "-k", target).Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no user service installed")
}

func spawnServe() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := paths.LogDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "serve.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "serve")
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return nil
}
