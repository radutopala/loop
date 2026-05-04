package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// --- WorkflowRun tests ---

func newMockWorkflowRunRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "workflow_name", "channel_id", "dir_path", "worktree_path",
		"status", "inputs", "paused_node_id", "error_text", "workflow_def", "started_at", "finished_at",
	})
}

func newMockNodeRunRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "run_id", "node_id", "status", "output", "error_text", "attempt", "started_at", "finished_at", "last_heartbeat_at",
	})
}

func (s *StoreSuite) TestCreateWorkflowRunWithNodes() {
	now := time.Now().UTC()
	run := &WorkflowRun{ID: "run-1", WorkflowName: "deploy", Status: WorkflowRunStatusRunning, StartedAt: now}

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`INSERT INTO workflow_runs`).
		WithArgs(run.ID, run.WorkflowName, run.ChannelID, run.DirPath, run.WorktreePath,
			string(run.Status), run.Inputs, run.PausedNodeID, run.ErrorText, run.WorkflowDef, run.StartedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(run.ID, "n1", string(NodeRunStatusPending)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(run.ID, "n2", string(NodeRunStatusPending)).
		WillReturnResult(sqlmock.NewResult(2, 1))
	s.mock.ExpectCommit()

	err := s.store.CreateWorkflowRunWithNodes(context.Background(), run, []string{"n1", "n2"})
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestCreateWorkflowRunWithNodesRunInsertError() {
	now := time.Now().UTC()
	run := &WorkflowRun{ID: "run-1", WorkflowName: "deploy", Status: WorkflowRunStatusRunning, StartedAt: now}

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`INSERT INTO workflow_runs`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	err := s.store.CreateWorkflowRunWithNodes(context.Background(), run, []string{"n1"})
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestCreateWorkflowRunWithNodesNodeInsertError() {
	now := time.Now().UTC()
	run := &WorkflowRun{ID: "run-1", WorkflowName: "deploy", Status: WorkflowRunStatusRunning, StartedAt: now}

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`INSERT INTO workflow_runs`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(run.ID, "n1", string(NodeRunStatusPending)).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	err := s.store.CreateWorkflowRunWithNodes(context.Background(), run, []string{"n1"})
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetWorkflowRun() {
	now := time.Now().UTC()
	finishedAt := now.Add(time.Minute)
	rows := newMockWorkflowRunRows().
		AddRow("run-1", "deploy", "ch1", "/project", "/worktree", "completed", `{"env":"prod"}`, "", "", `{"name":"deploy"}`, now, &finishedAt)

	s.mock.ExpectQuery(`FROM workflow_runs WHERE id`).
		WithArgs("run-1").
		WillReturnRows(rows)

	run, err := s.store.GetWorkflowRun(context.Background(), "run-1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), run)
	require.Equal(s.T(), "run-1", run.ID)
	require.Equal(s.T(), "deploy", run.WorkflowName)
	require.Equal(s.T(), "ch1", run.ChannelID)
	require.Equal(s.T(), WorkflowRunStatusCompleted, run.Status)
	require.Equal(s.T(), `{"name":"deploy"}`, run.WorkflowDef)
	require.NotNil(s.T(), run.FinishedAt)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetWorkflowRunNotFoundAndError() {
	// Not found: empty rows causes sql.ErrNoRows on Scan.
	s.mock.ExpectQuery(`FROM workflow_runs WHERE id`).
		WithArgs("run-missing").
		WillReturnRows(newMockWorkflowRunRows())

	run, err := s.store.GetWorkflowRun(context.Background(), "run-missing")
	require.NoError(s.T(), err)
	require.Nil(s.T(), run)

	// Query-level error: function returns (zero-value-run, err).
	s.mock.ExpectQuery(`FROM workflow_runs WHERE id`).
		WithArgs("run-missing").
		WillReturnError(sql.ErrConnDone)

	run, err = s.store.GetWorkflowRun(context.Background(), "run-missing")
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
	_ = run
}

func (s *StoreSuite) TestUpdateWorkflowRun() {
	finishedAt := time.Now().UTC()
	run := &WorkflowRun{
		ID:           "run-1",
		Status:       WorkflowRunStatusCompleted,
		PausedNodeID: "",
		ErrorText:    "",
		FinishedAt:   &finishedAt,
	}

	s.mock.ExpectExec(`UPDATE workflow_runs SET status`).
		WithArgs(string(run.Status), run.PausedNodeID, run.ErrorText, run.FinishedAt, run.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateWorkflowRun(context.Background(), run)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpdateWorkflowRunFailed() {
	run := &WorkflowRun{
		ID:        "run-1",
		Status:    WorkflowRunStatusFailed,
		ErrorText: "something went wrong",
	}

	s.mock.ExpectExec(`UPDATE workflow_runs SET status`).
		WithArgs(string(run.Status), run.PausedNodeID, run.ErrorText, run.FinishedAt, run.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateWorkflowRun(context.Background(), run)
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestUpdateWorkflowRunError() {
	run := &WorkflowRun{ID: "run-1", Status: WorkflowRunStatusFailed}

	s.mock.ExpectExec(`UPDATE workflow_runs SET status`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpdateWorkflowRun(context.Background(), run)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestMarkRunFailedWithStaleNodes() {
	finishedAt := time.Now().UTC()

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`UPDATE workflow_runs SET status = \?, error_text = \?, finished_at = \? WHERE id = \?`).
		WithArgs(string(WorkflowRunStatusFailed), "boom", finishedAt, "run-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectExec(`UPDATE workflow_node_runs SET status = \?, error_text = \?, finished_at = \?\s+WHERE run_id = \? AND status IN \(\?, \?\)`).
		WithArgs(string(NodeRunStatusFailed), "node boom", finishedAt, "run-1",
			string(NodeRunStatusPending), string(NodeRunStatusRunning)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	s.mock.ExpectCommit()

	err := s.store.MarkRunFailedWithStaleNodes(context.Background(), "run-1", "boom", "node boom", finishedAt)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestMarkRunFailedWithStaleNodesRunUpdateError() {
	finishedAt := time.Now().UTC()

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`UPDATE workflow_runs SET status`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	err := s.store.MarkRunFailedWithStaleNodes(context.Background(), "run-1", "boom", "node boom", finishedAt)
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestMarkRunFailedWithStaleNodesNodesUpdateError() {
	finishedAt := time.Now().UTC()

	s.mock.ExpectBegin()
	s.mock.ExpectExec(`UPDATE workflow_runs SET status`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectExec(`UPDATE workflow_node_runs SET status`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	err := s.store.MarkRunFailedWithStaleNodes(context.Background(), "run-1", "boom", "node boom", finishedAt)
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRuns() {
	now := time.Now().UTC()
	finishedAt := now.Add(time.Minute)
	rows := newMockWorkflowRunRows().
		AddRow("run-2", "build", "ch1", "/proj2", "", "completed", "", "", "", "", now, &finishedAt).
		AddRow("run-1", "deploy", "ch1", "/proj1", "/wt", "running", `{"k":"v"}`, "", "", "", now.Add(-time.Hour), nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT \? OFFSET \?`).
		WithArgs("ch1", "ch1", "ch1", 50, 0).
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch1", 50, 0)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 2)
	require.Equal(s.T(), "run-2", runs[0].ID)
	require.Equal(s.T(), WorkflowRunStatusCompleted, runs[0].Status)
	require.NotNil(s.T(), runs[0].FinishedAt)
	require.Equal(s.T(), "run-1", runs[1].ID)
	require.Equal(s.T(), WorkflowRunStatusRunning, runs[1].Status)
	require.Nil(s.T(), runs[1].FinishedAt)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsIncludesChildChannelRuns() {
	now := time.Now().UTC()
	// Three sources of runs that should all appear when listing for "dm":
	//   - run-direct: stored under "dm" itself (manual or direct task run)
	//   - run-child:  stored under "thread-real" whose parent is "dm"
	//   - run-ghost:  stored under "thread-ghost" — no channel row, but a
	//                 scheduled task on "dm" has thread_id="thread-ghost"
	rows := newMockWorkflowRunRows().
		AddRow("run-ghost", "build", "thread-ghost", "/proj", "", "completed", "", "", "", "", now, nil).
		AddRow("run-child", "test", "thread-real", "/proj", "", "completed", "", "", "", "", now.Add(-time.Minute), nil).
		AddRow("run-direct", "deploy", "dm", "/proj", "", "running", "", "", "", "", now.Add(-time.Hour), nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \?\s+OR channel_id IN \(SELECT channel_id FROM channels WHERE parent_id = \?\)\s+OR channel_id IN \(SELECT thread_id FROM scheduled_tasks WHERE channel_id = \? AND thread_id != ''\)`).
		WithArgs("dm", "dm", "dm", 50, 0).
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "dm", 50, 0)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 3)
	require.Equal(s.T(), "run-ghost", runs[0].ID)
	require.Equal(s.T(), "thread-ghost", runs[0].ChannelID)
	require.Equal(s.T(), "run-child", runs[1].ID)
	require.Equal(s.T(), "thread-real", runs[1].ChannelID)
	require.Equal(s.T(), "run-direct", runs[2].ID)
	require.Equal(s.T(), "dm", runs[2].ChannelID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsWithoutChannelFilter() {
	now := time.Now().UTC()
	rows := newMockWorkflowRunRows().
		AddRow("run-3", "test", "ch2", "/proj3", "", "failed", "", "", "timeout", "", now, nil).
		AddRow("run-1", "deploy", "ch1", "/proj1", "", "running", "", "", "", "", now.Add(-time.Hour), nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs ORDER BY started_at DESC LIMIT \? OFFSET \?`).
		WithArgs(10, 0).
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "", 10, 0)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 2)
	require.Equal(s.T(), "run-3", runs[0].ID)
	require.Equal(s.T(), "ch2", runs[0].ChannelID)
	require.Equal(s.T(), WorkflowRunStatusFailed, runs[0].Status)
	require.Equal(s.T(), "run-1", runs[1].ID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsEmpty() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT \? OFFSET \?`).
		WithArgs("ch-empty", "ch-empty", "ch-empty", 10, 0).
		WillReturnRows(newMockWorkflowRunRows())

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch-empty", 10, 0)
	require.NoError(s.T(), err)
	require.Nil(s.T(), runs)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT \? OFFSET \?`).
		WithArgs("ch1", "ch1", "ch1", 10, 0).
		WillReturnError(sql.ErrConnDone)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch1", 10, 0)
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs ORDER BY started_at DESC LIMIT \? OFFSET \?`).
		WithArgs(10, 0).
		WillReturnError(sql.ErrConnDone)

	runs, err = s.store.ListWorkflowRuns(context.Background(), "", 10, 0)
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestListWorkflowRunsScanError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT \? OFFSET \?`).
		WithArgs("ch1", "ch1", "ch1", 10, 0).
		WillReturnRows(newMockWorkflowRunRows().AddRow("bad-id", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch1", 10, 0)
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestListWorkflowRunsWithOffset() {
	now := time.Now().UTC()
	rows := newMockWorkflowRunRows().
		AddRow("run-paged", "deploy", "ch1", "/proj", "", "completed", "", "", "", "", now, nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs\s+WHERE channel_id = \? .+ ORDER BY started_at DESC LIMIT \? OFFSET \?`).
		WithArgs("ch1", "ch1", "ch1", 50, 50).
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRuns(context.Background(), "ch1", 50, 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 1)
	require.Equal(s.T(), "run-paged", runs[0].ID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsNegativeOffsetClamped() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs ORDER BY started_at DESC LIMIT \? OFFSET \?`).
		WithArgs(10, 0).
		WillReturnRows(newMockWorkflowRunRows())

	runs, err := s.store.ListWorkflowRuns(context.Background(), "", 10, -5)
	require.NoError(s.T(), err)
	require.Nil(s.T(), runs)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsByStatus() {
	now := time.Now().UTC()
	rows := newMockWorkflowRunRows().
		AddRow("run-1", "wf1", "ch1", "/p", "", "running", "{}", "", "", "", now, nil).
		AddRow("run-2", "wf2", "ch2", "/p", "", "paused", "{}", "approve", "", "", now, nil)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs WHERE status IN`).
		WithArgs("running", "paused").
		WillReturnRows(rows)

	runs, err := s.store.ListWorkflowRunsByStatus(context.Background(), []WorkflowRunStatus{
		WorkflowRunStatusRunning, WorkflowRunStatusPaused,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), runs, 2)
	require.Equal(s.T(), WorkflowRunStatusRunning, runs[0].Status)
	require.Equal(s.T(), WorkflowRunStatusPaused, runs[1].Status)
	require.Equal(s.T(), "approve", runs[1].PausedNodeID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListWorkflowRunsByStatusEmpty() {
	runs, err := s.store.ListWorkflowRunsByStatus(context.Background(), nil)
	require.NoError(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestListWorkflowRunsByStatusError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs WHERE status IN`).
		WithArgs("running").
		WillReturnError(sql.ErrConnDone)

	runs, err := s.store.ListWorkflowRunsByStatus(context.Background(), []WorkflowRunStatus{WorkflowRunStatusRunning})
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestListWorkflowRunsByStatusScanError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_runs WHERE status IN`).
		WithArgs("paused").
		WillReturnRows(newMockWorkflowRunRows().AddRow("bad", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))

	runs, err := s.store.ListWorkflowRunsByStatus(context.Background(), []WorkflowRunStatus{WorkflowRunStatusPaused})
	require.Error(s.T(), err)
	require.Nil(s.T(), runs)
}

func (s *StoreSuite) TestUpsertNodeRunInsert() {
	now := time.Now().UTC()
	nr := &NodeRun{
		RunID:     "run-1",
		NodeID:    "node-a",
		Status:    NodeRunStatusRunning,
		Output:    "",
		ErrorText: "",
		Attempt:   1,
		StartedAt: &now,
	}

	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(nr.RunID, nr.NodeID, string(nr.Status), nr.Output, nr.ErrorText, nr.Attempt, nr.StartedAt, nr.FinishedAt, nr.LastHeartbeatAt).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertNodeRun(context.Background(), nr)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertNodeRunUpdate() {
	startedAt := time.Now().UTC()
	finishedAt := startedAt.Add(time.Second * 30)
	nr := &NodeRun{
		RunID:      "run-1",
		NodeID:     "node-a",
		Status:     NodeRunStatusSuccess,
		Output:     "build passed",
		ErrorText:  "",
		Attempt:    1,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
	}

	// Second upsert simulates ON CONFLICT update path (same exec signature).
	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(nr.RunID, nr.NodeID, string(nr.Status), nr.Output, nr.ErrorText, nr.Attempt, nr.StartedAt, nr.FinishedAt, nr.LastHeartbeatAt).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpsertNodeRun(context.Background(), nr)
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertNodeRunError() {
	nr := &NodeRun{RunID: "run-1", NodeID: "node-a", Status: NodeRunStatusPending, Attempt: 1}

	s.mock.ExpectExec(`INSERT INTO workflow_node_runs`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpsertNodeRun(context.Background(), nr)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestListNodeRuns() {
	startedAt := time.Now().UTC()
	finishedAt := startedAt.Add(time.Second)
	rows := newMockNodeRunRows().
		AddRow(1, "run-1", "node-a", "success", "output-a", "", 1, &startedAt, &finishedAt, nil).
		AddRow(2, "run-1", "node-b", "running", "", "", 1, &startedAt, nil, &startedAt)

	s.mock.ExpectQuery(`SELECT .+ FROM workflow_node_runs WHERE run_id .+ ORDER BY id ASC`).
		WithArgs("run-1").
		WillReturnRows(rows)

	nodeRuns, err := s.store.ListNodeRuns(context.Background(), "run-1")
	require.NoError(s.T(), err)
	require.Len(s.T(), nodeRuns, 2)
	require.Equal(s.T(), int64(1), nodeRuns[0].ID)
	require.Equal(s.T(), "run-1", nodeRuns[0].RunID)
	require.Equal(s.T(), "node-a", nodeRuns[0].NodeID)
	require.Equal(s.T(), NodeRunStatusSuccess, nodeRuns[0].Status)
	require.Equal(s.T(), "output-a", nodeRuns[0].Output)
	require.NotNil(s.T(), nodeRuns[0].FinishedAt)
	require.Equal(s.T(), "node-b", nodeRuns[1].NodeID)
	require.Equal(s.T(), NodeRunStatusRunning, nodeRuns[1].Status)
	require.Nil(s.T(), nodeRuns[1].FinishedAt)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListNodeRunsEmpty() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_node_runs WHERE run_id .+ ORDER BY id ASC`).
		WithArgs("run-empty").
		WillReturnRows(newMockNodeRunRows())

	nodeRuns, err := s.store.ListNodeRuns(context.Background(), "run-empty")
	require.NoError(s.T(), err)
	require.Nil(s.T(), nodeRuns)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListNodeRunsError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_node_runs WHERE run_id .+ ORDER BY id ASC`).
		WithArgs("run-1").
		WillReturnError(sql.ErrConnDone)

	nodeRuns, err := s.store.ListNodeRuns(context.Background(), "run-1")
	require.Error(s.T(), err)
	require.Nil(s.T(), nodeRuns)
}

func (s *StoreSuite) TestListNodeRunsScanError() {
	s.mock.ExpectQuery(`SELECT .+ FROM workflow_node_runs WHERE run_id .+ ORDER BY id ASC`).
		WithArgs("run-1").
		WillReturnRows(newMockNodeRunRows().AddRow("bad-id", nil, nil, nil, nil, nil, nil, nil, nil, nil))

	nodeRuns, err := s.store.ListNodeRuns(context.Background(), "run-1")
	require.Error(s.T(), err)
	require.Nil(s.T(), nodeRuns)
}

func (s *StoreSuite) TestUpdateNodeHeartbeat() {
	s.mock.ExpectExec(`UPDATE workflow_node_runs SET last_heartbeat_at`).
		WithArgs(sqlmock.AnyArg(), "run-1", "node-a").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.store.UpdateNodeHeartbeat(context.Background(), "run-1", "node-a")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpdateNodeHeartbeatError() {
	s.mock.ExpectExec(`UPDATE workflow_node_runs SET last_heartbeat_at`).
		WithArgs(sqlmock.AnyArg(), "run-1", "node-a").
		WillReturnError(sql.ErrConnDone)

	err := s.store.UpdateNodeHeartbeat(context.Background(), "run-1", "node-a")
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestDeleteWorkflowRun() {
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM workflow_node_runs WHERE run_id`).
		WithArgs("run-1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	s.mock.ExpectExec(`DELETE FROM workflow_runs WHERE id`).
		WithArgs("run-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectCommit()

	err := s.store.DeleteWorkflowRun(context.Background(), "run-1")
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeleteWorkflowRunErrors() {
	// First exec (delete node runs) fails.
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM workflow_node_runs WHERE run_id`).
		WithArgs("run-1").
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	err := s.store.DeleteWorkflowRun(context.Background(), "run-1")
	require.Error(s.T(), err)

	// First exec succeeds, second (delete workflow run) fails.
	s.mock.ExpectBegin()
	s.mock.ExpectExec(`DELETE FROM workflow_node_runs WHERE run_id`).
		WithArgs("run-1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	s.mock.ExpectExec(`DELETE FROM workflow_runs WHERE id`).
		WithArgs("run-1").
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	err = s.store.DeleteWorkflowRun(context.Background(), "run-1")
	require.Error(s.T(), err)
}
