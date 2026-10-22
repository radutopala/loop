// Package review owns the per-channel review session: which PR is loaded,
// the worktree we checked it out into, the raw diff, and any review
// comments — pending or already pushed — that the agent has produced.
//
// Sessions are in-memory and per channel: opening a Review panel for a
// channel creates one, switching to a different PR replaces it, closing
// the panel tears it down. A Saver keeps the PR each one is on and the
// user's choices for its runs (see Saved), so the daemon can load the PR
// again after a restart; the findings and runs are kept in the daemon's
// review history, and loading the PR brings them back.
package review

import (
	"slices"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/githubapi"
)

// Comment is a single inline review note against a specific (path, line)
// pair in the PR diff. Pushed flips to true after PostPRComment succeeds
// — pre-existing GitHub comments are seeded with Pushed=true at Load.
// Source distinguishes agent-emitted comments (the current review run)
// from comments already filed on the PR via GitHub.
type Comment struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Side     string `json:"side"` // "RIGHT" added/modified, "LEFT" deleted
	Body     string `json:"body"`
	Category string `json:"category,omitempty"` // finding type slug, e.g. "correctness"
	// Verdict is the dedup pass's check of an agent comment against the
	// code: VerdictReal, VerdictFalsePositive or VerdictAlreadyFixed, with
	// VerdictReason saying why. Empty until a pass has checked it.
	Verdict       string `json:"verdict,omitempty"`
	VerdictReason string `json:"verdict_reason,omitempty"`
	// Impact is how much a real finding weighs on the PR: ImpactBlocking or
	// ImpactNiceToHave. Empty with any other verdict, or until judged.
	Impact    string    `json:"impact,omitempty"`
	Pushed    bool      `json:"pushed"`
	PushedAt  time.Time `json:"pushed_at,omitzero"`
	Source    string    `json:"source,omitempty"`     // "agent" | "github"
	Author    string    `json:"author,omitempty"`     // GitHub login (for source=github)
	URL       string    `json:"url,omitempty"`        // html_url for source=github
	CreatedAt string    `json:"created_at,omitempty"` // GitHub createdAt for source=github
	Outdated  bool      `json:"outdated,omitempty"`   // true when GH could not anchor to current head
	Resolved  bool      `json:"resolved,omitempty"`   // true when the GH review thread is marked resolved
	// GitHubID is the numeric comment id assigned by GitHub. Set when we
	// load existing GH comments (parsed from PRReviewComment.ID) or when
	// we successfully push an agent comment (captured from the POST
	// response). Used as the address for DELETE /pulls/comments/{id} —
	// zero means "no GH-side state to clean up; local removal only".
	GitHubID int64 `json:"github_id,omitempty"`
	// ReplyTo is the GitHub id of the first comment of the thread a GitHub
	// reply belongs to; zero for a thread's first comment and for agent
	// comments.
	ReplyTo int64 `json:"reply_to,omitempty"`
	// Transcripts are the Claude transcripts of the agent passes that
	// reported an agent comment or changed it (the review run, the dedup
	// pass, the verification), oldest first: the reasoning behind a
	// finding lives there and nowhere else. Absolute, and valid inside an
	// agent container too: containers run with HOME set to the host home
	// and ~/.claude bind-mounted at its host path.
	Transcripts []string `json:"transcripts,omitempty"`
	// RunID is the history id of the review run that reported an agent
	// comment, 0 when none was in flight or there is no history.
	RunID int64 `json:"run_id,omitempty"`
}

// Status describes where the session is in its lifecycle so the FE
// can render the right affordances (load button vs spinner vs diff).
type Status string

const (
	StatusIdle      Status = "idle"
	StatusLoading   Status = "loading"
	StatusReady     Status = "ready"
	StatusReviewing Status = "reviewing"
	// StatusVerifying is the check that follows a review: the dedup pass,
	// then an agent judging each agent comment against the code (see
	// VerifyPrompt). The session takes no other run until it is over, so
	// Address all and Push all act on the verdicts.
	StatusVerifying Status = "verifying"
	StatusError     Status = "error"
)

// ForkMode selects which Claude session a review run starts from. The
// zero value is the historical behaviour — every run gets a brand-new
// session that knows nothing about the channel's conversation.
//
// Forking is always a fork, never a plain resume: the review agent must
// not append its turns to the chat session the user is still talking to.
type ForkMode string

const (
	// ForkNone runs the review in a fresh session (default).
	ForkNone ForkMode = ""
	// ForkCurrent forks whatever session the channel is on at run time.
	// Resolved per run, not pinned at selection time, so a session that
	// rolls over (compaction, a fork of its own) is picked up.
	ForkCurrent ForkMode = "current"
	// ForkCustom forks the session id the user typed in.
	ForkCustom ForkMode = "custom"
)

// Session is the per-channel review state. All mutations go through
// Store so the mutex stays inside the package.
type Session struct {
	ChannelID    string            `json:"channel_id"`
	PR           *githubapi.PRInfo `json:"pr,omitempty"`
	HeadSHA      string            `json:"head_sha,omitempty"`
	WorktreePath string            `json:"worktree_path,omitempty"`
	RawDiff      string            `json:"raw_diff,omitempty"`
	Comments     []*Comment        `json:"comments"`
	Status       Status            `json:"status"`
	Error        string            `json:"error,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at"`
	// ForkMode / ForkSessionID are the user's choice for what the *next*
	// run forks from. They live on the session (not the request) so the
	// choice survives the FE's workflow-driven Run button, which reaches
	// /review/run through the CLI inside a workflow node and has nowhere
	// to put per-run options.
	ForkMode      ForkMode `json:"fork_mode,omitempty"`
	ForkSessionID string   `json:"fork_session_id,omitempty"`
	// Model / Effort are the user's model and reasoning-effort choice for
	// the next run, kept on the session for the same reason as ForkMode.
	// Empty inherits the config's claude_model / claude_effort.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Superseded maps each comment the latest run's dedup pass deleted to
	// the comment it was folded into, so `loop review:run --wait` can tell
	// a review loop's stop check that a reworded repeat isn't a new
	// finding. Each run replaces it wholesale; it is never mutated.
	Superseded map[string]string `json:"superseded,omitempty"`
	// Scope is the user's choice of what the next run reviews, kept on the
	// session for the same reason as ForkMode. ScopeAuto picks per run.
	Scope Scope `json:"scope,omitempty"`
	// LastRunScope / LastRunFromSHA are the scope the latest run resolved
	// to, and for an incremental one the commit its range starts at.
	LastRunScope   Scope  `json:"last_run_scope,omitempty"`
	LastRunFromSHA string `json:"last_run_from_sha,omitempty"`
	// ReviewedSHA is the commit the latest finished review run or
	// verification step judged. The outcome only holds while it is the
	// head.
	ReviewedSHA string `json:"reviewed_sha,omitempty"`
	// ApprovedSHA is the commit the PR was approved at from the panel.
	ApprovedSHA string `json:"approved_sha,omitempty"`
	// Threads are the verification step's assessments of the PR's
	// unresolved GitHub threads a person started, keyed by the GitHub id of
	// the thread's first comment. Replaced wholesale, never mutated.
	Threads map[int64]ThreadAssessment `json:"threads,omitempty"`
	// ReviewID names the latest review: a Run starts one, and every run of
	// its review loop belongs to it. ReviewRuns are the history ids of
	// those runs, so the comments they reported (by RunID) are the
	// review's new ones.
	ReviewID   string  `json:"review_id,omitempty"`
	ReviewRuns []int64 `json:"review_runs,omitempty"`
	// ReviewHead is the commit a review-only loop started at: its runs
	// review that commit, not the PR's newer head. Empty follows the head.
	ReviewHead string `json:"review_head,omitempty"`
	// HistoryDir keys the session's review history: the repo's main dir,
	// so every worktree of the repo shares it. Empty keeps no history.
	HistoryDir string `json:"-"`
}

// Saved is the part of a session kept across daemon restarts: the PR it is
// on and the user's choices for its runs. The rest is rebuilt by loading
// the PR again, the runs and findings from the review history.
type Saved struct {
	ChannelID     string
	PRNumber      int
	ForkMode      ForkMode
	ForkSessionID string
	Model         string
	Effort        string
	Scope         Scope
	ReviewedSHA   string
	ApprovedSHA   string
	// Threads is Session.Threads as JSON, so that Saved stays comparable.
	Threads string
}

// saved returns what of sess is kept, nil while it is on no PR.
func (sess *Session) saved() *Saved {
	if sess.PR == nil {
		return nil
	}
	return &Saved{
		ChannelID:     sess.ChannelID,
		PRNumber:      sess.PR.Number,
		ForkMode:      sess.ForkMode,
		ForkSessionID: sess.ForkSessionID,
		Model:         sess.Model,
		Effort:        sess.Effort,
		Scope:         sess.Scope,
		ReviewedSHA:   sess.ReviewedSHA,
		ApprovedSHA:   sess.ApprovedSHA,
		Threads:       encodeThreads(sess.Threads),
	}
}

// Apply sets the choices sv keeps on sess, which is on the same PR.
func (sv *Saved) Apply(sess *Session) {
	sess.ForkMode, sess.ForkSessionID = sv.ForkMode, sv.ForkSessionID
	sess.Model, sess.Effort = sv.Model, sv.Effort
	sess.Scope = sv.Scope
	sess.ReviewedSHA, sess.ApprovedSHA = sv.ReviewedSHA, sv.ApprovedSHA
	sess.Threads = decodeThreads(sv.Threads)
}

// Saver keeps sessions across daemon restarts. The store calls it under
// its lock, so the saves of a channel land in order, and only when what
// is kept changed.
type Saver interface {
	SaveSession(saved *Saved)
	DeleteSession(channelID string)
}

// Store is the in-memory registry of active sessions keyed by channel id.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	saver    Saver
	// saved is what was last kept per channel, which saver got.
	saved map[string]*Saved
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{sessions: make(map[string]*Session), saved: make(map[string]*Saved)}
}

// SetSaver keeps the sessions changed from now on with sv.
func (s *Store) SetSaver(sv Saver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saver = sv
}

// save records what is kept of sess, and hands it to the saver when it
// changed. The caller holds the write lock.
func (s *Store) save(sess *Session) {
	cur := sess.saved()
	if cur == nil {
		return
	}
	if prev := s.saved[sess.ChannelID]; prev != nil && *prev == *cur {
		return
	}
	if s.saver != nil {
		s.saver.SaveSession(cur)
	}
	s.saved[sess.ChannelID] = cur
}

// Get returns a copy of the session for channelID, or nil if there is
// none. The copy is shallow: comment pointers are shared with the
// store, but since comments are only mutated through Store methods that
// take the write lock, that's safe for read-only consumption.
func (s *Store) Get(channelID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return nil
	}
	cp := *sess
	// Start from a non-nil zero-length slice so the JSON encoder emits `[]`
	// instead of `null` when there are no comments yet — the renderer reads
	// `session.comments.length` directly and crashes on null.
	cp.Comments = append(make([]*Comment, 0, len(sess.Comments)), sess.Comments...)
	return &cp
}

// Put replaces (or creates) the session for channelID. The caller
// owns construction; the store only stamps UpdatedAt.
func (s *Store) Put(channelID string, sess *Session) {
	sess.ChannelID = channelID
	sess.UpdatedAt = time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[channelID] = sess
	s.save(sess)
}

// Settle replaces the loading session for channelID with sess, its PR now
// loaded. The run choices made on the loading session while the PR loaded
// carry over to sess: they are newer than those sess was built with.
func (s *Store) Settle(channelID string, sess *Session) {
	sess.ChannelID = channelID
	sess.UpdatedAt = time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.sessions[channelID]; ok && cur.Status == StatusLoading {
		sess.ForkMode, sess.ForkSessionID = cur.ForkMode, cur.ForkSessionID
		sess.Model, sess.Effort = cur.Model, cur.Effort
		sess.Scope = cur.Scope
	}
	s.sessions[channelID] = sess
	s.save(sess)
}

// Kept returns what was last kept of channelID's session while it was on
// PR prNumber, nil when nothing was.
func (s *Store) Kept(channelID string, prNumber int) *Saved {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sv := s.saved[channelID]
	if sv == nil || sv.PRNumber != prNumber {
		return nil
	}
	cp := *sv
	return &cp
}

// Keep records saved as kept already, without handing them to the saver:
// the sessions a daemon start restores, so a PR loaded again before the
// restore reaches its channel keeps their choices. A channel kept since
// is left as it is.
func (s *Store) Keep(saved []*Saved) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sv := range saved {
		if _, ok := s.saved[sv.ChannelID]; ok {
			continue
		}
		cp := *sv
		s.saved[sv.ChannelID] = &cp
	}
}

// SessionSummary is the minimal (channel_id, status, updated_at) triple
// returned by List — enough for the sidebar to decide whether to render a
// `rev` pill, and for the channel list to mark a running review and date
// the channel's activity, without dragging the whole session (raw diff +
// comments) over the wire.
type SessionSummary struct {
	ChannelID string    `json:"channel_id"`
	Status    Status    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// List returns a summary of every live session. Order is unspecified.
// Used at FE startup to hydrate the sidebar's rev-pill set; live
// transitions still come through the review.status WS event.
func (s *Store) List() []SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionSummary, 0, len(s.sessions))
	for id, sess := range s.sessions {
		out = append(out, SessionSummary{ChannelID: id, Status: sess.Status, UpdatedAt: sess.UpdatedAt})
	}
	return out
}

// Delete removes the session for channelID. No-op if none exists.
func (s *Store) Delete(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, channelID)
	delete(s.saved, channelID)
	if s.saver != nil {
		s.saver.DeleteSession(channelID)
	}
}

// AddTranscript appends path to the transcripts of the agent comments in
// ids that don't list it yet, and returns the comments it changed.
func (s *Store) AddTranscript(channelID, path string, ids map[string]bool) []*Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return nil
	}
	var out []*Comment
	for i, c := range sess.Comments {
		if !ids[c.ID] || !deletable(c) || slices.Contains(c.Transcripts, path) {
			continue
		}
		updated := *c
		updated.Transcripts = append(slices.Clone(c.Transcripts), path)
		sess.Comments[i] = &updated
		out = append(out, &updated)
	}
	if len(out) > 0 {
		sess.UpdatedAt = time.Now()
	}
	return out
}

// UpdateStatus transitions the session's status. Returns false if no
// session exists for channelID.
func (s *Store) UpdateStatus(channelID string, status Status, errMsg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.Status = status
	sess.Error = errMsg
	sess.UpdatedAt = time.Now()
	return true
}

// SetSuperseded replaces the session's Superseded map. Returns false if no
// session exists for channelID.
func (s *Store) SetSuperseded(channelID string, superseded map[string]string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.Superseded = superseded
	return true
}

// UpdateFork records which session the next review run should fork.
// sessionID is only meaningful for ForkCustom and is cleared otherwise,
// so a mode switch can't leave a stale id behind to be picked up later.
// Returns false if no session exists for channelID.
func (s *Store) UpdateFork(channelID string, mode ForkMode, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.ForkMode = mode
	sess.ForkSessionID = ""
	if mode == ForkCustom {
		sess.ForkSessionID = sessionID
	}
	sess.UpdatedAt = time.Now()
	s.save(sess)
	return true
}

// UpdateAgent records the model and reasoning effort the next review run
// uses; empty values fall back to the config. Returns false if no session
// exists for channelID.
func (s *Store) UpdateAgent(channelID, model, effort string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.Model = model
	sess.Effort = effort
	sess.UpdatedAt = time.Now()
	s.save(sess)
	return true
}

// UpdateScope records what the next review run reviews. Returns false if
// no session exists for channelID.
func (s *Store) UpdateScope(channelID string, scope Scope) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.Scope = scope
	sess.UpdatedAt = time.Now()
	s.save(sess)
	return true
}

// SetLastRunScope records the scope the run starting now resolved to.
// Returns false if no session exists for channelID.
func (s *Store) SetLastRunScope(channelID string, scope Scope, fromSHA string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.LastRunScope, sess.LastRunFromSHA = scope, fromSHA
	return true
}

// StartReview makes reviewID the latest review, with no runs yet, pinned
// to head ("" follows the PR's head). Returns false if no session exists
// for channelID.
func (s *Store) StartReview(channelID, reviewID, head string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.ReviewID, sess.ReviewRuns, sess.ReviewHead = reviewID, nil, head
	return true
}

// AddReviewRun adds the history id of a run of the latest review.
// Returns false if no session exists for channelID.
func (s *Store) AddReviewRun(channelID string, runID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.ReviewRuns = append(slices.Clip(sess.ReviewRuns), runID)
	return true
}

// UpdateRawDiff swaps the session's raw_diff. Used by the agent-stream
// path when a freshly-emitted comment lands outside the existing hunks
// and the diff has to be re-rendered with a widened `-U`. Returns false
// if no session exists for channelID.
func (s *Store) UpdateRawDiff(channelID, rawDiff string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.RawDiff = rawDiff
	sess.UpdatedAt = time.Now()
	return true
}

// AddComment appends a comment to the session. Returns false if no
// session exists for channelID, or if the session already holds a comment
// with the same id.
func (s *Store) AddComment(channelID string, c *Comment) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	// Dedup by stable id: the agent may report the same finding twice
	// (retries, or a rerun over an existing session).
	for _, existing := range sess.Comments {
		if existing != nil && existing.ID == c.ID {
			return false
		}
	}
	sess.Comments = append(sess.Comments, c)
	sess.UpdatedAt = time.Now()
	return true
}

// nearbyLines is how far the dedup pass may move a comment: a correction
// to a misplaced anchor is a nudge within the same function, not a new
// finding somewhere else.
const nearbyLines = 20

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// effectiveSide is side as the FE renders it: the parser leaves it empty for
// the common case, which is RIGHT.
func effectiveSide(side string) string {
	if side == "" {
		return "RIGHT"
	}
	return side
}

// MarkPushed flips Pushed=true on the comment with the matching ID and
// stamps GitHubID with the id GitHub assigned on POST (0 if unknown).
// Returns false if no session or no matching comment.
func (s *Store) MarkPushed(channelID, commentID string, githubID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	for _, c := range sess.Comments {
		if c.ID == commentID {
			c.Pushed = true
			c.PushedAt = time.Now()
			if githubID > 0 {
				c.GitHubID = githubID
			}
			sess.UpdatedAt = time.Now()
			return true
		}
	}
	return false
}

// RemoveComment drops the comment with the matching ID from the session.
// Returns the removed comment (or nil) so callers can inspect e.g. its
// GitHubID before deciding whether to also delete on GitHub. The bool
// is true when the session existed (regardless of whether the comment
// was found) so callers can distinguish 404-no-session from 404-no-comment.
func (s *Store) RemoveComment(channelID, commentID string) (*Comment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return nil, false
	}
	for i, c := range sess.Comments {
		if c.ID == commentID {
			sess.Comments = append(sess.Comments[:i], sess.Comments[i+1:]...)
			sess.UpdatedAt = time.Now()
			return c, true
		}
	}
	return nil, true
}

// EditLocalComment applies edit to the comment with the matching ID, the
// way the dedup pass folds a note into a kept comment or moves one to the
// right line. Only a local agent comment is changed: a GitHub comment isn't
// ours to edit, and a pushed one would drift from its copy on the PR. edit
// gets a copy that then replaces the comment, since FindComment hands out
// the pointer. A new body is a new claim, so it drops the verdict on the old
// one until a pass checks it again. Returns the updated comment, or nil when
// nothing changed.
func (s *Store) EditLocalComment(channelID, commentID string, edit func(*Comment)) *Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return nil
	}
	for i, c := range sess.Comments {
		if c.ID != commentID {
			continue
		}
		if !deletable(c) || c.Pushed {
			return nil
		}
		updated := *c
		edit(&updated)
		if updated.Body != c.Body {
			updated.Verdict, updated.VerdictReason, updated.Impact = "", "", ""
		}
		sess.Comments[i] = &updated
		sess.UpdatedAt = time.Now()
		return &updated
	}
	return nil
}

// SetVerdict records a verdict on the agent comment with ID v.ID, with its
// impact when the verdict is real (any other verdict clears it). Unlike
// EditLocalComment it takes a pushed comment too: the verdict stays local
// and never reaches the PR. Returns the updated comment, or nil when there
// is none or it is a GitHub comment.
func (s *Store) SetVerdict(channelID string, v DedupVerdict) *Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return nil
	}
	for i, c := range sess.Comments {
		if c.ID != v.ID {
			continue
		}
		if !deletable(c) {
			return nil
		}
		updated := *c
		updated.Verdict, updated.VerdictReason, updated.Impact = v.Verdict, v.Reason, ImpactOf(v.Verdict, v.Impact)
		sess.Comments[i] = &updated
		sess.UpdatedAt = time.Now()
		return &updated
	}
	return nil
}

// FindComment returns the comment with the matching ID (or nil) along
// with the session it belongs to. Caller must not mutate either.
func (s *Store) FindComment(channelID, commentID string) (*Comment, *Session) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return nil, nil
	}
	for _, c := range sess.Comments {
		if c.ID == commentID {
			return c, sess
		}
	}
	return nil, sess
}
