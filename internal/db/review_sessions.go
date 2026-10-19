package db

import "context"

// ReviewSession is the part of a channel's review session kept across
// daemon restarts: the PR it is on, the user's choices for its runs, the
// commits it was last reviewed and approved at, and the verification
// step's GitHub thread assessments as JSON. The rest (worktree, diff,
// comments, runs) is rebuilt by loading the PR again.
type ReviewSession struct {
	ChannelID     string
	PRNumber      int
	ForkMode      string
	ForkSessionID string
	Model         string
	Effort        string
	Scope         string
	ReviewedSHA   string
	ApprovedSHA   string
	Threads       string
}

// SaveReviewSession stores rs, replacing the channel's earlier one.
func (s *SQLiteStore) SaveReviewSession(ctx context.Context, rs *ReviewSession) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO review_sessions (channel_id, pr_number, fork_mode, fork_session_id, model, effort, scope, reviewed_sha, approved_sha, threads, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rs.ChannelID, rs.PRNumber, rs.ForkMode, rs.ForkSessionID, rs.Model, rs.Effort, rs.Scope, rs.ReviewedSHA, rs.ApprovedSHA, rs.Threads, s.nowFunc(),
	)
	return err
}

// DeleteReviewSession forgets channelID's review session.
func (s *SQLiteStore) DeleteReviewSession(ctx context.Context, channelID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM review_sessions WHERE channel_id = ?`, channelID)
	return err
}

// ListReviewSessions returns every saved review session, oldest change
// first.
func (s *SQLiteStore) ListReviewSessions(ctx context.Context) ([]*ReviewSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, pr_number, fork_mode, fork_session_id, model, effort, scope, reviewed_sha, approved_sha, threads
		 FROM review_sessions ORDER BY updated_at, channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReviewSession
	for rows.Next() {
		rs := &ReviewSession{}
		if err := rows.Scan(&rs.ChannelID, &rs.PRNumber, &rs.ForkMode, &rs.ForkSessionID, &rs.Model, &rs.Effort, &rs.Scope, &rs.ReviewedSHA, &rs.ApprovedSHA, &rs.Threads); err != nil {
			return nil, err
		}
		out = append(out, rs)
	}
	return out, rows.Err()
}
