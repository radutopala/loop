package api

import (
	"context"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/review"
)

// reviewService owns the PR-review domain: per-channel review sessions,
// the gh client, the PR worktree lifecycle, and in-flight review runs.
// It was extracted from Server so review state is reachable only through
// this struct; shared daemon deps are accessed via srv.
type reviewService struct {
	srv *Server // shared deps: store, logger, loopDir, eventsHub, resolve* helpers

	client       GitHubReview
	sessions     *review.Store
	worktree     review.PR
	runner       ReviewRunner
	systemPrompt string
	userPrompt   string
	runTimeout   time.Duration
	mu           sync.Mutex // guards active
	active       map[string]context.CancelFunc
}
