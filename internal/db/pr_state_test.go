package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/forge"
)

// TestPRState saves, replaces, gets and lists CI states against a real
// SQLite.
func (s *IntegrationSuite) TestPRState() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	none, err := store.GetPRState(ctx, "ch1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none)

	off := false
	require.NoError(s.T(), store.SavePRState(ctx, &PRState{ChannelID: "ch2", Watching: true}))
	require.NoError(s.T(), store.SavePRState(ctx, &PRState{ChannelID: "ch1", Forge: "pr", Number: 4}))
	full := &PRState{
		ChannelID: "ch1", Forge: "mr", Number: 7, AgentSHAs: []string{"a", "b"}, Attempts: 2,
		LastFailedSHA: "b", LastLogHash: "h", AutoFix: &off, MergeArmed: "squash", MergeVia: "loop", ArmedSHA: "b",
		Attention: "failed", Watching: true, InfraRetriedSHA: "a", Reason: "not_agent_commit",
		Failed: []forge.FailedCheck{{ID: "9", Name: "lint", Group: "test", URL: "https://example.com/9", RunID: "8"}}, Open: true, Threads: []string{"T1", "T2"}, UpdatedAt: now,
	}
	require.NoError(s.T(), store.SavePRState(ctx, full))

	got, err := store.GetPRState(ctx, "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), full, got)

	all, err := store.ListPRStates(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*PRState{full, {ChannelID: "ch2", Watching: true, UpdatedAt: now}}, all)
}

func (s *StoreSuite) TestPRStateErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"channel_id", "forge", "number", "agent_shas", "attempts", "last_failed_sha", "last_log_hash", "auto_fix_override", "merge_armed", "merge_via", "armed_sha", "attention", "watching", "infra_retried_sha", "reason", "failed", "open", "threads", "updated_at"}
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"save", false, func() error {
			s.mock.ExpectExec(`INSERT INTO pr_state`).WillReturnError(boom)
			return s.store.SavePRState(ctx, &PRState{})
		}},
		{"get", false, func() error {
			s.mock.ExpectQuery(`FROM pr_state WHERE`).WillReturnError(boom)
			_, err := s.store.GetPRState(ctx, "ch")
			return err
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM pr_state ORDER`).WillReturnError(boom)
			_, err := s.store.ListPRStates(ctx)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM pr_state ORDER`).WillReturnRows(sqlmock.NewRows(cols).AddRow("ch", "", 0, "", 0, "", "", nil, "", "", "", "", false, "", "", "", false, "", "not a time"))
			_, err := s.store.ListPRStates(ctx)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM pr_state ORDER`).WillReturnRows(sqlmock.NewRows(cols).AddRow("ch", "", 0, "", 0, "", "", nil, "", "", "", "", false, "", "", "", false, "", time.Now()).RowError(0, boom))
			_, err := s.store.ListPRStates(ctx)
			return err
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			err := tc.run()
			if tc.scan {
				require.Error(s.T(), err)
				return
			}
			require.ErrorIs(s.T(), err, boom)
		})
	}
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

// TestPRStateFailedUnparsable reads a stored failed list that isn't JSON as
// no failed checks.
func (s *StoreSuite) TestPRStateFailedUnparsable() {
	cols := []string{"channel_id", "forge", "number", "agent_shas", "attempts", "last_failed_sha", "last_log_hash", "auto_fix_override", "merge_armed", "merge_via", "armed_sha", "attention", "watching", "infra_retried_sha", "reason", "failed", "open", "threads", "updated_at"}
	s.mock.ExpectQuery(`FROM pr_state WHERE`).WillReturnRows(sqlmock.NewRows(cols).AddRow("ch", "", 0, "", 0, "", "", nil, "", "", "", "failed", false, "", "auto_fix_off", "{", false, "", time.Now()))
	st, err := s.store.GetPRState(context.Background(), "ch")
	require.NoError(s.T(), err)
	require.Nil(s.T(), st.Failed)
	require.Equal(s.T(), "auto_fix_off", st.Reason)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
