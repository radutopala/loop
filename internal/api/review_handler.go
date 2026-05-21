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
	FetchPRReviewComments(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int) ([]githubapi.PRReviewComment, error)
	PostPRComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, prNum int, commitID, path, side string, line int, body string) (int64, error)
	DeletePRReviewComment(ctx context.Context, workdir, ghUser string, slug githubapi.RepoSlug, commentID int64) error
}

// SetReviewService wires the dependencies for the /api/channels/{id}/review/*
// endpoints. All three are required; passing nil for any of them leaves
// the routes returning 501 (not configured).
func (s *Server) SetReviewService(client GitHubReview, store *review.Store, wt review.PR) {
	s.reviewClient = client
	s.reviewStore = store
	s.reviewWorktree = wt
}

// requireReviewEnabled writes a 403 and returns false when review.enabled
// is false in the merged config (global → project → worktree) for the
// given (dirPath, parentDirPath). All review endpoints that touch the
// PR or run the agent gate on this so the panel can't be reached when
// the project hasn't opted in, even if the FE forgets to hide it.
//
// The caller is responsible for resolving dirPath/parentDirPath itself
// (typically via the GetChannel + resolveParentDirPath dance the handler
// already does for FetchPR/ListOpenPRs). Pushing that resolution into
// the caller — rather than fetching the channel here — keeps the gate
// out of the way of tests that exercise pre-channel-lookup validation
// (e.g. malformed JSON / empty PR number) without forcing every such
// test to add a GetChannel mock just to satisfy this check.
//
// Read-only / cleanup endpoints (handleReviewGet, handleReviewDelete) do
// NOT call this — once a session exists in memory the FE may legitimately
// inspect or tear it down. Disabling the feature flag mid-session blocks
// new loads / runs but doesn't strand an existing session.
func (s *Server) requireReviewEnabled(w http.ResponseWriter, dirPath, parentDirPath string) bool {
	if !s.resolveReviewEnabled(dirPath, parentDirPath) {
		http.Error(w, "review panel disabled for this project", http.StatusForbidden)
		return false
	}
	return true
}

// errReviewDisabled is the sentinel returned from helpers (pushOneComment,
// the GH-side branch of handleReviewDeleteComment) when the merged config
// disables review for the channel's project. Handlers translate it to a
// 403; other errors bubble up as 500.
var errReviewDisabled = errors.New("review panel disabled for this project")

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
	if !s.requireReviewEnabled(w, dirPath, parentDirPath) {
		return
	}
	ghUser := s.resolveGHUser(dirPath, parentDirPath)

	// Refuse to Load over an in-flight run. The async run goroutine would
	// otherwise stomp the new StatusLoading session on completion, and
	// emit comments from the previous PR into the new session.
	if s.isReviewRunActive(channelID) {
		http.Error(w, "review run in flight for this channel", http.StatusConflict)
		return
	}

	// If we're replacing an existing session, drop its on-disk worktree
	// first — otherwise the parent repo's worktree metadata grows a
	// dangling `.worktrees/pr-N` entry for every PR the user loaded but
	// never explicitly closed. Best-effort: a remove failure is logged
	// but does not block the new Load.
	if prev := s.reviewStore.Get(channelID); prev != nil && prev.WorktreePath != "" {
		if err := s.reviewWorktree.Remove(r.Context(), dirPath, prev.WorktreePath); err != nil {
			s.logger.Warn("review worktree remove failed on load overwrite",
				"channel_id", channelID, "path", prev.WorktreePath, "err", err)
		}
	}

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

	// Seed the comment list with any inline review comments already filed
	// on the PR via GitHub — so the panel can render them alongside the
	// agent's pending comments in one unified diff view. A failure here is
	// non-fatal: the session loads without GH comments and the FE shows
	// the agent comments only. The Author field on PR comments often
	// requires a token; if FetchRepoSlug fails (e.g. detached / mirror
	// repo) skip the GH-comment fetch entirely.
	//
	// Fetched before Diff so the worktree can widen `-U` enough to absorb
	// any out-of-hunk comment lines into the rendered diff.
	ghComments := s.fetchExistingReviewComments(r.Context(), dirPath, ghUser, req.PRNumber)

	diff, err := s.reviewWorktree.Diff(r.Context(), dirPath, worktreePath, pr.BaseRef, ghComments)
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
		Comments:     ghComments,
		Status:       review.StatusReady,
	}
	s.reviewStore.Put(channelID, sess)
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: s.reviewStore.Get(channelID)}, s.logger)
}

// handleReviewSync re-fetches the PR head, the diff, and the GitHub
// review comments for an active review session — used by the FE Sync
// button so the panel reflects new commits / new GH comments without
// the user having to Close + Load. Agent-emitted comments are preserved
// (so a partially-done review survives a Sync); existing GH comments
// are replaced with a fresh snapshot. Requires Status=Ready (or
// Error/Reviewing? — we accept any non-Loading status: we never want
// two concurrent worktree mutations).
func (s *Server) handleReviewSync(w http.ResponseWriter, r *http.Request) {
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
	sess := s.reviewStore.Get(channelID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if sess.PR == nil || sess.WorktreePath == "" {
		http.Error(w, "session not ready (no PR or worktree)", http.StatusConflict)
		return
	}
	if sess.Status == review.StatusLoading || sess.Status == review.StatusReviewing {
		http.Error(w, "session busy (status="+string(sess.Status)+")", http.StatusConflict)
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
	if !s.requireReviewEnabled(w, dirPath, parentDirPath) {
		return
	}
	ghUser := s.resolveGHUser(dirPath, parentDirPath)

	if _, err := s.refreshReviewSession(r.Context(), channelID, dirPath, ghUser, sess); err != nil {
		respondReviewError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: s.reviewStore.Get(channelID)}, s.logger)
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
func (s *Server) refreshReviewSession(ctx context.Context, channelID, dirPath, ghUser string, sess *review.Session) (*review.Session, error) {
	prNum := sess.PR.Number
	headSHA, err := s.reviewClient.FetchPRHeadSHA(ctx, dirPath, ghUser, prNum)
	if err != nil {
		return nil, err
	}
	if err := s.reviewWorktree.Refresh(ctx, dirPath, sess.WorktreePath, prNum); err != nil {
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

	diff, err := s.reviewWorktree.Diff(ctx, dirPath, sess.WorktreePath, sess.PR.BaseRef, merged)
	if err != nil {
		return nil, err
	}
	raw := string(diff)
	s.reviewStore.Put(channelID, &review.Session{
		PR:           sess.PR,
		HeadSHA:      headSHA,
		WorktreePath: sess.WorktreePath,
		RawDiff:      raw,
		Comments:     merged,
		Status:       review.StatusReady,
	})
	if raw != sess.RawDiff {
		if hub := s.eventsHub; hub != nil {
			hub.BroadcastReviewDiff(channelID, events.ReviewDiffEventData{RawDiff: raw})
		}
	}
	return s.reviewStore.Get(channelID), nil
}

// fetchExistingReviewComments pulls inline review comments already filed
// on the PR via GitHub and converts them to review.Comment with
// Source="github" + Pushed=true so the FE renders them as read-only.
// Best-effort: any failure is logged and the function returns nil so
// Load still succeeds with the agent-only comment list.
func (s *Server) fetchExistingReviewComments(ctx context.Context, dirPath, ghUser string, prNum int) []*review.Comment {
	slug, err := s.reviewClient.FetchRepoSlug(ctx, dirPath, ghUser)
	if err != nil || slug == nil {
		if err != nil {
			s.logger.Debug("review: skip GH comment fetch (slug)", "err", err)
		}
		return nil
	}
	ghComments, err := s.reviewClient.FetchPRReviewComments(ctx, dirPath, ghUser, *slug, prNum)
	if err != nil {
		s.logger.Debug("review: skip GH comment fetch (api)", "err", err)
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
	if !s.requireReviewEnabled(w, dirPath, parentDirPath) {
		return
	}
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

// handleReviewSessions returns a (channel_id, status) summary for every
// live session. Used at FE startup to seed the sidebar's `rev` pill set
// so the indicator survives a renderer reload — review.status WS events
// only fire on transitions, and the FE doesn't subscribe to every
// channel, so without this any ready session that completed while the
// app was closed would never re-light its pill.
func (s *Server) handleReviewSessions(w http.ResponseWriter, _ *http.Request) {
	if s.reviewStore == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"sessions": s.reviewStore.List()}, s.logger)
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
		if errors.Is(err, errReviewDisabled) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
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
			if errors.Is(err, errReviewDisabled) {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			result.Failed++
			result.Errors = append(result.Errors, c.ID+": "+err.Error())
			continue
		}
		result.Pushed++
	}
	writeHTTPJSON(w, http.StatusOK, result, s.logger)
}

// handleReviewDeleteComment removes a single review comment from the
// session. If the comment has a GitHub-side id (either a github-source
// comment or an agent comment that was previously pushed) it is also
// deleted via `gh api DELETE /pulls/comments/{id}` so the PR no longer
// shows it. Local-only agent comments just disappear from the session.
//
// On success the response is 204 No Content. On a 4xx (session/comment
// missing) the local state is untouched. On a GitHub-side failure the
// local comment is also preserved so the user can retry — half-deleting
// (gone locally, still on GH) would be confusing.
func (s *Server) handleReviewDeleteComment(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
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

	// Only call GitHub when there's actually a GH-side comment to delete.
	// Agent comments that were never pushed have GitHubID==0 and live
	// purely in the in-memory session; just drop them locally.
	if c.GitHubID > 0 {
		if !requireConfigured(w, s.reviewClient, "review service not configured") {
			return
		}
		ch, err := s.store.GetChannel(r.Context(), channelID)
		if err != nil || ch == nil || ch.DirPath == "" {
			http.Error(w, "channel has no dir_path", http.StatusInternalServerError)
			return
		}
		parentDirPath := s.resolveParentDirPath(r.Context(), channelID)
		if !s.requireReviewEnabled(w, ch.DirPath, parentDirPath) {
			return
		}
		ghUser := s.resolveGHUser(ch.DirPath, parentDirPath)
		// GitHub-source comments are only deletable when their author
		// matches the configured gh user — GH would reject anyone else's
		// DELETE anyway, but failing fast here keeps the local copy
		// (otherwise our error path drops it). Agent comments don't
		// carry an Author (we posted them as the configured user) so
		// they pass through.
		if c.Source == "github" {
			if ghUser == "" || c.Author == "" || c.Author != ghUser {
				http.Error(w, "cannot delete a comment authored by another user on github", http.StatusForbidden)
				return
			}
		}
		slug, err := s.reviewClient.FetchRepoSlug(r.Context(), ch.DirPath, ghUser)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.reviewClient.DeletePRReviewComment(r.Context(), ch.DirPath, ghUser, *slug, c.GitHubID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	s.reviewStore.RemoveComment(channelID, commentID)
	w.WriteHeader(http.StatusNoContent)
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
	if !s.resolveReviewEnabled(ch.DirPath, parentDirPath) {
		return errReviewDisabled
	}
	ghUser := s.resolveGHUser(ch.DirPath, parentDirPath)
	slug, err := s.reviewClient.FetchRepoSlug(ctx, ch.DirPath, ghUser)
	if err != nil {
		return err
	}
	ghID, err := s.reviewClient.PostPRComment(ctx, ch.DirPath, ghUser, *slug, sess.PR.Number, sess.HeadSHA, c.Path, c.Side, c.Line, c.Body)
	if err != nil {
		return err
	}
	s.reviewStore.MarkPushed(channelID, c.ID, ghID)
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
	// Resolve the gh user once for the run so the agent knows which
	// account to switch to before shelling out to gh. dirPath is the
	// channel's own workdir (used for project-config layering), and
	// parentDirPath provides the worktree-merge layer.
	channelDirPath := ""
	if s.store != nil {
		if ch, err := s.store.GetChannel(r.Context(), channelID); err == nil && ch != nil {
			channelDirPath = ch.DirPath
			if channelDirPath == "" && s.loopDir != "" {
				channelDirPath = filepath.Join(s.loopDir, ch.ChannelID, "work")
			}
		}
	}
	if !s.resolveReviewEnabled(channelDirPath, parentDirPath) {
		s.unregisterReviewRun(channelID)
		http.Error(w, "review panel disabled for this project", http.StatusForbidden)
		return
	}
	ghUser := s.resolveGHUser(channelDirPath, parentDirPath)

	// Refresh the worktree + GH comments + diff before the agent kicks
	// off. Without this, the agent could review stale code (commits
	// pushed since Load) and miss out-of-band GH comments in its dedup
	// list. Mirrors Sync's behavior. Errors here unregister the run and
	// short-circuit before any status flip — the FE keeps showing
	// StatusReady and the error banner from the HTTP response.
	if channelDirPath == "" {
		s.unregisterReviewRun(channelID)
		http.Error(w, "channel has no dir_path", http.StatusBadRequest)
		return
	}
	refreshed, err := s.refreshReviewSession(r.Context(), channelID, channelDirPath, ghUser, sess)
	if err != nil {
		s.unregisterReviewRun(channelID)
		respondReviewError(w, err)
		return
	}
	sess = refreshed

	// Inlining the diff blew past Linux's MAX_ARG_STRLEN (~128KB per argv
	// entry) on large PRs and killed the container with "argument list too
	// long" before claude ever started. The worktree is already checked
	// out at PR head with `origin/<base>` fetched, so the agent can run
	// `git diff origin/<base>...HEAD` itself.
	fullPrompt := prompt + "\n\n" + buildReviewContext(sess, ghUser)
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
		s.maybeRediffForComment(channelID, worktreePath, parentDirPath, c)
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

// maybeRediffForComment re-runs git diff with widened unified context if
// the just-emitted comment lands on a known file but outside every
// existing hunk. Path-absent comments and comments already inside a
// hunk are no-ops. On success the session's raw_diff is swapped and a
// review.diff event is broadcast; on failure we log and continue —
// the FE keeps the old diff and the comment surfaces in the
// outside-of-diff section.
func (s *Server) maybeRediffForComment(channelID, worktreePath, parentDirPath string, c *review.Comment) {
	if s.reviewWorktree == nil {
		return
	}
	sess := s.reviewStore.Get(channelID)
	if sess == nil || sess.PR == nil || sess.PR.BaseRef == "" {
		return
	}
	if !review.ShouldRediff([]byte(sess.RawDiff), c.Path, c.Line, c.Side) {
		return
	}
	out, err := s.reviewWorktree.Diff(context.Background(), parentDirPath, worktreePath, sess.PR.BaseRef, sess.Comments)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("review re-diff failed", "channel_id", channelID, "error", err)
		}
		return
	}
	raw := string(out)
	if raw == sess.RawDiff {
		return
	}
	s.reviewStore.UpdateRawDiff(channelID, raw)
	if hub := s.eventsHub; hub != nil {
		hub.BroadcastReviewDiff(channelID, events.ReviewDiffEventData{RawDiff: raw})
	}
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

// isReviewRunActive reports whether a review run is currently in flight
// for channelID. Used by /review/load to refuse overwriting a session
// while its run goroutine is still executing.
func (s *Server) isReviewRunActive(channelID string) bool {
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	_, ok := s.reviewActive[channelID]
	return ok
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
// just drops one line without breaking the rest. When ghUser is set, an
// auth block tells the agent to switch the gh CLI to that account before
// running gh commands. Any existing review comments on the session are
// rendered as a dedup list so the agent does not re-emit them.
func buildReviewContext(sess *review.Session, ghUser string) string {
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
	if ghUser != "" {
		fmt.Fprintf(&b, "\nGitHub CLI account: %s\n", ghUser)
		fmt.Fprintf(&b, "If you need to run gh, switch to that account first with `gh auth switch -u %s` (only if it isn't already active).\n", ghUser)
	}
	if len(sess.Comments) > 0 {
		b.WriteString("\nExisting review comments on this PR — do NOT re-emit any of these. Only add NEW, non-duplicate findings.\n")
		for _, c := range sess.Comments {
			if c == nil {
				continue
			}
			label := "agent"
			if c.Source == "github" {
				label = "github"
				if c.Author != "" {
					label = "github @" + c.Author
				}
			}
			side := c.Side
			if side == "" {
				side = "RIGHT"
			}
			body := strings.TrimSpace(c.Body)
			if len(body) > 240 {
				body = body[:240] + "..."
			}
			fmt.Fprintf(&b, "- [%s] %s:L%d (%s): %s\n", label, c.Path, c.Line, side, body)
		}
	}
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
