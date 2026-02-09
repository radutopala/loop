package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	serviceLabel = "com.loop.agent"
	plistName    = serviceLabel + ".plist"
)

// System abstracts OS operations for testability.
type System interface {
	Executable() (string, error)
	UserHomeDir() (string, error)
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	RemoveFile(name string) error
	RunCommand(name string, args ...string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
}

// RealSystem implements System with real OS calls.
type RealSystem struct{}

func (RealSystem) Executable() (string, error)                  { return os.Executable() }
func (RealSystem) UserHomeDir() (string, error)                 { return os.UserHomeDir() }
func (RealSystem) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (RealSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (RealSystem) RemoveFile(name string) error { return os.Remove(name) }
func (RealSystem) RunCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
func (RealSystem) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }

// getUID is a package-level variable to allow overriding in tests.
var getUID = os.Getuid

// evalSymlinks is a package-level variable to allow overriding in tests.
var evalSymlinks = filepath.EvalSymlinks

// osGetenv is a package-level variable to allow overriding in tests.
var osGetenv = os.Getenv

// proxyKeys lists the environment variable names forwarded to the launchd plist.
var proxyKeys = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

// Start installs and bootstraps the launchd service.
func Start(sys System) error {
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
	logDir := filepath.Join(home, ".loop", "logs")

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

	plist := generatePlist(binPath, logDir, extraEnv)
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
			strings.Contains(s, "Could not find service") {
			return removeIfExists(sys, plistPath)
		}
		return fmt.Errorf("launchctl bootout: %s", strings.TrimSpace(s))
	}

	return removeIfExists(sys, plistPath)
}

func removeIfExists(sys System, path string) error {
	err := sys.RemoveFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist: %w", err)
	}
	return nil
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

func generatePlist(binaryPath, logDir string, extraEnv map[string]string) string {
	logFile := logDir + "/loop.log"

	var envEntries string
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(extraEnv))
	for k := range extraEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		envEntries += fmt.Sprintf("\t\t<key>%s</key>\n\t\t<string>%s</string>\n", k, extraEnv[k])
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
`, serviceLabel, binaryPath, logFile, logFile, envEntries)
}
