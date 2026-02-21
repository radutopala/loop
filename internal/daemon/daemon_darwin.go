//go:build darwin

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	serviceLabel = "com.loop.agent"
	plistName    = serviceLabel + ".plist"
)

// Start installs and bootstraps the launchd service.
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

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistName)
	logDir := filepath.Dir(logFile)

	if err := sys.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
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

	plist := generatePlist(binPath, logFile, extraEnv)
	if err := sys.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	uid := getUID()
	domain := fmt.Sprintf("gui/%d", uid)

	// Bootout any existing service first (ignore errors — it may not be loaded).
	sys.RunCommand("launchctl", "bootout", domain, plistPath) //nolint:errcheck

	out, err := sys.RunCommand("launchctl", "bootstrap", domain, plistPath)
	if err != nil {
		if strings.Contains(string(out), "already bootstrapped") {
			return nil
		}
		return fmt.Errorf("launchctl bootstrap: %s", strings.TrimSpace(string(out)))
	}

	return nil
}

// Stop unloads the launchd service and removes the plist file.
func Stop(sys System) error {
	home, err := sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistName)
	uid := getUID()

	out, err := sys.RunCommand("launchctl", "bootout", fmt.Sprintf("gui/%d", uid), plistPath)
	if err != nil {
		s := string(out)
		if strings.Contains(s, "No such file") ||
			strings.Contains(s, "not found") ||
			strings.Contains(s, "Could not find service") ||
			strings.Contains(s, "Input/output error") {
			return removeIfExists(sys, plistPath)
		}
		return fmt.Errorf("launchctl bootout: %s", strings.TrimSpace(s))
	}

	return removeIfExists(sys, plistPath)
}

// Status returns "running", "stopped", or "not installed".
func Status(sys System) (string, error) {
	home, err := sys.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistName)
	if _, err := sys.Stat(plistPath); err != nil {
		if os.IsNotExist(err) {
			return "not installed", nil
		}
		return "", fmt.Errorf("checking plist: %w", err)
	}

	uid := getUID()
	out, err := sys.RunCommand("launchctl", "print", fmt.Sprintf("gui/%d/%s", uid, serviceLabel))
	if err != nil {
		return "stopped", nil
	}

	if strings.Contains(string(out), "state = running") {
		return "running", nil
	}

	return "stopped", nil
}

func generatePlist(binaryPath, logFile string, extraEnv map[string]string) string {
	var envEntries strings.Builder
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(extraEnv))
	for k := range extraEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&envEntries, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", k, extraEnv[k])
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
	</array>
	<key>KeepAlive</key>
	<true/>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
%s	</dict>
</dict>
</plist>
`, serviceLabel, binaryPath, logFile, logFile, envEntries.String())
}
