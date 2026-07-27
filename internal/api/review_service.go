package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/events"

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

// ReviewRunner kicks off a single review pass. Findings arrive either
// through onComment — the built-in ReportFindings tool, picked off the
// agent's stream as it runs — or out of band through the
// report_review_findings MCP tool, which POSTs to the review-comments
// endpoint. Both funnel into ingestComment. Satisfied by *review.Runner;
// held as an interface so tests can drive the handler without a real
// agent container.
type ReviewRunner interface {
	Run(ctx context.Context, channelID, dirPath, parentDirPath, systemPrompt, prompt string, onComment func(*review.Comment)) (*agent.AgentResponse, error)
}

// refreshReviewSession fast-forwards the worktree to the PR's current
// head, refetches GH review comments, re-runs Diff with widened context
// (so every comment lands inside a hunk), and replaces the session in
// the store with Status=Ready. Used by Sync and Run — both want the
// agent and the diff view to reflect the latest PR state without
// forcing the user to Close + Load.
//
// Agent-emitted comments from a prior run are preserved verbatim;
// github-sourced ones are replaced with the fresh snapshot. Broadcasts
// review.diff when raw_diff changes so any FE that didn't drive this
// call (e.g. Run, whose HTTP response doesn't carry the session)
// still picks up the new diff over WS.
func (s *reviewService) refreshReviewSession(ctx context.Context, channelID, dirPath, ghUser string, sess *review.Session) (*review.Session, error) {
	prNum := sess.PR.Number
	headSHA, err := s.client.FetchPRHeadSHA(ctx, dirPath, ghUser, prNum)
	if err != nil {
		return nil, err
	}
	if err := s.worktree.Refresh(ctx, dirPath, sess.WorktreePath, prNum); err != nil {
		return nil, err
	}
	ghComments := s.fetchExistingReviewComments(ctx, dirPath, ghUser, prNum)

	var merged []*review.Comment
	for _, c := range sess.Comments {
		if c == nil || c.Source == "github" {
			continue
		}
		merged = append(merged, c)
	}
	merged = append(merged, ghComments...)

	diff, err := s.worktree.Diff(ctx, dirPath, sess.WorktreePath, sess.PR.BaseRef, merged)
	if err != nil {
		return nil, err
	}
	raw := string(diff)
	s.sessions.Put(channelID, &review.Session{
		PR:           sess.PR,
		HeadSHA:      headSHA,
		WorktreePath: sess.WorktreePath,
		RawDiff:      raw,
		Comments:     merged,
		Status:       review.StatusReady,
	})
	if raw != sess.RawDiff {
		if hub := s.deps.eventsHub; hub != nil {
			hub.BroadcastReviewDiff(channelID, events.ReviewDiffEventData{RawDiff: raw})
		}
	}
	return s.sessions.Get(channelID), nil
}

// fetchExistingReviewComments pulls inline review comments already filed
// on the PR via GitHub and converts them to review.Comment with
// Source="github" + Pushed=true so the FE renders them as read-only.
// Best-effort: any failure is logged and the function returns nil so
// Load still succeeds with the agent-only comment list.
func (s *reviewService) fetchExistingReviewComments(ctx context.Context, dirPath, ghUser string, prNum int) []*review.Comment {
	slug, err := s.client.FetchRepoSlug(ctx, dirPath, ghUser)
	if err != nil || slug == nil {
		if err != nil {
			s.deps.logger.Debug("review: skip GH comment fetch (slug)", "err", err)
		}
		return nil
	}
	ghComments, err := s.client.FetchPRReviewComments(ctx, dirPath, ghUser, *slug, prNum)
	if err != nil {
		s.deps.logger.Debug("review: skip GH comment fetch (api)", "err", err)
		return nil
	}
	out := make([]*review.Comment, 0, len(ghComments))
	for _, c := range ghComments {
		if c.Line <= 0 {
			continue
		}
		out = append(out, &review.Comment{
			ID:        fmt.Sprintf("gh-%d", c.ID),
			Path:      c.Path,
			Line:      c.Line,
			Side:      c.Side,
			Body:      c.Body,
			Pushed:    true,
			Source:    "github",
			Author:    c.Author,
			URL:       c.URL,
			CreatedAt: c.CreatedAt,
			Outdated:  c.Outdated,
			Resolved:  c.Resolved,
			GitHubID:  c.ID,
		})
	}
	return out
}

// pushOneComment resolves the repo slug, pushes via gh, and flips the
// Pushed flag on the in-memory comment. Returns the underlying error so
// callers can attach it to their response (single-push: bubble up;
// push-all: accumulate).
func (s *reviewService) pushOneComment(ctx context.Context, channelID string, sess *review.Session, c *review.Comment) error {
	if sess.PR == nil || sess.HeadSHA == "" {
		return errors.New("session not ready (no PR or head SHA)")
	}
	ch, err := s.deps.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil || ch.DirPath == "" {
		return errors.New("channel has no dir_path")
	}
	parentDirPath := s.deps.workspace.resolveParentDirPath(ctx, channelID)
	if !s.deps.configs.reviewEnabled(ch.DirPath, parentDirPath) {
		return errReviewDisabled
	}
	ghUser := s.deps.configs.ghUser(ch.DirPath, parentDirPath)
	slug, err := s.client.FetchRepoSlug(ctx, ch.DirPath, ghUser)
	if err != nil {
		return err
	}
	ghID, err := s.client.PostPRComment(ctx, ch.DirPath, ghUser, *slug, sess.PR.Number, sess.HeadSHA, c.Path, c.Side, c.Line, c.Body)
	if err != nil {
		return err
	}
	// Skip MarkPushed when the GitHub ID came back as 0 — that signals
	// the 422 fallback path took the unparseable-response branch
	// (postPRConversationComment couldn't decode gh's reply). Stamping
	// the comment as Pushed with GitHubID=0 leaves it permanently
	// undeletable through the UI (handleReviewDeleteComment skips the
	// remote DELETE call when GitHubID<=0). Leaving Pushed=false lets
	// the user re-push or surface the issue rather than silently losing
	// the ability to remove the comment from GitHub.
	if ghID == 0 {
		return nil
	}
	s.sessions.MarkPushed(channelID, c.ID, ghID)
	return nil
}

// runReviewAsync executes the review run on the goroutine that
// handleReviewRun spawns. It owns the in-flight registration cleanup
// and the final status broadcast.
//
// runCtx is the per-run cancellable context registered with
// registerReviewRun. Session-delete (handleReviewDelete) and graceful
// shutdown (Server.Stop) cancel it to detach the long-running agent
// container from a session it no longer has any reason to outlive.
//
// When reviewRunTimeout > 0 the agent run is further bounded by that
// timeout: on expiry the run ctx is cancelled (so the underlying agent
// stops) and the session flips to status=error with a clear "timed out"
// message instead of staying at status=reviewing forever. Without this
// gate, a hung container would leak the goroutine and any CLI/FE poller
// would keep hitting status=reviewing until its own deadline fired.
func (s *reviewService) runReviewAsync(runCtx context.Context, channelID, worktreePath, parentDirPath, systemPrompt, prompt string) {
	defer s.unregisterReviewRun(channelID)
	ctx := runCtx
	if s.runTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.runTimeout)
		defer cancel()
	}
	// Findings stream in as the agent reports them, so the panel fills up
	// during the run rather than all at once at the end. ingestComment is
	// safe to call concurrently and dedups by stable comment id, so a
	// finding that also arrives over MCP lands once.
	onComment := func(c *review.Comment) {
		s.ingestComment(channelID, worktreePath, parentDirPath, c)
	}
	_, err := s.runner.Run(ctx, channelID, worktreePath, parentDirPath, systemPrompt, prompt, onComment)
	if err != nil {
		msg := err.Error()
		// Re-shape ctx-deadline into a user-readable message. errors.Is
		// catches both the bare DeadlineExceeded and any err that wraps
		// it via fmt.Errorf("%w", ctx.Err()). The ctx.Err() check covers
		// agents that return their own error string (e.g. "stream closed")
		// after the ctx fired but before they observed the cancel.
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			msg = fmt.Sprintf("review timed out after %s", s.runTimeout)
		}
		// Session-delete or shutdown fired the registered cancel func.
		// Don't broadcast an "error" status — the session is going away
		// (or already gone), and an error WS event would just race the
		// delete and confuse the FE.
		if errors.Is(err, context.Canceled) || runCtx.Err() == context.Canceled {
			return
		}
		s.sessions.UpdateStatus(channelID, review.StatusError, msg)
		s.broadcastReviewStatus(channelID, review.StatusError, msg)
		return
	}
	s.sessions.UpdateStatus(channelID, review.StatusReady, "")
	s.broadcastReviewStatus(channelID, review.StatusReady, "")
}

// ingestComment persists one agent-reported finding into the channel's
// review session and broadcasts it to the panel. Returns false when the
// session is gone or the finding is a duplicate (same stable id) — the
// caller reports the added count back to the agent. worktreePath /
// parentDirPath feed the widened-context rediff, same as the in-run
// callback used to.
func (s *reviewService) ingestComment(channelID, worktreePath, parentDirPath string, c *review.Comment) bool {
	if !s.sessions.AddComment(channelID, c) {
		return false
	}
	if hub := s.deps.eventsHub; hub != nil {
		hub.BroadcastReviewComment(channelID, events.ReviewCommentEventData{
			ID:   c.ID,
			Path: c.Path,
			Line: c.Line,
			Side: c.Side,
			Body: c.Body,
		})
	}
	s.maybeRediffForComment(channelID, worktreePath, parentDirPath, c)
	return true
}

// maybeRediffForComment re-runs git diff with widened unified context if
// the just-emitted comment lands on a known file but outside every
// existing hunk. Path-absent comments and comments already inside a
// hunk are no-ops. On success the session's raw_diff is swapped and a
// review.diff event is broadcast; on failure we log and continue —
// the FE keeps the old diff and the comment surfaces in the
// outside-of-diff section.
func (s *reviewService) maybeRediffForComment(channelID, worktreePath, parentDirPath string, c *review.Comment) {
	if s.worktree == nil {
		return
	}
	sess := s.sessions.Get(channelID)
	if sess == nil || sess.PR == nil || sess.PR.BaseRef == "" {
		return
	}
	if !review.ShouldRediff([]byte(sess.RawDiff), c.Path, c.Line, c.Side) {
		return
	}
	out, err := s.worktree.Diff(context.Background(), parentDirPath, worktreePath, sess.PR.BaseRef, sess.Comments)
	if err != nil {
		if s.deps.logger != nil {
			s.deps.logger.Warn("review re-diff failed", "channel_id", channelID, "error", err)
		}
		return
	}
	raw := string(out)
	if raw == sess.RawDiff {
		return
	}
	s.sessions.UpdateRawDiff(channelID, raw)
	if hub := s.deps.eventsHub; hub != nil {
		hub.BroadcastReviewDiff(channelID, events.ReviewDiffEventData{RawDiff: raw})
	}
}

// registerReviewRun records that channelID has a review run in flight.
// Returns false if one was already in flight, in which case the caller
// should not start another goroutine — the existing one will continue
// streaming events. cancel is stored so that session delete + server
// Stop can detach the long-running agent ctx; runReviewAsync derives its
// agent ctx from this cancel.
func (s *reviewService) registerReviewRun(channelID string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.active[channelID]; ok {
		return false
	}
	s.active[channelID] = cancel
	return true
}

// unregisterReviewRun drops the in-flight marker so a subsequent run
// can register. Safe to call when no marker exists. Does NOT call the
// stored cancel — runReviewAsync defers cancel() locally so the run's
// natural completion path doesn't double-fire.
func (s *reviewService) unregisterReviewRun(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, channelID)
}

// cancelReviewRun cancels the agent ctx of an in-flight run for
// channelID. Safe to call when no run is active. Used by session-delete
// and server-Stop paths to keep a running agent from outliving the
// session it was reviewing.
func (s *reviewService) cancelReviewRun(channelID string) {
	s.mu.Lock()
	cancel, ok := s.active[channelID]
	s.mu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
}

// cancelAllReviewRuns fires every registered cancel func — used during
// graceful shutdown so agent containers don't outlive the daemon.
func (s *reviewService) cancelAllReviewRuns() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.active))
	for _, c := range s.active {
		cancels = append(cancels, c)
	}
	s.mu.Unlock()
	for _, c := range cancels {
		if c != nil {
			c()
		}
	}
}

// isReviewRunActive reports whether a review run is currently in flight
// for channelID. Used by /review/load to refuse overwriting a session
// while its run goroutine is still executing.
func (s *reviewService) isReviewRunActive(channelID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.active[channelID]
	return ok
}

func (s *reviewService) broadcastReviewStatus(channelID string, status review.Status, errMsg string) {
	hub := s.deps.eventsHub
	if hub == nil {
		return
	}
	hub.BroadcastReviewStatus(channelID, events.ReviewStatusEventData{
		Status: string(status),
		Error:  errMsg,
	})
}

// setBackends wires the gh client, session store, and PR-worktree
// lifecycle. All three are required; leaving any nil keeps the review
// routes returning 501 (not configured).
func (s *reviewService) setBackends(client GitHubReview, sessions *review.Store, wt review.PR) {
	s.client = client
	s.sessions = sessions
	s.worktree = wt
}

// setAgent wires the agent-run side of the review panel: the runner plus
// the resolved system/user prompt pair ("" -> built-in default).
func (s *reviewService) setAgent(runner ReviewRunner, systemPrompt, userPrompt string) {
	s.runner = runner
	s.systemPrompt = systemPrompt
	s.userPrompt = userPrompt
}

// setRunTimeout caps runReviewAsync; 0 leaves it unbounded. Production
// callers should stay below the CLI's --timeout so the daemon flips the
// session to status=error first.
func (s *reviewService) setRunTimeout(d time.Duration) {
	s.runTimeout = d
}

// WithReview configures the review panel's backends at construction.
func WithReview(client GitHubReview, sessions *review.Store, wt review.PR) Option {
	return func(s *Server) { s.review.setBackends(client, sessions, wt) }
}

// WithReviewAgent configures the review panel's agent runner and prompts.
func WithReviewAgent(runner ReviewRunner, systemPrompt, userPrompt string) Option {
	return func(s *Server) { s.review.setAgent(runner, systemPrompt, userPrompt) }
}

// WithReviewRunTimeout caps the daemon-side review run goroutine.
func WithReviewRunTimeout(d time.Duration) Option {
	return func(s *Server) { s.review.setRunTimeout(d) }
}
