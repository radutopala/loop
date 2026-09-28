// learn.go holds SQLiteStore methods for learn passes: the per-channel
// switch, the hidden learn thread and the proposals it files.
package db

import (
	"context"
	"database/sql"
	"errors"
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

// GetLearnChannel returns the hidden learn thread under parentID, or nil when
// it has none yet.
func (s *SQLiteStore) GetLearnChannel(ctx context.Context, parentID string) (*Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+channelColumns+`
		 FROM channels WHERE parent_id = ? AND kind = ? ORDER BY id LIMIT 1`,
		parentID, ChannelKindLearn,
	)
	ch, err := scanChannel(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return ch, err
}

// ErrLearnParentGone is returned by InsertLearnChannel when the learn
// thread's parent no longer exists.
var ErrLearnParentGone = errors.New("learn thread parent is gone")

// InsertLearnChannel creates a hidden learn thread. Kind is always
// ChannelKindLearn whatever ch says. The insert and the check that its
// parent still exists are one statement, so a parent deleted meanwhile
// can't be left with an orphan learn thread; that's ErrLearnParentGone.
func (s *SQLiteStore) InsertLearnChannel(ctx context.Context, ch *Channel) error {
	now := s.nowFunc()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (channel_id, guild_id, name, dir_path, parent_id, platform, active, kind, created_at, updated_at)
		 SELECT ?, ?, ?, ?, ?, ?, 1, ?, ?, ?
		 WHERE EXISTS (SELECT 1 FROM channels WHERE channel_id = ?)`,
		ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, ch.ParentID, ch.Platform, ChannelKindLearn, now, now, ch.ParentID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLearnParentGone
	}
	return nil
}

// InsertLearnProposals stores a learn pass's proposals as pending, filling in
// their ids, status and times.
func (s *SQLiteStore) InsertLearnProposals(ctx context.Context, proposals []*LearnProposal) error {
	now := s.nowFunc()
	return s.withTx(ctx, func(tx *sql.Tx) error {
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
}

const learnProposalColumns = `id, channel_id, learn_channel_id, kind, title, rationale, payload, status, error, created_at, updated_at`

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
		&p.Payload, &p.Status, &p.Error, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return p, nil
}
