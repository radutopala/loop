package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
)

// Review run statuses. A run is running until its agent is done; completed
// means the agent reviewed HeadSHA, so it is the head the next run's
// incremental scope starts from.
const (
	ReviewRunRunning   = "running"
	ReviewRunCompleted = "completed"
	ReviewRunFailed    = "failed"
	ReviewRunCancelled = "cancelled"
)

// Why a review finding was deleted.
const (
	ReviewFindingUserDeleted = "user_deleted"
	ReviewFindingDedupFolded = "dedup_folded"
)

// ReviewRun is one review run of a PR. DirPath is the repo's main dir, so
// every worktree of a repo shares its PRs' history. Scope is "full" or
// "incremental"; an incremental run reviewed the changes from FromSHA to
// HeadSHA.
type ReviewRun struct {
	ID         int64
	ReviewID   string
	DirPath    string
	PRNumber   int
	BaseRef    string
	ChannelID  string
	HeadSHA    string
	Scope      string
	FromSHA    string
	Model      string
	Effort     string
	ForkMode   string
	SessionID  string
	Status     string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time // zero while running
}

// ReviewFinding is an agent finding of a PR's review as it stands now: its
// text, line and verdict follow the edits, re-anchors and verdicts made on
// it since. RunID is the run that reported it, 0 when none was in flight.
// Outcome is "resolved" or "outdated" once its pushed comment is, on
// GitHub. A deleted finding keeps its row, with DeletedReason and, for one
// the dedup pass folded into another, SupersededBy. Transcripts are the
// Claude transcripts of the agent passes that reported or changed it,
// oldest first.
type ReviewFinding struct {
	DirPath       string
	PRNumber      int
	ID            string
	RunID         int64
	Path          string
	Line          int
	Side          string
	Category      string
	Body          string
	Verdict       string
	VerdictReason string
	Impact        string
	GitHubID      int64
	URL           string
	PushedAt      time.Time // zero until pushed
	Outcome       string
	Transcripts   []string
	DeletedReason string
	SupersededBy  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CreateReviewRun records run as started now and returns its id.
func (s *SQLiteStore) CreateReviewRun(ctx context.Context, run *ReviewRun) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO review_runs (review_id, dir_path, pr_number, base_ref, channel_id, head_sha, scope, from_sha, model, effort, fork_mode, status, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ReviewID, run.DirPath, run.PRNumber, run.BaseRef, run.ChannelID, run.HeadSHA, run.Scope, run.FromSHA, run.Model, run.Effort, run.ForkMode, ReviewRunRunning, s.nowFunc(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishReviewRun records how run id ended. model, when set, replaces the
// one recorded at the start: it's the model the agent reported, where the
// start only had the configured choice, often empty.
func (s *SQLiteStore) FinishReviewRun(ctx context.Context, id int64, status, sessionID, model, errText string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE review_runs SET status = ?, session_id = ?, model = COALESCE(NULLIF(?, ''), model), error = ?, finished_at = ? WHERE id = ?`,
		status, sessionID, model, errText, s.nowFunc(), id,
	)
	return err
}

// LastReviewedHead returns the head the newest completed run of the PR
// reviewed, or "" when none has completed.
func (s *SQLiteStore) LastReviewedHead(ctx context.Context, dirPath string, prNumber int) (string, error) {
	var head string
	err := s.db.QueryRowContext(ctx,
		`SELECT head_sha FROM review_runs WHERE dir_path = ? AND pr_number = ? AND status = ? ORDER BY finished_at DESC, id DESC LIMIT 1`,
		dirPath, prNumber, ReviewRunCompleted,
	).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return head, err
}

// ListReviewRuns returns the runs of the PR, oldest first.
func (s *SQLiteStore) ListReviewRuns(ctx context.Context, dirPath string, prNumber int) ([]*ReviewRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, review_id, base_ref, channel_id, head_sha, scope, from_sha, model, effort, fork_mode, session_id, status, error, started_at, finished_at
		 FROM review_runs WHERE dir_path = ? AND pr_number = ? ORDER BY started_at, id`,
		dirPath, prNumber,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReviewRun
	for rows.Next() {
		r := &ReviewRun{DirPath: dirPath, PRNumber: prNumber}
		var finished sql.NullTime
		if err := rows.Scan(&r.ID, &r.ReviewID, &r.BaseRef, &r.ChannelID, &r.HeadSHA, &r.Scope, &r.FromSHA, &r.Model, &r.Effort, &r.ForkMode, &r.SessionID, &r.Status, &r.Error, &r.StartedAt, &finished); err != nil {
			return nil, err
		}
		r.FinishedAt = finished.Time
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveReviewFinding stores f's current state. A finding saved again keeps
// the run that first reported it and its creation time, and stops being
// deleted: a finding reported again after a deletion is live again.
func (s *SQLiteStore) SaveReviewFinding(ctx context.Context, f *ReviewFinding) error {
	now := s.nowFunc()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO review_findings (dir_path, pr_number, id, run_id, path, line, side, category, body, verdict, verdict_reason, impact, github_id, url, pushed_at, outcome, transcripts, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(dir_path, pr_number, id) DO UPDATE SET
		   run_id = COALESCE(review_findings.run_id, excluded.run_id),
		   path = excluded.path, line = excluded.line, side = excluded.side, category = excluded.category,
		   body = excluded.body, verdict = excluded.verdict, verdict_reason = excluded.verdict_reason,
		   impact = excluded.impact, github_id = excluded.github_id, url = excluded.url,
		   pushed_at = COALESCE(excluded.pushed_at, review_findings.pushed_at),
		   outcome = excluded.outcome, transcripts = excluded.transcripts, deleted_reason = '', superseded_by = '', updated_at = excluded.updated_at`,
		f.DirPath, f.PRNumber, f.ID, nullInt64(f.RunID), f.Path, f.Line, f.Side, f.Category, f.Body, f.Verdict, f.VerdictReason, f.Impact,
		f.GitHubID, f.URL, nullTime(f.PushedAt), f.Outcome, strings.Join(f.Transcripts, "\n"), now, now,
	)
	return err
}

// DeleteReviewFinding marks a finding deleted for reason; supersededBy is
// the finding a dedup fold went into.
func (s *SQLiteStore) DeleteReviewFinding(ctx context.Context, dirPath string, prNumber int, id, reason, supersededBy string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE review_findings SET deleted_reason = ?, superseded_by = ?, updated_at = ? WHERE dir_path = ? AND pr_number = ? AND id = ?`,
		reason, supersededBy, s.nowFunc(), dirPath, prNumber, id,
	)
	return err
}

const reviewFindingColumns = `dir_path, pr_number, id, run_id, path, line, side, category, body, verdict, verdict_reason, impact, github_id, url, pushed_at, outcome, transcripts, deleted_reason, superseded_by, created_at, updated_at`

// ListReviewFindings returns the PR's findings that aren't deleted, oldest
// first.
func (s *SQLiteStore) ListReviewFindings(ctx context.Context, dirPath string, prNumber int) ([]*ReviewFinding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+reviewFindingColumns+` FROM review_findings WHERE dir_path = ? AND pr_number = ? AND deleted_reason = '' ORDER BY created_at, id`,
		dirPath, prNumber,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReviewFinding
	for rows.Next() {
		var f ReviewFinding
		var runID sql.NullInt64
		var pushedAt sql.NullTime
		var transcripts string
		if err := rows.Scan(&f.DirPath, &f.PRNumber, &f.ID, &runID, &f.Path, &f.Line, &f.Side, &f.Category, &f.Body, &f.Verdict, &f.VerdictReason, &f.Impact,
			&f.GitHubID, &f.URL, &pushedAt, &f.Outcome, &transcripts, &f.DeletedReason, &f.SupersededBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.RunID, f.PushedAt = runID.Int64, pushedAt.Time
		if transcripts != "" {
			f.Transcripts = strings.Split(transcripts, "\n")
		}
		out = append(out, &f)
	}
	return out, rows.Err()
}

// reviewStaleCTE selects the PRs with no run started and no finding
// changed since its parameter.
const reviewStaleCTE = `WITH stale AS (
	SELECT dir_path, pr_number FROM (
		SELECT dir_path, pr_number, started_at AS t FROM review_runs
		UNION ALL SELECT dir_path, pr_number, updated_at FROM review_findings
	) GROUP BY dir_path, pr_number HAVING MAX(t) < ?
) `

// PruneReviewHistory deletes the runs and findings of the PRs with no
// review activity since before, and returns how many runs went.
func (s *SQLiteStore) PruneReviewHistory(ctx context.Context, before time.Time) (int64, error) {
	if _, err := s.db.ExecContext(ctx,
		reviewStaleCTE+`DELETE FROM review_findings WHERE (dir_path, pr_number) IN (SELECT dir_path, pr_number FROM stale)`, before); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		reviewStaleCTE+`DELETE FROM review_runs WHERE (dir_path, pr_number) IN (SELECT dir_path, pr_number FROM stale)`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ReviewStatsFilter picks the history ReviewStats covers: a repo's main
// dir, one of its PRs (0 for all) and the runs started and findings
// reported since Since.
type ReviewStatsFilter struct {
	DirPath  string
	PRNumber int
	Since    time.Time
}

// ReviewStats sums up a repo's review history: its runs, its findings and
// what became of them, then the same per model, effort, scope and finding
// category.
type ReviewStats struct {
	Runs       ReviewRunStats     `json:"runs"`
	Findings   ReviewFindingStats `json:"findings"`
	ByModel    []ReviewStatsGroup `json:"by_model"`
	ByEffort   []ReviewStatsGroup `json:"by_effort"`
	ByScope    []ReviewStatsGroup `json:"by_scope"`
	ByCategory []ReviewStatsGroup `json:"by_category"`
}

// ReviewRunStats counts runs. The durations are of completed runs.
type ReviewRunStats struct {
	Total       int   `json:"total"`
	Completed   int   `json:"completed"`
	Failed      int   `json:"failed"`
	Cancelled   int   `json:"cancelled"`
	Running     int   `json:"running"`
	Full        int   `json:"full"`
	Incremental int   `json:"incremental"`
	PRs         int   `json:"prs"`
	MedianMs    int64 `json:"median_ms"`
	P90Ms       int64 `json:"p90_ms"`
}

// ReviewFindingStats counts findings: added in all, folded by the dedup
// pass, deleted by the user, pushed; then per verdict, the real ones per
// impact, and per outcome of the pushed ones. FalsePositiveRate is false
// positives over the findings with a verdict.
type ReviewFindingStats struct {
	Added             int     `json:"added"`
	Folded            int     `json:"folded"`
	Deleted           int     `json:"deleted"`
	Pushed            int     `json:"pushed"`
	Real              int     `json:"real"`
	FalsePositive     int     `json:"false_positive"`
	AlreadyFixed      int     `json:"already_fixed"`
	Unjudged          int     `json:"unjudged"`
	Blocking          int     `json:"blocking"`
	NiceToHave        int     `json:"nice_to_have"`
	Resolved          int     `json:"resolved"`
	Outdated          int     `json:"outdated"`
	FalsePositiveRate float64 `json:"false_positive_rate"`
}

// ReviewStatsGroup is one row of a breakdown: Key is the model, effort,
// scope or category ("" when unknown). Runs is 0 in the category one.
type ReviewStatsGroup struct {
	Key  string `json:"key"`
	Runs int    `json:"runs"`
	ReviewFindingStats
}

// reviewStatsFinding is what ReviewStats reads of a finding: its own state
// and the run that reported it.
type reviewStatsFinding struct {
	model, effort, scope string
	category, verdict    string
	impact               string
	deletedReason        string
	pushed               bool
	outcome              string
}

// ReviewStats returns the stats of the history f picks.
func (s *SQLiteStore) ReviewStats(ctx context.Context, f ReviewStatsFilter) (*ReviewStats, error) {
	runs, err := s.reviewStatsRuns(ctx, f)
	if err != nil {
		return nil, err
	}
	findings, err := s.reviewStatsFindings(ctx, f)
	if err != nil {
		return nil, err
	}
	return aggregateReviewStats(runs, findings), nil
}

func (s *SQLiteStore) reviewStatsRuns(ctx context.Context, f ReviewStatsFilter) ([]*ReviewRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pr_number, scope, model, effort, status, started_at, finished_at FROM review_runs
		 WHERE dir_path = ? AND (? = 0 OR pr_number = ?) AND started_at >= ?`,
		f.DirPath, f.PRNumber, f.PRNumber, f.Since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReviewRun
	for rows.Next() {
		var r ReviewRun
		var finished sql.NullTime
		if err := rows.Scan(&r.PRNumber, &r.Scope, &r.Model, &r.Effort, &r.Status, &r.StartedAt, &finished); err != nil {
			return nil, err
		}
		r.FinishedAt = finished.Time
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) reviewStatsFindings(ctx context.Context, f ReviewStatsFilter) ([]*reviewStatsFinding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(r.model, ''), COALESCE(r.effort, ''), COALESCE(r.scope, ''), f.category, f.verdict, f.impact, f.deleted_reason, f.pushed_at IS NOT NULL, f.outcome
		 FROM review_findings f LEFT JOIN review_runs r ON r.id = f.run_id
		 WHERE f.dir_path = ? AND (? = 0 OR f.pr_number = ?) AND f.created_at >= ?`,
		f.DirPath, f.PRNumber, f.PRNumber, f.Since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*reviewStatsFinding
	for rows.Next() {
		var x reviewStatsFinding
		if err := rows.Scan(&x.model, &x.effort, &x.scope, &x.category, &x.verdict, &x.impact, &x.deletedReason, &x.pushed, &x.outcome); err != nil {
			return nil, err
		}
		out = append(out, &x)
	}
	return out, rows.Err()
}

// aggregateReviewStats sums runs and findings up into ReviewStats.
func aggregateReviewStats(runs []*ReviewRun, findings []*reviewStatsFinding) *ReviewStats {
	st := &ReviewStats{}
	prs := map[int]bool{}
	var durations []int64
	byModel, byEffort, byScope, byCategory := newStatsGroups(), newStatsGroups(), newStatsGroups(), newStatsGroups()
	for _, r := range runs {
		st.Runs.Total++
		prs[r.PRNumber] = true
		switch r.Status {
		case ReviewRunCompleted:
			st.Runs.Completed++
			if !r.FinishedAt.IsZero() {
				durations = append(durations, r.FinishedAt.Sub(r.StartedAt).Milliseconds())
			}
		case ReviewRunFailed:
			st.Runs.Failed++
		case ReviewRunCancelled:
			st.Runs.Cancelled++
		default:
			st.Runs.Running++
		}
		if r.Scope == "incremental" {
			st.Runs.Incremental++
		} else {
			st.Runs.Full++
		}
		byModel.get(r.Model).Runs++
		byEffort.get(r.Effort).Runs++
		byScope.get(r.Scope).Runs++
	}
	st.Runs.PRs = len(prs)
	slices.Sort(durations)
	st.Runs.MedianMs = percentile(durations, 50)
	st.Runs.P90Ms = percentile(durations, 90)
	for _, f := range findings {
		st.Findings.add(f)
		byModel.get(f.model).add(f)
		byEffort.get(f.effort).add(f)
		byScope.get(f.scope).add(f)
		byCategory.get(f.category).add(f)
	}
	st.Findings.rate()
	st.ByModel, st.ByEffort, st.ByScope, st.ByCategory = byModel.list(), byEffort.list(), byScope.list(), byCategory.list()
	return st
}

func (c *ReviewFindingStats) add(f *reviewStatsFinding) {
	c.Added++
	switch f.deletedReason {
	case ReviewFindingDedupFolded:
		c.Folded++
	case ReviewFindingUserDeleted:
		c.Deleted++
	}
	if f.pushed {
		c.Pushed++
	}
	switch f.verdict {
	case "real":
		c.Real++
	case "false_positive":
		c.FalsePositive++
	case "already_fixed":
		c.AlreadyFixed++
	default:
		c.Unjudged++
	}
	switch f.impact {
	case "blocking":
		c.Blocking++
	case "nice_to_have":
		c.NiceToHave++
	}
	switch f.outcome {
	case "resolved":
		c.Resolved++
	case "outdated":
		c.Outdated++
	}
}

func (c *ReviewFindingStats) rate() {
	if judged := c.Real + c.FalsePositive + c.AlreadyFixed; judged > 0 {
		c.FalsePositiveRate = float64(c.FalsePositive) / float64(judged)
	}
}

// statsGroups is a breakdown under construction, keyed by Key.
type statsGroups map[string]*ReviewStatsGroup

func newStatsGroups() statsGroups { return statsGroups{} }

func (g statsGroups) get(key string) *ReviewStatsGroup {
	if x, ok := g[key]; ok {
		return x
	}
	x := &ReviewStatsGroup{Key: key}
	g[key] = x
	return x
}

// list returns the groups by runs then findings, most first, then by key.
func (g statsGroups) list() []ReviewStatsGroup {
	out := make([]ReviewStatsGroup, 0, len(g))
	for _, x := range g {
		x.rate()
		out = append(out, *x)
	}
	slices.SortFunc(out, func(a, b ReviewStatsGroup) int {
		if a.Runs != b.Runs {
			return b.Runs - a.Runs
		}
		if a.Added != b.Added {
			return b.Added - a.Added
		}
		if a.Key < b.Key {
			return -1
		}
		return 1
	})
	return out
}

// percentile returns the p-th percentile of sorted (nearest rank), 0 for
// none.
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted)*p+99)/100 - 1
	return sorted[max(i, 0)]
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
