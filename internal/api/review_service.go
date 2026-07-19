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
	deps *serverDeps // shared infrastructure; see serverDeps

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

// newReviewService creates the review domain with its run registry ready.
// Stage-2 wiring (gh client, session store, worktree, agent runner, timeout)
// arrives later via the Server setters — the daemon builds those after the
// server exists, and tests inject only the pieces a case needs.
func newReviewService(deps *serverDeps) *reviewService {
	return &reviewService{deps: deps, active: map[string]context.CancelFunc{}}
}
