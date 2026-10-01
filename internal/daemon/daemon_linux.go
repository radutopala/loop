//go:build linux

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	serviceLabel = "loop"
	unitName     = serviceLabel + ".service"

	// stopPolls * stopPollInterval bounds how long Stop waits for a detached
	// daemon to exit after SIGTERM.
	stopPolls        = 100
	stopPollInterval = 100 * time.Millisecond
)

// Start writes a systemd user unit file and enables the service. Without a
// reachable systemd user manager (containers, sandboxes, WSL without systemd)
// it runs the daemon as a detached process tracked by a pid file instead.
// logFile is the absolute path to the daemon log file.
func Start(sys System, logFile string) error {
	exe, err := sys.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}
	binPath, err := sys.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving symlinks: %w", err)
	}

	home, err := sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	if !systemdUserAvailable(sys) {
		return startDetached(sys, binPath, home, logFile)
	}

	unitDir := filepath.Join(home, ".config", "systemd", "user")
	unitPath := filepath.Join(unitDir, unitName)
	logDir := filepath.Dir(logFile)

	if err := sys.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("creating systemd user unit directory: %w", err)
	}
	if err := sys.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	extraEnv := make(map[string]string)
	for _, key := range proxyKeys {
		if v := sys.Getenv(key); v != "" {
			extraEnv[key] = v
		}
	}
	if shell := sys.Getenv("SHELL"); shell != "" {
		extraEnv["SHELL"] = shell
	}

	unit := generateUnit(binPath, logFile, extraEnv)
	if err := sys.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("writing unit file: %w", err)
	}

	if out, err := sys.RunCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(out)))
	}

	out, err := sys.RunCommand("systemctl", "--user", "enable", "--now", serviceLabel)
	if err != nil {
		return fmt.Errorf("systemctl enable: %s", strings.TrimSpace(string(out)))
	}

	// Enable linger so the service persists across logouts and starts at boot.
	// Best-effort: ignore errors (e.g. containers where logind is unavailable).
	sys.RunCommand("loginctl", "enable-linger") //nolint:errcheck

	return nil
}

// Stop stops a detached daemon if one is running, then disables and stops the
// systemd user service and removes the unit file.
func Stop(sys System) error {
	home, err := sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	if err := stopDetached(sys, home); err != nil {
		return err
	}
	if !systemdUserAvailable(sys) {
		return nil
	}

	unitPath := filepath.Join(home, ".config", "systemd", "user", unitName)

	out, err := sys.RunCommand("systemctl", "--user", "disable", "--now", serviceLabel)
	if err != nil {
		s := string(out)
		if !strings.Contains(s, "not loaded") && !strings.Contains(s, "not found") {
			return fmt.Errorf("systemctl disable: %s", strings.TrimSpace(s))
		}
	}

	if err := removeIfExists(sys, unitPath); err != nil {
		return err
	}

	if out, err := sys.RunCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(out)))
	}

	// Disable linger; ignore errors — it may not have been set.
	sys.RunCommand("loginctl", "disable-linger") //nolint:errcheck

	return nil
}

// Status returns "running", "stopped", or "not installed".
func Status(sys System) (string, error) {
	home, err := sys.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}

	if _, ok := runningPID(sys, pidFilePath(home)); ok {
		return "running", nil
	}

	unitPath := filepath.Join(home, ".config", "systemd", "user", unitName)
	if _, err := sys.Stat(unitPath); err != nil {
		if os.IsNotExist(err) {
			return "not installed", nil
		}
		return "", fmt.Errorf("checking unit file: %w", err)
	}

	out, _ := sys.RunCommand("systemctl", "--user", "is-active", serviceLabel)
	if strings.TrimSpace(string(out)) == "active" {
		return "running", nil
	}

	return "stopped", nil
}

// systemdUserAvailable reports whether the systemd user manager answers.
func systemdUserAvailable(sys System) bool {
	_, err := sys.RunCommand("systemctl", "--user", "show-environment")
	return err == nil
}

func pidFilePath(home string) string {
	return filepath.Join(home, ".loop", "daemon.pid")
}

// startDetached runs `binPath serve` in its own session and records its pid.
func startDetached(sys System, binPath, home, logFile string) error {
	pidPath := pidFilePath(home)
	if _, ok := runningPID(sys, pidPath); ok {
		return nil
	}

	if err := sys.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}
	if err := sys.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		return fmt.Errorf("creating pid directory: %w", err)
	}

	pid, err := sys.StartDetached(binPath, []string{"serve"}, logFile)
	if err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}
	if err := sys.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		sys.Terminate(pid) //nolint:errcheck
		return fmt.Errorf("writing pid file: %w", err)
	}
	return nil
}

// stopDetached terminates the daemon recorded in the pid file, waits for it
// to exit and removes the pid file. It is a no-op without a live daemon.
func stopDetached(sys System, home string) error {
	pidPath := pidFilePath(home)
	if pid, ok := runningPID(sys, pidPath); ok {
		if err := sys.Terminate(pid); err != nil {
			return fmt.Errorf("stopping daemon: %w", err)
		}
		for i := 0; isServeProcess(sys, pid); i++ {
			if i == stopPolls {
				return fmt.Errorf("daemon (pid %d) did not exit", pid)
			}
			sys.Sleep(stopPollInterval)
		}
	}
	if err := sys.RemoveFile(pidPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing pid file: %w", err)
	}
	return nil
}

// runningPID returns the pid from pidPath and whether it is a live daemon.
func runningPID(sys System, pidPath string) (int, bool) {
	data, err := sys.ReadPIDFile(pidPath)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, isServeProcess(sys, pid)
}

// isServeProcess reports whether pid is a running `<binary> serve`, so a pid
// recycled by an unrelated process is never signalled.
func isServeProcess(sys System, pid int) bool {
	cmdline, err := sys.ProcCmdline(pid)
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	return len(args) >= 2 && args[1] == "serve"
}

func generateUnit(binaryPath, logFile string, extraEnv map[string]string) string {
	var envEntries strings.Builder
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(extraEnv))
	for k := range extraEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&envEntries, "Environment=%s=%s\n", k, extraEnv[k])
	}

	return fmt.Sprintf(`[Unit]
Description=Loop Agent Daemon
After=network.target

[Service]
ExecStart=%s serve
Restart=always
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s
Environment=PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
%s
[Install]
WantedBy=default.target
`, binaryPath, logFile, logFile, envEntries.String())
}
