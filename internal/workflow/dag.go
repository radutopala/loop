package workflow

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// dagScheduler holds the shared mutable state of one DAG execution: the
// dependency graph, per-node statuses, and the goroutine bookkeeping. Both
// the fresh-start path (executeDAG) and the restart path
// (executeDAGFromCheckpoint) build one and then run it to completion —
// previously each carried its own copy of the graph build + enqueue loop,
// and every node goroutine threaded six of these fields as parameters.
//
// mu guards nodeStatus/inDegree AND runCtx (leaf executors template against
// runCtx concurrently); the critical sections stay short and I/O-free.
type dagScheduler struct {
	engine *defaultEngine
	run    *db.WorkflowRun
	runCtx *RunContext

	nodeMap    map[string]*config.NodeDef
	mu         sync.Mutex
	nodeStatus map[string]db.NodeRunStatus
	inDegree   map[string]int
	downstream map[string][]string // parent → children

	wg    sync.WaitGroup
	errCh chan error

	// execCtx is the run-scoped context captured at runToCompletion; the
	// ready sweep spawns node goroutines against it so late-enqueued nodes
	// observe run cancellation — the same semantics the pre-scheduler
	// enqueueReady closure had by closing over executeDAG's ctx.
	execCtx context.Context
}

// newDAGScheduler builds the dependency graph and the run context shared by
// every node of the run. All nodes start pending with their declared
// in-degree; checkpoint state is applied afterwards via applyCheckpoint.
func newDAGScheduler(e *defaultEngine, run *db.WorkflowRun, wfDef *config.WorkflowDef, inputs map[string]string) *dagScheduler {
	s := &dagScheduler{
		engine: e,
		run:    run,
		runCtx: &RunContext{
			Inputs:      inputs,
			NodeOutputs: make(map[string]string),
			RunMeta:     RunMeta{RunID: run.ID, WorktreePath: run.WorktreePath},
			ChannelID:   run.ChannelID,
		},
		nodeMap:    make(map[string]*config.NodeDef, len(wfDef.Nodes)),
		nodeStatus: make(map[string]db.NodeRunStatus, len(wfDef.Nodes)),
		inDegree:   make(map[string]int, len(wfDef.Nodes)),
		downstream: make(map[string][]string),
		errCh:      make(chan error, len(wfDef.Nodes)),
	}
	for i := range wfDef.Nodes {
		s.nodeMap[wfDef.Nodes[i].ID] = &wfDef.Nodes[i]
	}
	for _, n := range wfDef.Nodes {
		s.nodeStatus[n.ID] = db.NodeRunStatusPending
		s.inDegree[n.ID] = len(n.DependsOn)
		for _, dep := range n.DependsOn {
			s.downstream[dep] = append(s.downstream[dep], n.ID)
		}
	}
	return s
}

// applyCheckpoint overlays DB state from a previous daemon lifetime onto the
// fresh graph: completed/failed/skipped nodes keep their status and release
// their children; the paused approval node (DB status "running") is reset to
// pending so enqueueReady picks it up; any other running node was
// mid-execution when the server died and is treated as failed. Pre-failed
// nodes push their error onto errCh so finalizeDAG reports them.
func (s *dagScheduler) applyCheckpoint(completedNodes map[string]db.NodeRunStatus, completedOutputs map[string]string) {
	maps.Copy(s.runCtx.NodeOutputs, completedOutputs)

	release := func(nodeID string) {
		for _, childID := range s.downstream[nodeID] {
			s.inDegree[childID]--
		}
	}
	for id := range s.nodeStatus {
		dbStatus, exists := completedNodes[id]
		if !exists {
			continue
		}
		switch dbStatus {
		case db.NodeRunStatusSuccess, db.NodeRunStatusSkipped, db.NodeRunStatusFailed:
			s.nodeStatus[id] = dbStatus
			release(id)
		case db.NodeRunStatusRunning:
			if id == s.run.PausedNodeID {
				s.nodeStatus[id] = db.NodeRunStatusPending
			} else {
				s.nodeStatus[id] = db.NodeRunStatusFailed
				release(id)
			}
		}
	}
	for id, status := range s.nodeStatus {
		if status == db.NodeRunStatusFailed {
			s.errCh <- fmt.Errorf("node %s failed before restart", id)
		}
	}
}

// enqueueReady fires goroutines for all zero-in-degree pending nodes
// against the run-scoped execCtx. Must be called with s.mu held;
// completeNode re-invokes it after each terminal node releases its children.
func (s *dagScheduler) enqueueReady() {
	for id, deg := range s.inDegree {
		if deg == 0 && s.nodeStatus[id] == db.NodeRunStatusPending {
			s.nodeStatus[id] = db.NodeRunStatusRunning
			s.wg.Add(1)
			go func(nodeID string) {
				defer s.wg.Done()
				if !s.engine.acquireNodeSlot(s.execCtx) {
					return // context cancelled before slot acquired
				}
				defer s.engine.releaseNodeSlot()
				s.engine.executeNode(s.execCtx, s, s.nodeMap[nodeID])
			}(id)
		}
	}
}

// runToCompletion seeds the initial ready set, waits for every node
// goroutine to finish, and hands the drained error channel to finalizeDAG.
func (s *dagScheduler) runToCompletion(ctx context.Context) {
	s.execCtx = ctx
	s.mu.Lock()
	s.enqueueReady()
	s.mu.Unlock()

	s.wg.Wait()
	close(s.errCh)

	s.engine.finalizeDAG(ctx, s.run, s.errCh)
}

// executeDAG runs the workflow DAG, executing nodes in parallel where
// dependencies allow. Each ready node gets its own goroutine.
func (e *defaultEngine) executeDAG(ctx context.Context, run *db.WorkflowRun, wfDef *config.WorkflowDef, inputs map[string]string) {
	defer e.cleanupCancel(run.ID)
	newDAGScheduler(e, run, wfDef, inputs).runToCompletion(ctx)
}

// executeDAGFromCheckpoint resumes a workflow DAG from a checkpoint — typically
// after a server restart. Completed/failed/skipped nodes are pre-populated from
// the DB, and only pending nodes (including the paused approval node reset to
// pending) are executed.
func (e *defaultEngine) executeDAGFromCheckpoint(
	ctx context.Context,
	run *db.WorkflowRun,
	wfDef *config.WorkflowDef,
	inputs map[string]string,
	completedNodes map[string]db.NodeRunStatus,
	completedOutputs map[string]string,
) {
	defer e.cleanupCancel(run.ID)

	s := newDAGScheduler(e, run, wfDef, inputs)
	s.applyCheckpoint(completedNodes, completedOutputs)

	// Restore running status before enqueueing nodes.
	if err := e.updateRunStatus(ctx, run.ID, db.WorkflowRunStatusRunning, ""); err != nil {
		errCh := make(chan error, 1)
		errCh <- err
		close(errCh)
		e.finalizeDAG(ctx, run, errCh)
		return
	}

	s.runToCompletion(ctx)
}

// finalizeDAG drains the error channel, determines the terminal status, persists
// it to the DB (reading a fresh copy to avoid racing with CancelRun), and
// broadcasts the completion event. Shared by executeDAG and executeDAGFromCheckpoint.
func (e *defaultEngine) finalizeDAG(ctx context.Context, run *db.WorkflowRun, errCh chan error) {
	var firstErr error
	for err := range errCh {
		if firstErr == nil {
			firstErr = err
		}
	}

	var finalStatus db.WorkflowRunStatus
	var errText string
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		// Workflow-level timeout — treat as failure, not cancellation.
		finalStatus = db.WorkflowRunStatusFailed
		errText = "workflow timeout exceeded"
	case ctx.Err() != nil:
		finalStatus = db.WorkflowRunStatusCancelled
	case firstErr != nil:
		finalStatus = db.WorkflowRunStatusFailed
		errText = firstErr.Error()
	default:
		finalStatus = db.WorkflowRunStatusCompleted
	}

	// Use a detached context for the final DB write — the run context may
	// already be cancelled, but we still need to persist the terminal status.
	// Read a fresh copy from DB to avoid racing with CancelRun which may have
	// already written a terminal status.
	bgCtx := context.Background()
	dbRun, err := e.store.GetWorkflowRun(bgCtx, run.ID)
	switch {
	case err != nil || dbRun == nil:
		e.logger.Error("workflow: failed to read run for final update", "run_id", run.ID, "error", err)
	case dbRun.Status == db.WorkflowRunStatusRunning || dbRun.Status == db.WorkflowRunStatusPaused:
		// Only update if not already terminal (CancelRun may have written "cancelled").
		now := time.Now().UTC()
		dbRun.Status = finalStatus
		dbRun.ErrorText = errText
		dbRun.FinishedAt = &now
		if err := e.store.UpdateWorkflowRun(bgCtx, dbRun); err != nil {
			e.logger.Error("workflow: failed to update run", "run_id", run.ID, "error", err)
		}
	default:
		// Already terminal — use the DB's status for the broadcast.
		finalStatus = dbRun.Status
		errText = dbRun.ErrorText
	}

	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowRunCompleted(events.WorkflowRunEventData{
			RunID:        run.ID,
			WorkflowName: run.WorkflowName,
			ChannelID:    run.ChannelID,
			Status:       string(finalStatus),
			Error:        errText,
		})
	}
}

// executeNode drives one top-level node from running to terminal status:
// broadcast + persist "running", evaluate when/trigger gates, dispatch to the
// typed executor (with retry), and hand the result to completeNode.
func (e *defaultEngine) executeNode(ctx context.Context, s *dagScheduler, node *config.NodeDef) {
	// Check context cancellation.
	if ctx.Err() != nil {
		return
	}

	now := time.Now().UTC()

	// Broadcast node started.
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowNodeStarted(events.WorkflowNodeEventData{
			RunID:  s.run.ID,
			NodeID: node.ID,
			Status: string(db.NodeRunStatusRunning),
		})
	}

	// Persist running status.
	nr := &db.NodeRun{
		RunID:     s.run.ID,
		NodeID:    node.ID,
		Status:    db.NodeRunStatusRunning,
		Attempt:   1,
		StartedAt: &now,
	}
	if err := e.store.UpsertNodeRun(ctx, nr); err != nil {
		e.logger.Error("workflow: failed to update node run", "node_id", node.ID, "error", err)
	}

	// Start heartbeat for this node — periodically updates last_heartbeat_at
	// so the UI can show liveness and recovery can detect stale nodes.
	// Top-level executor: nothing wraps this node in a loop body, so the
	// only row in workflow_node_runs is at iteration=0. Passing 0 explicitly
	// keeps the heartbeat UPDATE aimed at that row (the WHERE clause filters
	// by iteration so per-iteration body rows don't get hijacked).
	stopHeartbeat := e.startHeartbeat(ctx, s.run.ID, node.ID, 0)
	defer stopHeartbeat()

	// Evaluate "when" condition.
	s.mu.Lock()
	shouldRun := e.evaluateWhen(node, s.runCtx)
	s.mu.Unlock()

	if !shouldRun {
		e.completeNode(s, node, nodeExecResult{}, db.NodeRunStatusSkipped, nil)
		return
	}

	// Check trigger rule against dependencies.
	s.mu.Lock()
	triggerOk := e.checkTriggerRule(node, s.nodeStatus)
	s.mu.Unlock()
	if !triggerOk {
		e.completeNode(s, node, nodeExecResult{}, db.NodeRunStatusSkipped, nil)
		return
	}

	// Apply node-level timeout for non-approval nodes. Approval nodes handle
	// timeout internally via their own timer + pause/resume semantics.
	nodeCtx := ctx
	if node.Type != config.NodeTypeApproval && node.Timeout != "" {
		if d, err := time.ParseDuration(node.Timeout); err == nil {
			var nodeCancel context.CancelFunc
			nodeCtx, nodeCancel = context.WithTimeout(ctx, d)
			defer nodeCancel()
		}
	}

	// Execute the node based on type, with optional retry.
	execFn := func() (nodeExecResult, error) {
		switch node.Type {
		case config.NodeTypePrompt:
			return e.executePromptNode(nodeCtx, s.run, node, s.runCtx, &s.mu)
		case config.NodeTypeBash:
			return e.executeBashNode(nodeCtx, s.run, node, s.runCtx, &s.mu)
		case config.NodeTypeLoop:
			out, err := e.executeLoopNode(nodeCtx, s.run, node, s.runCtx, &s.mu)
			return nodeExecResult{output: out}, err
		case config.NodeTypeApproval:
			out, err := e.executeApprovalNode(ctx, s.run, node, s.runCtx, &s.mu)
			return nodeExecResult{output: out}, err
		default:
			return nodeExecResult{}, fmt.Errorf("unsupported node type: %s", node.Type)
		}
	}

	res, execErr := e.executeWithRetry(nodeCtx, s.run, node, 0, execFn)

	status := db.NodeRunStatusSuccess
	if execErr != nil {
		status = db.NodeRunStatusFailed
	}

	e.completeNode(s, node, res, status, execErr)
}

// completeNode persists and broadcasts a top-level node's terminal status,
// releases its downstream children, and re-runs the ready sweep.
func (e *defaultEngine) completeNode(s *dagScheduler, node *config.NodeDef, res nodeExecResult, status db.NodeRunStatus, execErr error) {
	now := time.Now().UTC()

	// Persist node completion, including the rendered input + (prompt) session id
	// so the Workflows panel can show the full per-node input/output and the run
	// can hand off resumable prompt sessions.
	nr := &db.NodeRun{
		RunID:      s.run.ID,
		NodeID:     node.ID,
		Status:     status,
		Input:      res.input,
		SessionID:  res.sessionID,
		Output:     res.output,
		Attempt:    1,
		FinishedAt: &now,
	}
	if execErr != nil {
		nr.ErrorText = execErr.Error()
	}
	// Use a detached context — the run context may already be cancelled, but
	// we still need to persist the terminal node status.
	if err := e.store.UpsertNodeRun(context.Background(), nr); err != nil {
		e.logger.Error("workflow: failed to update node run", "node_id", node.ID, "error", err)
	}

	// Broadcast node completed (with input + session id so the panel's node-detail
	// view has everything without an extra fetch).
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowNodeCompleted(events.WorkflowNodeEventData{
			RunID:     s.run.ID,
			NodeID:    node.ID,
			Status:    string(status),
			Input:     truncateOutput(res.input, 1000),
			SessionID: res.sessionID,
			Output:    truncateOutput(res.output, 1000),
		})
	}

	s.mu.Lock()
	s.nodeStatus[node.ID] = status

	// Decrement in-degree for downstream nodes and enqueue newly ready ones.
	for _, childID := range s.downstream[node.ID] {
		s.inDegree[childID]--
	}
	s.enqueueReady()
	s.mu.Unlock()

	if execErr != nil {
		s.errCh <- fmt.Errorf("node %s failed: %w", node.ID, execErr)
	}
}

// nodeExecResult carries a node execution's output plus the metadata persisted
// for the unified run view: the rendered input (script/prompt) and, for prompt
// nodes, the Claude session id (so its transcript is locatable/resumable).
type nodeExecResult struct {
	output    string
	input     string
	sessionID string
}

// updateRunStatus atomically reads the run from DB, updates status fields, and
// writes back. This avoids data races from mutating the shared run struct.
func (e *defaultEngine) updateRunStatus(ctx context.Context, runID string, status db.WorkflowRunStatus, pausedNodeID string) error {
	dbRun, err := e.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		e.logger.Error("workflow: failed to read run for status update", "run_id", runID, "error", err)
		return fmt.Errorf("read run for status update: %w", err)
	}
	if dbRun == nil {
		e.logger.Error("workflow: run not found for status update", "run_id", runID)
		return fmt.Errorf("run %s not found for status update", runID)
	}
	dbRun.Status = status
	dbRun.PausedNodeID = pausedNodeID
	if err := e.store.UpdateWorkflowRun(ctx, dbRun); err != nil {
		e.logger.Error("workflow: failed to update run status", "run_id", runID, "error", err)
		return fmt.Errorf("update run status: %w", err)
	}
	return nil
}
