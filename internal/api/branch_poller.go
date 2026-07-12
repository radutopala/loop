package api

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/events"
)

// gitState captures the values a branch poller compares between ticks.
// When any field changes for a channel, the poller broadcasts a
// channel.updated event so the sidebar refreshes without a full /api/channels
// round-trip.
type gitState struct {
	Branch        string
	Commit        string
	DiffAdditions int
	DiffDeletions int
}

// BranchPoller polls each channel's workdir for branch/commit/diff changes
// and emits channel.updated events. Tick cadence is fixed at construction
// time. Run blocks until the context is cancelled.
//
// The poller is also the git-state cache for GET /api/channels: it computes
// each unique directory ONCE per tick (channels/threads sharing a worktree
// dir reuse the result) and the handler serves Snapshot instead of spawning
// git subprocesses per channel per request.
type BranchPoller struct {
	store    ChannelLister
	hub      *EventsHub
	loopDir  string
	interval time.Duration
	logger   *slog.Logger
	gitInfo  func(ctx context.Context, dir string) gitState
	// onDirChange fires once per dir per tick when its git state changed
	// since the previous tick. Wired to Server.InvalidatePRCacheForDir so a
	// new commit/branch (the push that precedes a PR) makes the next PR
	// lookup bypass the cache.
	onDirChange func(dir string)

	mu       sync.Mutex
	state    map[string]gitState // per channelID, for change broadcasts
	dirState map[string]gitState // per dirPath, for API snapshots
}

// NewBranchPoller constructs a poller. interval defaults to 5s when zero.
func NewBranchPoller(store ChannelLister, hub *EventsHub, loopDir string, interval time.Duration, logger *slog.Logger) *BranchPoller {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &BranchPoller{
		store:    store,
		hub:      hub,
		loopDir:  loopDir,
		interval: interval,
		logger:   logger,
		gitInfo:  collectGitState,
		state:    make(map[string]gitState),
		dirState: make(map[string]gitState),
	}
}

// SetOnDirChange registers the per-dir change callback (see field docs).
// Must be called before Run.
func (p *BranchPoller) SetOnDirChange(fn func(dir string)) {
	p.onDirChange = fn
}

// Snapshot returns the last polled git state for dir. ok is false when the
// poller hasn't covered the dir yet (fresh channel between ticks, or the
// poller isn't running) — callers fall back to computing inline.
func (p *BranchPoller) Snapshot(dir string) (gitState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.dirState[dir]
	return st, ok
}

// Run polls until ctx is cancelled. Safe to call in a goroutine.
func (p *BranchPoller) Run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()

	// Prime state on first tick before any broadcast — this lets the
	// initial render seed the cache without flooding the sidebar with
	// "updated" events for unchanged channels.
	p.tick(ctx, true)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx, false)
		}
	}
}

func (p *BranchPoller) tick(ctx context.Context, prime bool) {
	if p.store == nil || p.hub == nil {
		return
	}
	channels, err := p.store.ListChannels(ctx)
	if err != nil {
		p.logger.Debug("branch poller: list channels failed", "err", err)
		return
	}

	// Compute once per unique dir — channels and threads sharing a worktree
	// dir would otherwise multiply the git subprocess cost.
	computed := make(map[string]gitState)
	changedDirs := make(map[string]struct{})
	seen := make(map[string]struct{}, len(channels))
	for _, ch := range channels {
		seen[ch.ChannelID] = struct{}{}
		dirPath := ch.DirPath
		if dirPath == "" && p.loopDir != "" {
			dirPath = filepath.Join(p.loopDir, ch.ChannelID, "work")
		}
		if dirPath == "" {
			continue
		}
		next, ok := computed[dirPath]
		if !ok {
			next = p.gitInfo(ctx, dirPath)
			computed[dirPath] = next
		}

		p.mu.Lock()
		prev, known := p.state[ch.ChannelID]
		p.state[ch.ChannelID] = next
		p.mu.Unlock()

		if prime || !known {
			continue
		}
		if prev == next {
			continue
		}
		changedDirs[dirPath] = struct{}{}
		p.hub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID:     ch.ChannelID,
			Branch:        next.Branch,
			Commit:        next.Commit,
			DiffAdditions: next.DiffAdditions,
			DiffDeletions: next.DiffDeletions,
		})
	}
	if p.onDirChange != nil {
		for dir := range changedDirs {
			p.onDirChange(dir)
		}
	}

	// Swap in this tick's dir snapshots and drop state for channels that no
	// longer exist so the maps don't grow unbounded over a long-running daemon.
	p.mu.Lock()
	p.dirState = computed
	for id := range p.state {
		if _, ok := seen[id]; !ok {
			delete(p.state, id)
		}
	}
	p.mu.Unlock()
}
