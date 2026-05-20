package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/review"
)

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
	PostPRComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int, commitID, path, side string, line int, body string) error
}

// SetReviewService wires the dependencies for the /api/channels/{id}/review/*
// endpoints. All three are required; passing nil for any of them leaves
// the routes returning 501 (not configured).
func (s *Server) SetReviewService(client GitHubReview, store *review.Store, wt review.PR) {
	s.reviewClient = client
	s.reviewStore = store
	s.reviewWorktree = wt
}

// ReviewRunner kicks off a single review pass and streams the parsed
// comments out through onComment. Satisfied by *review.Runner; held as
// an interface so tests can drive the handler without a real agent
// container.
type ReviewRunner interface {
	Run(ctx context.Context, channelID, dirPath, parentDirPath, systemPrompt, prompt string, onComment func(*review.Comment)) (*agent.AgentResponse, error)
}

// SetReviewAgent wires the agent-run side of the review panel. runner
// drives the agent; systemPrompt + userPrompt are the resolved prompt
// pair (caller is expected to have read them out of config and applied
// any defaulting). All three are required: nil/"" leaves
// POST .../review/run returning 501.
func (s *Server) SetReviewAgent(runner ReviewRunner, systemPrompt, userPrompt string) {
	s.reviewRunner = runner
	s.reviewSystemPrompt = systemPrompt
	s.reviewPrompt = userPrompt
}

// reviewLoadRequest carries the PR number to load. The FE also accepts
// pasting a PR URL; URL parsing happens FE-side so the backend only sees
// a number.
type reviewLoadRequest struct {
	PRNumber int `json:"pr_number"`
}

// reviewSessionResponse mirrors review.Session for the wire. We re-pack
// rather than json-marshalling the session directly so we can include a
// `present` flag that distinguishes "no session" from "session exists but
// is empty" on the FE without trapped optional chaining.
type reviewSessionResponse struct {
	Present bool            `json:"present"`
	Session *review.Session `json:"session,omitempty"`
}

func (s *Server) handleReviewLoad(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.reviewClient, "review service not configured") {
		return
	}
	if s.reviewStore == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	if !requireConfigured(w, s.reviewWorktree, "review service not configured") {
		return
	}

	channelID := r.PathValue("id")
	var req reviewLoadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.PRNumber <= 0 {
		http.Error(w, "pr_number is required", http.StatusBadRequest)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	dirPath := ch.DirPath
	if dirPath == "" && s.loopDir != "" {
		dirPath = filepath.Join(s.loopDir, ch.ChannelID, "work")
	}
	if dirPath == "" {
		http.Error(w, "channel has no dir_path", http.StatusBadRequest)
		return
	}

	parentDirPath := s.resolveParentDirPath(r.Context(), channelID)
	ghUser := s.resolveGHUser(dirPath, parentDirPath)

	// Mark loading early so the GET endpoint can show a spinner while the
	// gh + git work runs.
	s.reviewStore.Put(channelID, &review.Session{Status: review.StatusLoading})

	pr, err := s.reviewClient.FetchPRByNumber(r.Context(), dirPath, ghUser, req.PRNumber)
	if err != nil {
		s.reviewStore.UpdateStatus(channelID, review.StatusError, errorMessage(err))
		respondReviewError(w, err)
		return
	}
	if pr == nil {
		s.reviewStore.UpdateStatus(channelID, review.StatusError, "PR not found")
		http.Error(w, "PR not found", http.StatusNotFound)
		return
	}

	headSHA, err := s.reviewClient.FetchPRHeadSHA(r.Context(), dirPath, ghUser, req.PRNumber)
	if err != nil {
		s.reviewStore.UpdateStatus(channelID, review.StatusError, errorMessage(err))
		respondReviewError(w, err)
		return
	}

	// Check out the PR head locally first so the diff (and the review
	// agent) can read the actual files in their post-merge form.
	worktreePath, err := s.reviewWorktree.Add(r.Context(), dirPath, req.PRNumber)
	if err != nil {
		s.reviewStore.UpdateStatus(channelID, review.StatusError, errorMessage(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	diff, err := s.reviewWorktree.Diff(r.Context(), dirPath, worktreePath, pr.BaseRef)
	if err != nil {
		s.reviewStore.UpdateStatus(channelID, review.StatusError, errorMessage(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sess := &review.Session{
		PR:           pr,
		HeadSHA:      headSHA,
		WorktreePath: worktreePath,
		RawDiff:      string(diff),
		Status:       review.StatusReady,
	}
	s.reviewStore.Put(channelID, sess)
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: s.reviewStore.Get(channelID)}, s.logger)
}

// handleReviewListPRs returns the list of open PRs in the repo backing the
// channel's working directory. The FE renders these as a picker so the user
// can click a row to auto-load instead of pasting a PR number or URL.
func (s *Server) handleReviewListPRs(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.reviewClient, "review service not configured") {
		return
	}

	channelID := r.PathValue("id")
	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	dirPath := ch.DirPath
	if dirPath == "" && s.loopDir != "" {
		dirPath = filepath.Join(s.loopDir, ch.ChannelID, "work")
	}
	if dirPath == "" {
		http.Error(w, "channel has no dir_path", http.StatusBadRequest)
		return
	}

	parentDirPath := s.resolveParentDirPath(r.Context(), channelID)
	ghUser := s.resolveGHUser(dirPath, parentDirPath)

	prs, err := s.reviewClient.ListOpenPRs(r.Context(), dirPath, ghUser)
	if err != nil {
		respondReviewError(w, err)
		return
	}
	if prs == nil {
		prs = []githubapi.PRInfo{}
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"prs": prs}, s.logger)
}

func (s *Server) handleReviewGet(w http.ResponseWriter, r *http.Request) {
	if s.reviewStore == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	sess := s.reviewStore.Get(channelID)
	if sess == nil {
		writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: false}, s.logger)
		return
	}
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: sess}, s.logger)
}

func (s *Server) handleReviewDelete(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if s.reviewStore == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	if !requireConfigured(w, s.reviewWorktree, "review service not configured") {
		return
	}
	channelID := r.PathValue("id")
	sess := s.reviewStore.Get(channelID)
	if sess == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if sess.WorktreePath != "" {
		ch, err := s.store.GetChannel(r.Context(), channelID)
		if err == nil && ch != nil && ch.DirPath != "" {
			if err := s.reviewWorktree.Remove(r.Context(), ch.DirPath, sess.WorktreePath); err != nil {
				s.logger.Warn("review worktree remove failed", "channel_id", channelID, "path", sess.WorktreePath, "err", err)
			}
		}
	}
	s.reviewStore.Delete(channelID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReviewPushComment(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.reviewClient, "review service not configured") {
		return
	}
	if s.reviewStore == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	commentID := r.PathValue("cid")
	c, sess := s.reviewStore.FindComment(channelID, commentID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if c == nil {
		http.Error(w, "comment not found", http.StatusNotFound)
		return
	}
	if c.Pushed {
		writeHTTPJSON(w, http.StatusOK, map[string]any{"pushed": true, "already": true}, s.logger)
		return
	}
	if err := s.pushOneComment(r.Context(), channelID, sess, c); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"pushed": true}, s.logger)
}

// pushAllResult captures the outcome of POST .../review/push-all. Errors
// are accumulated rather than short-circuiting so a single bad comment
// doesn't block the rest.
type pushAllResult struct {
	Pushed int      `json:"pushed"`
	Failed int      `json:"failed"`
	Errors []string `json:"errors,omitempty"`
}

func (s *Server) handleReviewPushAll(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.reviewClient, "review service not configured") {
		return
	}
	if s.reviewStore == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	sess := s.reviewStore.Get(channelID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	var result pushAllResult
	for _, c := range sess.Comments {
		if c.Pushed {
			continue
		}
		if err := s.pushOneComment(r.Context(), channelID, sess, c); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, c.ID+": "+err.Error())
			continue
		}
		result.Pushed++
	}
	writeHTTPJSON(w, http.StatusOK, result, s.logger)
}

// pushOneComment resolves the repo slug, pushes via gh, and flips the
// Pushed flag on the in-memory comment. Returns the underlying error so
// callers can attach it to their response (single-push: bubble up;
// push-all: accumulate).
func (s *Server) pushOneComment(ctx context.Context, channelID string, sess *review.Session, c *review.Comment) error {
	if sess.PR == nil || sess.HeadSHA == "" {
		return errors.New("session not ready (no PR or head SHA)")
	}
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil || ch.DirPath == "" {
		return errors.New("channel has no dir_path")
	}
	parentDirPath := s.resolveParentDirPath(ctx, channelID)
	ghUser := s.resolveGHUser(ch.DirPath, parentDirPath)
	slug, err := s.reviewClient.FetchRepoSlug(ctx, ch.DirPath, ghUser)
	if err != nil {
		return err
	}
	if err := s.reviewClient.PostPRComment(ctx, ch.DirPath, ghUser, *slug, sess.PR.Number, sess.HeadSHA, c.Path, c.Side, c.Line, c.Body); err != nil {
		return err
	}
	s.reviewStore.MarkPushed(channelID, c.ID)
	return nil
}

// handleReviewRun kicks off an agent review pass for the channel's
// current session. The session must already be in StatusReady (i.e.
// /review/load has succeeded); a second concurrent run on the same
// channel returns 202 with status "in_progress" without restarting.
//
// The handler returns 202 immediately and the run continues in a
// background goroutine that streams review.comment + review.status
// events through eventsHub. The FE consumes those over WS.
func (s *Server) handleReviewRun(w http.ResponseWriter, r *http.Request) {
	if s.reviewStore == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	if s.reviewRunner == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}

	channelID := r.PathValue("id")
	sess := s.reviewStore.Get(channelID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if sess.WorktreePath == "" {
		http.Error(w, "session has no worktree", http.StatusConflict)
		return
	}

	// In-flight check must come before the status guard: a second call
	// while the first run is in flight should coalesce (202 "in_progress")
	// rather than 409, since the session was already moved to Reviewing
	// by the first call.
	if !s.registerReviewRun(channelID) {
		writeHTTPJSON(w, http.StatusAccepted, map[string]string{"status": "in_progress"}, s.logger)
		return
	}
	if sess.Status != review.StatusReady {
		s.unregisterReviewRun(channelID)
		http.Error(w, "session not ready (status="+string(sess.Status)+")", http.StatusConflict)
		return
	}

	// The PR worktree's `.git` is a pointer file referencing the *shared*
	// gitdir, which only lives under the main repo. The container needs
	// that mounted so the reference resolves inside the sandbox —
	// otherwise the agent dies on startup and the run returns "no result
	// event found". For a worktree-thread channel, the channel itself
	// IS a worktree, so the shared gitdir lives under the *parent*
	// channel's dir — resolveParentDirPath walks that chain. For a
	// root channel, resolveParentDirPath returns "" and we fall back to
	// the channel's own dir (which is the main repo).
	parentDirPath := s.resolveParentDirPath(r.Context(), channelID)
	if parentDirPath == "" && s.store != nil {
		if ch, err := s.store.GetChannel(r.Context(), channelID); err == nil && ch != nil {
			parentDirPath = ch.DirPath
			if parentDirPath == "" && s.loopDir != "" {
				parentDirPath = filepath.Join(s.loopDir, ch.ChannelID, "work")
			}
		}
	}

	prompt := s.reviewPrompt
	if prompt == "" {
		prompt = defaultReviewPrompt
	}
	// Inlining the diff blew past Linux's MAX_ARG_STRLEN (~128KB per argv
	// entry) on large PRs and killed the container with "argument list too
	// long" before claude ever started. The worktree is already checked
	// out at PR head with `origin/<base>` fetched, so the agent can run
	// `git diff origin/<base>...HEAD` itself.
	fullPrompt := prompt + "\n\n" + buildReviewContext(sess)
	worktreePath := sess.WorktreePath
	sysPrompt := s.reviewSystemPrompt

	s.reviewStore.UpdateStatus(channelID, review.StatusReviewing, "")
	s.broadcastReviewStatus(channelID, review.StatusReviewing, "")

	go s.runReviewAsync(channelID, worktreePath, parentDirPath, sysPrompt, fullPrompt)
	writeHTTPJSON(w, http.StatusAccepted, map[string]string{"status": "started"}, s.logger)
}

// runReviewAsync executes the review run on the goroutine that
// handleReviewRun spawns. It owns the in-flight registration cleanup
// and the final status broadcast.
func (s *Server) runReviewAsync(channelID, worktreePath, parentDirPath, systemPrompt, prompt string) {
	defer s.unregisterReviewRun(channelID)
	onComment := func(c *review.Comment) {
		if !s.reviewStore.AddComment(channelID, c) {
			return
		}
		if hub := s.eventsHub; hub != nil {
			hub.BroadcastReviewComment(channelID, events.ReviewCommentEventData{
				ID:   c.ID,
				Path: c.Path,
				Line: c.Line,
				Side: c.Side,
				Body: c.Body,
			})
		}
	}
	_, err := s.reviewRunner.Run(context.Background(), channelID, worktreePath, parentDirPath, systemPrompt, prompt, onComment)
	if err != nil {
		s.reviewStore.UpdateStatus(channelID, review.StatusError, err.Error())
		s.broadcastReviewStatus(channelID, review.StatusError, err.Error())
		return
	}
	s.reviewStore.UpdateStatus(channelID, review.StatusReady, "")
	s.broadcastReviewStatus(channelID, review.StatusReady, "")
}

// registerReviewRun records that channelID has a review run in flight.
// Returns false if one was already in flight, in which case the caller
// should not start another goroutine — the existing one will continue
// streaming events.
func (s *Server) registerReviewRun(channelID string) bool {
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	if s.reviewActive == nil {
		s.reviewActive = make(map[string]struct{})
	}
	if _, ok := s.reviewActive[channelID]; ok {
		return false
	}
	s.reviewActive[channelID] = struct{}{}
	return true
}

// unregisterReviewRun drops the in-flight marker so a subsequent run
// can register. Safe to call when no marker exists.
func (s *Server) unregisterReviewRun(channelID string) {
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	delete(s.reviewActive, channelID)
}

func (s *Server) broadcastReviewStatus(channelID string, status review.Status, errMsg string) {
	hub := s.eventsHub
	if hub == nil {
		return
	}
	hub.BroadcastReviewStatus(channelID, events.ReviewStatusEventData{
		Status: string(status),
		Error:  errMsg,
	})
}

// defaultReviewPrompt is the user-facing prompt sent to the review
// agent when no override is configured. The system prompt (separately
// configurable) is left empty in that case — the user prompt alone is
// enough to drive the review.
const defaultReviewPrompt = `You are reviewing a GitHub pull request. You can read any file directly.

For each actionable issue you find — bugs, security risks, breakage, regressions — emit exactly one block:

<review-comment path="path/to/file" line="N" side="RIGHT">
One paragraph describing the issue and what should change.
</review-comment>

side defaults to "RIGHT" (added/modified lines). Use side="LEFT" only when commenting on a line removed from the base.

Do not emit blocks for style nits, formatting, or matters of taste. Only emit blocks for problems that should block the PR.`

// buildReviewContext renders the per-PR context block appended to the
// configured review prompt. Each known field gets its own labelled line so
// the agent can quote it verbatim and so a missing field (e.g. empty Title)
// just drops one line without breaking the rest.
func buildReviewContext(sess *review.Session) string {
	var b strings.Builder
	b.WriteString("Pull request under review:\n")
	if sess.PR != nil {
		if sess.PR.Number > 0 {
			fmt.Fprintf(&b, "- Number: #%d\n", sess.PR.Number)
		}
		if sess.PR.URL != "" {
			fmt.Fprintf(&b, "- URL: %s\n", sess.PR.URL)
		}
		if sess.PR.Title != "" {
			fmt.Fprintf(&b, "- Title: %s\n", sess.PR.Title)
		}
		if sess.PR.BaseRef != "" {
			fmt.Fprintf(&b, "- Target branch (base): %s\n", sess.PR.BaseRef)
		}
		if sess.PR.HeadRef != "" {
			fmt.Fprintf(&b, "- Source branch (head): %s\n", sess.PR.HeadRef)
		}
	}
	if sess.HeadSHA != "" {
		fmt.Fprintf(&b, "- Head SHA: %s\n", sess.HeadSHA)
	}
	b.WriteString("\nThe PR head is checked out at your current working directory.")
	if sess.PR != nil && sess.PR.BaseRef != "" {
		fmt.Fprintf(&b, " Run `git diff origin/%s...HEAD` to read the diff before commenting.", sess.PR.BaseRef)
	}
	b.WriteString("\n")
	return b.String()
}

// respondReviewError maps gh-specific errors to the right HTTP status
// before bubbling up the message, so the FE can distinguish gh-missing
// (degrade gracefully) from a real fetch failure.
func respondReviewError(w http.ResponseWriter, err error) {
	if errors.Is(err, githubapi.ErrGhNotInstalled) {
		http.Error(w, "gh CLI not installed", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// errorMessage returns err.Error() guarded against nil.
func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
