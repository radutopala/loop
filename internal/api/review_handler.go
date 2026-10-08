package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/review"
)

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
func (s *reviewService) requireReviewEnabled(w http.ResponseWriter, dirPath, parentDirPath string) bool {
	if !s.deps.configs.reviewEnabled(dirPath, parentDirPath) {
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

func (s *reviewService) handleReviewLoad(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.deps.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.client, "review service not configured") {
		return
	}
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	if !requireConfigured(w, s.worktree, "review service not configured") {
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

	ch, err := s.deps.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	dirPath := ch.DirPath
	if dirPath == "" && s.deps.loopDir != "" {
		dirPath = filepath.Join(s.deps.loopDir, ch.ChannelID, "work")
	}
	if dirPath == "" {
		http.Error(w, "channel has no dir_path", http.StatusBadRequest)
		return
	}

	parentDirPath := s.deps.workspace.resolveParentDirPath(r.Context(), channelID)
	if !s.requireReviewEnabled(w, dirPath, parentDirPath) {
		return
	}
	ghUser := s.deps.configs.ghUser(dirPath, parentDirPath)

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
	if prev := s.sessions.Get(channelID); prev != nil && prev.WorktreePath != "" {
		if err := s.worktree.Remove(r.Context(), dirPath, prev.WorktreePath); err != nil {
			s.deps.logger.Warn("review worktree remove failed on load overwrite",
				"channel_id", channelID, "path", prev.WorktreePath, "err", err)
		}
	}

	// Mark loading early so the GET endpoint can show a spinner while the
	// gh + git work runs.
	s.sessions.Put(channelID, &review.Session{Status: review.StatusLoading})

	pr, err := s.client.FetchPRByNumber(r.Context(), dirPath, ghUser, req.PRNumber)
	if err != nil {
		s.sessions.UpdateStatus(channelID, review.StatusError, errorMessage(err))
		respondReviewError(w, err)
		return
	}
	if pr == nil {
		s.sessions.UpdateStatus(channelID, review.StatusError, "PR not found")
		http.Error(w, "PR not found", http.StatusNotFound)
		return
	}

	headSHA, err := s.client.FetchPRHeadSHA(r.Context(), dirPath, ghUser, req.PRNumber)
	if err != nil {
		s.sessions.UpdateStatus(channelID, review.StatusError, errorMessage(err))
		respondReviewError(w, err)
		return
	}

	// Check out the PR head locally first so the diff (and the review
	// agent) can read the actual files in their post-merge form.
	worktreePath, err := s.worktree.Add(r.Context(), dirPath, req.PRNumber)
	if err != nil {
		s.sessions.UpdateStatus(channelID, review.StatusError, errorMessage(err))
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

	diff, err := s.worktree.Diff(r.Context(), dirPath, worktreePath, pr.BaseRef, ghComments)
	if err != nil {
		s.sessions.UpdateStatus(channelID, review.StatusError, errorMessage(err))
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
		// Reviews start from the chat's context by default: the conversation
		// that produced the change is usually what the reviewer is missing.
		// With no chat yet, the run falls back to a fresh session.
		ForkMode: review.ForkCurrent,
	}
	s.sessions.Put(channelID, sess)
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: s.sessions.Get(channelID)}, s.deps.logger)
}

// handleReviewSync re-fetches the PR head, the diff, and the GitHub
// review comments for an active review session — used by the FE Sync
// button so the panel reflects new commits / new GH comments without
// the user having to Close + Load. Agent-emitted comments are preserved
// (so a partially-done review survives a Sync); existing GH comments
// are replaced with a fresh snapshot. Requires Status=Ready (or
// Error/Reviewing? — we accept any non-Loading status: we never want
// two concurrent worktree mutations).
func (s *reviewService) handleReviewSync(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.deps.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.client, "review service not configured") {
		return
	}
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	if !requireConfigured(w, s.worktree, "review service not configured") {
		return
	}

	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
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

	ch, err := s.deps.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	dirPath := ch.DirPath
	if dirPath == "" && s.deps.loopDir != "" {
		dirPath = filepath.Join(s.deps.loopDir, ch.ChannelID, "work")
	}
	if dirPath == "" {
		http.Error(w, "channel has no dir_path", http.StatusBadRequest)
		return
	}
	parentDirPath := s.deps.workspace.resolveParentDirPath(r.Context(), channelID)
	if !s.requireReviewEnabled(w, dirPath, parentDirPath) {
		return
	}
	ghUser := s.deps.configs.ghUser(dirPath, parentDirPath)

	if _, err := s.refreshReviewSession(r.Context(), channelID, dirPath, ghUser, sess); err != nil {
		respondReviewError(w, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: s.sessions.Get(channelID)}, s.deps.logger)
}

// handleReviewListPRs returns the list of open PRs in the repo backing the
// channel's working directory. The FE renders these as a picker so the user
// can click a row to auto-load instead of pasting a PR number or URL.
func (s *reviewService) handleReviewListPRs(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.deps.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.client, "review service not configured") {
		return
	}

	channelID := r.PathValue("id")
	ch, err := s.deps.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	dirPath := ch.DirPath
	if dirPath == "" && s.deps.loopDir != "" {
		dirPath = filepath.Join(s.deps.loopDir, ch.ChannelID, "work")
	}
	if dirPath == "" {
		http.Error(w, "channel has no dir_path", http.StatusBadRequest)
		return
	}

	parentDirPath := s.deps.workspace.resolveParentDirPath(r.Context(), channelID)
	if !s.requireReviewEnabled(w, dirPath, parentDirPath) {
		return
	}
	ghUser := s.deps.configs.ghUser(dirPath, parentDirPath)

	prs, err := s.client.ListOpenPRs(r.Context(), dirPath, ghUser)
	if err != nil {
		respondReviewError(w, err)
		return
	}
	if prs == nil {
		prs = []githubapi.PRInfo{}
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"prs": prs}, s.deps.logger)
}

func (s *reviewService) handleReviewGet(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
	if sess == nil {
		writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: false}, s.deps.logger)
		return
	}
	// ?diff=false leaves out the PR diff, by far the largest field, for
	// callers that only want the comments. Get returns a copy.
	if r.URL.Query().Get("diff") == "false" {
		sess.RawDiff = ""
	}
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: sess}, s.deps.logger)
}

// reviewForkRequest is the body of PUT /review/fork. mode is one of
// "" (none), "current", or "custom"; session_id is read only for
// "custom".
type reviewForkRequest struct {
	Mode      string `json:"mode"`
	SessionID string `json:"session_id"`
}

// handleReviewSetFork records which Claude session the next review run
// should fork from. The choice is stored on the review session rather
// than passed per-run because the FE's Run button dispatches a workflow,
// and the review CLI inside that workflow has no channel for per-run
// options.
//
// Returns the updated session so the FE renders the new state without a
// follow-up GET.
func (s *reviewService) handleReviewSetFork(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	var body reviewForkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	mode := review.ForkMode(body.Mode)
	switch mode {
	case review.ForkNone, review.ForkCurrent:
	case review.ForkCustom:
		if strings.TrimSpace(body.SessionID) == "" {
			http.Error(w, "session_id is required for mode=custom", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "invalid mode (want \"\", \"current\", or \"custom\")", http.StatusBadRequest)
		return
	}
	if !s.sessions.UpdateFork(channelID, mode, strings.TrimSpace(body.SessionID)) {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: s.sessions.Get(channelID)}, s.deps.logger)
}

// handleReviewSetAgent records the model and reasoning effort the next
// review run uses. Stored on the review session for the same reason as the
// fork choice: the Run button reaches /review/run through a workflow. Empty
// values inherit the config. Returns the updated session.
func (s *reviewService) handleReviewSetAgent(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	var body agentConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	model := strings.TrimSpace(body.Model)
	effort := strings.TrimSpace(body.Effort)
	if _, ok := validEfforts[effort]; !ok {
		http.Error(w, "invalid effort: must be one of low, medium, high, xhigh, max (or empty)", http.StatusBadRequest)
		return
	}
	if !s.sessions.UpdateAgent(channelID, model, effort) {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	writeHTTPJSON(w, http.StatusOK, reviewSessionResponse{Present: true, Session: s.sessions.Get(channelID)}, s.deps.logger)
}

// handleReviewSessions returns a (channel_id, status) summary for every
// live session. Used at FE startup to seed the sidebar's `rev` pill set
// so the indicator survives a renderer reload — review.status WS events
// only fire on transitions, and the FE doesn't subscribe to every
// channel, so without this any ready session that completed while the
// app was closed would never re-light its pill.
func (s *reviewService) handleReviewSessions(w http.ResponseWriter, _ *http.Request) {
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"sessions": s.sessions.List()}, s.deps.logger)
}

func (s *reviewService) handleReviewDelete(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.deps.store, "channel listing not configured") {
		return
	}
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	if !requireConfigured(w, s.worktree, "review service not configured") {
		return
	}
	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
	if sess == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Detach any in-flight agent run before tearing down the session.
	// Otherwise the goroutine keeps running for 5–20 min, holding a
	// container slot, and the post-run UpdateStatus/Broadcast writes
	// into a deleted session.
	s.cancelReviewRun(channelID)
	if sess.WorktreePath != "" {
		ch, err := s.deps.store.GetChannel(r.Context(), channelID)
		if err == nil && ch != nil && ch.DirPath != "" {
			if err := s.worktree.Remove(r.Context(), ch.DirPath, sess.WorktreePath); err != nil {
				s.deps.logger.Warn("review worktree remove failed", "channel_id", channelID, "path", sess.WorktreePath, "err", err)
			}
		}
	}
	s.sessions.Delete(channelID)
	// Every window's sidebar marks a reviewing channel as running and lights
	// a ready one's pill, so they all need to hear the session is gone.
	s.broadcastReviewStatus(channelID, review.StatusIdle, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *reviewService) handleReviewPushComment(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.deps.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.client, "review service not configured") {
		return
	}
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	commentID := r.PathValue("cid")
	c, sess := s.sessions.FindComment(channelID, commentID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if c == nil {
		http.Error(w, "comment not found", http.StatusNotFound)
		return
	}
	if c.Pushed {
		writeHTTPJSON(w, http.StatusOK, map[string]any{"pushed": true, "already": true}, s.deps.logger)
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
	writeHTTPJSON(w, http.StatusOK, map[string]any{"pushed": true}, s.deps.logger)
}

// pushAllResult captures the outcome of POST .../review/push-all. Errors
// are accumulated rather than short-circuiting so a single bad comment
// doesn't block the rest.
type pushAllResult struct {
	Pushed int      `json:"pushed"`
	Failed int      `json:"failed"`
	Errors []string `json:"errors,omitempty"`
}

// handleReviewIngestComments accepts agent-reported findings from the
// report_review_findings MCP tool and feeds each through ingestComment
// (persist + broadcast + widened-context rediff). 404 when the channel
// has no review session — e.g. the session was deleted while the agent
// was still running. Malformed findings (empty path/body, line <= 0)
// and duplicates are skipped; the response reports how many were added
// so the agent can tell a dead session from an all-dup batch.
func (s *reviewService) handleReviewIngestComments(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	// Findings decode individually so one malformed entry (e.g. a string
	// where line should be a number) skips just that finding instead of
	// failing the whole batch — the agent already gets per-finding
	// accounting via the added/skipped counts.
	var body struct {
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Same parent-dir resolution as handleReviewRun: for a root channel
	// the worktree-parent resolver returns "" and the channel's own dir
	// (the main repo) is the diff workdir.
	_, parentDirPath := s.reviewRunDirs(r.Context(), channelID)
	added, skipped := 0, 0
	for _, raw := range body.Findings {
		var f struct {
			Path     string `json:"path"`
			Line     int    `json:"line"`
			Side     string `json:"side"`
			Body     string `json:"body"`
			Category string `json:"category"`
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			skipped++
			continue
		}
		c := review.NewComment(f.Path, f.Line, f.Side, f.Body)
		if c == nil {
			skipped++
			continue
		}
		c.Category = review.NormalizeCategory(f.Category)
		if s.ingestComment(channelID, sess.WorktreePath, parentDirPath, c) {
			added++
		} else {
			skipped++
		}
	}
	writeHTTPJSON(w, http.StatusOK, map[string]int{"added": added, "skipped": skipped}, s.deps.logger)
}

func (s *reviewService) handleReviewPushAll(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.deps.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.client, "review service not configured") {
		return
	}
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
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
	writeHTTPJSON(w, http.StatusOK, result, s.deps.logger)
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
func (s *reviewService) handleReviewDeleteComment(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.deps.store, "channel listing not configured") {
		return
	}
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	commentID := r.PathValue("cid")
	c, sess := s.sessions.FindComment(channelID, commentID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if c == nil {
		http.Error(w, "comment not found", http.StatusNotFound)
		return
	}
	// A GitHub comment is someone's, the user's own included. The user may
	// delete theirs from the panel; an agent may not delete any.
	if c.Source == "github" && apiauth.IsAgent(r.Context()) {
		http.Error(w, "agents can only delete agent comments", http.StatusForbidden)
		return
	}

	if err := s.deleteOneComment(r.Context(), channelID, c); err != nil {
		var herr *reviewHTTPError
		if errors.As(err, &herr) {
			http.Error(w, herr.msg, herr.status)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReviewUpdateComment replaces a comment's body, as when folding what
// a duplicate added into the comment kept. Only an unpushed agent comment
// can change: a GitHub comment isn't ours, and a pushed one would drift
// from its copy on the PR. Answers the updated comment and broadcasts it.
func (s *reviewService) handleReviewUpdateComment(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(body.Body)
	if text == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}
	channelID := r.PathValue("id")
	commentID := r.PathValue("cid")
	c, sess := s.sessions.FindComment(channelID, commentID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if c == nil {
		http.Error(w, "comment not found", http.StatusNotFound)
		return
	}
	updated := s.editComment(channelID, commentID, func(c *review.Comment) { c.Body = text })
	if updated == nil {
		http.Error(w, "only unpushed agent comments can be edited", http.StatusConflict)
		return
	}
	writeHTTPJSON(w, http.StatusOK, updated, s.deps.logger)
}

// reviewHTTPError is an error that carries the HTTP status to answer with.
// Errors without one are answered with 500.
type reviewHTTPError struct {
	status int
	msg    string
}

func (e *reviewHTTPError) Error() string { return e.msg }

// deleteOneComment deletes c on GitHub when it has a GitHub-side copy, then
// removes it from the session and tells the panel. On any failure the local
// comment is kept, so the user (or the dedup pass) can retry: half-deleting,
// gone locally but still on GitHub, would be confusing.
func (s *reviewService) deleteOneComment(ctx context.Context, channelID string, c *review.Comment) error {
	// Only call GitHub when there's actually a GH-side comment to delete.
	// Agent comments that were never pushed have GitHubID==0 and live
	// purely in the in-memory session; just drop them locally.
	if c.GitHubID > 0 {
		if s.client == nil {
			return &reviewHTTPError{http.StatusNotImplemented, "review service not configured"}
		}
		ch, err := s.deps.store.GetChannel(ctx, channelID)
		if err != nil || ch == nil || ch.DirPath == "" {
			return errors.New("channel has no dir_path")
		}
		parentDirPath := s.deps.workspace.resolveParentDirPath(ctx, channelID)
		if !s.deps.configs.reviewEnabled(ch.DirPath, parentDirPath) {
			return &reviewHTTPError{http.StatusForbidden, errReviewDisabled.Error()}
		}
		ghUser := s.deps.configs.ghUser(ch.DirPath, parentDirPath)
		// GitHub-source comments are only deletable when their author
		// matches the configured gh user — GH would reject anyone else's
		// DELETE anyway, but failing fast here keeps the local copy
		// (otherwise our error path drops it). Agent comments don't
		// carry an Author (we posted them as the configured user) so
		// they pass through.
		if c.Source == "github" {
			if ghUser == "" || c.Author == "" || c.Author != ghUser {
				return &reviewHTTPError{http.StatusForbidden, "cannot delete a comment authored by another user on github"}
			}
		}
		slug, err := s.client.FetchRepoSlug(ctx, ch.DirPath, ghUser)
		if err != nil {
			return err
		}
		if err := s.client.DeletePRReviewComment(ctx, ch.DirPath, ghUser, *slug, c.GitHubID); err != nil {
			return err
		}
	}

	s.sessions.RemoveComment(channelID, c.ID)
	if hub := s.deps.eventsHub; hub != nil {
		hub.BroadcastReviewCommentRemoved(channelID, c.ID)
	}
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
func (s *reviewService) handleReviewRun(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	if s.runner == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}

	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
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
	// by the first call. The cancel func registered here lets session-
	// delete and server-Stop detach the long-running agent ctx so the
	// container doesn't outlive its session.
	runCtx, cancelRun := context.WithCancel(context.Background())
	if !s.registerReviewRun(channelID, cancelRun) {
		cancelRun()
		writeHTTPJSON(w, http.StatusAccepted, map[string]string{"status": "in_progress"}, s.deps.logger)
		return
	}
	if sess.Status != review.StatusReady {
		s.unregisterReviewRun(channelID)
		http.Error(w, "session not ready (status="+string(sess.Status)+")", http.StatusConflict)
		return
	}

	channelDirPath, parentDirPath := s.reviewRunDirs(r.Context(), channelID)

	prompt := s.userPrompt
	if prompt == "" {
		prompt = defaultReviewPrompt
	}
	if !s.deps.configs.reviewEnabled(channelDirPath, parentDirPath) {
		s.unregisterReviewRun(channelID)
		http.Error(w, "review panel disabled for this project", http.StatusForbidden)
		return
	}
	// Resolve the gh user once for the run so the agent knows which
	// account to switch to before shelling out to gh. dirPath is the
	// channel's own workdir (used for project-config layering), and
	// parentDirPath provides the worktree-merge layer.
	ghUser := s.deps.configs.ghUser(channelDirPath, parentDirPath)

	// Refresh the worktree + GH comments + diff before the agent kicks
	// off. Without this, the agent could review stale code (commits
	// pushed since Load), and the dedup pass after it would miss
	// out-of-band GH comments. Mirrors Sync's behavior. Errors here unregister the run and
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
	//
	// A slash-command prompt (the default /code-review, or a user-configured
	// one) must stay bare — anything appended would be parsed as skill
	// arguments — so the output contract and the PR context ride in the
	// system prompt instead. The default one does take the run's effort as
	// its level argument: left to pick its own, the skill runs a --effort
	// medium review at its high level.
	worktreePath := sess.WorktreePath
	if s.userPrompt == "" {
		effort := sess.Effort
		if effort == "" {
			effort = s.deps.configs.claudeEffort(worktreePath, parentDirPath)
		}
		prompt = reviewPromptAt(effort)
	}
	reviewContext := buildReviewContext(sess, ghUser)
	fullPrompt := prompt
	sysPrompt := s.systemPrompt
	if strings.HasPrefix(prompt, "/") {
		if sysPrompt == "" {
			sysPrompt = defaultReviewSystemPrompt
		}
		sysPrompt = sysPrompt + "\n\n" + reviewContext
	} else {
		fullPrompt = prompt + "\n\n" + reviewContext
	}

	// Resolve and stage the fork before flipping to Reviewing: a bad fork
	// config (no chat session yet, an id with no transcript on disk) is the
	// user's to fix, and it should surface as a failed Run rather than as a
	// session stuck in Reviewing behind a container that never had a chance.
	forkSessionID, err := s.prepareForkSession(r.Context(), sess, channelID, channelDirPath, worktreePath)
	if err != nil {
		s.unregisterReviewRun(channelID)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// The comments the run starts from, so the dedup pass after it knows
	// which ones the run added.
	before := make(map[string]bool, len(sess.Comments))
	for _, c := range sess.Comments {
		before[c.ID] = true
	}
	s.sessions.SetSuperseded(channelID, nil)
	s.sessions.UpdateStatus(channelID, review.StatusReviewing, "")
	s.broadcastReviewStatus(channelID, review.StatusReviewing, "")

	go s.runReviewAsync(runCtx, before, review.RunRequest{
		ChannelID:            channelID,
		DirPath:              worktreePath,
		ParentDirPath:        parentDirPath,
		SystemPrompt:         sysPrompt,
		SubagentSystemPrompt: reviewSubagentContext,
		Prompt:               fullPrompt,
		ForkSessionID:        forkSessionID,
		Model:                sess.Model,
		Effort:               sess.Effort,
	})
	writeHTTPJSON(w, http.StatusAccepted, map[string]string{"status": "started"}, s.deps.logger)
}

// reviewRunDirs returns the channel's own work dir and the dir a review
// container mounts as its parent.
//
// The PR worktree's `.git` is a pointer file referencing the *shared*
// gitdir, which only lives under the main repo. The container needs that
// mounted so the reference resolves inside the sandbox — otherwise the
// agent dies on startup and the run returns "no result event found". For
// a worktree-thread channel, the channel itself IS a worktree, so the
// shared gitdir lives under the *parent* channel's dir —
// resolveParentDirPath walks that chain. For a root channel it returns ""
// and the channel's own dir (the main repo) is the parent.
func (s *reviewService) reviewRunDirs(ctx context.Context, channelID string) (channelDirPath, parentDirPath string) {
	if s.deps.store != nil {
		if ch, err := s.deps.store.GetChannel(ctx, channelID); err == nil && ch != nil {
			channelDirPath = ch.DirPath
			if channelDirPath == "" && s.deps.loopDir != "" {
				channelDirPath = filepath.Join(s.deps.loopDir, ch.ChannelID, "work")
			}
		}
	}
	parentDirPath = s.deps.workspace.resolveParentDirPath(ctx, channelID)
	if parentDirPath == "" {
		parentDirPath = channelDirPath
	}
	return channelDirPath, parentDirPath
}

// reviewDedupResult is the response of POST .../review/dedup: the ids of
// the comments deleted as duplicates, how many comments the model was
// shown, and any deletions that failed (those comments are kept).
type reviewDedupResult struct {
	Removed  []string              `json:"removed"`
	Clusters []reviewDedupCluster  `json:"clusters"`
	Related  []review.DedupRelated `json:"related"`
	Moved    []reviewDedupMove     `json:"moved"`
	Trimmed  []reviewDedupTrim     `json:"trimmed"`
	Verdicts []review.DedupVerdict `json:"verdicts"`
	Checked  int                   `json:"checked"`
	Errors   []string              `json:"errors,omitempty"`
}

// reviewDedupCluster reports one merged group so a pass can be audited:
// which comment was kept, which were removed, why, and the note on what the
// removed ones added. NoteAdded is false when the keeper couldn't take the
// note (a GitHub or pushed comment); the note is still reported here.
type reviewDedupCluster struct {
	Kept      string   `json:"kept"`
	Removed   []string `json:"removed"`
	Reason    string   `json:"reason,omitempty"`
	Note      string   `json:"note,omitempty"`
	NoteAdded bool     `json:"note_added,omitempty"`
}

// reviewDedupMove reports a comment the pass re-anchored.
type reviewDedupMove struct {
	ID   string `json:"id"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

// reviewDedupTrim reports a bundled comment the pass cut down to the issues
// no other comment covers; CoveredBy reports the part cut out.
type reviewDedupTrim struct {
	ID        string `json:"id"`
	CoveredBy string `json:"covered_by"`
	Reason    string `json:"reason,omitempty"`
}

// handleReviewDedup runs the dedup pass over the whole of the channel's
// review session (`loop review dedup`); every review run already checks the
// comments it added (dedupAfterRun). It refreshes the session (so the PR's
// GitHub comments are current), has a read-only agent
// group the comments that report the same issue (review.BuildDedupPrompt),
// and deletes every group's extra agent comments, on GitHub too when they
// were pushed. GitHub comments are never deleted. A comment bundling an
// issue another comment covers is rewritten to the rest when it is an
// unpushed agent finding.
//
// It answers when the pass is done. It takes the channel's review-run slot,
// so it can't overlap a review run: that answers 409. A session with no
// file holding two comments, one of them the agent's, is answered at once
// without running the agent.
func (s *reviewService) handleReviewDedup(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil || s.runner == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}
	channelID := r.PathValue("id")
	sess := s.sessions.Get(channelID)
	if sess == nil {
		http.Error(w, "no review session for channel", http.StatusNotFound)
		return
	}
	if sess.WorktreePath == "" {
		http.Error(w, "session has no worktree", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if !s.registerReviewRun(channelID, cancel) {
		http.Error(w, "a review run is in progress", http.StatusConflict)
		return
	}
	defer s.unregisterReviewRun(channelID)
	if sess.Status != review.StatusReady {
		http.Error(w, "session not ready (status="+string(sess.Status)+")", http.StatusConflict)
		return
	}
	channelDirPath, parentDirPath := s.reviewRunDirs(ctx, channelID)
	if !s.requireReviewEnabled(w, channelDirPath, parentDirPath) {
		return
	}
	if channelDirPath == "" {
		http.Error(w, "channel has no dir_path", http.StatusBadRequest)
		return
	}
	ghUser := s.deps.configs.ghUser(channelDirPath, parentDirPath)
	sess, err := s.refreshReviewSession(ctx, channelID, channelDirPath, ghUser, sess)
	if err != nil {
		respondReviewError(w, err)
		return
	}

	cands := review.DedupCandidates(sess.Comments)
	if len(cands) == 0 {
		writeHTTPJSON(w, http.StatusOK, newReviewDedupResult(), s.deps.logger)
		return
	}

	s.sessions.UpdateStatus(channelID, review.StatusReviewing, "")
	s.broadcastReviewStatus(channelID, review.StatusReviewing, "")
	defer func() {
		s.sessions.UpdateStatus(channelID, review.StatusReady, "")
		s.broadcastReviewStatus(channelID, review.StatusReady, "")
	}()
	ctx, cancelTimeout := s.withRunTimeout(ctx)
	defer cancelTimeout()
	res, err := s.runDedupPass(ctx, channelID, parentDirPath, sess, cands, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, res, s.deps.logger)
}

// newReviewDedupResult returns an empty result whose lists encode as [].
func newReviewDedupResult() reviewDedupResult {
	return reviewDedupResult{Removed: []string{}, Clusters: []reviewDedupCluster{}, Related: []review.DedupRelated{}, Moved: []reviewDedupMove{}, Trimmed: []reviewDedupTrim{}, Verdicts: []review.DedupVerdict{}}
}

// runDedupPass has a read-only agent group cands (review.DedupCandidates of
// sess) by root cause and applies its plan: trims, then each group's extra
// agent comments deleted (on GitHub too when pushed) with a note on the
// keeper, then re-anchored lines, then each kept agent comment's verdict
// recorded on it. fresh (review.DedupFresh) marks the
// comments the latest review run added, for the agent to check against the
// rest; nil regroups the whole session. A failed deletion keeps its comment
// and is reported in Errors. The caller owns the run slot and the status.
func (s *reviewService) runDedupPass(ctx context.Context, channelID, parentDirPath string, sess *review.Session, cands []*review.Comment, fresh map[string]bool) (reviewDedupResult, error) {
	res := newReviewDedupResult()
	res.Checked = len(cands)
	reads := &fileReads{}
	resp, err := s.runner.Run(ctx, review.RunRequest{
		ChannelID:     channelID,
		DirPath:       sess.WorktreePath,
		ParentDirPath: parentDirPath,
		Prompt:        review.BuildDedupPrompt(cands, fresh),
		ReadOnly:      true,
		Model:         sess.Model,
		Effort:        sess.Effort,
		OnFileRead:    reads.add,
	})
	if resp != nil {
		s.sessions.AppendRunSession(channelID, resp.SessionID, s.transcriptDir(sess.WorktreePath))
	}
	if err != nil {
		return res, fmt.Errorf("dedup run: %w", err)
	}
	plan, err := review.ParseDedupReply(resp.Response, cands, fresh)
	if err != nil {
		return res, err
	}
	// Trims replace a body, so they go before the notes that append to one.
	for _, tr := range plan.Trims {
		if s.editComment(channelID, tr.ID, func(c *review.Comment) { c.Body = tr.Body }) != nil {
			res.Trimmed = append(res.Trimmed, reviewDedupTrim{ID: tr.ID, CoveredBy: tr.CoveredBy, Reason: tr.Reason})
		}
	}
	for _, cl := range plan.Clusters {
		out := reviewDedupCluster{Kept: cl.Keep, Removed: []string{}, Reason: cl.Reason, Note: cl.Note}
		for _, id := range cl.Drop {
			c, _ := s.sessions.FindComment(channelID, id)
			if c == nil {
				continue
			}
			if err := s.deleteOneComment(ctx, channelID, c); err != nil {
				res.Errors = append(res.Errors, id+": "+err.Error())
				continue
			}
			out.Removed = append(out.Removed, id)
		}
		if len(out.Removed) == 0 {
			continue
		}
		res.Removed = append(res.Removed, out.Removed...)
		if cl.Note != "" {
			out.NoteAdded = s.editComment(channelID, cl.Keep, func(c *review.Comment) {
				c.Body = review.WithDedupNote(c.Body, cl.Note)
			}) != nil
		}
		res.Clusters = append(res.Clusters, out)
	}
	res.Related = append(res.Related, plan.Related...)
	// A line move or a verdict is a claim about the code, so it only counts
	// when the pass actually opened the comment's file; otherwise it is a
	// guess from the comment text, and is dropped.
	paths := make(map[string]string, len(cands))
	for _, c := range cands {
		paths[c.ID] = c.Path
	}
	unread := func(id, what string) bool {
		if reads.has(paths[id]) {
			return false
		}
		s.deps.logger.Info("review dedup: comment file not read, dropping claim", "claim", what, "channel_id", channelID, "comment_id", id, "path", paths[id])
		return true
	}
	for _, mv := range plan.Moves {
		if unread(mv.ID, "move") {
			continue
		}
		from := 0
		c := s.editComment(channelID, mv.ID, func(c *review.Comment) {
			from, c.Line = c.Line, mv.Line
		})
		if c == nil {
			continue
		}
		res.Moved = append(res.Moved, reviewDedupMove{ID: mv.ID, From: from, To: mv.Line})
		s.maybeRediffForComment(channelID, sess.WorktreePath, parentDirPath, c)
	}
	for _, v := range plan.Verdicts {
		if unread(v.ID, "verdict") {
			continue
		}
		if s.setVerdict(channelID, v) != nil {
			res.Verdicts = append(res.Verdicts, v)
		}
	}
	return res, nil
}

// fileReads records the file_path of each Read tool call a dedup pass makes.
// add runs on the agent's stream goroutine; has runs once the pass is over.
type fileReads struct {
	mu    sync.Mutex
	paths []string
}

func (f *fileReads) add(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, path)
}

// has reports whether a Read opened rel, a repo-relative comment path. Read
// takes absolute paths, which inside the container sit under the worktree.
func (f *fileReads) has(rel string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return rel != "" && slices.ContainsFunc(f.paths, func(p string) bool {
		return p == rel || strings.HasSuffix(p, "/"+rel)
	})
}

// defaultReviewPrompt is the user-facing prompt sent to the review agent
// when no override is configured. It is a bare slash command: the built-in
// code-review skill ships with disable-model-invocation, so a Skill-tool
// call from the model is rejected — but a prompt that IS the slash command
// counts as a user invocation and runs the full skill. The output contract
// rides in defaultReviewSystemPrompt instead (slash prompts can't carry
// extra instructions), and the PR context block moves to the system prompt
// too — see the slash-prompt branch in handleReviewStart.
const defaultReviewPrompt = `/code-review`

// reviewPromptAt returns defaultReviewPrompt with effort as the skill's
// level argument, or bare when no effort is set. A config's claude_effort
// isn't validated where it's read, so a value the CLI doesn't know is left
// off too rather than handed to the skill as its argument.
func reviewPromptAt(effort string) string {
	if _, ok := validEfforts[effort]; !ok || effort == "" {
		return defaultReviewPrompt
	}
	return defaultReviewPrompt + " " + effort
}

// defaultReviewSystemPrompt carries the review-panel output contract when
// the prompt is the bare /code-review slash command. The command reports
// through Claude Code's own ReportFindings tool, and a review run enables
// it (agent.AgentRequest.ReviewMode) precisely so it can: Loop reads the
// tool_use straight off the agent's stream and ingests each finding. So
// this prompt reinforces the skill's own contract instead of competing
// with it — steering the model to Loop's MCP tool here would fight the
// skill's instructions, and pulling ReportFindings back out would make
// the command fork into a silent subagent again.
//
// The `line` requirement is the one addition that matters: ReportFindings
// treats line as optional, but a finding without one can't be placed in
// the diff and gets dropped on ingest.
const defaultReviewSystemPrompt = `You are reviewing a GitHub pull request for an external review pipeline. Report every finding by calling the ReportFindings tool once with the full list.

Every finding MUST carry a repo-relative ` + "`file`" + ` and a 1-based ` + "`line`" + ` in the current revision — a finding without a line is discarded, so pick the most relevant line rather than omitting it. Write ` + "`summary`" + ` as the one-line defect and ` + "`failure_scenario`" + ` as the concrete inputs or state that trigger it plus the wrong output or crash; both are shown to the reviewer.

Do not print the findings as your reply and do not emit XML blocks — the tool call is the only channel that reaches the reviewer. If there are no findings, skip the call. Do not fix anything yourself: the user triages comments from the Review panel.`

// buildReviewContext renders the per-PR context block appended to the
// configured review prompt. Each known field gets its own labelled line so
// the agent can quote it verbatim and so a missing field (e.g. empty Title)
// just drops one line without breaking the rest. When ghUser is set, an
// auth block tells the agent to switch the gh CLI to that account before
// running gh commands. The session's existing comments are left out: the
// review repeated some of them anyway, and the dedup pass after the run
// folds the repeats.
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
	b.WriteString("\n\n" + reviewLineRule + "\n")
	if ghUser != "" {
		fmt.Fprintf(&b, "\nGitHub CLI account: %s\n", ghUser)
		fmt.Fprintf(&b, "If you need to run gh, switch to that account first with `gh auth switch -u %s` (only if it isn't already active).\n", ghUser)
	}
	return b.String()
}

// reviewSubagentContext carries the line rule to the subagents the review
// command fans out to: they are the ones that pick each finding's line. The
// CLI applies --append-system-prompt to the main agent only, hence the
// separate --append-subagent-system-prompt. The framing line attributes the
// rule to the host that launched the run, so a subagent doesn't read it as a
// prompt injection.
const reviewSubagentContext = "Review pipeline context (authoritative, supplied by the host that launched this review):\n\n" + reviewLineRule + "\n"

// reviewLineRule tells the reviewer, and the subagents that derive its
// findings, how to pick a finding's line. Left to read the diff, which
// carries no line numbers, the agent counts from the hunk headers and lands
// a line or three off: on a blank line or a closing brace next to the code
// it means.
const reviewLineRule = "Line numbers: `git diff` output has none, and counting from hunk headers drifts. Before reporting a finding, confirm its `line` with a numbered read of the file at HEAD (the Read tool, `grep -n` or `nl -ba`), and point it at the statement the finding is about, never at a blank line or a lone closing brace."

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
