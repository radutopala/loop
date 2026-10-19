package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestReviewSessions saves, replaces, lists and deletes review sessions
// against a real SQLite.
func (s *IntegrationSuite) TestReviewSessions() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	got, err := store.ListReviewSessions(ctx)
	require.NoError(s.T(), err)
	require.Empty(s.T(), got)

	full := &ReviewSession{ChannelID: "ch1", PRNumber: 7, ForkMode: "custom", ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: "incremental",
		ReviewedSHA: "abc", ApprovedSHA: "abc", Threads: `{"9":{"status":"addressed"}}`}
	require.NoError(s.T(), store.SaveReviewSession(ctx, &ReviewSession{ChannelID: "ch1", PRNumber: 6}))
	now = now.Add(time.Minute)
	require.NoError(s.T(), store.SaveReviewSession(ctx, &ReviewSession{ChannelID: "ch2", PRNumber: 8}))
	now = now.Add(time.Minute)
	require.NoError(s.T(), store.SaveReviewSession(ctx, full))

	got, err = store.ListReviewSessions(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*ReviewSession{{ChannelID: "ch2", PRNumber: 8}, full}, got)

	require.NoError(s.T(), store.DeleteReviewSession(ctx, "ch2"))
	got, err = store.ListReviewSessions(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*ReviewSession{full}, got)
}

func (s *StoreSuite) TestReviewSessionsErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"channel_id", "pr_number", "fork_mode", "fork_session_id", "model", "effort", "scope", "reviewed_sha", "approved_sha", "threads"}
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"save", false, func() error {
			s.mock.ExpectExec(`INSERT OR REPLACE INTO review_sessions`).WillReturnError(boom)
			return s.store.SaveReviewSession(ctx, &ReviewSession{})
		}},
		{"delete", false, func() error {
			s.mock.ExpectExec(`DELETE FROM review_sessions`).WillReturnError(boom)
			return s.store.DeleteReviewSession(ctx, "ch1")
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM review_sessions`).WillReturnError(boom)
			_, err := s.store.ListReviewSessions(ctx)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM review_sessions`).WillReturnRows(sqlmock.NewRows(cols).AddRow("ch1", "not a number", "", "", "", "", "", "", "", ""))
			_, err := s.store.ListReviewSessions(ctx)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM review_sessions`).WillReturnRows(sqlmock.NewRows(cols).AddRow("ch1", 1, "", "", "", "", "", "", "", "").RowError(0, boom))
			_, err := s.store.ListReviewSessions(ctx)
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
