// Package browser manages Chrome browser instances as sidecar containers,
// providing CDP access for both screencast streaming and MCP browser tools.
package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// DockerClient abstracts Docker container and network operations for testing.
type DockerClient interface {
	ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error)
	ContainerStart(ctx context.Context, container string, options containertypes.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options containertypes.StopOptions) error
	ContainerRemove(ctx context.Context, container string, options containertypes.RemoveOptions) error
	ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error)
	ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error)
	NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
	NetworkRemove(ctx context.Context, networkID string) error
}

// browserSession tracks a running Chrome sidecar container.
type browserSession struct {
	chromeContainerID string
	networkName       string
	hostPort          string // mapped host port for CDP access from the host
	targetID          string // active page target ID (shared between browser pane and MCP)
	cdp               any    // cached *browser.CDPClient for the browser pane (avoids target destruction on WS reconnect)
	lastUsedAt        time.Time
	paneCount         int // number of connected browser panes
}

// Manager manages Chrome sidecar containers (one per channel).
// Chrome runs in a dedicated container on a shared Docker network so both
// the agent's mcp-browser and the desktop browser panel can access it.
type Manager struct {
	api               DockerClient
	image             string // Docker image to use for Chrome containers
	screen            string
	logger            *slog.Logger
	timeNow           func() time.Time // injectable clock for testing
	idleCheckInterval time.Duration    // override defaultIdleCheckInterval for testing

	mu       sync.Mutex
	sessions map[string]*browserSession // channelID → session
}

const (
	// CDPPort is the Chrome DevTools Protocol port used inside containers.
	CDPPort = 9222

	// chromeLabel identifies Chrome sidecar containers.
	chromeLabel = "loop-chrome"

	// networkPrefix is the prefix for per-channel Docker networks.
	networkPrefix = "loop-net-"

	// containerPrefix is the prefix for Chrome container names.
	containerPrefix = "loop-chrome-"
)

// NewManager creates a new browser Manager.
func NewManager(api DockerClient, image, screen string, logger *slog.Logger) *Manager {
	return &Manager{
		api:      api,
		image:    image,
		screen:   screen,
		logger:   logger,
		timeNow:  time.Now,
		sessions: make(map[string]*browserSession),
	}
}

// chromeArgs returns CMD args for the chrome container (appended to ENTRYPOINT).
func (m *Manager) chromeArgs() []string {
	return []string{"--window-size=" + m.screen, "about:blank"}
}

// sanitizeID replaces non-alphanumeric characters with hyphens, collapses
// consecutive hyphens, trims leading/trailing hyphens, and lowercases.
// This ensures channel IDs like Slack thread IDs ("C0AG3Q1GH0Q:1773661657.701029")
// produce valid Docker container names and hostnames.
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

func sanitizeID(id string) string {
	s := strings.ToLower(id)
	s = nonAlphanumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
		s = strings.TrimRight(s, "-")
	}
	return s
}

// NetworkName returns the Docker network name for a channel.
func NetworkName(channelID string) string {
	return networkPrefix + sanitizeID(channelID)
}

// ChromeHostname returns the Chrome container hostname for a channel.
func ChromeHostname(channelID string) string {
	return containerPrefix + sanitizeID(channelID)
}

// EnsureBrowser ensures a Chrome sidecar container is running for the channel.
// Creates the Docker network and Chrome container if they don't exist.
func (m *Manager) EnsureBrowser(ctx context.Context, channelID, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for existing session.
	if sess, ok := m.sessions[channelID]; ok {
		if m.isContainerRunning(ctx, sess.chromeContainerID) {
			sess.lastUsedAt = m.timeNow()
			return nil
		}
		// Stale session, clean up.
		delete(m.sessions, channelID)
	}

	netName := NetworkName(channelID)
	containerName := ChromeHostname(channelID)

	// Check if Chrome container already exists (e.g. from a previous daemon run).
	if id, port := m.findExistingChrome(ctx, containerName); id != "" {
		if isChromeReachable(port) {
			m.logger.Info("reusing existing Chrome container",
				"channel_id", channelID,
				"container_id", id,
				"host_port", port,
			)
			m.sessions[channelID] = &browserSession{
				chromeContainerID: id,
				networkName:       netName,
				hostPort:          port,
				lastUsedAt:        m.timeNow(),
			}
			return nil
		}
		// Stale container — remove it so we can create a fresh one.
		m.logger.Info("removing stale Chrome container", "container_id", id)
		_ = m.api.ContainerRemove(ctx, id, containertypes.RemoveOptions{Force: true})
	}

	m.logger.Info("creating Chrome sidecar container",
		"channel_id", channelID,
		"network", netName,
		"container", containerName,
	)

	// Ensure Docker network exists.
	if _, err := m.api.NetworkCreate(ctx, netName, network.CreateOptions{
		Driver: "bridge",
	}); err != nil {
		// Ignore "already exists" errors.
		if !isAlreadyExists(err) {
			return fmt.Errorf("creating network %s: %w", netName, err)
		}
	}

	// Create Chrome container.
	resp, err := m.api.ContainerCreate(ctx,
		&containertypes.Config{
			Image:        m.image,
			Cmd:          m.chromeArgs(),
			Labels:       map[string]string{chromeLabel: channelID},
			ExposedPorts: nat.PortSet{"9222/tcp": struct{}{}},
			Hostname:     containerName,
		},
		&containertypes.HostConfig{
			Resources: containertypes.Resources{
				Memory:    512 * 1024 * 1024,
				CPUQuota:  50000,
				CPUPeriod: 100000,
			},
			PortBindings: nat.PortMap{
				"9222/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				netName: {Aliases: []string{containerName}},
			},
		},
		nil, containerName,
	)
	if err != nil {
		return fmt.Errorf("creating chrome container: %w", err)
	}

	// Start it.
	if err := m.api.ContainerStart(ctx, resp.ID, containertypes.StartOptions{}); err != nil {
		return fmt.Errorf("starting chrome container: %w", err)
	}

	// Discover the mapped host port.
	hostPort, err := m.getHostPort(ctx, resp.ID)
	if err != nil {
		return fmt.Errorf("getting host port: %w", err)
	}

	m.sessions[channelID] = &browserSession{
		chromeContainerID: resp.ID,
		networkName:       netName,
		hostPort:          hostPort,
		lastUsedAt:        m.timeNow(),
	}

	m.logger.Info("Chrome sidecar started",
		"channel_id", channelID,
		"container_id", resp.ID,
		"host_port", hostPort,
	)

	return nil
}

// EnsureAgentNetwork connects an agent container to the channel's Docker network
// so mcp-browser inside the agent can reach the Chrome container by hostname.
func (m *Manager) EnsureAgentNetwork(ctx context.Context, channelID, agentContainerID string) error {
	netName := NetworkName(channelID)

	// Ensure network exists.
	if _, err := m.api.NetworkCreate(ctx, netName, network.CreateOptions{
		Driver: "bridge",
	}); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("creating network %s: %w", netName, err)
	}

	// The agent container was already created — we can't add it to the network
	// via ContainerCreate. But since we set NetworkName in ContainerConfig,
	// the container client already connected it at creation time.
	// This method exists for cases where the agent is already running.
	return nil
}

// GetCDPEndpoint returns the CDP WebSocket URL for the channel's Chrome container.
func (m *Manager) GetCDPEndpoint(channelID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok && sess.hostPort != "" {
		return fmt.Sprintf("ws://127.0.0.1:%s", sess.hostPort)
	}
	return fmt.Sprintf("ws://127.0.0.1:%d", CDPPort)
}

// SetTargetID stores the active page target ID for the channel.
// Called by the browser pane handler after connecting CDP.
func (m *Manager) SetTargetID(channelID, targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		sess.targetID = targetID
	}
}

// GetTargetID returns the active page target ID for the channel, if set.
func (m *Manager) GetTargetID(channelID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		return sess.targetID
	}
	return ""
}

// SetCDP caches a CDP client for the channel's browser pane.
// This prevents the page target from being destroyed on WS reconnect.
func (m *Manager) SetCDP(channelID string, cdp any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		sess.cdp = cdp
	}
}

// GetCDP returns the cached CDP client for the channel, or nil.
func (m *Manager) GetCDP(channelID string) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		return sess.cdp
	}
	return nil
}

// GetContainerID returns the Chrome container ID for the channel.
func (m *Manager) GetContainerID(channelID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok {
		return "", false
	}
	return sess.chromeContainerID, true
}

// StopBrowser stops and removes the Chrome container for a channel.
func (m *Manager) StopBrowser(ctx context.Context, channelID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[channelID]
	if ok {
		delete(m.sessions, channelID)
	}
	m.mu.Unlock()

	if !ok {
		return nil
	}

	m.logger.Info("stopping Chrome sidecar",
		"channel_id", channelID,
		"container_id", sess.chromeContainerID,
	)

	timeout := 5
	_ = m.api.ContainerStop(ctx, sess.chromeContainerID, containertypes.StopOptions{
		Timeout: &timeout,
	})
	_ = m.api.ContainerRemove(ctx, sess.chromeContainerID, containertypes.RemoveOptions{
		Force: true,
	})

	return nil
}

// IsRunning returns true if Chrome is running for the given channel.
func (m *Manager) IsRunning(ctx context.Context, channelID string) bool {
	m.mu.Lock()
	sess, ok := m.sessions[channelID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	return m.isContainerRunning(ctx, sess.chromeContainerID)
}

// Cleanup stops all Chrome sidecar containers and removes their networks.
func (m *Manager) Cleanup(ctx context.Context) {
	m.mu.Lock()
	channels := make([]string, 0, len(m.sessions))
	for ch := range m.sessions {
		channels = append(channels, ch)
	}
	m.mu.Unlock()

	for _, ch := range channels {
		_ = m.StopBrowser(ctx, ch)
		_ = m.api.NetworkRemove(ctx, NetworkName(ch))
	}
}

// SessionChannels returns the list of channel IDs with active browser sessions.
func (m *Manager) SessionChannels() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	channels := make([]string, 0, len(m.sessions))
	for ch := range m.sessions {
		channels = append(channels, ch)
	}
	return channels
}

// TouchBrowser updates the last-used timestamp for the channel's browser session.
// Called by the MCP server on each tool invocation to prevent idle shutdown.
func (m *Manager) TouchBrowser(channelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		sess.lastUsedAt = m.timeNow()
	}
}

// PaneConnected increments the browser pane count for the channel.
// While paneCount > 0, the idle monitor will not stop Chrome.
func (m *Manager) PaneConnected(channelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		sess.paneCount++
		sess.lastUsedAt = m.timeNow()
	}
}

// PaneDisconnected decrements the browser pane count for the channel.
func (m *Manager) PaneDisconnected(channelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok && sess.paneCount > 0 {
		sess.paneCount--
	}
}

// idleCheckInterval is the interval between idle session checks.
// Exposed as a field on Manager for testing; defaults to 1 minute.
const defaultIdleCheckInterval = time.Minute

// RunIdleMonitor periodically checks for idle browser sessions and stops them.
// A session is idle when no browser pane is connected and lastUsedAt exceeds the timeout.
func (m *Manager) RunIdleMonitor(ctx context.Context, timeout time.Duration) {
	interval := defaultIdleCheckInterval
	if m.idleCheckInterval > 0 {
		interval = m.idleCheckInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.stopIdleSessions(ctx, timeout)
		}
	}
}

// stopIdleSessions collects idle channel IDs under lock, then stops each outside lock.
func (m *Manager) stopIdleSessions(ctx context.Context, timeout time.Duration) {
	m.mu.Lock()
	now := m.timeNow()
	var idle []string
	for ch, sess := range m.sessions {
		if sess.paneCount > 0 {
			continue
		}
		if now.Sub(sess.lastUsedAt) > timeout {
			idle = append(idle, ch)
		}
	}
	m.mu.Unlock()

	for _, ch := range idle {
		m.logger.Info("idle-stopping Chrome", "channel_id", ch)
		_ = m.StopBrowser(ctx, ch)
	}
}

// ChromeBinaryPath returns the path to the Chromium binary inside containers.
func ChromeBinaryPath() string {
	return "chromium-browser"
}

// CDPAddress returns the address Chrome's CDP listens on inside the container.
func CDPAddress() string {
	return "0.0.0.0"
}

// --- internal helpers ---

func (m *Manager) isContainerRunning(ctx context.Context, containerID string) bool {
	info, err := m.api.ContainerInspect(ctx, containerID)
	if err != nil {
		return false
	}
	return info.State != nil && info.State.Running
}

func (m *Manager) findExistingChrome(ctx context.Context, containerName string) (id, hostPort string) {
	containers, err := m.api.ContainerList(ctx, containertypes.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", containerName)),
	})
	if err != nil || len(containers) == 0 {
		return "", ""
	}
	c := containers[0]
	// Start it if stopped.
	if c.State != "running" {
		if err := m.api.ContainerStart(ctx, c.ID, containertypes.StartOptions{}); err != nil {
			return "", ""
		}
	}
	port, _ := m.getHostPort(ctx, c.ID)
	return c.ID, port
}

func (m *Manager) getHostPort(ctx context.Context, containerID string) (string, error) {
	info, err := m.api.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	if info.NetworkSettings != nil {
		if bindings, ok := info.NetworkSettings.Ports["9222/tcp"]; ok && len(bindings) > 0 {
			return bindings[0].HostPort, nil
		}
	}
	return "", fmt.Errorf("no host port mapping for 9222/tcp")
}

func isAlreadyExists(err error) bool {
	return err != nil && (contains(err.Error(), "already exists") || contains(err.Error(), "Conflict"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// isChromeReachable checks if Chrome's CDP endpoint is already reachable via the host port.
func isChromeReachable(hostPort string) bool {
	if hostPort == "" {
		return false
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/json/version", hostPort)) //nolint:gosec,noctx
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
