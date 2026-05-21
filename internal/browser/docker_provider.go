// Package browser manages Chrome browser instances as sidecar containers,
// providing CDP access for both screencast streaming and MCP browser tools.
package browser

import (
	"context"
	"fmt"
	"log/slog"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/radutopala/loop/internal/container"
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

// DockerProvider manages Chrome sidecar containers (one per channel).
// Chrome runs in a dedicated container on a shared Docker network so both
// the agent's mcp-browser and the desktop browser panel can access it.
type DockerProvider struct {
	sessionManager // embedded shared session management

	api      DockerClient
	image    string // Docker image to use for Chrome containers
	screen   string
	logger   *slog.Logger
	registry container.ContainerRegistry
}

const (
	// CDPPort is the Chrome DevTools Protocol port used inside containers.
	CDPPort = 9222

	// chromeLabel identifies Chrome sidecar containers.
	chromeLabel = "loop-chrome"

	// containerPrefix is the prefix for Chrome container names.
	containerPrefix = "loop-chrome-"
)

// NewDockerProvider creates a new browser DockerProvider.
func NewDockerProvider(api DockerClient, image, screen string, logger *slog.Logger) *DockerProvider {
	return &DockerProvider{
		sessionManager: newSessionManager(),
		api:            api,
		image:          image,
		screen:         screen,
		logger:         logger,
	}
}

// SetContainerRegistry configures the container registry for lifecycle tracking.
func (m *DockerProvider) SetContainerRegistry(reg container.ContainerRegistry) {
	m.registry = reg
}

// chromeArgs returns CMD args for the chrome container (appended to ENTRYPOINT).
func (m *DockerProvider) chromeArgs() []string {
	return []string{"--window-size=" + m.screen, "about:blank"}
}

// ChromeHostname returns the Chrome container hostname for a channel.
func ChromeHostname(channelID string) string {
	return containerPrefix + container.SanitizeName(channelID)
}

// EnsureBrowser ensures a Chrome sidecar container is running for the channel.
// Creates the Chrome container if it doesn't exist; the host connects via the
// mapped 127.0.0.1:hostPort (no Docker network required).
func (m *DockerProvider) EnsureBrowser(ctx context.Context, channelID, _ string) error {
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

	// Fast path: check registry for an existing Chrome container (populated by Restore at startup).
	// Avoids a Docker list query by using the container ID from the in-memory registry.
	if m.registry != nil {
		if info := m.registry.FindByChannelAndType(channelID, container.ContainerTypeChrome); info != nil {
			if m.isContainerRunning(ctx, info.ContainerID) {
				if port, err := m.getHostPort(ctx, info.ContainerID); err == nil && isChromeReachable("127.0.0.1:"+port) {
					m.logger.Info("reusing Chrome container from registry",
						"channel_id", channelID,
						"container_id", info.ContainerID,
						"host_port", port,
					)
					sess := newBrowserSession(m.timeNow())
					sess.chromeContainerID = info.ContainerID
					sess.hostPort = port
					m.sessions[channelID] = sess
					return nil
				}
			}
			// Registry says it exists but it's not usable — clean up.
			m.logger.Info("removing stale Chrome container from registry", "container_id", info.ContainerID)
			_ = m.api.ContainerRemove(ctx, info.ContainerID, containertypes.RemoveOptions{Force: true, RemoveVolumes: true})
			m.registry.Unregister(info.ContainerID)
		}
	}

	containerName := ChromeHostname(channelID)

	// Check if Chrome container already exists (e.g. from a previous daemon run
	// before the registry was introduced, or created outside of loop).
	if id, port := m.findExistingChrome(ctx, containerName); id != "" {
		if isChromeReachable("127.0.0.1:" + port) {
			m.logger.Info("reusing existing Chrome container",
				"channel_id", channelID,
				"container_id", id,
				"host_port", port,
			)
			sess := newBrowserSession(m.timeNow())
			sess.chromeContainerID = id
			sess.hostPort = port
			m.sessions[channelID] = sess
			if m.registry != nil {
				m.registry.Register(&container.ContainerInfo{
					ContainerID:   id,
					ChannelID:     channelID,
					Type:          container.ContainerTypeChrome,
					ContainerName: containerName,
				})
			}
			return nil
		}
		// Stale container — remove it so we can create a fresh one.
		m.logger.Info("removing stale Chrome container", "container_id", id)
		_ = m.api.ContainerRemove(ctx, id, containertypes.RemoveOptions{Force: true, RemoveVolumes: true})
		if m.registry != nil {
			m.registry.Unregister(id)
		}
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

	sess := newBrowserSession(m.timeNow())
	sess.chromeContainerID = resp.ID
	sess.hostPort = hostPort
	m.sessions[channelID] = sess

	if m.registry != nil {
		m.registry.Register(&container.ContainerInfo{
			ContainerID:   resp.ID,
			ChannelID:     channelID,
			Type:          container.ContainerTypeChrome,
			ContainerName: containerName,
		})
	}

	m.logger.Info("Chrome sidecar started",
		"channel_id", channelID,
		"container_id", resp.ID,
		"host_port", hostPort,
	)

	return nil
}

// GetCDPEndpoint returns the CDP WebSocket URL for the channel's Chrome container.
func (m *DockerProvider) GetCDPEndpoint(channelID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[channelID]; ok && sess.hostPort != "" {
		return fmt.Sprintf("ws://127.0.0.1:%s", sess.hostPort)
	}
	return fmt.Sprintf("ws://127.0.0.1:%d", CDPPort)
}

// IsHostMode returns false — DockerProvider always uses Docker containers.
func (m *DockerProvider) IsHostMode() bool {
	return false
}

// GetContainerID returns the Chrome container ID for a channel.
func (m *DockerProvider) GetContainerID(channelID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[channelID]
	if !ok {
		return "", false
	}
	return sess.chromeContainerID, true
}

// StopBrowser stops the Chrome container for a channel and cleans up the
// session. The container is not removed from Docker or unregistered from
// the registry — callers should use ScheduleRemove or RemoveContainer for that.
// Returns the stopped container ID, or empty string if no session existed.
func (m *DockerProvider) StopBrowser(ctx context.Context, channelID string) (string, error) {
	m.mu.Lock()
	sess, ok := m.sessions[channelID]
	if ok {
		delete(m.sessions, channelID)
	}
	m.mu.Unlock()

	if !ok {
		return "", nil
	}

	m.logger.Info("stopping Chrome sidecar",
		"channel_id", channelID,
		"container_id", sess.chromeContainerID,
	)

	timeout := 5
	_ = m.api.ContainerStop(ctx, sess.chromeContainerID, containertypes.StopOptions{
		Timeout: &timeout,
	})

	if m.registry != nil {
		m.registry.UpdateStatus(sess.chromeContainerID, container.ContainerStatusStopped)
	}

	return sess.chromeContainerID, nil
}

// IsRunning returns true if Chrome is running for the given channel.
func (m *DockerProvider) IsRunning(ctx context.Context, channelID string) bool {
	m.mu.Lock()
	sess, ok := m.sessions[channelID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	return m.isContainerRunning(ctx, sess.chromeContainerID)
}

// Cleanup stops and removes all Chrome sidecar containers.
func (m *DockerProvider) Cleanup(ctx context.Context) {
	m.mu.Lock()
	channels := make([]string, 0, len(m.sessions))
	for ch := range m.sessions {
		channels = append(channels, ch)
	}
	m.mu.Unlock()

	for _, ch := range channels {
		containerID, _ := m.StopBrowser(ctx, ch)
		if containerID != "" && m.registry != nil {
			_ = m.registry.RemoveContainer(ctx, containerID)
		}
	}
}

// SessionChannels returns the list of channel IDs with active browser sessions.
func (m *DockerProvider) SessionChannels() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	channels := make([]string, 0, len(m.sessions))
	for ch := range m.sessions {
		channels = append(channels, ch)
	}
	return channels
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

func (m *DockerProvider) isContainerRunning(ctx context.Context, containerID string) bool {
	info, err := m.api.ContainerInspect(ctx, containerID)
	if err != nil {
		return false
	}
	return info.State != nil && info.State.Running
}

func (m *DockerProvider) findExistingChrome(ctx context.Context, containerName string) (id, hostPort string) {
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

func (m *DockerProvider) getHostPort(ctx context.Context, containerID string) (string, error) {
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
