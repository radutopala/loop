// workflows.go holds SQLiteStore methods for workflow_runs and workflow_node_runs.
package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// CreateWorkflowRunWithNodes inserts the workflow run and seeds initial pending
// node runs in a single transaction so a partial failure can never leave a run
// without its node rows.
func (s *SQLiteStore) CreateWorkflowRunWithNodes(ctx context.Context, run *WorkflowRun, nodeIDs []string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflow_runs (id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, run.WorkflowName, run.ChannelID, run.DirPath, run.WorktreePath, string(run.Status), run.Inputs, run.PausedNodeID, run.ErrorText, run.WorkflowDef, run.StartedAt,
		); err != nil {
			return err
		}
		for _, nodeID := range nodeIDs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO workflow_node_runs (run_id, node_id, iteration, status, output, error_text, attempt, started_at, finished_at, last_heartbeat_at)
				 VALUES (?, ?, 0, ?, '', '', 0, NULL, NULL, NULL)`,
				run.ID, nodeID, string(NodeRunStatusPending),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLiteStore) GetWorkflowRun(ctx context.Context, id string) (*WorkflowRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at, finished_at
		 FROM workflow_runs WHERE id = ?`, id,
	)
	run := &WorkflowRun{}
	err := row.Scan(&run.ID, &run.WorkflowName, &run.ChannelID, &run.DirPath, &run.WorktreePath, &run.Status, &run.Inputs, &run.PausedNodeID, &run.ErrorText, &run.WorkflowDef, &run.StartedAt, &run.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return run, err
}

func (s *SQLiteStore) UpdateWorkflowRun(ctx context.Context, run *WorkflowRun) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE workflow_runs SET status = ?, paused_node_id = ?, error_text = ?, finished_at = ? WHERE id = ?`,
		string(run.Status), run.PausedNodeID, run.ErrorText, run.FinishedAt, run.ID,
	)
	return err
}

// MarkRunFailedWithStaleNodes marks a running workflow as failed and bulk-updates
// all of its still-pending/running node rows to failed in a single transaction.
// Used by recovery on startup so a crash mid-update can never leave a failed run
// with zombie pending/running nodes that the next restart would re-execute.
func (s *SQLiteStore) MarkRunFailedWithStaleNodes(ctx context.Context, runID, errorText, nodeErrorText string, finishedAt time.Time) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflow_runs SET status = ?, error_text = ?, finished_at = ? WHERE id = ?`,
			string(WorkflowRunStatusFailed), errorText, finishedAt, runID,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflow_node_runs SET status = ?, error_text = ?, finished_at = ?
			 WHERE run_id = ? AND status IN (?, ?)`,
			string(NodeRunStatusFailed), nodeErrorText, finishedAt, runID,
			string(NodeRunStatusPending), string(NodeRunStatusRunning),
		); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) ListWorkflowRuns(ctx context.Context, channelID string, limit, offset int) ([]*WorkflowRun, error) {
	if offset < 0 {
		offset = 0
	}
	var rows *sql.Rows
	var err error
	if channelID != "" {
		// Include runs whose channel is the requested channel OR a child
		// channel of it. Two sources of "child":
		//   1. channels.parent_id == channelID — real persisted threads.
		//   2. scheduled_tasks.thread_id where task.channel_id == channelID
		//      — ghost threads (e.g. local-platform tasks) where the MCP
		//      server's channelID is the thread_id but no channel row exists.
		// Scheduled tasks that fire inside such a thread store the workflow
		// run under the thread's channel ID; the user expects to see those
		// when viewing the parent channel.
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at, finished_at
			 FROM workflow_runs
			 WHERE channel_id = ?
			    OR channel_id IN (SELECT channel_id FROM channels WHERE parent_id = ?)
			    OR channel_id IN (SELECT thread_id FROM scheduled_tasks WHERE channel_id = ? AND thread_id != '')
			 ORDER BY started_at DESC LIMIT ? OFFSET ?`, channelID, channelID, channelID, limit, offset)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at, finished_at
			 FROM workflow_runs ORDER BY started_at DESC LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*WorkflowRun
	for rows.Next() {
		run := &WorkflowRun{}
		if err := rows.Scan(&run.ID, &run.WorkflowName, &run.ChannelID, &run.DirPath, &run.WorktreePath, &run.Status, &run.Inputs, &run.PausedNodeID, &run.ErrorText, &run.WorkflowDef, &run.StartedAt, &run.FinishedAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) ListWorkflowRunsByStatus(ctx context.Context, statuses []WorkflowRunStatus) ([]*WorkflowRun, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, st := range statuses {
		placeholders[i] = "?"
		args[i] = string(st)
	}
	query := `SELECT id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at, finished_at
		 FROM workflow_runs WHERE status IN (` + strings.Join(placeholders, ",") + `) ORDER BY started_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*WorkflowRun
	for rows.Next() {
		run := &WorkflowRun{}
		if err := rows.Scan(&run.ID, &run.WorkflowName, &run.ChannelID, &run.DirPath, &run.WorktreePath, &run.Status, &run.Inputs, &run.PausedNodeID, &run.ErrorText, &run.WorkflowDef, &run.StartedAt, &run.FinishedAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) UpsertNodeRun(ctx context.Context, nr *NodeRun) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO workflow_node_runs (run_id, node_id, iteration, status, output, error_text, attempt, started_at, finished_at, last_heartbeat_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, node_id, iteration) DO UPDATE SET
		   status = excluded.status,
		   output = excluded.output,
		   error_text = excluded.error_text,
		   attempt = excluded.attempt,
		   started_at = COALESCE(excluded.started_at, workflow_node_runs.started_at),
		   finished_at = excluded.finished_at,
		   last_heartbeat_at = COALESCE(excluded.last_heartbeat_at, workflow_node_runs.last_heartbeat_at)`,
		nr.RunID, nr.NodeID, nr.Iteration, string(nr.Status), nr.Output, nr.ErrorText, nr.Attempt, nr.StartedAt, nr.FinishedAt, nr.LastHeartbeatAt,
	)
	return err
}

func (s *SQLiteStore) ListNodeRuns(ctx context.Context, runID string) ([]*NodeRun, error) { //nolint:dupl
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, node_id, iteration, status, output, error_text, attempt, started_at, finished_at, last_heartbeat_at
		 FROM workflow_node_runs WHERE run_id = ? ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodeRuns []*NodeRun
	for rows.Next() {
		nr := &NodeRun{}
		if err := rows.Scan(&nr.ID, &nr.RunID, &nr.NodeID, &nr.Iteration, &nr.Status, &nr.Output, &nr.ErrorText, &nr.Attempt, &nr.StartedAt, &nr.FinishedAt, &nr.LastHeartbeatAt); err != nil {
			return nil, err
		}
		nodeRuns = append(nodeRuns, nr)
	}
	return nodeRuns, rows.Err()
}

// UpdateNodeHeartbeat refreshes last_heartbeat_at for the (run_id, node_id,
// iteration) row. The iteration filter matters because a body child node
// shares its node_id across iterations — without it, a heartbeat for
// iteration N would also bump iteration N-1's row, hiding a stalled-and-
// recovered scenario from the recovery sweeper.
func (s *SQLiteStore) UpdateNodeHeartbeat(ctx context.Context, runID, nodeID string, iteration int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE workflow_node_runs SET last_heartbeat_at = ? WHERE run_id = ? AND node_id = ? AND iteration = ?`,
		s.nowFunc(), runID, nodeID, iteration,
	)
	return err
}

func (s *SQLiteStore) DeleteWorkflowRun(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workflow_node_runs WHERE run_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workflow_runs WHERE id = ?`, id); err != nil {
			return err
		}
		return nil
	})
}
