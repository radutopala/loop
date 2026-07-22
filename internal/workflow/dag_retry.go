package workflow

import (
	"context"
	"time"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// executeWithRetry wraps a node execution function with optional retry logic.
// iteration scopes the retry-attempt UpsertNodeRun to the correct
// (run_id, node_id, iteration) row; for top-level nodes that is 0, for
// loop body children it is the current iteration. Without it, a retry of
// a body child at iter N>0 would silently overwrite the (run_id, node_id, 0)
// row's status — corrupting iter-0's terminal state and burying the actual
// retrying iteration in the Workflows graph.
func (e *defaultEngine) executeWithRetry(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, iteration int, fn func() (nodeExecResult, error)) (nodeExecResult, error) {
	if node.Retry == nil || node.Retry.MaxRetries <= 0 {
		return fn()
	}

	backoffBase := 5 * time.Second
	if node.Retry.BackoffBase != "" {
		if parsed, err := time.ParseDuration(node.Retry.BackoffBase); err == nil {
			backoffBase = parsed
		}
	}
	backoffMax := 5 * time.Minute
	if node.Retry.BackoffMax != "" {
		if parsed, err := time.ParseDuration(node.Retry.BackoffMax); err == nil {
			backoffMax = parsed
		}
	}

	var lastErr error
	var res nodeExecResult
	for attempt := 0; attempt <= node.Retry.MaxRetries; attempt++ {
		if attempt > 0 {
			// Persist retry attempt. Iteration is load-bearing for body
			// children — without it the UPSERT targets the (run_id, node_id, 0)
			// row regardless of which iteration is actually retrying.
			nr := &db.NodeRun{
				RunID:     run.ID,
				NodeID:    node.ID,
				Iteration: iteration,
				Status:    db.NodeRunStatusRunning,
				Attempt:   attempt + 1,
			}
			if err := e.store.UpsertNodeRun(ctx, nr); err != nil {
				e.logger.Error("workflow: failed to update retry attempt", "node_id", node.ID, "iteration", iteration, "error", err)
			}

			shift := min(attempt-1, 30)
			delay := time.Duration(min(float64(backoffBase)*float64(uint64(1)<<shift), float64(backoffMax)))
			e.logger.Info("workflow: retrying node", "node_id", node.ID, "attempt", attempt+1, "delay", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nodeExecResult{}, ctx.Err()
			}
		}

		res, lastErr = fn()
		if lastErr == nil {
			return res, nil
		}
	}

	return res, lastErr
}

// startHeartbeat launches a background goroutine that periodically updates
// last_heartbeat_at for the given (run, node, iteration) row. The returned
// function cancels the goroutine and waits for it to exit. iteration is
// load-bearing: a node's row is keyed by (run_id, node_id, iteration) and
// the heartbeat UPDATE filters by all three — without it, a heartbeat for
// iteration N would also bump iteration N-1's last_heartbeat_at and hide a
// stalled node from the recovery sweeper.
func (e *defaultEngine) startHeartbeat(ctx context.Context, runID, nodeID string, iteration int) func() {
	hbCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Immediate first heartbeat.
		if err := e.store.UpdateNodeHeartbeat(hbCtx, runID, nodeID, iteration); err != nil {
			e.logger.Error("workflow: heartbeat update failed", "run_id", runID, "node_id", nodeID, "iteration", iteration, "error", err)
		}
		ticker := time.NewTicker(e.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := e.store.UpdateNodeHeartbeat(hbCtx, runID, nodeID, iteration); err != nil {
					e.logger.Error("workflow: heartbeat update failed", "run_id", runID, "node_id", nodeID, "iteration", iteration, "error", err)
				}
			case <-hbCtx.Done():
				return
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
