package api

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// gitState captures the values a branch poller compares between ticks.
// When any field changes for a channel, the poller broadcasts a
// channel.updated event so the sidebar refreshes without a full /api/channels
// round-trip.
type gitState struct {
	Branch        string
	Commit        string
	Subject       string // the commit's subject line
	DiffAdditions int
	DiffDeletions int
	// Upstream is the branch's tracking branch, e.g. origin/main, with the
	// commits the branch is ahead of and behind it.
	Upstream string
	Ahead    int
	Behind   int
	// SyncBase is the branch a worktree thread's checkout was cut from, set
	// when it still resolves, with the commits the checkout is ahead of and
	// behind it.
	SyncBase   string
	BaseAhead  int
	BaseBehind int
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
	gitInfo  func(ctx context.Context, dir, base string, prev gitState) gitState
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
	bases := worktreeBases(channels, p.loopDir)
	p.mu.Lock()
	prevDirs := p.dirState
	p.mu.Unlock()
	computed := make(map[string]gitState)
	changedDirs := make(map[string]struct{})
	seen := make(map[string]struct{}, len(channels))
	for _, ch := range channels {
		seen[ch.ChannelID] = struct{}{}
		dirPath := channelDir(ch, p.loopDir)
		if dirPath == "" {
			continue
		}
		next, ok := computed[dirPath]
		if !ok {
			next = p.gitInfo(ctx, dirPath, bases[dirPath], prevDirs[dirPath])
			computed[dirPath] = next
		}

		p.mu.Lock()
		prev, known := p.state[ch.ChannelID]
		p.state[ch.ChannelID] = next
		p.mu.Unlock()

		if prime || (known && prev == next) {
			continue
		}
		// A channel first seen after the prime tick is broadcast too: the
		// sidebar fetched it when it was created, and its git state may have
		// moved on since (e.g. a commit right after a worktree was cut).
		if known {
			changedDirs[dirPath] = struct{}{}
		}
		p.hub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID:     ch.ChannelID,
			Branch:        next.Branch,
			Commit:        next.Commit,
			Subject:       next.Subject,
			DiffAdditions: next.DiffAdditions,
			DiffDeletions: next.DiffDeletions,
			Upstream:      next.Upstream,
			Ahead:         next.Ahead,
			Behind:        next.Behind,
			SyncBase:      next.SyncBase,
			BaseAhead:     next.BaseAhead,
			BaseBehind:    next.BaseBehind,
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

// channelDir is the directory a channel's git state is read from: its own,
// or its workspace under loopDir when it has none. Empty when neither is set.
func channelDir(ch *db.Channel, loopDir string) string {
	if ch.DirPath == "" && loopDir != "" {
		return filepath.Join(loopDir, ch.ChannelID, "work")
	}
	return ch.DirPath
}

// worktreeBases maps each worktree thread's dir to the branch it was cut
// from, so its git state can count the commits between them.
func worktreeBases(channels []*db.Channel, loopDir string) map[string]string {
	bases := make(map[string]string)
	for _, ch := range channels {
		if ch.Worktree && ch.BaseBranch != "" {
			bases[channelDir(ch, loopDir)] = ch.BaseBranch
		}
	}
	return bases
}
