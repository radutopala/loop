//go:build windows

package daemon

import (
	"fmt"
	"strings"
)

const serviceName = "Loop"

// Start creates and starts the Loop Windows service via sc.exe.
// logFile is accepted for interface compatibility but Windows services
// redirect output via the binary's own log configuration.
func Start(sys System, _ string) error {
	exe, err := sys.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}
	binPath, err := sys.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving symlinks: %w", err)
	}

	// binpath= value must quote the exe path in case it contains spaces,
	// followed by the sub-command argument.
	binSpec := fmt.Sprintf(`"%s" serve`, binPath)

	out, err := sys.RunCommand("sc.exe", "create", serviceName,
		"binpath=", binSpec,
		"start=", "auto",
		"displayname=", "Loop Agent Daemon")
	if err != nil {
		s := string(out)
		if !strings.Contains(s, "already exists") {
			return fmt.Errorf("sc create: %s", strings.TrimSpace(s))
		}
	}

	out, err = sys.RunCommand("sc.exe", "start", serviceName)
	if err != nil {
		s := string(out)
		if strings.Contains(s, "already running") {
			return nil
		}
		return fmt.Errorf("sc start: %s", strings.TrimSpace(s))
	}

	_ = out
	return nil
}

// Stop stops and deletes the Loop Windows service via sc.exe.
func Stop(sys System) error {
	// Stop is best-effort — the service may already be stopped.
	sys.RunCommand("sc.exe", "stop", serviceName) //nolint:errcheck

	out, err := sys.RunCommand("sc.exe", "delete", serviceName)
	if err != nil {
		s := string(out)
		if strings.Contains(s, "does not exist") || strings.Contains(s, "1060") {
			return nil
		}
		return fmt.Errorf("sc delete: %s", strings.TrimSpace(s))
	}

	return nil
}

// Status returns "running", "stopped", or "not installed".
func Status(sys System) (string, error) {
	out, err := sys.RunCommand("sc.exe", "query", serviceName)
	if err != nil {
		s := string(out)
		if strings.Contains(s, "does not exist") || strings.Contains(s, "1060") {
			return "not installed", nil
		}
		return "", fmt.Errorf("sc query: %s", strings.TrimSpace(s))
	}

	if strings.Contains(string(out), "RUNNING") {
		return "running", nil
	}
	return "stopped", nil
}
