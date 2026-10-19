package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestReviewHistory records runs and findings against a real SQLite, then
// reads back the last reviewed head, the live findings and the stats, and
// prunes the stale PRs.
func (s *IntegrationSuite) TestReviewHistory() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }

	head, err := store.LastReviewedHead(ctx, "/repo", 7)
	require.NoError(s.T(), err)
	require.Empty(s.T(), head)

	run1, err := store.CreateReviewRun(ctx, &ReviewRun{ReviewID: "r1", DirPath: "/repo", PRNumber: 7, BaseRef: "main", ChannelID: "ch", HeadSHA: "aaa", Scope: "full", Effort: "high", ForkMode: "current"})
	require.NoError(s.T(), err)
	now = now.Add(10 * time.Minute)
	require.NoError(s.T(), store.FinishReviewRun(ctx, run1, ReviewRunCompleted, "sess-1", "opus", ""))
	run2, err := store.CreateReviewRun(ctx, &ReviewRun{DirPath: "/repo", PRNumber: 7, HeadSHA: "bbb", Scope: "incremental", FromSHA: "aaa", Model: "sonnet", Effort: "high"})
	require.NoError(s.T(), err)
	now = now.Add(20 * time.Minute)
	require.NoError(s.T(), store.FinishReviewRun(ctx, run2, ReviewRunFailed, "", "", "boom"))
	_, err = store.CreateReviewRun(ctx, &ReviewRun{DirPath: "/repo", PRNumber: 8, HeadSHA: "ccc", Scope: "full"})
	require.NoError(s.T(), err)

	head, err = store.LastReviewedHead(ctx, "/repo", 7)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "aaa", head, "a failed run reviewed nothing")

	runs, err := store.ListReviewRuns(ctx, "/repo", 7)
	require.NoError(s.T(), err)
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	require.Equal(s.T(), []*ReviewRun{
		{ID: run1, ReviewID: "r1", DirPath: "/repo", PRNumber: 7, BaseRef: "main", ChannelID: "ch", HeadSHA: "aaa", Scope: "full", Model: "opus", Effort: "high", ForkMode: "current",
			SessionID: "sess-1", Status: ReviewRunCompleted, StartedAt: start, FinishedAt: start.Add(10 * time.Minute)},
		{ID: run2, DirPath: "/repo", PRNumber: 7, HeadSHA: "bbb", Scope: "incremental", FromSHA: "aaa", Model: "sonnet", Effort: "high",
			Status: ReviewRunFailed, Error: "boom", StartedAt: start.Add(10 * time.Minute), FinishedAt: now},
	}, runs)

	pushed := now.Add(-time.Minute)
	require.NoError(s.T(), store.SaveReviewFinding(ctx, &ReviewFinding{DirPath: "/repo", PRNumber: 7, ID: "f1", RunID: run1, Path: "a.go", Line: 3, Body: "bug", Category: "correctness", Verdict: "real", Transcripts: []string{"/t/run1.jsonl"}}))
	require.NoError(s.T(), store.SaveReviewFinding(ctx, &ReviewFinding{DirPath: "/repo", PRNumber: 7, ID: "f2", RunID: run1, Path: "b.go", Line: 4, Body: "nit", Category: "style", Verdict: "false_positive"}))
	require.NoError(s.T(), store.SaveReviewFinding(ctx, &ReviewFinding{DirPath: "/repo", PRNumber: 7, ID: "f3", RunID: run2, Path: "a.go", Line: 9, Body: "same bug"}))
	require.NoError(s.T(), store.SaveReviewFinding(ctx, &ReviewFinding{DirPath: "/repo", PRNumber: 7, ID: "f4", Path: "c.go", Line: 1, Body: "other"}))
	// Saved again, by another run: the first run keeps it; it is pushed and resolved now.
	require.NoError(s.T(), store.SaveReviewFinding(ctx, &ReviewFinding{DirPath: "/repo", PRNumber: 7, ID: "f1", RunID: run2, Path: "a.go", Line: 4, Body: "bug", Category: "correctness", Verdict: "real", Impact: "blocking", GitHubID: 99, URL: "u", PushedAt: pushed, Outcome: "resolved", Transcripts: []string{"/t/run1.jsonl", "/t/dedup.jsonl"}}))
	require.NoError(s.T(), store.DeleteReviewFinding(ctx, "/repo", 7, "f3", ReviewFindingDedupFolded, "f1"))
	require.NoError(s.T(), store.DeleteReviewFinding(ctx, "/repo", 7, "f4", ReviewFindingUserDeleted, ""))

	got, err := store.ListReviewFindings(ctx, "/repo", 7)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []*ReviewFinding{
		{DirPath: "/repo", PRNumber: 7, ID: "f1", RunID: run1, Path: "a.go", Line: 4, Body: "bug", Category: "correctness", Verdict: "real", Impact: "blocking", GitHubID: 99, URL: "u", PushedAt: pushed, Outcome: "resolved", Transcripts: []string{"/t/run1.jsonl", "/t/dedup.jsonl"}, CreatedAt: now, UpdatedAt: now},
		{DirPath: "/repo", PRNumber: 7, ID: "f2", RunID: run1, Path: "b.go", Line: 4, Body: "nit", Category: "style", Verdict: "false_positive", CreatedAt: now, UpdatedAt: now},
	}, got)

	stats, err := store.ReviewStats(ctx, ReviewStatsFilter{DirPath: "/repo"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), ReviewRunStats{Total: 3, Completed: 1, Failed: 1, Running: 1, Full: 2, Incremental: 1, PRs: 2, MedianMs: 600000, P90Ms: 600000}, stats.Runs)
	require.Equal(s.T(), ReviewFindingStats{Added: 4, Folded: 1, Deleted: 1, Pushed: 1, Real: 1, FalsePositive: 1, Unjudged: 2, Blocking: 1, Resolved: 1, FalsePositiveRate: 0.5}, stats.Findings)
	require.Equal(s.T(), []ReviewStatsGroup{
		{Key: "opus", Runs: 1, ReviewFindingStats: ReviewFindingStats{Added: 2, Pushed: 1, Real: 1, FalsePositive: 1, Blocking: 1, Resolved: 1, FalsePositiveRate: 0.5}},
		{Key: "", Runs: 1, ReviewFindingStats: ReviewFindingStats{Added: 1, Deleted: 1, Unjudged: 1}},
		{Key: "sonnet", Runs: 1, ReviewFindingStats: ReviewFindingStats{Added: 1, Folded: 1, Unjudged: 1}},
	}, stats.ByModel)

	one, err := store.ReviewStats(ctx, ReviewStatsFilter{DirPath: "/repo", PRNumber: 8})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, one.Runs.Total)
	require.Zero(s.T(), one.Findings.Added)

	// PR 8 has a run started later than the cutoff; PR 7's last activity is before it.
	n, err := store.PruneReviewHistory(ctx, now.Add(-time.Second))
	require.NoError(s.T(), err)
	require.Zero(s.T(), n)
	n, err = store.PruneReviewHistory(ctx, now.Add(time.Second))
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 3, n)
	got, err = store.ListReviewFindings(ctx, "/repo", 7)
	require.NoError(s.T(), err)
	require.Empty(s.T(), got)
}

var reviewRunCols = []string{"id", "review_id", "base_ref", "channel_id", "head_sha", "scope", "from_sha", "model", "effort", "fork_mode", "session_id", "status", "error", "started_at", "finished_at"}

func (s *StoreSuite) TestReviewHistoryErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	findingCols := []string{"dir_path", "pr_number", "id", "run_id", "path", "line", "side", "category", "body", "verdict", "verdict_reason", "github_id", "url", "pushed_at", "outcome", "transcripts", "deleted_reason", "superseded_by", "created_at", "updated_at"}
	runCols := []string{"pr_number", "scope", "model", "effort", "status", "started_at", "finished_at"}
	statCols := []string{"model", "effort", "scope", "category", "verdict", "deleted_reason", "pushed", "outcome"}
	tests := []struct {
		name string
		scan bool
		run  func() error
	}{
		{"create", false, func() error {
			s.mock.ExpectExec(`INSERT INTO review_runs`).WillReturnError(boom)
			_, err := s.store.CreateReviewRun(ctx, &ReviewRun{})
			return err
		}},
		{"finish", false, func() error {
			s.mock.ExpectExec(`UPDATE review_runs`).WillReturnError(boom)
			return s.store.FinishReviewRun(ctx, 1, ReviewRunCompleted, "", "", "")
		}},
		{"last head", false, func() error {
			s.mock.ExpectQuery(`SELECT head_sha FROM review_runs`).WillReturnError(boom)
			_, err := s.store.LastReviewedHead(ctx, "/repo", 1)
			return err
		}},
		{"runs query", false, func() error {
			s.mock.ExpectQuery(`FROM review_runs WHERE dir_path`).WillReturnError(boom)
			_, err := s.store.ListReviewRuns(ctx, "/repo", 1)
			return err
		}},
		{"runs scan", true, func() error {
			s.mock.ExpectQuery(`FROM review_runs WHERE dir_path`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			_, err := s.store.ListReviewRuns(ctx, "/repo", 1)
			return err
		}},
		{"runs rows", false, func() error {
			s.mock.ExpectQuery(`FROM review_runs WHERE dir_path`).WillReturnRows(sqlmock.NewRows(reviewRunCols).
				AddRow(1, "", "", "", "", "", "", "", "", "", "", "", "", time.Now(), nil).RowError(0, boom))
			_, err := s.store.ListReviewRuns(ctx, "/repo", 1)
			return err
		}},
		{"save", false, func() error {
			s.mock.ExpectExec(`INSERT INTO review_findings`).WillReturnError(boom)
			return s.store.SaveReviewFinding(ctx, &ReviewFinding{})
		}},
		{"delete", false, func() error {
			s.mock.ExpectExec(`UPDATE review_findings`).WillReturnError(boom)
			return s.store.DeleteReviewFinding(ctx, "/repo", 1, "x", ReviewFindingUserDeleted, "")
		}},
		{"list query", false, func() error {
			s.mock.ExpectQuery(`FROM review_findings`).WillReturnError(boom)
			_, err := s.store.ListReviewFindings(ctx, "/repo", 1)
			return err
		}},
		{"list scan", true, func() error {
			s.mock.ExpectQuery(`FROM review_findings`).WillReturnRows(sqlmock.NewRows(findingCols).
				AddRow("/repo", 1, "x", nil, "", 0, "", "", "", "", "", 0, "", nil, "", "", "", "", "not a time", time.Now()))
			_, err := s.store.ListReviewFindings(ctx, "/repo", 1)
			return err
		}},
		{"list rows", false, func() error {
			s.mock.ExpectQuery(`FROM review_findings`).WillReturnRows(sqlmock.NewRows(findingCols).
				AddRow("/repo", 1, "x", nil, "", 0, "", "", "", "", "", 0, "", nil, "", "", "", "", time.Now(), time.Now()).RowError(0, boom))
			_, err := s.store.ListReviewFindings(ctx, "/repo", 1)
			return err
		}},
		{"prune findings", false, func() error {
			s.mock.ExpectExec(`DELETE FROM review_findings`).WillReturnError(boom)
			_, err := s.store.PruneReviewHistory(ctx, time.Now())
			return err
		}},
		{"prune runs", false, func() error {
			s.mock.ExpectExec(`DELETE FROM review_findings`).WillReturnResult(sqlmock.NewResult(0, 0))
			s.mock.ExpectExec(`DELETE FROM review_runs`).WillReturnError(boom)
			_, err := s.store.PruneReviewHistory(ctx, time.Now())
			return err
		}},
		{"stats runs query", false, func() error {
			s.mock.ExpectQuery(`FROM review_runs`).WillReturnError(boom)
			_, err := s.store.ReviewStats(ctx, ReviewStatsFilter{})
			return err
		}},
		{"stats runs scan", true, func() error {
			s.mock.ExpectQuery(`FROM review_runs`).WillReturnRows(sqlmock.NewRows(runCols).AddRow(1, "", "", "", "", "not a time", nil))
			_, err := s.store.ReviewStats(ctx, ReviewStatsFilter{})
			return err
		}},
		{"stats runs rows", false, func() error {
			s.mock.ExpectQuery(`FROM review_runs`).WillReturnRows(sqlmock.NewRows(runCols).AddRow(1, "", "", "", "", time.Now(), nil).RowError(0, boom))
			_, err := s.store.ReviewStats(ctx, ReviewStatsFilter{})
			return err
		}},
		{"stats findings query", false, func() error {
			s.mock.ExpectQuery(`FROM review_runs`).WillReturnRows(sqlmock.NewRows(runCols))
			s.mock.ExpectQuery(`FROM review_findings`).WillReturnError(boom)
			_, err := s.store.ReviewStats(ctx, ReviewStatsFilter{})
			return err
		}},
		{"stats findings scan", true, func() error {
			s.mock.ExpectQuery(`FROM review_runs`).WillReturnRows(sqlmock.NewRows(runCols))
			s.mock.ExpectQuery(`FROM review_findings`).WillReturnRows(sqlmock.NewRows(statCols).AddRow("", "", "", "", "", "", "not a bool", ""))
			_, err := s.store.ReviewStats(ctx, ReviewStatsFilter{})
			return err
		}},
		{"stats findings rows", false, func() error {
			s.mock.ExpectQuery(`FROM review_runs`).WillReturnRows(sqlmock.NewRows(runCols))
			s.mock.ExpectQuery(`FROM review_findings`).WillReturnRows(sqlmock.NewRows(statCols).AddRow("", "", "", "", "", "", false, "").RowError(0, boom))
			_, err := s.store.ReviewStats(ctx, ReviewStatsFilter{})
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

func (s *StoreSuite) TestCreateReviewRunLastInsertIDError() {
	boom := errors.New("boom")
	s.mock.ExpectExec(`INSERT INTO review_runs`).WillReturnResult(sqlmock.NewErrorResult(boom))
	_, err := s.store.CreateReviewRun(context.Background(), &ReviewRun{})
	require.ErrorIs(s.T(), err, boom)
}

func (s *StoreSuite) TestAggregateReviewStats() {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	runs := []*ReviewRun{
		{PRNumber: 1, Status: ReviewRunCompleted, Scope: "full", StartedAt: t0, FinishedAt: t0.Add(time.Second)},
		{PRNumber: 1, Status: ReviewRunCompleted, Scope: "incremental", StartedAt: t0, FinishedAt: t0.Add(3 * time.Second)},
		{PRNumber: 1, Status: ReviewRunCompleted, Scope: "incremental", StartedAt: t0},
		{PRNumber: 2, Status: ReviewRunCancelled, Scope: "full", StartedAt: t0},
	}
	findings := []*reviewStatsFinding{
		{scope: "incremental", category: "b", verdict: "already_fixed", outcome: "outdated"},
		{scope: "full", category: "a", verdict: "real", impact: "nice_to_have"},
		{scope: "incremental", category: "c"},
	}
	st := aggregateReviewStats(runs, findings)
	require.Equal(s.T(), ReviewRunStats{Total: 4, Completed: 3, Cancelled: 1, Full: 2, Incremental: 2, PRs: 2, MedianMs: 1000, P90Ms: 3000}, st.Runs)
	require.Equal(s.T(), ReviewFindingStats{Added: 3, Real: 1, AlreadyFixed: 1, Unjudged: 1, NiceToHave: 1, Outdated: 1}, st.Findings)
	require.Equal(s.T(), []string{"a", "b", "c"}, []string{st.ByCategory[0].Key, st.ByCategory[1].Key, st.ByCategory[2].Key}, "ties go by key")
	require.Equal(s.T(), "incremental", st.ByScope[0].Key, "ties on runs go to the one with more findings")

	empty := aggregateReviewStats(nil, nil)
	require.Zero(s.T(), empty.Runs.MedianMs)
	require.Empty(s.T(), empty.ByModel)
}
