package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestCIState saves, replaces, gets and lists CI states against a real
// SQLite.
func (s *IntegrationSuite) TestCIState() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	none, err := store.GetCIState(ctx, "ch1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none)

	off := false
	require.NoError(s.T(), store.SaveCIState(ctx, &CIState{ChannelID: "ch2", Watching: true}))
	require.NoError(s.T(), store.SaveCIState(ctx, &CIState{ChannelID: "ch1", Forge: "pr", Number: 4}))
	full := &CIState{
		ChannelID: "ch1", Forge: "mr", Number: 7, AgentSHAs: []string{"a", "b"}, Attempts: 2,
		LastFailedSHA: "b", LastLogHash: "h", AutoFix: &off, MergeArmed: "squash", MergeVia: "loop", ArmedSHA: "b",
		Attention: "failed", Watching: true, InfraRetriedSHA: "a", UpdatedAt: now,
	}
	require.NoError(s.T(), store.SaveCIState(ctx, full))

	got, err := store.GetCIState(ctx, "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), full, got)

	all, err := store.ListCIStates(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*CIState{full, {ChannelID: "ch2", Watching: true, UpdatedAt: now}}, all)
}

func (s *StoreSuite) TestCIStateErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"channel_id", "forge", "number", "agent_shas", "attempts", "last_failed_sha", "last_log_hash", "auto_fix_override", "merge_armed", "merge_via", "armed_sha", "attention", "watching", "infra_retried_sha", "updated_at"}
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"save", false, func() error {
			s.mock.ExpectExec(`INSERT INTO ci_state`).WillReturnError(boom)
			return s.store.SaveCIState(ctx, &CIState{})
		}},
		{"get", false, func() error {
			s.mock.ExpectQuery(`FROM ci_state WHERE`).WillReturnError(boom)
			_, err := s.store.GetCIState(ctx, "ch")
			return err
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM ci_state ORDER`).WillReturnError(boom)
			_, err := s.store.ListCIStates(ctx)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM ci_state ORDER`).WillReturnRows(sqlmock.NewRows(cols).AddRow("ch", "", 0, "", 0, "", "", nil, "", "", "", "", false, "", "not a time"))
			_, err := s.store.ListCIStates(ctx)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM ci_state ORDER`).WillReturnRows(sqlmock.NewRows(cols).AddRow("ch", "", 0, "", 0, "", "", nil, "", "", "", "", false, "", time.Now()).RowError(0, boom))
			_, err := s.store.ListCIStates(ctx)
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
