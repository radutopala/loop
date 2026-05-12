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
type BranchPoller struct {
	store     ChannelLister
	hub       *EventsHub
	loopDir   string
	interval  time.Duration
	logger    *slog.Logger
	gitBranch func(ctx context.Context, dir string) string
	gitCommit func(ctx context.Context, dir string) string
	gitDiff   func(ctx context.Context, dir string) (int, int)

	mu    sync.Mutex
	state map[string]gitState
}

// NewBranchPoller constructs a poller. interval defaults to 5s when zero.
func NewBranchPoller(store ChannelLister, hub *EventsHub, loopDir string, interval time.Duration, logger *slog.Logger) *BranchPoller {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &BranchPoller{
		store:     store,
		hub:       hub,
		loopDir:   loopDir,
		interval:  interval,
		logger:    logger,
		gitBranch: gitBranch,
		gitCommit: gitCommit,
		gitDiff:   gitDiffStats,
		state:     make(map[string]gitState),
	}
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
		next := gitState{
			Branch: p.gitBranch(ctx, dirPath),
			Commit: p.gitCommit(ctx, dirPath),
		}
		next.DiffAdditions, next.DiffDeletions = p.gitDiff(ctx, dirPath)

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
		p.hub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID:     ch.ChannelID,
			Branch:        next.Branch,
			Commit:        next.Commit,
			DiffAdditions: next.DiffAdditions,
			DiffDeletions: next.DiffDeletions,
		})
	}

	// Drop state for channels that no longer exist so the map doesn't grow
	// unbounded over a long-running daemon.
	p.mu.Lock()
	for id := range p.state {
		if _, ok := seen[id]; !ok {
			delete(p.state, id)
		}
	}
	p.mu.Unlock()
}
