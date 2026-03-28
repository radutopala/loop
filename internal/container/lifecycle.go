package container

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/events"
)

// ImageBroadcaster is a narrow interface for broadcasting image events.
type ImageBroadcaster interface {
	BroadcastImageBuildStatus(data events.ImageBuildStatusData)
	BroadcastImageUpdateAvailable(data events.ImageUpdateAvailableData)
}

// lifecycleSystem abstracts OS calls needed by the lifecycle manager.
type lifecycleSystem interface {
	UserHomeDir() (string, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
}

// ImageBuildStatus represents the current state of an image build.
type ImageBuildStatus struct {
	State     string    `json:"state"`           // "idle", "building", "completed", "failed"
	Phase     string    `json:"phase,omitempty"` // "removing", "building", ""
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

// ImageVersions stores the versions baked into the Docker image.
type ImageVersions struct {
	LoopVersion   string    `json:"loop_version"`
	ClaudeVersion string    `json:"claude_version"`
	BuiltAt       time.Time `json:"built_at"`
}

// ImageLifecycleManager orchestrates image builds, version tracking,
// and update checking for the Loop agent Docker image.
type ImageLifecycleManager struct {
	client      DockerClient
	broadcaster ImageBroadcaster
	sys         lifecycleSystem
	logger      *slog.Logger

	mu       sync.Mutex
	status   ImageBuildStatus
	versions ImageVersions

	containerDir        string
	imageName           string
	loopVersion         string
	latestClaudeVersion func() string
}

// NewImageLifecycleManager creates a new lifecycle manager.
func NewImageLifecycleManager(
	client DockerClient,
	broadcaster ImageBroadcaster,
	sys lifecycleSystem,
	logger *slog.Logger,
	containerDir, imageName, loopVersion string,
	latestClaudeVersion func() string,
) *ImageLifecycleManager {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	m := &ImageLifecycleManager{
		client:              client,
		broadcaster:         broadcaster,
		sys:                 sys,
		logger:              logger,
		containerDir:        containerDir,
		imageName:           imageName,
		loopVersion:         loopVersion,
		latestClaudeVersion: latestClaudeVersion,
		status:              ImageBuildStatus{State: "idle"},
	}
	return m
}

// Status returns the current image build status.
func (m *ImageLifecycleManager) Status() ImageBuildStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Versions returns the version info for the current image by reading Docker labels.
func (m *ImageLifecycleManager) Versions() ImageVersions {
	if labels, err := m.client.ImageInspectLabels(context.Background(), m.imageName); err == nil && labels != nil {
		return ImageVersions{
			LoopVersion:   labels["loop.version"],
			ClaudeVersion: labels["loop.claude_version"],
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.versions
}

// RemoveImage removes the current image and all containers using it.
func (m *ImageLifecycleManager) RemoveImage(ctx context.Context) error {
	return m.client.RemoveImageAndContainers(ctx, m.imageName)
}

// Rebuild removes the old image and builds a new one asynchronously.
// Returns an error if a build is already in progress.
func (m *ImageLifecycleManager) Rebuild(ctx context.Context) error {
	m.mu.Lock()
	if m.status.State == "building" {
		m.mu.Unlock()
		return fmt.Errorf("build already in progress")
	}
	m.status = ImageBuildStatus{State: "building", Phase: "building", StartedAt: time.Now()}
	m.mu.Unlock()

	m.broadcastStatus()

	// Use a background context — the caller's request context will be
	// canceled as soon as the 202 response is sent.
	go m.doRebuild(context.Background())
	return nil
}

func (m *ImageLifecycleManager) doRebuild(ctx context.Context) {
	// No need to remove — docker build with the same tag overwrites in place.
	if err := m.client.ImageBuild(ctx, m.containerDir, m.imageName); err != nil {
		m.mu.Lock()
		m.status = ImageBuildStatus{State: "failed", Error: err.Error()}
		m.mu.Unlock()
		m.broadcastStatus()
		m.logger.Error("image lifecycle: build failed", "error", err)
		return
	}

	// Read versions from the newly built image labels.
	var v ImageVersions
	if labels, err := m.client.ImageInspectLabels(ctx, m.imageName); err == nil && labels != nil {
		v = ImageVersions{
			LoopVersion:   labels["loop.version"],
			ClaudeVersion: labels["loop.claude_version"],
			BuiltAt:       time.Now(),
		}
	} else {
		v = ImageVersions{
			LoopVersion:   m.loopVersion,
			ClaudeVersion: "unknown",
			BuiltAt:       time.Now(),
		}
	}

	m.mu.Lock()
	m.versions = v
	m.status = ImageBuildStatus{State: "completed"}
	m.mu.Unlock()

	m.broadcastStatus()
	m.logger.Info("image lifecycle: build completed", "loop_version", v.LoopVersion, "claude_version", v.ClaudeVersion)
}

// CheckClaudeUpdate checks if a newer Claude Code version is available.
func (m *ImageLifecycleManager) CheckClaudeUpdate() (latestVersion string, available bool) {
	latest := m.latestClaudeVersion()
	if latest == "" || len(latest) > 20 {
		return "", false // invalid or error response
	}

	m.mu.Lock()
	current := m.versions.ClaudeVersion
	m.mu.Unlock()

	if current == "" || current == latest {
		return "", false
	}
	return latest, true
}

// RunUpdateChecker periodically checks for Claude Code updates and broadcasts events.
func (m *ImageLifecycleManager) RunUpdateChecker(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Check once at startup.
	m.checkAndBroadcast()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAndBroadcast()
		}
	}
}

func (m *ImageLifecycleManager) checkAndBroadcast() {
	latest, available := m.CheckClaudeUpdate()
	if !available {
		return
	}

	m.mu.Lock()
	current := m.versions.ClaudeVersion
	m.mu.Unlock()

	m.logger.Info("image lifecycle: Claude Code update available", "current", current, "latest", latest)
	if m.broadcaster != nil {
		m.broadcaster.BroadcastImageUpdateAvailable(events.ImageUpdateAvailableData{
			CurrentVersion: current,
			LatestVersion:  latest,
			Component:      "claude_code",
		})
	}
}

func (m *ImageLifecycleManager) broadcastStatus() {
	if m.broadcaster == nil {
		return
	}
	m.mu.Lock()
	s := m.status
	m.mu.Unlock()

	m.broadcaster.BroadcastImageBuildStatus(events.ImageBuildStatusData{
		State: s.State,
		Phase: s.Phase,
		Error: s.Error,
	})
}
