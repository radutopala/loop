// Package review owns the per-channel review session: which PR is loaded,
// the worktree we checked it out into, the raw diff, and any review
// comments — pending or already pushed — that the agent has produced.
//
// Sessions are in-memory and per channel: opening a Review panel for a
// channel creates one, switching to a different PR replaces it, closing
// the panel tears it down. They do not survive daemon restart, which
// matches how the rest of the per-channel ephemeral state behaves.
package review

import (
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
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	Side      string    `json:"side"` // "RIGHT" added/modified, "LEFT" deleted
	Body      string    `json:"body"`
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
}

// Status describes where the session is in its lifecycle so the FE
// can render the right affordances (load button vs spinner vs diff).
type Status string

const (
	StatusIdle      Status = "idle"
	StatusLoading   Status = "loading"
	StatusReady     Status = "ready"
	StatusReviewing Status = "reviewing"
	StatusError     Status = "error"
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
}

// Store is the in-memory registry of active sessions keyed by channel id.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{sessions: make(map[string]*Session)}
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
}

// Delete removes the session for channelID. No-op if none exists.
func (s *Store) Delete(channelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, channelID)
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
// session exists for channelID.
func (s *Store) AddComment(channelID string, c *Comment) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[channelID]
	if !ok {
		return false
	}
	sess.Comments = append(sess.Comments, c)
	sess.UpdatedAt = time.Now()
	return true
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
