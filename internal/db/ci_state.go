package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/forge"
)

// CIState is what Loop knows of a thread's PR/MR CI. Forge and Number name
// the PR/MR it last saw; AgentSHAs are the commits the thread's agent made,
// recorded before a PR exists too. Attempts counts the fixes sent for the
// current failure streak, LastFailedSHA and LastLogHash the failure the last
// fix was for. AutoFix overrides the config's auto_fix when set. MergeArmed
// is the merge method the owner armed, MergeVia whether the forge ("forge")
// or Loop ("loop") merges and ArmedSHA the head it was armed on. Attention
// is "", "failed" or "fixing"; Reason says why, and Failed are the failed
// checks of the head that failed.
type CIState struct {
	ChannelID       string
	Forge           string
	Number          int
	AgentSHAs       []string
	Attempts        int
	LastFailedSHA   string
	LastLogHash     string
	AutoFix         *bool
	MergeArmed      string
	MergeVia        string
	ArmedSHA        string
	Attention       string
	Watching        bool
	InfraRetriedSHA string
	Reason          string
	Failed          []forge.FailedCheck
	UpdatedAt       time.Time
}

const ciStateColumns = `channel_id, forge, number, agent_shas, attempts, last_failed_sha, last_log_hash, auto_fix_override, merge_armed, merge_via, armed_sha, attention, watching, infra_retried_sha, reason, failed, updated_at`

// SaveCIState stores st, replacing the channel's row.
func (s *SQLiteStore) SaveCIState(ctx context.Context, st *CIState) error {
	var autoFix sql.NullBool
	if st.AutoFix != nil {
		autoFix = sql.NullBool{Bool: *st.AutoFix, Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ci_state (`+ciStateColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
		   forge = excluded.forge, number = excluded.number, agent_shas = excluded.agent_shas,
		   attempts = excluded.attempts, last_failed_sha = excluded.last_failed_sha,
		   last_log_hash = excluded.last_log_hash, auto_fix_override = excluded.auto_fix_override,
		   merge_armed = excluded.merge_armed, merge_via = excluded.merge_via, armed_sha = excluded.armed_sha,
		   attention = excluded.attention, watching = excluded.watching,
		   infra_retried_sha = excluded.infra_retried_sha, reason = excluded.reason,
		   failed = excluded.failed, updated_at = excluded.updated_at`,
		st.ChannelID, st.Forge, st.Number, strings.Join(st.AgentSHAs, "\n"), st.Attempts,
		st.LastFailedSHA, st.LastLogHash, autoFix, st.MergeArmed, st.MergeVia, st.ArmedSHA,
		st.Attention, st.Watching, st.InfraRetriedSHA, st.Reason, failedJSON(st.Failed), s.nowFunc(),
	)
	return err
}

// GetCIState returns the channel's CI state, nil when it has none.
func (s *SQLiteStore) GetCIState(ctx context.Context, channelID string) (*CIState, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+ciStateColumns+` FROM ci_state WHERE channel_id = ?`, channelID)
	st, err := scanCIState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return st, err
}

// ListCIStates returns every channel's CI state, by channel.
func (s *SQLiteStore) ListCIStates(ctx context.Context) ([]*CIState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+ciStateColumns+` FROM ci_state ORDER BY channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CIState
	for rows.Next() {
		st, err := scanCIState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func scanCIState(r interface{ Scan(...any) error }) (*CIState, error) {
	var st CIState
	var shas, failed string
	var autoFix sql.NullBool
	if err := r.Scan(&st.ChannelID, &st.Forge, &st.Number, &shas, &st.Attempts, &st.LastFailedSHA,
		&st.LastLogHash, &autoFix, &st.MergeArmed, &st.MergeVia, &st.ArmedSHA, &st.Attention, &st.Watching,
		&st.InfraRetriedSHA, &st.Reason, &failed, &st.UpdatedAt); err != nil {
		return nil, err
	}
	if failed != "" {
		// A row that doesn't parse reads as no failed checks: the next poll
		// lists them again.
		_ = json.Unmarshal([]byte(failed), &st.Failed)
	}
	if shas != "" {
		st.AgentSHAs = strings.Split(shas, "\n")
	}
	if autoFix.Valid {
		st.AutoFix = &autoFix.Bool
	}
	return &st, nil
}

// failedJSON is the failed checks as stored, "" for none.
func failedJSON(fcs []forge.FailedCheck) string {
	if len(fcs) == 0 {
		return ""
	}
	b, _ := json.Marshal(fcs) // a slice of plain structs always marshals
	return string(b)
}
