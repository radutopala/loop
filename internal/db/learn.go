// learn.go holds SQLiteStore methods for learn passes: the per-channel
// switch and the proposals the hidden learn thread files.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// UpdateChannelLearnOverride sets a channel's learn switch: LearnOn, LearnOff
// or empty to inherit the config.
func (s *SQLiteStore) UpdateChannelLearnOverride(ctx context.Context, channelID, value string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET learn_override = ?, updated_at = ? WHERE channel_id = ?`,
		value, s.nowFunc(), channelID,
	)
	return err
}

// LearnWithdrawError is returned by FileLearnProposals when a proposal to
// withdraw isn't an open (pending or failed) one of the channel. Status is
// its status, or empty when the channel has no proposal with that id.
type LearnWithdrawError struct {
	ID     int64
	Status string
}

func (e *LearnWithdrawError) Error() string {
	if e.Status == "" {
		return fmt.Sprintf("proposal %d not found in this channel", e.ID)
	}
	return fmt.Sprintf("proposal %d is %s; only pending or failed proposals can be withdrawn", e.ID, e.Status)
}

// FileLearnProposals withdraws a learn pass's stale proposals of channelID
// and stores its new ones as pending, filling in their ids, status and
// times. It returns the withdrawn proposals. It's one transaction: when a
// proposal to withdraw isn't open (a *LearnWithdrawError), nothing changes.
// Like ClaimLearnProposal, withdrawing takes a pending or failed proposal
// only, so one the user is applying stays theirs, and one withdrawn can't
// be claimed.
func (s *SQLiteStore) FileLearnProposals(ctx context.Context, channelID string, proposals []*LearnProposal, withdraw []LearnWithdrawal) ([]*LearnProposal, error) {
	now := s.nowFunc()
	withdrawn := make([]*LearnProposal, 0, len(withdraw))
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		for _, w := range withdraw {
			p, err := withdrawLearnProposal(ctx, tx, channelID, w, now)
			if err != nil {
				return err
			}
			withdrawn = append(withdrawn, p)
		}
		for _, p := range proposals {
			res, err := tx.ExecContext(ctx,
				`INSERT INTO learn_proposals (channel_id, learn_channel_id, kind, title, rationale, payload, status, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				p.ChannelID, p.LearnChannelID, p.Kind, p.Title, p.Rationale, p.Payload, LearnPending, now, now,
			)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			p.ID, p.Status, p.CreatedAt, p.UpdatedAt = id, LearnPending, now, now
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return withdrawn, nil
}

// withdrawLearnProposal moves one open proposal of channelID to withdrawn
// and returns it.
func withdrawLearnProposal(ctx context.Context, tx *sql.Tx, channelID string, w LearnWithdrawal, now time.Time) (*LearnProposal, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE learn_proposals SET status = ?, withdrawn_reason = ?, error = '', updated_at = ?
		 WHERE id = ? AND channel_id = ? AND status IN (?, ?)`,
		LearnWithdrawn, w.Reason, now, w.ID, channelID, LearnPending, LearnFailed,
	)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		werr := &LearnWithdrawError{ID: w.ID}
		err := tx.QueryRowContext(ctx,
			`SELECT status FROM learn_proposals WHERE id = ? AND channel_id = ?`, w.ID, channelID,
		).Scan(&werr.Status)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		return nil, werr
	}
	return scanLearnProposal(tx.QueryRowContext(ctx,
		`SELECT `+learnProposalColumns+` FROM learn_proposals WHERE id = ?`, w.ID,
	))
}

const learnProposalColumns = `id, channel_id, learn_channel_id, kind, title, rationale, payload, status, error, withdrawn_reason, created_at, updated_at`

// ListLearnProposals returns a channel's proposals, newest first.
func (s *SQLiteStore) ListLearnProposals(ctx context.Context, channelID string) ([]*LearnProposal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+learnProposalColumns+` FROM learn_proposals WHERE channel_id = ? ORDER BY id DESC`,
		channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LearnProposal
	for rows.Next() {
		p, err := scanLearnProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetLearnProposal returns one proposal, or nil when there's no such id.
func (s *SQLiteStore) GetLearnProposal(ctx context.Context, id int64) (*LearnProposal, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+learnProposalColumns+` FROM learn_proposals WHERE id = ?`, id,
	)
	p, err := scanLearnProposal(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// learnApplyStale is how long a proposal may sit in applying before it can
// be claimed again. An apply takes well under a second; one still applying
// after this lost its outcome (the status save failed, or Loop stopped
// mid-apply) and would otherwise be stuck there for good.
const learnApplyStale = time.Minute

// ClaimLearnProposal moves a pending or failed proposal, or one stuck in
// applying for over learnApplyStale, to applying. It reports false when the
// proposal is in any other state, so only one caller ever applies it.
func (s *SQLiteStore) ClaimLearnProposal(ctx context.Context, id int64) (bool, error) {
	now := s.nowFunc()
	res, err := s.db.ExecContext(ctx,
		`UPDATE learn_proposals SET status = ?, error = '', updated_at = ?
		 WHERE id = ? AND (status IN (?, ?) OR (status = ? AND updated_at < ?))`,
		LearnApplying, now, id, LearnPending, LearnFailed, LearnApplying, now.Add(-learnApplyStale),
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SetLearnProposalStatus sets a proposal's status and error text.
func (s *SQLiteStore) SetLearnProposalStatus(ctx context.Context, id int64, status, errText string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE learn_proposals SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
		status, errText, s.nowFunc(), id,
	)
	return err
}

func scanLearnProposal(scanner rowScanner) (*LearnProposal, error) {
	p := &LearnProposal{}
	if err := scanner.Scan(&p.ID, &p.ChannelID, &p.LearnChannelID, &p.Kind, &p.Title, &p.Rationale,
		&p.Payload, &p.Status, &p.Error, &p.WithdrawnReason, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return p, nil
}
