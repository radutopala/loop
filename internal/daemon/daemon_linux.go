//go:build linux

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	serviceLabel = "loop"
	unitName     = serviceLabel + ".service"
)

// Start writes a systemd user unit file and enables the service.
// logFile is the absolute path to the daemon log file.
func Start(sys System, logFile string) error {
	exe, err := sys.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}
	binPath, err := evalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving symlinks: %w", err)
	}

	home, err := sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
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
		if v := osGetenv(key); v != "" {
			extraEnv[key] = v
		}
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

// Stop disables and stops the systemd user service and removes the unit file.
func Stop(sys System) error {
	home, err := sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
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
