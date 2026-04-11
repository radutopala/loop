package container

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// ContainerType identifies the kind of container.
type ContainerType string

const (
	ContainerTypeAgent  ContainerType = "agent"
	ContainerTypeShell  ContainerType = "shell"
	ContainerTypeChrome ContainerType = "chrome"
)

// Docker label keys and values used to identify and classify Loop containers.
const (
	ContainerLabel   = "loop-agent"   // label value identifying agent containers (app=loop-agent)
	ContainerTypeKey = "loop-type"    // label key for container type (agent, shell, chrome)
	ChannelLabelKey  = "loop-channel" // label key associating containers with channels
)

// ContainerStatus represents the lifecycle state of a container.
type ContainerStatus string

const (
	ContainerStatusRunning        ContainerStatus = "running"
	ContainerStatusStopped        ContainerStatus = "stopped"
	ContainerStatusPendingRemoval ContainerStatus = "pending-removal"
)

// ContainerInfo holds metadata about a tracked container.
type ContainerInfo struct {
	ContainerID   string          `json:"container_id"`
	ChannelID     string          `json:"channel_id"`
	Type          ContainerType   `json:"type"`
	Status        ContainerStatus `json:"status"`
	ContainerName string          `json:"container_name,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	RemoveAt      *time.Time      `json:"remove_at,omitempty"`
}

// ContainerEventData is the payload for container lifecycle events.
type ContainerEventData struct {
	ContainerID   string     `json:"container_id"`
	ChannelID     string     `json:"channel_id"`
	Type          string     `json:"type"`
	Status        string     `json:"status"`
	ContainerName string     `json:"container_name,omitempty"`
	RemoveAt      *time.Time `json:"remove_at,omitempty"`
}

// ContainerBroadcaster emits container lifecycle events.
type ContainerBroadcaster interface {
	BroadcastContainerRegistered(data ContainerEventData)
	BroadcastContainerRemoved(data ContainerEventData)
	BroadcastContainerStatusChanged(data ContainerEventData)
}

// ContainerRegistry tracks container lifecycle.
type ContainerRegistry interface {
	Register(info *ContainerInfo) *ContainerInfo
	Unregister(containerID string)
	UpdateStatus(containerID string, status ContainerStatus)
	List() []*ContainerInfo
	ListByChannel(channelID string) []*ContainerInfo
	FindByChannelAndType(channelID string, containerType ContainerType) *ContainerInfo
	RunningChannelIDs(ctx context.Context) map[string]struct{}
	RemoveContainer(ctx context.Context, containerID string) error
	ScheduleRemove(containerID string, delay time.Duration)
	FindOrCreateShell(ctx context.Context, channelID, dirPath, parentDirPath string) (string, error)
}

// singletonTypes are container types that allow at most one running
// instance per channel. Agent containers have no such limit.
var singletonTypes = map[ContainerType]bool{
	ContainerTypeShell:  true,
	ContainerTypeChrome: true,
}

// containerRemover removes a Docker container by ID.
type containerRemover interface {
	ContainerRemove(ctx context.Context, containerID string) error
}

// shellCreator creates shell containers on-demand for terminal access.
type shellCreator interface {
	CreateShellContainer(ctx context.Context, channelID, dirPath, parentDirPath string) (string, error)
}

// Registry is a thread-safe, in-memory container registry.
// All methods are goroutine-safe.
type Registry struct {
	mu          sync.RWMutex
	containers  map[string]*ContainerInfo      // containerID -> info
	byChannel   map[string]map[string]struct{} // channelID -> set of containerIDs
	broadcaster ContainerBroadcaster
	remover     containerRemover
	creator     shellCreator
	logger      *slog.Logger
	timeNow     func() time.Time
	afterFunc   func(time.Duration, func()) *time.Timer // injectable time.AfterFunc
	pendingMu   sync.Mutex                              // protects pending map
	pending     map[string]*sync.Mutex                  // per-channel mutex for FindOrCreateShell
	timersMu    sync.Mutex                              // protects timers map
	timers      map[string]*time.Timer                  // containerID -> pending removal timer
}

// NewRegistry creates a new container registry with an optional broadcaster.
func NewRegistry(broadcaster ContainerBroadcaster) *Registry {
	return &Registry{
		containers:  make(map[string]*ContainerInfo),
		byChannel:   make(map[string]map[string]struct{}),
		broadcaster: broadcaster,
		timeNow:     time.Now,
		afterFunc:   time.AfterFunc,
		pending:     make(map[string]*sync.Mutex),
		timers:      make(map[string]*time.Timer),
	}
}

// SetLogger configures the registry logger.
func (r *Registry) SetLogger(logger *slog.Logger) {
	r.logger = logger
}

// SetContainerRemover configures the Docker container remover.
func (r *Registry) SetContainerRemover(remover containerRemover) {
	r.remover = remover
}

// SetShellCreator configures the shell container creator.
func (r *Registry) SetShellCreator(creator shellCreator) {
	r.creator = creator
}

// SetTimeNow overrides the time function (for testing).
func (r *Registry) SetTimeNow(fn func() time.Time) {
	r.timeNow = fn
}

// SetAfterFunc overrides the timer function (for testing).
func (r *Registry) SetAfterFunc(fn func(time.Duration, func()) *time.Timer) {
	r.afterFunc = fn
}

// SetBroadcaster sets or replaces the event broadcaster.
func (r *Registry) SetBroadcaster(b ContainerBroadcaster) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.broadcaster = b
}

func (r *Registry) eventData(info *ContainerInfo) ContainerEventData {
	return ContainerEventData{
		ContainerID:   info.ContainerID,
		ChannelID:     info.ChannelID,
		Type:          string(info.Type),
		Status:        string(info.Status),
		ContainerName: info.ContainerName,
		RemoveAt:      info.RemoveAt,
	}
}

// Register adds a container to the registry and returns the registered entry.
// For singleton types (shell, chrome), if a running container of the same type
// already exists for the channel, the existing entry is returned instead.
// If a container with the same ID already exists, its metadata is updated.
func (r *Registry) Register(info *ContainerInfo) *ContainerInfo {
	r.mu.Lock()
	now := r.timeNow()

	if existing, ok := r.containers[info.ContainerID]; ok {
		existing.ChannelID = info.ChannelID
		existing.Type = info.Type
		existing.ContainerName = info.ContainerName
		existing.Status = ContainerStatusRunning
		existing.RemoveAt = nil
		existing.UpdatedAt = now
		data := r.eventData(existing)
		cp := *existing
		broadcaster := r.broadcaster
		r.mu.Unlock()
		// Cancel any pending removal timer for this container.
		r.cancelTimer(info.ContainerID)
		if broadcaster != nil {
			broadcaster.BroadcastContainerStatusChanged(data)
		}
		return &cp
	}

	// For singleton types, return existing running container for this channel+type.
	if singletonTypes[info.Type] {
		if existing := r.findByChannelAndTypeLocked(info.ChannelID, info.Type); existing != nil {
			cp := *existing
			r.mu.Unlock()
			return &cp
		}
	}

	info.Status = ContainerStatusRunning
	info.CreatedAt = now
	info.UpdatedAt = now
	r.containers[info.ContainerID] = info

	if _, ok := r.byChannel[info.ChannelID]; !ok {
		r.byChannel[info.ChannelID] = make(map[string]struct{})
	}
	r.byChannel[info.ChannelID][info.ContainerID] = struct{}{}

	data := r.eventData(info)
	cp := *info
	broadcaster := r.broadcaster
	r.mu.Unlock()

	if broadcaster != nil {
		broadcaster.BroadcastContainerRegistered(data)
	}
	return &cp
}

// Unregister removes a container from the registry.
// Idempotent — safe to call multiple times for the same container.
func (r *Registry) Unregister(containerID string) {
	r.mu.Lock()

	info, ok := r.containers[containerID]
	if !ok {
		r.mu.Unlock()
		return
	}

	delete(r.containers, containerID)

	if channelSet, exists := r.byChannel[info.ChannelID]; exists {
		delete(channelSet, containerID)
		if len(channelSet) == 0 {
			delete(r.byChannel, info.ChannelID)
		}
	}

	broadcaster := r.broadcaster
	r.mu.Unlock()

	if broadcaster != nil {
		broadcaster.BroadcastContainerRemoved(r.eventData(info))
	}
}

// UpdateStatus changes a container's status. No-op if the container is not found.
func (r *Registry) UpdateStatus(containerID string, status ContainerStatus) {
	r.mu.Lock()

	info, ok := r.containers[containerID]
	if !ok {
		r.mu.Unlock()
		return
	}

	info.Status = status
	info.UpdatedAt = r.timeNow()

	data := r.eventData(info)
	broadcaster := r.broadcaster
	r.mu.Unlock()

	if broadcaster != nil {
		broadcaster.BroadcastContainerStatusChanged(data)
	}
}

// Get returns a copy of a container's info, or nil if not found.
func (r *Registry) Get(containerID string) *ContainerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info := r.containers[containerID]
	if info == nil {
		return nil
	}
	cp := *info
	return &cp
}

// List returns all tracked containers, sorted with running containers first
// (latest first), then non-running containers (latest first).
func (r *Registry) List() []*ContainerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ContainerInfo, 0, len(r.containers))
	for _, info := range r.containers {
		cp := *info
		result = append(result, &cp)
	}
	sort.Slice(result, func(i, j int) bool {
		ri := result[i].Status == ContainerStatusRunning
		rj := result[j].Status == ContainerStatusRunning
		if ri != rj {
			return ri
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

// FindByChannelAndType returns a copy of the first running container matching
// the channel and type, or nil if none exists.
func (r *Registry) FindByChannelAndType(channelID string, containerType ContainerType) *ContainerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info := r.findByChannelAndTypeLocked(channelID, containerType)
	if info == nil {
		return nil
	}
	cp := *info
	return &cp
}

// findByChannelAndTypeLocked is the lock-free version for internal use.
// Caller must hold at least r.mu.RLock().
func (r *Registry) findByChannelAndTypeLocked(channelID string, containerType ContainerType) *ContainerInfo {
	for id := range r.byChannel[channelID] {
		if info, ok := r.containers[id]; ok && info.Type == containerType && info.Status == ContainerStatusRunning {
			return info
		}
	}
	return nil
}

// ListByChannel returns copies of containers for a given channel. Returns an empty slice (not nil) if none.
func (r *Registry) ListByChannel(channelID string) []*ContainerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.byChannel[channelID]
	result := make([]*ContainerInfo, 0, len(ids))
	for id := range ids {
		if info, ok := r.containers[id]; ok {
			cp := *info
			result = append(result, &cp)
		}
	}
	return result
}

// RunningChannelIDs returns the set of channel IDs that have at least one
// container with status "running".
func (r *Registry) RunningChannelIDs(_ context.Context) map[string]struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]struct{})
	for _, info := range r.containers {
		if info.Status == ContainerStatusRunning {
			result[info.ChannelID] = struct{}{}
		}
	}
	return result
}

// Reconcile removes registry entries for containers that no longer exist in Docker.
// It compares tracked container IDs against the provided set of live IDs and
// unregisters any that are missing. Returns the IDs of removed entries.
func (r *Registry) Reconcile(liveIDs map[string]struct{}) []string {
	r.mu.RLock()
	var stale []string
	for id := range r.containers {
		if _, alive := liveIDs[id]; !alive {
			stale = append(stale, id)
		}
	}
	r.mu.RUnlock()

	for _, id := range stale {
		r.Unregister(id)
	}
	return stale
}

// RemoveContainer removes a container from Docker and unregisters it from the registry.
// If a ScheduleRemove timer is pending for this container, it is cancelled first.
// If the Docker removal fails, the container remains registered.
func (r *Registry) RemoveContainer(ctx context.Context, containerID string) error {
	if r.remover == nil {
		return fmt.Errorf("container remover not configured")
	}
	// Cancel any pending scheduled removal.
	r.cancelTimer(containerID)

	if err := r.remover.ContainerRemove(ctx, containerID); err != nil {
		return err
	}
	r.Unregister(containerID)
	return nil
}

// ScheduleRemove marks a container as pending-removal and schedules its
// removal after the given delay. This keeps the container available for
// `docker logs` debugging shortly after a run completes.
// If called again for the same container, the previous timer is cancelled.
func (r *Registry) ScheduleRemove(containerID string, delay time.Duration) {
	// Cancel any existing timer for this container.
	r.cancelTimer(containerID)

	removeAt := r.timeNow().Add(delay)
	r.mu.Lock()
	if info, ok := r.containers[containerID]; ok {
		info.RemoveAt = &removeAt
	}
	r.mu.Unlock()

	r.UpdateStatus(containerID, ContainerStatusPendingRemoval)
	if r.logger != nil {
		r.logger.Info("container pending removal", "container_id", containerID, "delay", delay)
	}

	timer := r.afterFunc(delay, func() {
		r.timersMu.Lock()
		delete(r.timers, containerID)
		r.timersMu.Unlock()

		if r.logger != nil {
			r.logger.Info("removing container after delay", "container_id", containerID)
		}
		if r.remover != nil {
			if err := r.remover.ContainerRemove(context.Background(), containerID); err != nil && r.logger != nil {
				r.logger.Warn("scheduled container removal failed", "container_id", containerID, "error", err)
			}
		}
		r.Unregister(containerID)
	})

	r.timersMu.Lock()
	r.timers[containerID] = timer
	r.timersMu.Unlock()
}

// cancelTimer stops and removes a pending removal timer for the container.
func (r *Registry) cancelTimer(containerID string) {
	r.timersMu.Lock()
	if t, ok := r.timers[containerID]; ok {
		t.Stop()
		delete(r.timers, containerID)
	}
	r.timersMu.Unlock()

	r.mu.Lock()
	if info, ok := r.containers[containerID]; ok {
		info.RemoveAt = nil
	}
	r.mu.Unlock()
}

// FindOrCreateShell returns the ID of a running shell container for the channel.
// If no shell container exists, a new one is created automatically.
// Uses a per-channel mutex to prevent duplicate containers when multiple
// terminal panes connect simultaneously.
func (r *Registry) FindOrCreateShell(ctx context.Context, channelID, dirPath, parentDirPath string) (string, error) {
	// Fast path: check for an existing shell container.
	if info := r.FindByChannelAndType(channelID, ContainerTypeShell); info != nil {
		return info.ContainerID, nil
	}

	if r.creator == nil {
		return "", fmt.Errorf("shell creator not configured")
	}

	// Get or create a per-channel mutex.
	r.pendingMu.Lock()
	chMu, ok := r.pending[channelID]
	if !ok {
		chMu = &sync.Mutex{}
		r.pending[channelID] = chMu
	}
	r.pendingMu.Unlock()

	// Serialize container lookup + creation for this channel.
	chMu.Lock()
	defer chMu.Unlock()

	// Double-check after acquiring the per-channel lock.
	if info := r.FindByChannelAndType(channelID, ContainerTypeShell); info != nil {
		return info.ContainerID, nil
	}

	id, err := r.creator.CreateShellContainer(ctx, channelID, dirPath, parentDirPath)
	if err != nil {
		return "", fmt.Errorf("creating shell container: %w", err)
	}
	return id, nil
}

// Restore populates the registry from a list of existing containers.
// This is used at startup to recover state from Docker containers that
// survived a daemon restart. Existing entries are not overwritten.
// No events are broadcast during restore.
func (r *Registry) Restore(containers []*ContainerInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.timeNow()
	for _, info := range containers {
		if _, ok := r.containers[info.ContainerID]; ok {
			continue
		}
		if info.Status == "" {
			info.Status = ContainerStatusRunning
		}
		if info.CreatedAt.IsZero() {
			info.CreatedAt = now
		}
		if info.UpdatedAt.IsZero() {
			info.UpdatedAt = now
		}
		r.containers[info.ContainerID] = info
		if _, ok := r.byChannel[info.ChannelID]; !ok {
			r.byChannel[info.ChannelID] = make(map[string]struct{})
		}
		r.byChannel[info.ChannelID][info.ContainerID] = struct{}{}
	}
}

// containerInfoLister queries Docker for running containers.
type containerInfoLister interface {
	ListContainerInfos(ctx context.Context) ([]*ContainerInfo, error)
}

// RunReconcileLoop periodically queries Docker for live containers,
// removes registry entries whose containers no longer exist, updates
// statuses for containers whose Docker state has changed, and schedules
// removal for containers that transitioned to stopped.
func (r *Registry) RunReconcileLoop(ctx context.Context, lister containerInfoLister, interval time.Duration, removalDelay time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			infos, err := lister.ListContainerInfos(ctx)
			if err != nil {
				logger.Debug("registry reconcile: failed to list containers", "error", err)
				continue
			}
			liveIDs := make(map[string]struct{}, len(infos))
			for _, info := range infos {
				liveIDs[info.ContainerID] = struct{}{}
			}
			stale := r.Reconcile(liveIDs)
			for _, id := range stale {
				logger.Info("registry reconcile: removed stale entry", "container_id", id)
			}
			// Sync statuses for containers whose Docker state changed.
			for _, info := range infos {
				if existing := r.Get(info.ContainerID); existing != nil &&
					existing.Status != info.Status &&
					existing.Status != ContainerStatusPendingRemoval {
					logger.Info("registry reconcile: status changed", "container_id", info.ContainerID, "from", existing.Status, "to", info.Status)
					r.UpdateStatus(info.ContainerID, info.Status)
					// Schedule removal for containers that just stopped.
					if info.Status == ContainerStatusStopped {
						r.ScheduleRemove(info.ContainerID, removalDelay)
					}
				}
			}
		}
	}
}
