package api

import (
	"context"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/githubapi"

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

// GitHubReview is the subset of githubapi.Client the review panel needs.
// Kept as an interface so tests can stub gh shell-outs without a binary.
// Diff is intentionally absent — the patch is produced locally from the
// PR worktree instead of via `gh pr diff` so the review agent has direct
// filesystem access to the actual code under review.
type GitHubReview interface {
	FetchPRByNumber(ctx context.Context, workdir, ghUser string, number int) (*githubapi.PRInfo, error)
	FetchPRHeadSHA(ctx context.Context, workdir, ghUser string, number int) (string, error)
	FetchRepoSlug(ctx context.Context, workdir, ghUser string) (*githubapi.RepoSlug, error)
	ListOpenPRs(ctx context.Context, workdir, ghUser string) ([]githubapi.PRInfo, error)
	FetchPRReviewComments(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int) ([]githubapi.PRReviewComment, error)
	PostPRComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int, commitID, path, side string, line int, body string) (int64, error)
	DeletePRReviewComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, commentID int64) error
}

// ReviewRunner kicks off a single review pass and streams the parsed
// comments out through onComment. Satisfied by *review.Runner; held as
// an interface so tests can drive the handler without a real agent
// container.
type ReviewRunner interface {
	Run(ctx context.Context, channelID, dirPath, parentDirPath, systemPrompt, prompt string, onComment func(*review.Comment)) (*agent.AgentResponse, error)
}
