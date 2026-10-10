// Package runtime runs plugin containers: one per plugin and channel,
// started on first use, stopped when idle, and restarted with backoff after
// a failed start.
package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

// DockerClient is the part of the Docker API the Manager uses.
type DockerClient interface {
	ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error)
	ContainerStart(ctx context.Context, container string, options containertypes.StartOptions) error
	ContainerRemove(ctx context.Context, container string, options containertypes.RemoveOptions) error
	ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error)
	ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error)
	ContainerLogs(ctx context.Context, container string, options containertypes.LogsOptions) (io.ReadCloser, error)
	VolumeRemove(ctx context.Context, volumeID string, force bool) error
	ImageInspect(ctx context.Context, imageID string, opts ...client.ImageInspectOption) (image.InspectResponse, error)
}

// Store records each plugin container's state.
type Store interface {
	UpsertPluginContainer(ctx context.Context, c *db.PluginContainer) error
	DeletePluginContainer(ctx context.Context, pluginID, channelID string) error
}

// Tokens issues each plugin container its API token and revokes it when the
// container goes.
type Tokens interface {
	IssuePlugin(containerID, channelID, dirPath, pluginID string) (string, error)
	Revoke(containerID string) error
}

// Plugin is an installed plugin: its manifest and the image it runs, pinned
// to its digest.
type Plugin struct {
	Manifest *plugin.Manifest
	Image    string
}

// Channel is the channel a plugin container runs for.
type Channel struct {
	ID      string
	DirPath string
}

// Endpoint is where a running plugin container is reached, and the secret
// Loop's requests to it carry.
type Endpoint struct {
	URL    string
	Secret string
}

const (
	// Port is the port a plugin listens on inside its container.
	Port = 8080
	// LabelKey labels a plugin container with its plugin's id.
	LabelKey = "loop-plugin"
	// channelLabelKey labels a plugin container with its channel.
	channelLabelKey = "loop-plugin-channel"

	// runDir is where a container's run dir, holding its token and secret,
	// is mounted.
	runDir = "/run/loop"
	// dataDir is where a container's volume is mounted.
	dataDir = "/data"
	// tokenFile and secretFile are the names of the plugin's API token and
	// the secret Loop's requests carry, in the run dir.
	tokenFile  = "plugin-token"
	secretFile = "loop-secret"

	// defaultMemory and defaultCPUs are a plugin's resources when its
	// manifest asks for none; maxMemory and maxCPUs cap what it may ask for.
	defaultMemory = 256 << 20
	defaultCPUs   = 0.5
	maxMemory     = 2 << 30
	maxCPUs       = 2
	pidsLimit     = 256
	cpuPeriod     = 100000

	// maxFailures is how many starts in a row may fail before the container
	// is marked failed and no longer started on use.
	maxFailures = 3

	// logLines and maxLogBytes bound the log tail a failed start records.
	logLines    = 20
	maxLogBytes = 8 << 10
)

// ContainerName is the name of a plugin's container, and of its data volume,
// in a channel.
func ContainerName(pluginID, channelID string) string {
	return "loop-plugin-" + pluginID + "-" + channelID
}

// Config configures a Manager.
type Config struct {
	// Root is the dir plugins keep host files in, ~/.loop/plugins.
	Root string
	// APIURL is the Loop API as a plugin container reaches it.
	APIURL string
	// IdleTimeout is how long a container may go unused before it's stopped.
	IdleTimeout time.Duration
	// StartWait caps how long a started container has to become healthy.
	StartWait time.Duration
	// Proxy is the proxy plugin containers run with.
	Proxy container.ProxySettings
	// Gate, when set, runs plugin containers under agentgate.
	Gate *GateConfig
}

// instance is a running plugin container.
type instance struct {
	containerID string
	hostPort    int
	endpoint    Endpoint
	lastUsed    time.Time
}

// key names a plugin's container in a channel.
type key struct{ pluginID, channelID string }

// failure counts a plugin container's failed starts in a row.
type failure struct {
	count   int
	retryAt time.Time
	err     string
}

// Manager runs plugin containers.
type Manager struct {
	api       DockerClient
	store     Store
	tokens    Tokens
	imageGate container.ImageGate
	cfg       Config
	logger    *slog.Logger

	getenv   func(string) string
	now      func() time.Time
	readRand func([]byte) (int, error)
	// healthy reports whether the container serving url answers its health
	// check.
	healthy func(ctx context.Context, url, secret string) bool
	// pollInterval is how often a starting container's health is checked.
	pollInterval time.Duration

	mu       sync.Mutex
	locks    map[key]*sync.Mutex
	running  map[key]*instance
	failures map[key]*failure
}

// NewManager returns a Manager.
func NewManager(api DockerClient, store Store, tokens Tokens, cfg Config, logger *slog.Logger) *Manager {
	client := &http.Client{Timeout: 2 * time.Second}
	return &Manager{
		api:          api,
		store:        store,
		tokens:       tokens,
		cfg:          cfg,
		logger:       logger,
		getenv:       os.Getenv,
		now:          time.Now,
		readRand:     rand.Read,
		healthy:      func(ctx context.Context, url, secret string) bool { return checkHealth(ctx, client, url, secret) },
		pollInterval: 250 * time.Millisecond,
		locks:        map[key]*sync.Mutex{},
		running:      map[key]*instance{},
		failures:     map[key]*failure{},
	}
}

// SetImageGate makes container creation wait out builds of a plugin's image.
func (m *Manager) SetImageGate(gate container.ImageGate) {
	m.imageGate = gate
}

func (m *Manager) lock(k key) func() {
	m.mu.Lock()
	l, ok := m.locks[k]
	if !ok {
		l = &sync.Mutex{}
		m.locks[k] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Ensure returns the endpoint of p's container in ch, starting the
// container when it isn't running. A start that fails is retried on a
// later use, after a backoff; after maxFailures in a row the container is
// marked failed and Ensure refuses until Reset.
func (m *Manager) Ensure(ctx context.Context, p Plugin, ch Channel) (Endpoint, error) {
	id := p.Manifest.ID
	k := key{id, ch.ID}
	defer m.lock(k)()

	m.mu.Lock()
	inst := m.running[k]
	f := m.failures[k]
	m.mu.Unlock()
	if inst != nil {
		if m.isRunning(ctx, inst.containerID) {
			m.mu.Lock()
			inst.lastUsed = m.now()
			m.mu.Unlock()
			return inst.endpoint, nil
		}
		m.logger.Info("plugin container exited", "plugin_id", id, "channel_id", ch.ID, "container_id", inst.containerID)
		m.discard(ctx, k, inst.containerID)
	}
	if f != nil {
		if f.count >= maxFailures {
			return Endpoint{}, fmt.Errorf("plugin %s failed to start in channel %s %d times: %s", id, ch.ID, f.count, f.err)
		}
		if wait := f.retryAt.Sub(m.now()); wait > 0 {
			return Endpoint{}, fmt.Errorf("plugin %s failed to start in channel %s, retrying in %s: %s", id, ch.ID, wait.Round(time.Second), f.err)
		}
	}

	inst, err := m.start(ctx, p, ch)
	if err != nil {
		m.recordFailure(ctx, id, ch.ID, err)
		return Endpoint{}, err
	}
	m.mu.Lock()
	m.running[k] = inst
	delete(m.failures, k)
	m.mu.Unlock()
	m.upsert(ctx, &db.PluginContainer{PluginID: id, ChannelID: ch.ID, ContainerID: inst.containerID, HostPort: inst.hostPort, Status: db.PluginContainerRunning})
	return inst.endpoint, nil
}

// start creates and starts p's container in ch and waits for it to become
// healthy. Whatever it leaves behind on failure is removed.
func (m *Manager) start(ctx context.Context, p Plugin, ch Channel) (*instance, error) {
	id := p.Manifest.ID
	name := ContainerName(id, ch.ID)
	// A container left from an earlier daemon holds a token this one
	// doesn't know; replace it.
	m.removeByName(ctx, name)

	if m.imageGate != nil {
		err := m.imageGate.WaitBuilds(ctx, []string{p.Image}, func(image string) {
			m.logger.Info("waiting for the image build before starting the plugin", "plugin_id", id, "channel_id", ch.ID, "image", image)
		})
		if err != nil {
			return nil, fmt.Errorf("waiting for the image build: %w", err)
		}
	}

	run := m.runDir(id, ch.ID)
	if err := os.MkdirAll(run, 0o755); err != nil {
		return nil, fmt.Errorf("creating the plugin run dir: %w", err)
	}
	secret, err := m.newSecret()
	if err != nil {
		return nil, err
	}
	memory, cpus := resources(p.Manifest.Resources)
	cfg := &containertypes.Config{
		Image:        p.Image,
		Labels:       map[string]string{LabelKey: id, channelLabelKey: ch.ID},
		ExposedPorts: nat.PortSet{portKey: struct{}{}},
		Env: append([]string{
			"LOOP_PLUGIN_ID=" + id,
			"LOOP_CHANNEL_ID=" + ch.ID,
			"LOOP_CHANNEL_DIR=" + ch.DirPath,
			fmt.Sprintf("LOOP_PLUGIN_PORT=%d", Port),
			"LOOP_API_URL=" + m.cfg.APIURL,
			"LOOP_PLUGIN_TOKEN_FILE=" + runDir + "/" + tokenFile,
			"LOOP_PLUGIN_SECRET_FILE=" + runDir + "/" + secretFile,
			"LOOP_DATA_DIR=" + dataDir,
		}, container.ProxyEnv(m.cfg.Proxy, m.getenv)...),
	}
	host := &containertypes.HostConfig{
		ExtraHosts:     []string{"host.docker.internal:host-gateway"},
		ReadonlyRootfs: true,
		Tmpfs:          map[string]string{"/tmp": "rw,noexec,nosuid,size=64m"},
		SecurityOpt:    []string{"no-new-privileges"},
		CapDrop:        []string{"ALL"},
		Resources: containertypes.Resources{
			Memory:    memory,
			CPUQuota:  int64(cpus * cpuPeriod),
			CPUPeriod: cpuPeriod,
			PidsLimit: new(int64(pidsLimit)),
		},
		PortBindings: nat.PortMap{portKey: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}}},
		Mounts: []mount.Mount{
			{Type: mount.TypeVolume, Source: name, Target: dataDir},
			{Type: mount.TypeBind, Source: run, Target: runDir, ReadOnly: true},
		},
	}
	if m.cfg.Gate != nil {
		if err := m.applyGate(ctx, p, ch, cfg, host); err != nil {
			return nil, err
		}
	}
	resp, err := m.api.ContainerCreate(ctx, cfg, host, nil, nil, name)
	if err != nil {
		return nil, fmt.Errorf("creating the plugin container: %w", err)
	}
	inst, err := m.boot(ctx, p, ch, resp.ID, run, secret)
	if err != nil {
		m.discard(ctx, key{id, ch.ID}, resp.ID)
		return nil, err
	}
	m.logger.Info("plugin container started", "plugin_id", id, "channel_id", ch.ID, "container_id", resp.ID, "url", inst.endpoint.URL)
	return inst, nil
}

// boot issues a created container its token, writes its run dir, starts it
// and waits for it to become healthy.
func (m *Manager) boot(ctx context.Context, p Plugin, ch Channel, containerID, run, secret string) (*instance, error) {
	tok, err := m.tokens.IssuePlugin(containerID, ch.ID, ch.DirPath, p.Manifest.ID)
	if err != nil {
		return nil, fmt.Errorf("issuing the plugin token: %w", err)
	}
	if err := os.WriteFile(filepath.Join(run, tokenFile), []byte(tok), 0o644); err != nil {
		return nil, fmt.Errorf("writing the plugin token: %w", err)
	}
	if err := os.WriteFile(filepath.Join(run, secretFile), []byte(secret), 0o644); err != nil {
		return nil, fmt.Errorf("writing the plugin secret: %w", err)
	}
	if err := m.api.ContainerStart(ctx, containerID, containertypes.StartOptions{}); err != nil {
		return nil, fmt.Errorf("starting the plugin container: %w", err)
	}
	info, err := m.api.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspecting the plugin container: %w", err)
	}
	port := 0
	if info.NetworkSettings != nil {
		if b := info.NetworkSettings.Ports[portKey]; len(b) > 0 {
			port, _ = strconv.Atoi(b[0].HostPort)
		}
	}
	if port == 0 {
		return nil, fmt.Errorf("plugin container has no host port for %s", portKey)
	}
	inst := &instance{containerID: containerID, hostPort: port, endpoint: Endpoint{URL: fmt.Sprintf("http://127.0.0.1:%d", port), Secret: secret}, lastUsed: m.now()}
	if err := m.waitHealthy(ctx, containerID, inst.endpoint); err != nil {
		return nil, m.withLogs(ctx, containerID, err)
	}
	return inst, nil
}

// withLogs adds the last lines a container logged to err, so a plugin that
// fails to come up says why.
func (m *Manager) withLogs(ctx context.Context, containerID string, err error) error {
	rc, lerr := m.api.ContainerLogs(ctx, containerID, containertypes.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(logLines)})
	if lerr != nil {
		return err
	}
	defer rc.Close()
	var buf bytes.Buffer
	// A stream cut at the cap ends mid-frame; what came before it is kept.
	_, _ = stdcopy.StdCopy(&buf, &buf, io.LimitReader(rc, maxLogBytes))
	logs := strings.TrimSpace(buf.String())
	if logs == "" {
		return err
	}
	return fmt.Errorf("%w; its last log lines:\n%s", err, logs)
}

// portKey is the plugin's port as Docker names it.
const portKey = nat.Port("8080/tcp")

// waitHealthy waits for the container at ep to answer its health check,
// up to the configured StartWait, failing early when the container exits.
func (m *Manager) waitHealthy(ctx context.Context, containerID string, ep Endpoint) error {
	deadline := m.now().Add(m.cfg.StartWait)
	for {
		if m.healthy(ctx, ep.URL, ep.Secret) {
			return nil
		}
		info, err := m.api.ContainerInspect(ctx, containerID)
		if err == nil && info.State != nil && !info.State.Running {
			return fmt.Errorf("plugin container exited with code %d before becoming healthy", info.State.ExitCode)
		}
		if !m.now().Before(deadline) {
			return fmt.Errorf("plugin container not healthy after %s", m.cfg.StartWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.pollInterval):
		}
	}
}

// checkHealth reports whether GET url/healthz answers 200.
func checkHealth(ctx context.Context, client *http.Client, url, secret string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/healthz", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// recordFailure counts a failed start and backs off the next one: 1s, 2s,
// 4s and so on, until maxFailures marks the container failed.
func (m *Manager) recordFailure(ctx context.Context, pluginID, channelID string, err error) {
	k := key{pluginID, channelID}
	m.mu.Lock()
	f := m.failures[k]
	if f == nil {
		f = &failure{}
		m.failures[k] = f
	}
	f.count++
	f.err = err.Error()
	f.retryAt = m.now().Add(time.Second << (f.count - 1))
	count := f.count
	m.mu.Unlock()
	status := db.PluginContainerStopped
	if count >= maxFailures {
		status = db.PluginContainerFailed
	}
	m.logger.Warn("plugin container failed to start", "plugin_id", pluginID, "channel_id", channelID, "failures", count, "error", err)
	m.upsert(ctx, &db.PluginContainer{PluginID: pluginID, ChannelID: channelID, Status: status, LastError: err.Error()})
}

// Reset clears a plugin container's failed starts, so the next use starts
// it again.
func (m *Manager) Reset(pluginID, channelID string) {
	m.mu.Lock()
	delete(m.failures, key{pluginID, channelID})
	m.mu.Unlock()
}

// Stop removes a plugin's container in a channel, keeping its data volume.
// The next use starts a new one.
func (m *Manager) Stop(ctx context.Context, pluginID, channelID string) {
	defer m.lock(key{pluginID, channelID})()
	m.stopLocked(ctx, pluginID, channelID)
}

func (m *Manager) stopLocked(ctx context.Context, pluginID, channelID string) {
	k := key{pluginID, channelID}
	m.mu.Lock()
	inst := m.running[k]
	m.mu.Unlock()
	if inst == nil {
		return
	}
	m.logger.Info("stopping plugin container", "plugin_id", pluginID, "channel_id", channelID, "container_id", inst.containerID)
	m.discard(ctx, k, inst.containerID)
	m.upsert(ctx, &db.PluginContainer{PluginID: pluginID, ChannelID: channelID, Status: db.PluginContainerStopped})
}

// Remove removes a plugin's container in a channel, its run and gate dirs
// and its row; purge also removes its data volume and gate audit log.
func (m *Manager) Remove(ctx context.Context, pluginID, channelID string, purge bool) error {
	defer m.lock(key{pluginID, channelID})()
	m.stopLocked(ctx, pluginID, channelID)
	m.removeByName(ctx, ContainerName(pluginID, channelID))
	m.Reset(pluginID, channelID)
	var errs []error
	if err := os.RemoveAll(m.runDir(pluginID, channelID)); err != nil {
		errs = append(errs, fmt.Errorf("removing the plugin run dir: %w", err))
	}
	if err := os.RemoveAll(m.gateDir(pluginID, channelID)); err != nil {
		errs = append(errs, fmt.Errorf("removing the plugin gate dir: %w", err))
	}
	if err := m.store.DeletePluginContainer(ctx, pluginID, channelID); err != nil {
		errs = append(errs, fmt.Errorf("deleting the plugin container row: %w", err))
	}
	if purge {
		if err := m.api.VolumeRemove(ctx, ContainerName(pluginID, channelID), true); err != nil {
			errs = append(errs, fmt.Errorf("removing the plugin data volume: %w", err))
		}
		if err := os.RemoveAll(m.auditDir(pluginID, channelID)); err != nil {
			errs = append(errs, fmt.Errorf("removing the plugin gate audit dir: %w", err))
		}
	}
	return errors.Join(errs...)
}

// StopIdle stops the containers unused for longer than IdleTimeout.
func (m *Manager) StopIdle(ctx context.Context) {
	cutoff := m.now().Add(-m.cfg.IdleTimeout)
	m.mu.Lock()
	var idle []key
	for k, inst := range m.running {
		if inst.lastUsed.Before(cutoff) {
			idle = append(idle, k)
		}
	}
	m.mu.Unlock()
	for _, k := range idle {
		m.Stop(ctx, k.pluginID, k.channelID)
	}
}

// RunIdleLoop stops idle containers every interval until ctx ends.
func (m *Manager) RunIdleLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.StopIdle(ctx)
		}
	}
}

// RemoveLeftovers removes the plugin containers an earlier daemon left
// running. Their tokens died with it; each starts again on its next use.
func (m *Manager) RemoveLeftovers(ctx context.Context) error {
	list, err := m.api.ContainerList(ctx, containertypes.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", LabelKey))})
	if err != nil {
		return fmt.Errorf("listing plugin containers: %w", err)
	}
	for _, c := range list {
		m.logger.Info("removing a plugin container left from an earlier run", "container_id", c.ID, "plugin_id", c.Labels[LabelKey], "channel_id", c.Labels[channelLabelKey])
		m.discard(ctx, key{}, c.ID)
		m.upsert(ctx, &db.PluginContainer{PluginID: c.Labels[LabelKey], ChannelID: c.Labels[channelLabelKey], Status: db.PluginContainerStopped})
	}
	return nil
}

// StopAll stops every running container, at shutdown.
func (m *Manager) StopAll(ctx context.Context) {
	m.stopWhere(ctx, func(key) bool { return true })
}

// StopPlugin stops a plugin's containers in every channel and forgets their
// failed starts, so the next use starts its current image afresh.
func (m *Manager) StopPlugin(ctx context.Context, pluginID string) {
	m.stopWhere(ctx, func(k key) bool { return k.pluginID == pluginID })
	m.mu.Lock()
	for k := range m.failures {
		if k.pluginID == pluginID {
			delete(m.failures, k)
		}
	}
	m.mu.Unlock()
}

// stopWhere stops the running containers whose key match picks.
func (m *Manager) stopWhere(ctx context.Context, match func(key) bool) {
	m.mu.Lock()
	var keys []key
	for k := range m.running {
		if match(k) {
			keys = append(keys, k)
		}
	}
	m.mu.Unlock()
	for _, k := range keys {
		m.Stop(ctx, k.pluginID, k.channelID)
	}
}

// discard removes a container, revokes its token and forgets it as k's.
func (m *Manager) discard(ctx context.Context, k key, containerID string) {
	if err := m.api.ContainerRemove(ctx, containerID, containertypes.RemoveOptions{Force: true}); err != nil {
		m.logger.Warn("removing plugin container", "container_id", containerID, "error", err)
	}
	if err := m.tokens.Revoke(containerID); err != nil {
		m.logger.Warn("revoking plugin token", "container_id", containerID, "error", err)
	}
	m.mu.Lock()
	if inst := m.running[k]; inst != nil && inst.containerID == containerID {
		delete(m.running, k)
	}
	m.mu.Unlock()
}

// removeByName removes a container by name, if there is one.
func (m *Manager) removeByName(ctx context.Context, name string) {
	list, err := m.api.ContainerList(ctx, containertypes.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("name", "^/"+name+"$"))})
	if err != nil {
		m.logger.Warn("listing plugin containers", "name", name, "error", err)
		return
	}
	for _, c := range list {
		m.discard(ctx, key{}, c.ID)
	}
}

func (m *Manager) isRunning(ctx context.Context, containerID string) bool {
	info, err := m.api.ContainerInspect(ctx, containerID)
	return err == nil && info.State != nil && info.State.Running
}

func (m *Manager) upsert(ctx context.Context, c *db.PluginContainer) {
	if err := m.store.UpsertPluginContainer(ctx, c); err != nil {
		m.logger.Warn("recording plugin container", "plugin_id", c.PluginID, "channel_id", c.ChannelID, "error", err)
	}
}

func (m *Manager) runDir(pluginID, channelID string) string {
	return filepath.Join(m.cfg.Root, pluginID, "run", channelID)
}

func (m *Manager) newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := m.readRand(b); err != nil {
		return "", fmt.Errorf("generating the plugin secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// resources returns the memory, in bytes, and the CPUs a plugin's container
// gets: what its manifest asks for, within Loop's caps. Validate has
// checked the memory parses.
func resources(r *plugin.Resources) (int64, float64) {
	memory, cpus := int64(defaultMemory), defaultCPUs
	if r != nil {
		if r.Memory != "" {
			memory, _ = units.RAMInBytes(r.Memory)
		}
		if r.CPUs > 0 {
			cpus = r.CPUs
		}
	}
	return min(memory, maxMemory), min(cpus, maxCPUs)
}
