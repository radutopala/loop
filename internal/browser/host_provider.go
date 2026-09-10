package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// HostProvider implements BrowserProvider by connecting to a Chrome browser
// running on the host machine via CDP, instead of managing Docker containers.
//
// Discovery: reads Chrome's DevToolsActivePort file from the user data directory
// (written when chrome://inspect/#remote-debugging is enabled). Falls back to
// connecting on the configured port if the file is not found.
type HostProvider struct {
	sessionManager // embedded shared session management

	port   int
	logger *slog.Logger

	// portFor resolves browser.host_cdp_port for one channel. The port is
	// per-project config while the provider is a single daemon-lifetime
	// object, so the constructor's value can only ever be the global layer;
	// asking per channel is what lets a project's override apply to its own
	// channels. Returning false means "no answer" — the constructor's port
	// stands.
	portFor func(ctx context.Context, channelID string) (int, bool)

	// readFile is injectable for testing.
	readFile func(string) ([]byte, error)
	// userHomeDir is injectable for testing.
	userHomeDir func() (string, error)
}

// NewHostProvider creates a new HostProvider that connects to Chrome on the given port.
func NewHostProvider(port int, logger *slog.Logger) *HostProvider {
	return &HostProvider{
		sessionManager: newSessionManager(),
		port:           port,
		logger:         logger,
		readFile:       os.ReadFile,
		userHomeDir:    os.UserHomeDir,
	}
}

// SetPortResolver installs the per-channel CDP port lookup. Called once
// during wiring, before any channel connects.
func (h *HostProvider) SetPortResolver(fn func(ctx context.Context, channelID string) (int, bool)) {
	h.portFor = fn
}

// PortFor returns the port channelID's host Chrome is expected on: the port
// discovered for a live session, else the resolver's answer, else the
// constructor's.
//
// Discovery wins because it is evidence — DevToolsActivePort is written by
// the Chrome that is actually running — while config is only a statement of
// intent.
func (h *HostProvider) PortFor(ctx context.Context, channelID string) int {
	h.mu.Lock()
	if sess, ok := h.sessions[channelID]; ok && sess.cdpPort != 0 {
		port := sess.cdpPort
		h.mu.Unlock()
		return port
	}
	h.mu.Unlock()
	return h.configuredPort(ctx, channelID)
}

// configuredPort returns the channel's configured port, ignoring any live
// session. Kept separate from PortFor because EnsureBrowser resolves it while
// already holding h.mu.
func (h *HostProvider) configuredPort(ctx context.Context, channelID string) int {
	if h.portFor != nil {
		if port, ok := h.portFor(ctx, channelID); ok && port > 0 {
			return port
		}
	}
	return h.port
}

// devToolsActivePort reads Chrome's DevToolsActivePort file and returns
// the WebSocket endpoint (e.g. "ws://127.0.0.1:9222/devtools/browser/{id}").
// The file is written by Chrome when remote debugging is enabled via
// chrome://inspect/#remote-debugging and contains:
//
//	Line 1: port number
//	Line 2: WebSocket path (e.g. /devtools/browser/{guid})
func (h *HostProvider) devToolsActivePort() (wsEndpoint string, port int, err error) {
	return parseDevToolsActivePort(h.readFile, h.userHomeDir)
}

// DiscoverWSEndpoint discovers the Chrome CDP WebSocket endpoint by reading
// the DevToolsActivePort file. Returns an error if the file is not available.
func DiscoverWSEndpoint() (string, error) {
	return discoverWSEndpoint(os.ReadFile, os.UserHomeDir)
}

func discoverWSEndpoint(readFile func(string) ([]byte, error), userHomeDir func() (string, error)) (string, error) {
	wsEndpoint, _, err := parseDevToolsActivePort(readFile, userHomeDir)
	if err != nil {
		return "", err
	}
	return wsEndpoint, nil
}

// parseDevToolsActivePort reads Chrome's DevToolsActivePort file.
// Extracted for reuse across HostProvider and standalone callers.
func parseDevToolsActivePort(readFile func(string) ([]byte, error), userHomeDir func() (string, error)) (wsEndpoint string, port int, err error) {
	dataDir := chromeUserDataDir(userHomeDir)
	if dataDir == "" {
		return "", 0, fmt.Errorf("could not determine Chrome user data directory")
	}

	portFile := filepath.Join(dataDir, "DevToolsActivePort")
	data, err := readFile(portFile)
	if err != nil {
		return "", 0, fmt.Errorf("reading DevToolsActivePort: %w — enable remote debugging at chrome://inspect/#remote-debugging", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return "", 0, fmt.Errorf("invalid DevToolsActivePort content: expected 2 lines, got %d", len(lines))
	}

	p, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || p <= 0 || p > 65535 {
		return "", 0, fmt.Errorf("invalid port in DevToolsActivePort: %q", lines[0])
	}

	wsPath := strings.TrimSpace(lines[1])
	if wsPath == "" {
		return "", 0, fmt.Errorf("empty WebSocket path in DevToolsActivePort")
	}

	return fmt.Sprintf("ws://127.0.0.1:%d%s", p, wsPath), p, nil
}

// chromeUserDataDir returns the default Chrome user data directory for the given OS.
func chromeUserDataDir(homeDir func() (string, error)) string {
	return chromeUserDataDirForOS(homeDir, runtime.GOOS)
}

func chromeUserDataDirForOS(homeDir func() (string, error), goos string) string {
	home, err := homeDir()
	if err != nil {
		return ""
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "linux":
		return filepath.Join(home, ".config", "google-chrome")
	case "windows":
		return filepath.Join(home, "AppData", "Local", "Google", "Chrome", "User Data")
	default:
		return ""
	}
}

// EnsureBrowser discovers the host Chrome via DevToolsActivePort or falls back
// to the configured port.
func (h *HostProvider) EnsureBrowser(ctx context.Context, channelID, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.sessions[channelID]; ok {
		return nil // session already exists
	}

	configured := h.configuredPort(ctx, channelID)

	// Try DevToolsActivePort discovery first.
	if wsEndpoint, port, err := h.devToolsActivePort(); err == nil {
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if dialErr == nil {
			conn.Close()
			if h.logger != nil {
				h.logger.Info("host browser: discovered via DevToolsActivePort", "ws_endpoint", wsEndpoint, "port", port)
			}
			h.port = port
			sess := newBrowserSession(h.timeNow())
			sess.cdpPort = port
			h.sessions[channelID] = sess
			return nil
		}
		if h.logger != nil {
			h.logger.Warn("host browser: DevToolsActivePort file exists but port not reachable", "port", port, "error", dialErr)
		}
	}

	// Fallback: try the configured port with /json/version.
	if isChromeReachable(fmt.Sprintf("127.0.0.1:%d", configured)) {
		sess := newBrowserSession(h.timeNow())
		sess.cdpPort = configured
		h.sessions[channelID] = sess
		return nil
	}

	return fmt.Errorf("host Chrome not reachable — enable remote debugging at chrome://inspect/#remote-debugging")
}

// GetCDPEndpoint returns the WebSocket endpoint for the host Chrome.
// If DevToolsActivePort was discovered, returns the full WS endpoint.
// Otherwise returns ws://127.0.0.1:{port}.
func (h *HostProvider) GetCDPEndpoint(channelID string) string {
	// Try DevToolsActivePort for the full WS endpoint with browser GUID.
	if wsEndpoint, _, err := h.devToolsActivePort(); err == nil {
		return wsEndpoint
	}
	return fmt.Sprintf("ws://127.0.0.1:%d", h.PortFor(context.Background(), channelID))
}

// GetContainerID always returns empty since there is no container.
func (h *HostProvider) GetContainerID(_ string) (string, bool) {
	return "", false
}

// StopBrowser clears the session state but does NOT kill Chrome.
// Always returns an empty container ID since host mode has no Docker container.
func (h *HostProvider) StopBrowser(_ context.Context, channelID string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, channelID)
	return "", nil
}

// IsRunning checks if host Chrome is reachable.
func (h *HostProvider) IsRunning(ctx context.Context, channelID string) bool {
	if _, _, err := h.devToolsActivePort(); err == nil {
		return true
	}
	return isChromeReachable(fmt.Sprintf("127.0.0.1:%d", h.PortFor(ctx, channelID)))
}

// IsHostMode returns true — this provider always uses the host browser.
func (h *HostProvider) IsHostMode() bool {
	return true
}

// RemoveProfile is a no-op. Host mode drives the user's own Chrome against
// their own profile directory; wiping it is never ours to do.
func (h *HostProvider) RemoveProfile(_ context.Context, _ string) error {
	return nil
}
