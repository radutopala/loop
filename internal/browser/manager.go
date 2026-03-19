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

// DockerClient abstracts Docker container operations for testing.
type DockerClient interface {
	ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error)
	ContainerStart(ctx context.Context, container string, options containertypes.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options containertypes.StopOptions) error
	ContainerRemove(ctx context.Context, container string, options containertypes.RemoveOptions) error
	ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error)
	ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error)
}

// browserSession tracks a running Chrome sidecar container.
type browserSession struct {
	chromeContainerID string
	hostPort          string         // mapped host port for CDP access from the host
	activeTargetID    string         // currently active target for screencast
	cdpTargets        map[string]any // targetID → CDPClient (one per tab)
	lastUsedAt        time.Time
	paneCount         int          // number of connected browser panes
	targetSwitchCh    chan string  // signals MCP-initiated tab switches to the browser pane
	tabAddedCh        chan TabInfo // signals MCP-initiated tab additions to the browser pane
	tabRemovedCh      chan string  // signals MCP-initiated tab removals to the browser pane
	tabOrder          []string     // ordered target IDs (insertion order, like a real browser tab bar)
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

// ChromeHostname returns the Chrome container hostname for a channel.
func ChromeHostname(channelID string) string {
	return containerPrefix + sanitizeID(channelID)
}

// EnsureBrowser ensures a Chrome sidecar container is running for the channel.
// Creates the Chrome container if it doesn't exist; the host connects via the
// mapped 127.0.0.1:hostPort (no Docker network required).
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
				hostPort:          port,
				cdpTargets:        make(map[string]any),
				lastUsedAt:        m.timeNow(),
				targetSwitchCh:    make(chan string, 1),
				tabAddedCh:        make(chan TabInfo, 1),
				tabRemovedCh:      make(chan string, 1),
			}
			return nil
		}
		// Stale container — remove it so we can create a fresh one.
		m.logger.Info("removing stale Chrome container", "container_id", id)
		_ = m.api.ContainerRemove(ctx, id, containertypes.RemoveOptions{Force: true})
	}

	m.logger.Info("creating Chrome sidecar container",
		"channel_id", channelID,
		"container", containerName,
	)

	// Create Chrome container without a Docker network — the host connects via
	// the mapped 127.0.0.1:hostPort, not through a Docker network.
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
		nil, nil, containerName,
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
		hostPort:          hostPort,
		cdpTargets:        make(map[string]any),
		lastUsedAt:        m.timeNow(),
		targetSwitchCh:    make(chan string, 1),
		tabAddedCh:        make(chan TabInfo, 1),
		tabRemovedCh:      make(chan string, 1),
	}

	m.logger.Info("Chrome sidecar started",
		"channel_id", channelID,
		"container_id", resp.ID,
		"host_port", hostPort,
	)

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
		sess.activeTargetID = targetID
	}
}

// GetTargetID returns the active page target ID for the channel, if set.
func (m *Manager) GetTargetID(channelID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		return sess.activeTargetID
	}
	return ""
}

// NotifyTargetSwitch signals the browser pane that the active target has changed.
// Called by the MCP server when it switches tabs.
func (m *Manager) NotifyTargetSwitch(channelID, targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok {
		return
	}
	sess.activeTargetID = targetID
	// Non-blocking send; drop stale signal if channel is full.
	select {
	case sess.targetSwitchCh <- targetID:
	default:
	}
}

// TargetSwitchCh returns a channel that receives the new target ID
// whenever the MCP agent switches tabs.
func (m *Manager) TargetSwitchCh(channelID string) <-chan string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		return sess.targetSwitchCh
	}
	return nil
}

// NotifyTabAdded signals the browser pane that a tab was added.
// Called by the HTTP handler when the MCP server opens a new tab.
func (m *Manager) NotifyTabAdded(channelID string, tab TabInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok {
		return
	}
	// Non-blocking send; drop stale signal if channel is full.
	select {
	case sess.tabAddedCh <- tab:
	default:
	}
}

// TabAddedCh returns a channel that receives TabInfo whenever the MCP agent
// opens a new tab.
func (m *Manager) TabAddedCh(channelID string) <-chan TabInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		return sess.tabAddedCh
	}
	return nil
}

// NotifyTabRemoved signals the browser pane that a tab was removed.
// Called by the HTTP handler when the MCP server closes a tab.
func (m *Manager) NotifyTabRemoved(channelID, targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok {
		return
	}
	select {
	case sess.tabRemovedCh <- targetID:
	default:
	}
}

// TabRemovedCh returns a channel that receives the target ID whenever the MCP agent
// closes a tab.
func (m *Manager) TabRemovedCh(channelID string) <-chan string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		return sess.tabRemovedCh
	}
	return nil
}

// TrackTab appends a target ID to the tab order if not already present.
func (m *Manager) TrackTab(channelID, targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok {
		return
	}
	for _, id := range sess.tabOrder {
		if id == targetID {
			return
		}
	}
	sess.tabOrder = append(sess.tabOrder, targetID)
}

// UntrackTab removes a target ID from the tab order.
func (m *Manager) UntrackTab(channelID, targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok {
		return
	}
	filtered := make([]string, 0, len(sess.tabOrder))
	for _, id := range sess.tabOrder {
		if id != targetID {
			filtered = append(filtered, id)
		}
	}
	sess.tabOrder = filtered
}

// NextTabID returns the tab to switch to after closing targetID.
// Returns the tab before it in the order, or the tab after it, or "".
func (m *Manager) NextTabID(channelID, closedTargetID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok {
		return ""
	}
	for i, id := range sess.tabOrder {
		if id == closedTargetID {
			if i > 0 {
				return sess.tabOrder[i-1] // tab before
			}
			if i+1 < len(sess.tabOrder) {
				return sess.tabOrder[i+1] // tab after
			}
			return ""
		}
	}
	return ""
}

// OrderTabs reorders tabs to match the stored insertion order.
// Tabs not in the stored order are appended at the end.
func (m *Manager) OrderTabs(channelID string, tabs []TabInfo) []TabInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok || len(sess.tabOrder) == 0 {
		return tabs
	}

	byID := make(map[string]TabInfo, len(tabs))
	for _, t := range tabs {
		byID[t.TargetID] = t
	}

	ordered := make([]TabInfo, 0, len(tabs))
	for _, id := range sess.tabOrder {
		if t, ok := byID[id]; ok {
			ordered = append(ordered, t)
			delete(byID, id)
		}
	}
	// Append any tabs not tracked (e.g. created externally via MCP)
	// and persist them into tabOrder so future calls maintain their position.
	for _, t := range tabs {
		if _, exists := byID[t.TargetID]; exists {
			ordered = append(ordered, t)
			sess.tabOrder = append(sess.tabOrder, t.TargetID)
		}
	}
	return ordered
}

// SetCDPForTarget caches a CDP client for a specific target.
func (m *Manager) SetCDPForTarget(channelID, targetID string, cdp any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		sess.cdpTargets[targetID] = cdp
	}
}

// GetCDPForTarget returns the cached CDP client for a specific target, or nil.
func (m *Manager) GetCDPForTarget(channelID, targetID string) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		return sess.cdpTargets[targetID]
	}
	return nil
}

// RemoveCDPForTarget removes and returns the cached CDP client for a target.
func (m *Manager) RemoveCDPForTarget(channelID, targetID string) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		cdp := sess.cdpTargets[targetID]
		delete(sess.cdpTargets, targetID)
		return cdp
	}
	return nil
}

// GetActiveCDP returns the CDP client for the active target, or nil.
func (m *Manager) GetActiveCDP(channelID string) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok {
		return sess.cdpTargets[sess.activeTargetID]
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

// Cleanup stops all Chrome sidecar containers.
func (m *Manager) Cleanup(ctx context.Context) {
	m.mu.Lock()
	channels := make([]string, 0, len(m.sessions))
	for ch := range m.sessions {
		channels = append(channels, ch)
	}
	m.mu.Unlock()

	for _, ch := range channels {
		_ = m.StopBrowser(ctx, ch)
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
