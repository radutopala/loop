package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/review"
)

// ReviewHistoryStore keeps the review runs and agent findings of each PR
// past the in-memory session: a PR loaded again gets its findings back,
// the next run knows the head the last one reviewed, and the stats route
// reads both.
type ReviewHistoryStore interface {
	CreateReviewRun(ctx context.Context, run *db.ReviewRun) (int64, error)
	FinishReviewRun(ctx context.Context, id int64, status, sessionID, model, errText string) error
	LastReviewedHead(ctx context.Context, dirPath string, prNumber int) (string, error)
	ListReviewRuns(ctx context.Context, dirPath string, prNumber int) ([]*db.ReviewRun, error)
	SaveReviewFinding(ctx context.Context, f *db.ReviewFinding) error
	DeleteReviewFinding(ctx context.Context, dirPath string, prNumber int, id, reason, supersededBy string) error
	ListReviewFindings(ctx context.Context, dirPath string, prNumber int) ([]*db.ReviewFinding, error)
	PruneReviewHistory(ctx context.Context, before time.Time) (int64, error)
	ReviewStats(ctx context.Context, f db.ReviewStatsFilter) (*db.ReviewStats, error)
	SaveReviewSession(ctx context.Context, rs *db.ReviewSession) error
	DeleteReviewSession(ctx context.Context, channelID string) error
	ListReviewSessions(ctx context.Context) ([]*db.ReviewSession, error)
}

// reviewHistoryKeep is how long a PR's review history is kept after its
// last run or finding change.
const reviewHistoryKeep = 180 * 24 * time.Hour

// WithReviewHistory keeps the review history in store.
func WithReviewHistory(store ReviewHistoryStore) Option {
	return func(s *Server) { s.review.history = store }
}

// RunReviewHistoryPrune prunes the review history now and then every
// interval, until ctx is done.
func (s *Server) RunReviewHistoryPrune(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s.review.pruneHistory(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pruneHistory deletes the history of the PRs idle for reviewHistoryKeep.
func (s *reviewService) pruneHistory(ctx context.Context) {
	if s.history == nil {
		return
	}
	n, err := s.history.PruneReviewHistory(ctx, time.Now().Add(-reviewHistoryKeep))
	if err != nil {
		s.deps.logger.Warn("review: pruning the history failed", "error", err)
		return
	}
	if n > 0 {
		s.deps.logger.Info("review: pruned the history of idle PRs", "runs", n)
	}
}

// RestoreReviewSessions keeps the review sessions in the history from now
// on, and loads the PR of each one kept before the daemon started, with
// its choices. A session whose channel or PR is gone is dropped; one that
// fails to load otherwise is kept, to load at the next start.
func (s *Server) RestoreReviewSessions(ctx context.Context) {
	rs := s.review
	if rs.history == nil || rs.sessions == nil || rs.client == nil || rs.worktree == nil || rs.deps.store == nil {
		return
	}
	rs.sessions.SetSaver(reviewSessionSaver{history: rs.history, logger: rs.deps.logger})
	saved, err := rs.history.ListReviewSessions(ctx)
	if err != nil {
		rs.deps.logger.Warn("review: listing the kept sessions failed", "error", err)
		return
	}
	for _, row := range saved {
		// Loaded since the daemon started: the user's newer choice wins.
		if rs.sessions.Get(row.ChannelID) != nil {
			continue
		}
		_, err := rs.loadPR(ctx, row.ChannelID, row.PRNumber, &review.Saved{
			ChannelID:     row.ChannelID,
			PRNumber:      row.PRNumber,
			ForkMode:      review.ForkMode(row.ForkMode),
			ForkSessionID: row.ForkSessionID,
			Model:         row.Model,
			Effort:        row.Effort,
			Scope:         review.Scope(row.Scope),
			ReviewedSHA:   row.ReviewedSHA,
			ApprovedSHA:   row.ApprovedSHA,
			Threads:       row.Threads,
		})
		if err == nil {
			continue
		}
		if he, ok := errors.AsType[*reviewHTTPError](err); ok && he.status == http.StatusNotFound {
			rs.sessions.Delete(row.ChannelID)
			continue
		}
		rs.deps.logger.Warn("review: restoring a session failed", "channel_id", row.ChannelID, "pr", row.PRNumber, "error", err)
	}
}

// reviewSessionSaver keeps the review sessions in the history store. Its
// errors are logged: a session not kept only loses its restore.
type reviewSessionSaver struct {
	history ReviewHistoryStore
	logger  *slog.Logger
}

func (sv reviewSessionSaver) SaveSession(saved *review.Saved) {
	err := sv.history.SaveReviewSession(context.Background(), &db.ReviewSession{
		ChannelID:     saved.ChannelID,
		PRNumber:      saved.PRNumber,
		ForkMode:      string(saved.ForkMode),
		ForkSessionID: saved.ForkSessionID,
		Model:         saved.Model,
		Effort:        saved.Effort,
		Scope:         string(saved.Scope),
		ReviewedSHA:   saved.ReviewedSHA,
		ApprovedSHA:   saved.ApprovedSHA,
		Threads:       saved.Threads,
	})
	if err != nil {
		sv.logger.Warn("review: keeping the session failed", "channel_id", saved.ChannelID, "error", err)
	}
}

func (sv reviewSessionSaver) DeleteSession(channelID string) {
	if err := sv.history.DeleteReviewSession(context.Background(), channelID); err != nil {
		sv.logger.Warn("review: forgetting the session failed", "channel_id", channelID, "error", err)
	}
}

// historyKey returns the session's history key, and false when the
// session keeps none.
func (s *reviewService) historyKey(sess *review.Session) (string, int, bool) {
	if s.history == nil || sess == nil || sess.PR == nil || sess.HistoryDir == "" {
		return "", 0, false
	}
	return sess.HistoryDir, sess.PR.Number, true
}

// lastReviewedHead returns the head the last completed run of the
// session's PR reviewed, "" when none did or there's no history.
func (s *reviewService) lastReviewedHead(ctx context.Context, sess *review.Session) string {
	dir, pr, ok := s.historyKey(sess)
	if !ok {
		return ""
	}
	head, err := s.history.LastReviewedHead(ctx, dir, pr)
	if err != nil {
		s.deps.logger.Warn("review: reading the last reviewed head failed", "error", err)
	}
	return head
}

// startHistoryRun records the run starting now as one of the session's
// latest review, a new one when there's none yet, and remembers its id
// for the findings it reports. The history's errors are logged: they
// never fail a run.
func (s *reviewService) startHistoryRun(ctx context.Context, channelID string, sess *review.Session, scope review.ScopeResult) {
	dir, pr, ok := s.historyKey(sess)
	if !ok {
		return
	}
	reviewID := sess.ReviewID
	if reviewID == "" {
		reviewID = s.newReviewID()
		s.sessions.StartReview(channelID, reviewID, "")
	}
	id, err := s.history.CreateReviewRun(ctx, &db.ReviewRun{
		ReviewID:  reviewID,
		DirPath:   dir,
		PRNumber:  pr,
		BaseRef:   sess.PR.BaseRef,
		ChannelID: channelID,
		HeadSHA:   sess.HeadSHA,
		Scope:     string(scope.Scope),
		FromSHA:   scope.FromSHA,
		Model:     sess.Model,
		Effort:    sess.Effort,
		ForkMode:  string(sess.ForkMode),
	})
	if err != nil {
		s.deps.logger.Warn("review: recording the run failed", "channel_id", channelID, "error", err)
		return
	}
	s.sessions.AddReviewRun(channelID, id)
	s.mu.Lock()
	s.runIDs[channelID] = id
	s.mu.Unlock()
}

// historyRunID returns the id of channelID's run in flight, 0 when none.
func (s *reviewService) historyRunID(channelID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runIDs[channelID]
}

// finishHistoryRun records how channelID's run in flight ended.
func (s *reviewService) finishHistoryRun(channelID, status, sessionID, model, errText string) {
	id := s.historyRunID(channelID)
	if id == 0 {
		return
	}
	if err := s.history.FinishReviewRun(context.Background(), id, status, sessionID, model, errText); err != nil {
		s.deps.logger.Warn("review: recording the run's end failed", "channel_id", channelID, "error", err)
	}
}

// saveFinding records an agent comment's current state. GitHub comments
// aren't kept: they are fetched again from the PR.
func (s *reviewService) saveFinding(channelID string, c *review.Comment) {
	if c == nil || c.Source == "github" {
		return
	}
	dir, pr, ok := s.historyKey(s.sessions.Get(channelID))
	if !ok {
		return
	}
	err := s.history.SaveReviewFinding(context.Background(), &db.ReviewFinding{
		DirPath:       dir,
		PRNumber:      pr,
		ID:            c.ID,
		RunID:         c.RunID,
		Path:          c.Path,
		Line:          c.Line,
		Side:          c.Side,
		Category:      c.Category,
		Body:          c.Body,
		Verdict:       c.Verdict,
		VerdictReason: c.VerdictReason,
		Impact:        c.Impact,
		GitHubID:      c.GitHubID,
		URL:           c.URL,
		PushedAt:      c.PushedAt,
		Outcome:       findingOutcome(c),
		Transcripts:   c.Transcripts,
	})
	if err != nil {
		s.deps.logger.Warn("review: recording a finding failed", "channel_id", channelID, "id", c.ID, "error", err)
	}
}

// deleteFinding records an agent comment's deletion for reason.
func (s *reviewService) deleteFinding(channelID string, c *review.Comment, reason, supersededBy string) {
	if c.Source == "github" {
		return
	}
	dir, pr, ok := s.historyKey(s.sessions.Get(channelID))
	if !ok {
		return
	}
	if err := s.history.DeleteReviewFinding(context.Background(), dir, pr, c.ID, reason, supersededBy); err != nil {
		s.deps.logger.Warn("review: recording a finding's deletion failed", "channel_id", channelID, "id", c.ID, "error", err)
	}
}

// findingOutcome is what became of a pushed finding on GitHub.
func findingOutcome(c *review.Comment) string {
	switch {
	case c.Resolved:
		return "resolved"
	case c.Outdated:
		return "outdated"
	}
	return ""
}

// restoreRuns sets on sess the scope of channelID's last run of the PR,
// and the runs of its latest review. Runs of other channels are left out:
// they ran in other sessions. A run recorded before reviews were is a
// review of its own.
func (s *reviewService) restoreRuns(ctx context.Context, channelID string, sess *review.Session) {
	dir, pr, ok := s.historyKey(sess)
	if !ok {
		return
	}
	runs, err := s.history.ListReviewRuns(ctx, dir, pr)
	if err != nil {
		s.deps.logger.Warn("review: restoring the runs failed", "pr", pr, "error", err)
		return
	}
	for _, r := range runs {
		if r.ChannelID != channelID {
			continue
		}
		sess.LastRunScope, sess.LastRunFromSHA = review.Scope(r.Scope), r.FromSHA
		if r.ReviewID == "" || r.ReviewID != sess.ReviewID {
			sess.ReviewRuns = nil
		}
		sess.ReviewID = r.ReviewID
		sess.ReviewRuns = append(sess.ReviewRuns, r.ID)
	}
}

// restoreFindings returns the PR's recorded agent findings as comments.
func (s *reviewService) restoreFindings(ctx context.Context, dir string, pr int) []*review.Comment {
	if s.history == nil || dir == "" {
		return nil
	}
	found, err := s.history.ListReviewFindings(ctx, dir, pr)
	if err != nil {
		s.deps.logger.Warn("review: restoring the findings failed", "pr", pr, "error", err)
		return nil
	}
	out := make([]*review.Comment, 0, len(found))
	for _, f := range found {
		out = append(out, &review.Comment{
			ID:            f.ID,
			Path:          f.Path,
			Line:          f.Line,
			Side:          f.Side,
			Body:          f.Body,
			Category:      f.Category,
			Verdict:       f.Verdict,
			VerdictReason: f.VerdictReason,
			Impact:        f.Impact,
			Pushed:        !f.PushedAt.IsZero(),
			PushedAt:      f.PushedAt,
			URL:           f.URL,
			GitHubID:      f.GitHubID,
			Resolved:      f.Outcome == "resolved",
			Outdated:      f.Outcome == "outdated",
			Transcripts:   f.Transcripts,
			RunID:         f.RunID,
		})
	}
	return out
}
