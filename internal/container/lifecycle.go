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

// ReclaimResult reports the bytes freed by a "reclaim Docker space" action,
// broken down by source so the UI can show what was cleaned.
type ReclaimResult struct {
	BuildCacheReclaimed uint64 `json:"build_cache_reclaimed"`
	ImagesReclaimed     uint64 `json:"images_reclaimed"`
	TotalReclaimed      uint64 `json:"total_reclaimed"`
}

// containerUnregisterer is the subset of ContainerRegistry needed to clean up
// stale entries when containers are force-removed during image rebuild/removal.
type containerUnregisterer interface {
	List() []*ContainerInfo
	Unregister(containerID string)
}

// ImageLifecycleManager orchestrates image builds, version tracking,
// and update checking for the Loop agent Docker image.
type ImageLifecycleManager struct {
	client      DockerClient
	broadcaster ImageBroadcaster
	sys         lifecycleSystem
	logger      *slog.Logger
	registry    containerUnregisterer

	mu              sync.Mutex
	status          ImageBuildStatus
	versions        ImageVersions
	updateAvailable *events.ImageUpdateAvailableData

	containerDir        string
	imageName           string
	loopVersion         string
	latestClaudeVersion func() string
	childRebuilder      func(ctx context.Context, handoff func()) // optional child-image cascade; see SetChildRebuilder
	sidecarRebuilder    func(context.Context) error
	sidecarImage        string

	// builds counts the builds in progress per image, keyed by normalized
	// ref; changed, when not nil, is closed when one of them ends. See
	// WaitBuilds.
	buildsMu sync.Mutex
	builds   map[string]int
	changed  chan struct{}
}

// BeginBuild marks images as being built, so containers created on them
// until end is called wait for it (see WaitBuilds). Call end whether the
// build succeeded or not; only its first call counts.
//
// A base image build hands over to the child-image cascade that follows it
// rather than ending on its own: the status says "completed" once the base
// image is built, but project images FROM it are still to be rebuilt, and a
// container started on one meanwhile would run the old project image against
// the new daemon. So the cascade marks the project images it will rebuild
// before the base build ends (see RebuildChildren).
func (m *ImageLifecycleManager) BeginBuild(images ...string) (end func()) {
	m.buildsMu.Lock()
	defer m.buildsMu.Unlock()
	for _, image := range images {
		m.builds[normalizeImageRef(image)]++
	}
	return sync.OnceFunc(func() {
		m.buildsMu.Lock()
		defer m.buildsMu.Unlock()
		for _, image := range images {
			ref := normalizeImageRef(image)
			if m.builds[ref]--; m.builds[ref] == 0 {
				delete(m.builds, ref)
			}
		}
		if m.changed != nil {
			close(m.changed)
			m.changed = nil
		}
	})
}

// WaitBuilds returns once none of images is being built, or with ctx's error;
// builds of other images don't hold it. onWait, when not nil, is called with
// the image it waits for, again each time that changes (the base image's
// build handing over to the project image's, say).
func (m *ImageLifecycleManager) WaitBuilds(ctx context.Context, images []string, onWait func(image string)) error {
	waitingFor := ""
	for {
		m.buildsMu.Lock()
		building := ""
		for _, image := range images {
			if m.builds[normalizeImageRef(image)] > 0 {
				building = image
				break
			}
		}
		if building == "" {
			m.buildsMu.Unlock()
			return nil
		}
		if m.changed == nil {
			m.changed = make(chan struct{})
		}
		changed := m.changed
		m.buildsMu.Unlock()

		if building != waitingFor && onWait != nil {
			onWait(building)
		}
		waitingFor = building
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// SetSidecarRebuilder wires the browser sidecar image build, run as part of
// every API-driven rebuild, and the image it builds.
//
// The daemon builds that image itself at startup, but only when it is missing,
// so an install that edits the sidecar Dockerfile has no other way to act on
// the edit: the rebuild action offers to rebuild "the image" and used to leave
// the sidecar on whatever it was built with months ago.
func (m *ImageLifecycleManager) SetSidecarRebuilder(image string, fn func(context.Context) error) {
	m.sidecarImage = image
	m.sidecarRebuilder = fn
}

// SetChildRebuilder wires the child-image cascade, invoked after every
// successful base-image build so project images FROM the base get rebuilt.
// fn must call handoff once it has marked the images it will rebuild with
// BeginBuild, and before it builds them.
func (m *ImageLifecycleManager) SetChildRebuilder(fn func(ctx context.Context, handoff func())) {
	m.childRebuilder = fn
}

// RebuildChildren runs the child-image cascade if one is wired, calling
// handoff (the base build's end) once the project images it rebuilds are
// marked as building, so a container can't start on one in between. Without
// a cascade, handoff is called right away. Exposed so the daemon's startup
// ensure-image path can trigger the same cascade the API-driven Rebuild uses.
func (m *ImageLifecycleManager) RebuildChildren(ctx context.Context, handoff func()) {
	if m.childRebuilder == nil {
		handoff()
		return
	}
	m.childRebuilder(ctx, handoff)
}

// SetContainerRegistry configures the registry so that containers removed
// during image removal are also unregistered from the in-memory registry.
func (m *ImageLifecycleManager) SetContainerRegistry(reg containerUnregisterer) {
	m.registry = reg
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
		builds:              map[string]int{},
	}
	return m
}

// Status returns the current image build status.
func (m *ImageLifecycleManager) Status() ImageBuildStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// SetStatus updates the build status. Used by the startup image build
// to keep the API status endpoint in sync with WebSocket events.
func (m *ImageLifecycleManager) SetStatus(s ImageBuildStatus) {
	m.mu.Lock()
	m.status = s
	m.mu.Unlock()
}

// UpdateAvailable returns the cached update info, or nil if no update is available.
func (m *ImageLifecycleManager) UpdateAvailable() *events.ImageUpdateAvailableData {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateAvailable
}

// Versions returns the version info for the current image by reading Docker labels.
func (m *ImageLifecycleManager) Versions() ImageVersions {
	if labels, err := m.client.ImageInspectLabels(context.Background(), m.imageName); err == nil && labels != nil {
		v := ImageVersions{
			LoopVersion:   labels["loop.version"],
			ClaudeVersion: labels["loop.claude_version"],
		}
		if t, err := time.Parse(time.RFC3339, labels["loop.built_at"]); err == nil {
			v.BuiltAt = t
		}
		return v
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.versions
}

// RemoveImage removes the current image and all containers using it.
// If a registry is configured, all tracked containers are unregistered
// since RemoveImageAndContainers force-removes them from Docker.
func (m *ImageLifecycleManager) RemoveImage(ctx context.Context) error {
	err := m.client.RemoveImageAndContainers(ctx, m.imageName)
	if err != nil {
		return err
	}
	if m.registry != nil {
		for _, info := range m.registry.List() {
			m.registry.Unregister(info.ContainerID)
		}
	}
	return nil
}

// ReclaimSpace frees Docker disk by pruning every BuildKit cache entry not in
// use by a running build (unusedFor=0, all) and dangling images, returning the
// bytes freed by each. Build-cache pruning is daemon-global, not scoped to
// Loop's builds. If image pruning fails after the cache was already dropped,
// the build-cache total is still reported alongside the error so nothing looks
// silently lost.
//
// This is the action reached for when Docker is out of room, so it prunes
// reusable cache too: the alternative left most of the disk unreclaimed, which
// is worse than the slower build that follows.
func (m *ImageLifecycleManager) ReclaimSpace(ctx context.Context) (ReclaimResult, error) {
	buildCache, err := m.client.PruneBuildCache(ctx, 0, true)
	if err != nil {
		return ReclaimResult{}, err
	}
	images, err := m.client.PruneDanglingImages(ctx)
	if err != nil {
		return ReclaimResult{BuildCacheReclaimed: buildCache, TotalReclaimed: buildCache}, err
	}
	return ReclaimResult{
		BuildCacheReclaimed: buildCache,
		ImagesReclaimed:     images,
		TotalReclaimed:      buildCache + images,
	}, nil
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
	endBase := m.BeginBuild(m.imageName)
	endSidecar := func() {}
	if m.sidecarRebuilder != nil {
		endSidecar = m.BeginBuild(m.sidecarImage)
	}

	m.broadcastStatus()

	// Use a background context — the caller's request context will be
	// canceled as soon as the 202 response is sent.
	go func() {
		defer endBase()
		defer endSidecar()
		m.doRebuild(context.Background(), endBase, endSidecar)
	}()
	return nil
}

// failBuild records a failed build, tells the UI, and logs why.
func (m *ImageLifecycleManager) failBuild(err error, msg string) {
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "failed", Error: err.Error()}
	m.mu.Unlock()
	m.broadcastStatus()
	m.logger.Error(msg, "error", err)
}

// rebuildSidecar builds the browser sidecar image, when one is wired.
//
// It reports its own phase because the build is a slow one — the sidecar image
// is built with --pull --no-cache, so it fetches a fresh Chromium every time —
// and a UI that said only "building" for those minutes would look stuck after
// the agent image was already done.
func (m *ImageLifecycleManager) rebuildSidecar(ctx context.Context) error {
	if m.sidecarRebuilder == nil {
		return nil
	}

	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "browser", StartedAt: m.status.StartedAt}
	m.mu.Unlock()
	m.broadcastStatus()

	return m.sidecarRebuilder(ctx)
}

// doRebuild builds the agent image, then the sidecar image, then the child
// images on the new base. endBase and endSidecar end the builds Rebuild
// began: the sidecar's once it is built, the base's once the child cascade
// has taken over from it (or on failure, by Rebuild).
func (m *ImageLifecycleManager) doRebuild(ctx context.Context, endBase, endSidecar func()) {
	// No need to remove — docker build with the same tag overwrites in place.
	if err := m.client.ImageBuild(ctx, m.containerDir, m.imageName); err != nil {
		m.failBuild(err, "image lifecycle: build failed")
		return
	}

	err := m.rebuildSidecar(ctx)
	endSidecar()
	if err != nil {
		m.failBuild(err, "image lifecycle: browser sidecar build failed")
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
	// Re-check now rather than waiting for the next tick of the update
	// checker, which is half an hour away: the build was very likely the
	// answer to a pending update prompt.
	m.checkAndBroadcast()
	m.RebuildChildren(ctx, endBase)
}

// CheckClaudeUpdate checks if a newer Claude Code version is available.
//
// The current version comes from the image's own labels (via Versions, which
// falls back to the in-memory copy when the image cannot be inspected) rather
// than from the in-memory copy alone. That copy is only written by a rebuild
// in this process, so reading it directly means a daemon that has not rebuilt
// anything never reports an update at all, and one that just rebuilt keeps
// comparing against the version it replaced.
func (m *ImageLifecycleManager) CheckClaudeUpdate() (latestVersion string, available bool) {
	latest := m.latestClaudeVersion()
	if latest == "" || len(latest) > 20 {
		return "", false // invalid or error response
	}

	current := m.Versions().ClaudeVersion
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
		m.mu.Lock()
		had := m.updateAvailable != nil
		m.updateAvailable = nil
		m.mu.Unlock()
		// Clearing has to be announced, not just recorded: subscribers latch
		// the banner on the last event they saw, so without this the prompt to
		// update survives the rebuild that satisfied it. An empty
		// latest_version is the "nothing to update" signal.
		if had && m.broadcaster != nil {
			m.logger.Info("image lifecycle: Claude Code update no longer pending")
			m.broadcaster.BroadcastImageUpdateAvailable(events.ImageUpdateAvailableData{Component: "claude_code"})
		}
		return
	}

	current := m.Versions().ClaudeVersion

	data := &events.ImageUpdateAvailableData{
		CurrentVersion: current,
		LatestVersion:  latest,
		Component:      "claude_code",
	}

	m.mu.Lock()
	m.updateAvailable = data
	m.mu.Unlock()

	m.logger.Info("image lifecycle: Claude Code update available", "current", current, "latest", latest)
	if m.broadcaster != nil {
		m.broadcaster.BroadcastImageUpdateAvailable(*data)
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
