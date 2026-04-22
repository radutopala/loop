package workflow

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// executeDAG runs the workflow DAG, executing nodes in parallel where
// dependencies allow. Each ready node gets its own goroutine.
func (e *defaultEngine) executeDAG(ctx context.Context, run *db.WorkflowRun, wfDef *config.WorkflowDef, inputs map[string]string) {
	defer e.cleanupCancel(run.ID)

	runCtx := &RunContext{
		Inputs:      inputs,
		NodeOutputs: make(map[string]string),
		RunMeta: RunMeta{
			RunID:        run.ID,
			WorktreePath: run.WorktreePath,
		},
	}

	// Build node map and dependency graph.
	nodeMap := make(map[string]*config.NodeDef, len(wfDef.Nodes))
	for i := range wfDef.Nodes {
		nodeMap[wfDef.Nodes[i].ID] = &wfDef.Nodes[i]
	}

	// Track node statuses.
	var mu sync.Mutex
	nodeStatus := make(map[string]db.NodeRunStatus, len(wfDef.Nodes))
	for _, n := range wfDef.Nodes {
		nodeStatus[n.ID] = db.NodeRunStatusPending
	}

	// In-degree map for topological execution.
	inDegree := make(map[string]int, len(wfDef.Nodes))
	downstream := make(map[string][]string) // parent → children
	for _, n := range wfDef.Nodes {
		inDegree[n.ID] = len(n.DependsOn)
		for _, dep := range n.DependsOn {
			downstream[dep] = append(downstream[dep], n.ID)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(wfDef.Nodes))

	// enqueueReady fires goroutines for all zero-in-degree nodes.
	var enqueueReady func()
	enqueueReady = func() {
		for id, deg := range inDegree {
			if deg == 0 && nodeStatus[id] == db.NodeRunStatusPending {
				nodeStatus[id] = db.NodeRunStatusRunning
				wg.Add(1)
				go func(nodeID string) {
					defer wg.Done()
					if !e.acquireNodeSlot(ctx) {
						return // context cancelled before slot acquired
					}
					defer e.releaseNodeSlot()
					e.executeNode(ctx, run, nodeMap[nodeID], runCtx, &mu, nodeStatus, inDegree, downstream, enqueueReady, errCh)
				}(id)
			}
		}
	}

	mu.Lock()
	enqueueReady()
	mu.Unlock()

	// Wait for all nodes to complete.
	wg.Wait()
	close(errCh)

	e.finalizeDAG(ctx, run, errCh)
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

	runCtx := &RunContext{
		Inputs:      inputs,
		NodeOutputs: make(map[string]string),
		RunMeta:     RunMeta{RunID: run.ID, WorktreePath: run.WorktreePath},
	}
	maps.Copy(runCtx.NodeOutputs, completedOutputs)

	// Build node map and dependency graph.
	nodeMap := make(map[string]*config.NodeDef, len(wfDef.Nodes))
	for i := range wfDef.Nodes {
		nodeMap[wfDef.Nodes[i].ID] = &wfDef.Nodes[i]
	}

	var mu sync.Mutex
	nodeStatus := make(map[string]db.NodeRunStatus, len(wfDef.Nodes))
	inDegree := make(map[string]int, len(wfDef.Nodes))
	downstream := make(map[string][]string)

	for _, n := range wfDef.Nodes {
		inDegree[n.ID] = len(n.DependsOn)
		for _, dep := range n.DependsOn {
			downstream[dep] = append(downstream[dep], n.ID)
		}
	}

	// Apply checkpoint state.
	for _, n := range wfDef.Nodes {
		dbStatus, exists := completedNodes[n.ID]
		if !exists {
			nodeStatus[n.ID] = db.NodeRunStatusPending
			continue
		}
		switch dbStatus {
		case db.NodeRunStatusSuccess, db.NodeRunStatusSkipped, db.NodeRunStatusFailed:
			nodeStatus[n.ID] = dbStatus
			for _, childID := range downstream[n.ID] {
				inDegree[childID]--
			}
		case db.NodeRunStatusRunning:
			// The paused approval node has DB status "running" — reset it to
			// pending so enqueueReady picks it up. Any other running node was
			// mid-execution when the server died — treat as failed.
			if n.ID == run.PausedNodeID {
				nodeStatus[n.ID] = db.NodeRunStatusPending
			} else {
				nodeStatus[n.ID] = db.NodeRunStatusFailed
				for _, childID := range downstream[n.ID] {
					inDegree[childID]--
				}
			}
		default:
			nodeStatus[n.ID] = db.NodeRunStatusPending
		}
	}

	// Restore running status before enqueueing nodes.
	if err := e.updateRunStatus(ctx, run.ID, db.WorkflowRunStatusRunning, ""); err != nil {
		errCh := make(chan error, 1)
		errCh <- err
		close(errCh)
		e.finalizeDAG(ctx, run, errCh)
		return
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(wfDef.Nodes))

	// Collect errors from pre-failed nodes.
	for _, n := range wfDef.Nodes {
		if nodeStatus[n.ID] == db.NodeRunStatusFailed {
			errCh <- fmt.Errorf("node %s failed before restart", n.ID)
		}
	}

	var enqueueReady func()
	enqueueReady = func() {
		for id, deg := range inDegree {
			if deg == 0 && nodeStatus[id] == db.NodeRunStatusPending {
				nodeStatus[id] = db.NodeRunStatusRunning
				wg.Add(1)
				go func(nodeID string) {
					defer wg.Done()
					if !e.acquireNodeSlot(ctx) {
						return // context cancelled before slot acquired
					}
					defer e.releaseNodeSlot()
					e.executeNode(ctx, run, nodeMap[nodeID], runCtx, &mu, nodeStatus, inDegree, downstream, enqueueReady, errCh)
				}(id)
			}
		}
	}

	mu.Lock()
	enqueueReady()
	mu.Unlock()

	wg.Wait()
	close(errCh)

	e.finalizeDAG(ctx, run, errCh)
}

func (e *defaultEngine) executeNode(
	ctx context.Context,
	run *db.WorkflowRun,
	node *config.NodeDef,
	runCtx *RunContext,
	mu *sync.Mutex,
	nodeStatus map[string]db.NodeRunStatus,
	inDegree map[string]int,
	downstream map[string][]string,
	enqueueReady func(),
	errCh chan<- error,
) {
	// Check context cancellation.
	if ctx.Err() != nil {
		return
	}

	now := time.Now().UTC()

	// Broadcast node started.
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowNodeStarted(events.WorkflowNodeEventData{
			RunID:  run.ID,
			NodeID: node.ID,
			Status: string(db.NodeRunStatusRunning),
		})
	}

	// Persist running status.
	nr := &db.NodeRun{
		RunID:     run.ID,
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
	stopHeartbeat := e.startHeartbeat(ctx, run.ID, node.ID)
	defer stopHeartbeat()

	// Evaluate "when" condition.
	mu.Lock()
	shouldRun := e.evaluateWhen(node, runCtx)
	mu.Unlock()

	if !shouldRun {
		e.completeNode(run, node, "", db.NodeRunStatusSkipped, nil, mu, nodeStatus, inDegree, downstream, enqueueReady, errCh)
		return
	}

	// Check trigger rule against dependencies.
	mu.Lock()
	triggerOk := e.checkTriggerRule(node, nodeStatus)
	mu.Unlock()
	if !triggerOk {
		e.completeNode(run, node, "", db.NodeRunStatusSkipped, nil, mu, nodeStatus, inDegree, downstream, enqueueReady, errCh)
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
	var output string
	var execErr error

	execFn := func() (string, error) {
		switch node.Type {
		case config.NodeTypePrompt:
			return e.executePromptNode(nodeCtx, run, node, runCtx, mu)
		case config.NodeTypeBash:
			return e.executeBashNode(nodeCtx, run, node, runCtx, mu)
		case config.NodeTypeLoop:
			return e.executeLoopNode(nodeCtx, run, node, runCtx, mu)
		case config.NodeTypeApproval:
			return e.executeApprovalNode(ctx, run, node, runCtx, mu)
		default:
			return "", fmt.Errorf("unsupported node type: %s", node.Type)
		}
	}

	output, execErr = e.executeWithRetry(nodeCtx, run, node, execFn)

	status := db.NodeRunStatusSuccess
	if execErr != nil {
		status = db.NodeRunStatusFailed
	}

	e.completeNode(run, node, output, status, execErr, mu, nodeStatus, inDegree, downstream, enqueueReady, errCh)
}

func (e *defaultEngine) completeNode(
	run *db.WorkflowRun,
	node *config.NodeDef,
	output string,
	status db.NodeRunStatus,
	execErr error,
	mu *sync.Mutex,
	nodeStatus map[string]db.NodeRunStatus,
	inDegree map[string]int,
	downstream map[string][]string,
	enqueueReady func(),
	errCh chan<- error,
) {
	now := time.Now().UTC()

	// Persist node completion.
	nr := &db.NodeRun{
		RunID:      run.ID,
		NodeID:     node.ID,
		Status:     status,
		Output:     output,
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

	// Broadcast node completed.
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowNodeCompleted(events.WorkflowNodeEventData{
			RunID:  run.ID,
			NodeID: node.ID,
			Status: string(status),
			Output: truncateOutput(output, 1000),
		})
	}

	mu.Lock()
	nodeStatus[node.ID] = status

	// Decrement in-degree for downstream nodes and enqueue newly ready ones.
	for _, childID := range downstream[node.ID] {
		inDegree[childID]--
	}
	enqueueReady()
	mu.Unlock()

	if execErr != nil {
		errCh <- fmt.Errorf("node %s failed: %w", node.ID, execErr)
	}
}

func (e *defaultEngine) executePromptNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (string, error) {
	promptText, err := node.ResolvePrompt(e.loopDir, os.ReadFile)
	if err != nil {
		return "", fmt.Errorf("resolving prompt: %w", err)
	}
	mu.Lock()
	prompt, err := renderTemplate(promptText, runCtx)
	mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("rendering prompt template: %w", err)
	}

	var systemPrompt string
	if node.SystemPrompt != "" {
		mu.Lock()
		systemPrompt, err = renderTemplate(node.SystemPrompt, runCtx)
		mu.Unlock()
		if err != nil {
			return "", fmt.Errorf("rendering system prompt template: %w", err)
		}
	}

	req := &agent.AgentRequest{
		ChannelID:    run.ChannelID,
		DirPath:      run.DirPath,
		Prompt:       prompt,
		SystemPrompt: systemPrompt,
	}

	resp, err := e.runner.Run(ctx, req)
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return resp.Response, fmt.Errorf("agent error: %s", resp.Error)
	}

	// Store output for downstream nodes.
	mu.Lock()
	runCtx.NodeOutputs[node.ID] = resp.Response
	mu.Unlock()

	return resp.Response, nil
}

func (e *defaultEngine) executeBashNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (string, error) {
	mu.Lock()
	script, err := renderTemplate(node.Script, runCtx)
	mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("rendering script template: %w", err)
	}

	output, err := e.bashRunner.RunBash(ctx, script, run.ChannelID, run.DirPath)
	if err != nil {
		return output, err
	}

	// Store output for downstream nodes.
	mu.Lock()
	runCtx.NodeOutputs[node.ID] = output
	mu.Unlock()

	return output, nil
}

// evaluateWhen evaluates the "when" condition. Must be called with mu held.
func (e *defaultEngine) evaluateWhen(node *config.NodeDef, runCtx *RunContext) bool {
	if node.When == "" {
		return true
	}
	result, err := renderTemplate(node.When, runCtx)
	if err != nil {
		e.logger.Warn("workflow: when condition failed", "node_id", node.ID, "error", err)
		return true // default to running on template error
	}
	return result == "true"
}

// checkTriggerRule checks whether a node's trigger rule is satisfied. Must be called with mu held.
func (e *defaultEngine) checkTriggerRule(node *config.NodeDef, nodeStatus map[string]db.NodeRunStatus) bool {
	rule := node.TriggerRule
	if rule == "" {
		rule = "all_success"
	}

	switch rule {
	case "all_success":
		for _, dep := range node.DependsOn {
			if nodeStatus[dep] != db.NodeRunStatusSuccess {
				return false
			}
		}
		return true
	case "all_done":
		for _, dep := range node.DependsOn {
			s := nodeStatus[dep]
			if s != db.NodeRunStatusSuccess && s != db.NodeRunStatusFailed && s != db.NodeRunStatusSkipped {
				return false
			}
		}
		return true
	case "one_success":
		for _, dep := range node.DependsOn {
			if nodeStatus[dep] == db.NodeRunStatusSuccess {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// renderTemplate renders a Go text/template string with the given RunContext.
func renderTemplate(tmplStr string, data *RunContext) (string, error) {
	if tmplStr == "" {
		return "", nil
	}
	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (e *defaultEngine) executeLoopNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (string, error) {
	maxIter := node.MaxIterations
	if maxIter <= 0 {
		maxIter = 10 // default
	}

	var lastOutput string
	for i := 0; i < maxIter; i++ {
		if ctx.Err() != nil {
			return lastOutput, ctx.Err()
		}

		output, err := e.executePromptNode(ctx, run, node, runCtx, mu)
		if err != nil {
			return output, err
		}
		lastOutput = output

		// Evaluate stop condition.
		if node.Condition != "" {
			mu.Lock()
			result, tmplErr := renderTemplate(node.Condition, runCtx)
			mu.Unlock()
			if tmplErr != nil {
				e.logger.Warn("workflow: loop condition failed", "node_id", node.ID, "error", tmplErr)
				continue
			}
			if result == "true" {
				break
			}
		}
	}

	return lastOutput, nil
}

func (e *defaultEngine) executeApprovalNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (string, error) {
	// Render message template.
	mu.Lock()
	message, err := renderTemplate(node.Message, runCtx)
	mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("rendering approval message template: %w", err)
	}

	// Parse timeout.
	timeout := 24 * time.Hour // default 24h
	if node.Timeout != "" {
		if parsed, parseErr := time.ParseDuration(node.Timeout); parseErr == nil {
			timeout = parsed
		}
	}

	// Create approval channel keyed by run:node to support parallel approvals.
	approvalKey := run.ID + ":" + node.ID
	approvalCh := make(chan string, 1)
	e.pendingApprovals.Store(approvalKey, approvalCh)
	defer e.pendingApprovals.Delete(approvalKey)

	// Persist paused status via a fresh DB write to avoid racing on the shared
	// run struct. Other goroutines may read/write run fields concurrently.
	if err := e.updateRunStatus(ctx, run.ID, db.WorkflowRunStatusPaused, node.ID); err != nil {
		return "", fmt.Errorf("failed to persist paused status: %w", err)
	}

	// Broadcast paused event.
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowRunPaused(events.WorkflowRunEventData{
			RunID:        run.ID,
			WorkflowName: run.WorkflowName,
			ChannelID:    run.ChannelID,
			Status:       string(db.WorkflowRunStatusPaused),
			PausedNodeID: node.ID,
		})
	}

	// Wait for resume, timeout, or cancellation.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case response := <-approvalCh:
		// Resume — restore running status.
		if err := e.updateRunStatus(ctx, run.ID, db.WorkflowRunStatusRunning, ""); err != nil {
			return "", fmt.Errorf("failed to restore running status: %w", err)
		}

		// Store the response as node output.
		if response == "" {
			response = "approved"
		}
		mu.Lock()
		runCtx.NodeOutputs[node.ID] = response
		mu.Unlock()
		return message + "\nApproval response: " + response, nil

	case <-timer.C:
		return "", fmt.Errorf("approval timed out after %s", timeout)

	case <-ctx.Done():
		return "", ctx.Err()
	}
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

// executeWithRetry wraps a node execution function with optional retry logic.
func (e *defaultEngine) executeWithRetry(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, fn func() (string, error)) (string, error) {
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
	var output string
	for attempt := 0; attempt <= node.Retry.MaxRetries; attempt++ {
		if attempt > 0 {
			// Persist retry attempt.
			nr := &db.NodeRun{
				RunID:   run.ID,
				NodeID:  node.ID,
				Status:  db.NodeRunStatusRunning,
				Attempt: attempt + 1,
			}
			if err := e.store.UpsertNodeRun(ctx, nr); err != nil {
				e.logger.Error("workflow: failed to update retry attempt", "node_id", node.ID, "error", err)
			}

			shift := min(attempt-1, 30)
			delay := time.Duration(min(float64(backoffBase)*float64(uint64(1)<<shift), float64(backoffMax)))
			e.logger.Info("workflow: retrying node", "node_id", node.ID, "attempt", attempt+1, "delay", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		output, lastErr = fn()
		if lastErr == nil {
			return output, nil
		}
	}

	return output, lastErr
}

// startHeartbeat launches a background goroutine that periodically updates
// last_heartbeat_at for the given node. The returned function cancels the
// goroutine and waits for it to exit.
func (e *defaultEngine) startHeartbeat(ctx context.Context, runID, nodeID string) func() {
	hbCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Immediate first heartbeat.
		if err := e.store.UpdateNodeHeartbeat(hbCtx, runID, nodeID); err != nil {
			e.logger.Error("workflow: heartbeat update failed", "run_id", runID, "node_id", nodeID, "error", err)
		}
		ticker := time.NewTicker(e.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := e.store.UpdateNodeHeartbeat(hbCtx, runID, nodeID); err != nil {
					e.logger.Error("workflow: heartbeat update failed", "run_id", runID, "node_id", nodeID, "error", err)
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
