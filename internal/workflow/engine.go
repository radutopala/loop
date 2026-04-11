package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// Runner executes agent requests (prompt nodes) in Docker containers.
type Runner interface {
	Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error)
}

// BashRunner executes shell scripts (typically in Docker containers).
type BashRunner interface {
	RunBash(ctx context.Context, script, channelID, dirPath string) (string, error)
}

// EventBroadcaster sends workflow events to connected clients.
type EventBroadcaster interface {
	BroadcastWorkflowRunStarted(data events.WorkflowRunEventData)
	BroadcastWorkflowRunCompleted(data events.WorkflowRunEventData)
	BroadcastWorkflowRunPaused(data events.WorkflowRunEventData)
	BroadcastWorkflowNodeStarted(data events.WorkflowNodeEventData)
	BroadcastWorkflowNodeCompleted(data events.WorkflowNodeEventData)
}

// Engine orchestrates workflow execution.
type Engine interface {
	StartRun(ctx context.Context, opts StartRunOptions) (string, error)
	ResumeRun(ctx context.Context, runID, response string) error
	CancelRun(ctx context.Context, runID string) error
	DeleteRun(ctx context.Context, runID string) error
	RetryRun(ctx context.Context, runID string) (string, error)
	GetRun(ctx context.Context, runID string) (*db.WorkflowRun, []*db.NodeRun, error)
	ListRuns(ctx context.Context, channelID string, limit int) ([]*db.WorkflowRun, error)
	ListWorkflows(ctx context.Context, dirPath, parentDirPath string) ([]config.WorkflowDef, error)
	RecoverRuns(ctx context.Context) error
}

// StartRunOptions configures a new workflow run.
type StartRunOptions struct {
	WorkflowName  string
	ChannelID     string
	DirPath       string
	ParentDirPath string // parent channel dir for worktree config merging
	Inputs        map[string]string
}

type defaultEngine struct {
	store       db.Store
	runner      Runner
	bashRunner  BashRunner
	broadcaster EventBroadcaster
	workflows   func(dirPath, parentDirPath string) []config.WorkflowDef // returns merged workflows from config
	loopDir     string
	logger      *slog.Logger

	// Concurrency semaphores. nil means unlimited.
	runSem  chan struct{} // limits concurrent workflow runs
	nodeSem chan struct{} // limits concurrent node goroutines across all runs

	heartbeatInterval time.Duration // how often running nodes update their heartbeat
	heartbeatTimeout  time.Duration // how long before a missing heartbeat means the node is dead

	mu               sync.Mutex
	cancelFns        map[string]context.CancelFunc
	pendingApprovals sync.Map // runID:nodeID → chan string
}

// NewEngine creates a workflow engine. The concurrency config controls how many
// workflow runs and node goroutines may execute in parallel (0 = unlimited).
func NewEngine(store db.Store, runner Runner, bashRunner BashRunner, broadcaster EventBroadcaster, workflowsFunc func(dirPath, parentDirPath string) []config.WorkflowDef, loopDir string, concurrency config.WorkflowConcurrency, logger *slog.Logger) Engine {
	e := &defaultEngine{
		store:             store,
		runner:            runner,
		bashRunner:        bashRunner,
		broadcaster:       broadcaster,
		workflows:         workflowsFunc,
		loopDir:           loopDir,
		logger:            logger,
		heartbeatInterval: 10 * time.Second,
		heartbeatTimeout:  30 * time.Second, // 3× heartbeat interval
		cancelFns:         make(map[string]context.CancelFunc),
	}
	if concurrency.MaxConcurrentRuns > 0 {
		e.runSem = make(chan struct{}, concurrency.MaxConcurrentRuns)
	}
	if concurrency.MaxConcurrentNodes > 0 {
		e.nodeSem = make(chan struct{}, concurrency.MaxConcurrentNodes)
	}
	return e
}

func (e *defaultEngine) StartRun(ctx context.Context, opts StartRunOptions) (string, error) {
	// Find workflow definition.
	wfDef, err := e.findWorkflow(opts.WorkflowName, opts.DirPath, opts.ParentDirPath)
	if err != nil {
		return "", err
	}

	// Validate required inputs.
	for name, input := range wfDef.Inputs {
		if input.Required {
			if _, ok := opts.Inputs[name]; !ok {
				return "", fmt.Errorf("missing required input: %s", name)
			}
		}
	}

	// Acquire run semaphore slot before creating DB records.
	if e.runSem != nil {
		select {
		case e.runSem <- struct{}{}:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	// Apply input defaults then user-provided inputs.
	inputs := make(map[string]string)
	for name, input := range wfDef.Inputs {
		if input.Default != "" {
			inputs[name] = input.Default
		}
	}
	maps.Copy(inputs, opts.Inputs)

	inputsJSON, _ := json.Marshal(inputs)

	// Snapshot the workflow definition at start time so that recovery and
	// in-flight execution always use the same definition, even if the config
	// is edited later (version pinning).
	wfDefJSON, _ := json.Marshal(wfDef)

	runID := generateRunID()
	now := time.Now().UTC()
	run := &db.WorkflowRun{
		ID:           runID,
		WorkflowName: opts.WorkflowName,
		ChannelID:    opts.ChannelID,
		DirPath:      opts.DirPath,
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       string(inputsJSON),
		WorkflowDef:  string(wfDefJSON),
		StartedAt:    now,
	}
	if err := e.store.CreateWorkflowRun(ctx, run); err != nil {
		e.releaseRunSlot()
		return "", fmt.Errorf("creating workflow run: %w", err)
	}

	// Create initial node runs.
	for _, node := range wfDef.Nodes {
		nr := &db.NodeRun{
			RunID:  runID,
			NodeID: node.ID,
			Status: db.NodeRunStatusPending,
		}
		if err := e.store.UpsertNodeRun(ctx, nr); err != nil {
			e.releaseRunSlot()
			return "", fmt.Errorf("creating node run for %s: %w", node.ID, err)
		}
	}

	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowRunStarted(events.WorkflowRunEventData{
			RunID:        runID,
			WorkflowName: opts.WorkflowName,
			ChannelID:    opts.ChannelID,
			Status:       string(db.WorkflowRunStatusRunning),
		})
	}

	// Execute the DAG in a background goroutine. If the workflow has a timeout,
	// the context will have a deadline; otherwise it's a plain cancellable context.
	runCtx, cancel := e.createRunContext(wfDef)
	e.mu.Lock()
	e.cancelFns[runID] = cancel
	e.mu.Unlock()

	go e.executeDAG(runCtx, run, wfDef, inputs)

	return runID, nil
}

func (e *defaultEngine) ResumeRun(ctx context.Context, runID, response string) error {
	// Look up the paused node to form the composite approval key.
	run, err := e.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("looking up run: %w", err)
	}
	if run == nil {
		return fmt.Errorf("workflow run not found: %s", runID)
	}
	if run.PausedNodeID == "" {
		return fmt.Errorf("no pending approval for run: %s", runID)
	}

	approvalKey := runID + ":" + run.PausedNodeID
	v, ok := e.pendingApprovals.Load(approvalKey)
	if !ok {
		return fmt.Errorf("no pending approval for run: %s", runID)
	}
	ch := v.(chan string)
	select {
	case ch <- response:
		return nil
	default:
		return fmt.Errorf("approval already resumed for run: %s", runID)
	}
}

func (e *defaultEngine) CancelRun(ctx context.Context, runID string) error {
	e.mu.Lock()
	cancel, ok := e.cancelFns[runID]
	e.mu.Unlock()
	if ok {
		cancel()
	}

	run, err := e.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("workflow run not found: %s", runID)
	}
	if run.Status != db.WorkflowRunStatusRunning && run.Status != db.WorkflowRunStatusPaused {
		return nil // already terminal
	}

	// Copy before mutating — finalizeDAG may concurrently read another
	// *WorkflowRun returned by the same GetWorkflowRun call.
	updated := *run
	now := time.Now().UTC()
	updated.Status = db.WorkflowRunStatusCancelled
	updated.FinishedAt = &now
	return e.store.UpdateWorkflowRun(ctx, &updated)
}

func (e *defaultEngine) DeleteRun(ctx context.Context, runID string) error {
	run, err := e.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("workflow run not found: %s", runID)
	}
	// Cancel if still active.
	if run.Status == db.WorkflowRunStatusRunning || run.Status == db.WorkflowRunStatusPaused {
		_ = e.CancelRun(ctx, runID)
	}
	if err := e.store.DeleteWorkflowRun(ctx, runID); err != nil {
		return fmt.Errorf("deleting workflow run: %w", err)
	}
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowRunCompleted(events.WorkflowRunEventData{
			RunID:        runID,
			WorkflowName: run.WorkflowName,
			ChannelID:    run.ChannelID,
			Status:       "deleted",
		})
	}
	return nil
}

func (e *defaultEngine) RetryRun(ctx context.Context, runID string) (string, error) {
	run, err := e.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("looking up run: %w", err)
	}
	if run == nil {
		return "", fmt.Errorf("workflow run not found: %s", runID)
	}
	if run.Status == db.WorkflowRunStatusRunning || run.Status == db.WorkflowRunStatusPaused {
		return "", fmt.Errorf("cannot retry a run that is still %s", run.Status)
	}

	// Reconstruct inputs from the original run.
	var inputs map[string]string
	if run.Inputs != "" {
		if err := json.Unmarshal([]byte(run.Inputs), &inputs); err != nil {
			return "", fmt.Errorf("parsing original inputs: %w", err)
		}
	}

	return e.StartRun(ctx, StartRunOptions{
		WorkflowName:  run.WorkflowName,
		ChannelID:     run.ChannelID,
		DirPath:       run.DirPath,
		ParentDirPath: "", // not stored; will use channel-based resolution
		Inputs:        inputs,
	})
}

func (e *defaultEngine) GetRun(ctx context.Context, runID string) (*db.WorkflowRun, []*db.NodeRun, error) {
	run, err := e.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	if run == nil {
		return nil, nil, nil
	}
	nodeRuns, err := e.store.ListNodeRuns(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	return run, nodeRuns, nil
}

func (e *defaultEngine) ListRuns(ctx context.Context, channelID string, limit int) ([]*db.WorkflowRun, error) {
	if limit <= 0 {
		limit = 50
	}
	return e.store.ListWorkflowRuns(ctx, channelID, limit)
}

func (e *defaultEngine) ListWorkflows(_ context.Context, dirPath, parentDirPath string) ([]config.WorkflowDef, error) {
	return e.workflows(dirPath, parentDirPath), nil
}

func (e *defaultEngine) findWorkflow(name, dirPath, parentDirPath string) (*config.WorkflowDef, error) {
	for _, wf := range e.workflows(dirPath, parentDirPath) {
		if wf.Name == name {
			return &wf, nil
		}
	}
	return nil, fmt.Errorf("workflow not found: %s", name)
}

// resolveWorkflowDef returns the version-pinned definition stored in the run
// record, falling back to the live config for runs created before version
// pinning was introduced (empty WorkflowDef field).
func (e *defaultEngine) resolveWorkflowDef(run *db.WorkflowRun) (*config.WorkflowDef, error) {
	if run.WorkflowDef != "" {
		var wfDef config.WorkflowDef
		if err := json.Unmarshal([]byte(run.WorkflowDef), &wfDef); err != nil {
			return nil, fmt.Errorf("parsing pinned workflow definition: %w", err)
		}
		return &wfDef, nil
	}
	// Fallback: no pinned definition (legacy run).
	return e.findWorkflow(run.WorkflowName, run.DirPath, "")
}

// acquireNodeSlot blocks until a node semaphore slot is available, or the
// context is cancelled. Returns true if a slot was acquired, false if the
// context was cancelled before a slot became available.
// No-op (returns true) when the semaphore is nil (unlimited).
func (e *defaultEngine) acquireNodeSlot(ctx context.Context) bool {
	if e.nodeSem == nil {
		return true
	}
	// Check for cancellation first — if the context is already done, bail
	// immediately rather than racing with a free semaphore slot.
	select {
	case <-ctx.Done():
		return false
	default:
	}
	select {
	case e.nodeSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// releaseNodeSlot frees a node semaphore slot. No-op when unlimited.
func (e *defaultEngine) releaseNodeSlot() {
	if e.nodeSem == nil {
		return
	}
	<-e.nodeSem
}

// releaseRunSlot frees a run semaphore slot. No-op when unlimited.
func (e *defaultEngine) releaseRunSlot() {
	if e.runSem == nil {
		return
	}
	<-e.runSem
}

func (e *defaultEngine) cleanupCancel(runID string) {
	e.mu.Lock()
	delete(e.cancelFns, runID)
	e.mu.Unlock()

	e.releaseRunSlot()
}

func (e *defaultEngine) RecoverRuns(ctx context.Context) error {
	runs, err := e.store.ListWorkflowRunsByStatus(ctx, []db.WorkflowRunStatus{
		db.WorkflowRunStatusRunning, db.WorkflowRunStatusPaused,
	})
	if err != nil {
		return fmt.Errorf("listing stale runs: %w", err)
	}
	if len(runs) == 0 {
		return nil
	}

	e.logger.Info("workflow: recovering stale runs", "count", len(runs))
	for _, run := range runs {
		if run.Status == db.WorkflowRunStatusPaused {
			e.recoverPausedRun(ctx, run)
		} else {
			e.recoverRunningRun(ctx, run)
		}
	}
	return nil
}

// recoverPausedRun resumes a paused workflow run from its DB checkpoint.
// It reconstructs completed node outputs, creates a cancel context, and
// launches executeDAGFromCheckpoint in a background goroutine.
func (e *defaultEngine) recoverPausedRun(ctx context.Context, run *db.WorkflowRun) {
	// Prefer the version-pinned definition stored at run start time.
	// Fall back to the live config for runs created before version pinning.
	wfDef, err := e.resolveWorkflowDef(run)
	if err != nil {
		e.logger.Error("workflow: cannot recover run — workflow definition not found", "run_id", run.ID, "workflow", run.WorkflowName, "error", err)
		e.failStaleRun(ctx, run)
		return
	}

	nodeRuns, err := e.store.ListNodeRuns(ctx, run.ID)
	if err != nil {
		e.logger.Error("workflow: cannot recover run — failed to list node runs", "run_id", run.ID, "error", err)
		e.failStaleRun(ctx, run)
		return
	}

	completedNodes := make(map[string]db.NodeRunStatus, len(nodeRuns))
	completedOutputs := make(map[string]string)
	for _, nr := range nodeRuns {
		completedNodes[nr.NodeID] = nr.Status
		if nr.Status == db.NodeRunStatusSuccess && nr.Output != "" {
			completedOutputs[nr.NodeID] = nr.Output
		}
	}

	var inputs map[string]string
	if run.Inputs != "" {
		if err := json.Unmarshal([]byte(run.Inputs), &inputs); err != nil {
			e.logger.Error("workflow: cannot recover run — failed to parse inputs", "run_id", run.ID, "error", err)
			e.failStaleRun(ctx, run)
			return
		}
	}

	// Acquire run semaphore slot (non-blocking during recovery — best effort).
	if e.runSem != nil {
		select {
		case e.runSem <- struct{}{}:
		default:
			e.logger.Warn("workflow: run semaphore full, failing paused run instead of recovering", "run_id", run.ID)
			e.failStaleRun(ctx, run)
			return
		}
	}

	runCtx, cancel := e.createRunContext(wfDef)
	e.mu.Lock()
	e.cancelFns[run.ID] = cancel
	e.mu.Unlock()

	e.logger.Info("workflow: recovering paused run from checkpoint", "run_id", run.ID, "workflow", run.WorkflowName, "paused_node", run.PausedNodeID)
	go e.executeDAGFromCheckpoint(runCtx, run, wfDef, inputs, completedNodes, completedOutputs)
}

// isNodeHeartbeatFresh returns true if the node's last heartbeat is within the
// heartbeat timeout threshold, meaning the node was actively executing when the
// server stopped. Nodes with no heartbeat are considered stale.
func (e *defaultEngine) isNodeHeartbeatFresh(nr *db.NodeRun) bool {
	if nr.LastHeartbeatAt == nil {
		return false
	}
	return time.Since(*nr.LastHeartbeatAt) < e.heartbeatTimeout
}

// recoverRunningRun attempts to resume a running workflow from its DB
// checkpoint, using heartbeat data to determine which running nodes should be
// re-executed vs. failed. Nodes with a fresh heartbeat are reset to pending
// for re-execution; nodes with stale or missing heartbeats are marked failed.
// This is more resilient than failStaleRun because completed nodes are
// preserved and pending downstream nodes can still proceed.
func (e *defaultEngine) recoverRunningRun(ctx context.Context, run *db.WorkflowRun) {
	wfDef, err := e.resolveWorkflowDef(run)
	if err != nil {
		e.logger.Error("workflow: cannot recover running run — workflow definition not found, failing", "run_id", run.ID, "workflow", run.WorkflowName, "error", err)
		e.failStaleRun(ctx, run)
		return
	}

	nodeRuns, err := e.store.ListNodeRuns(ctx, run.ID)
	if err != nil {
		e.logger.Error("workflow: cannot recover running run — failed to list node runs, failing", "run_id", run.ID, "error", err)
		e.failStaleRun(ctx, run)
		return
	}

	// Classify running nodes using heartbeat freshness.
	completedNodes := make(map[string]db.NodeRunStatus, len(nodeRuns))
	completedOutputs := make(map[string]string)
	for _, nr := range nodeRuns {
		status := nr.Status
		if status == db.NodeRunStatusRunning {
			if e.isNodeHeartbeatFresh(nr) {
				// Node was actively executing — reset to pending for re-execution.
				status = db.NodeRunStatusPending
				e.logger.Info("workflow: resetting healthy node for re-execution (fresh heartbeat)",
					"run_id", run.ID, "node_id", nr.NodeID, "last_heartbeat", nr.LastHeartbeatAt)
			}
			// Stale/no heartbeat → stays Running; executeDAGFromCheckpoint will
			// mark it failed (the else branch of the Running case).
		}
		completedNodes[nr.NodeID] = status
		if status == db.NodeRunStatusSuccess && nr.Output != "" {
			completedOutputs[nr.NodeID] = nr.Output
		}
	}

	var inputs map[string]string
	if run.Inputs != "" {
		if err := json.Unmarshal([]byte(run.Inputs), &inputs); err != nil {
			e.logger.Error("workflow: cannot recover running run — failed to parse inputs, failing", "run_id", run.ID, "error", err)
			e.failStaleRun(ctx, run)
			return
		}
	}

	// Acquire run semaphore slot (non-blocking during recovery — best effort).
	if e.runSem != nil {
		select {
		case e.runSem <- struct{}{}:
		default:
			e.logger.Warn("workflow: run semaphore full, failing running run instead of recovering", "run_id", run.ID)
			e.failStaleRun(ctx, run)
			return
		}
	}

	runCtx, cancel := e.createRunContext(wfDef)
	e.mu.Lock()
	e.cancelFns[run.ID] = cancel
	e.mu.Unlock()

	e.logger.Info("workflow: recovering running run from checkpoint using heartbeat data", "run_id", run.ID, "workflow", run.WorkflowName)
	go e.executeDAGFromCheckpoint(runCtx, run, wfDef, inputs, completedNodes, completedOutputs)
}

// failStaleRun marks a running workflow and its non-terminal nodes as failed
// because the server restarted while they were executing.
func (e *defaultEngine) failStaleRun(ctx context.Context, run *db.WorkflowRun) {
	now := time.Now().UTC()
	run.Status = db.WorkflowRunStatusFailed
	run.ErrorText = "server restarted while workflow was running"
	run.FinishedAt = &now
	if err := e.store.UpdateWorkflowRun(ctx, run); err != nil {
		e.logger.Error("workflow: failed to mark stale run as failed", "run_id", run.ID, "error", err)
	}

	nodeRuns, err := e.store.ListNodeRuns(ctx, run.ID)
	if err != nil {
		e.logger.Error("workflow: failed to list node runs for stale run", "run_id", run.ID, "error", err)
		return
	}
	for _, nr := range nodeRuns {
		if nr.Status == db.NodeRunStatusPending || nr.Status == db.NodeRunStatusRunning {
			nr.Status = db.NodeRunStatusFailed
			nr.ErrorText = "server restarted"
			nr.FinishedAt = &now
			if err := e.store.UpsertNodeRun(ctx, nr); err != nil {
				e.logger.Error("workflow: failed to mark stale node as failed", "run_id", run.ID, "node_id", nr.NodeID, "error", err)
			}
		}
	}

	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowRunCompleted(events.WorkflowRunEventData{
			RunID:        run.ID,
			WorkflowName: run.WorkflowName,
			ChannelID:    run.ChannelID,
			Status:       string(db.WorkflowRunStatusFailed),
			Error:        run.ErrorText,
		})
	}

	e.logger.Info("workflow: marked stale run as failed", "run_id", run.ID, "workflow", run.WorkflowName)
}

// createRunContext creates a context for workflow execution with an optional
// workflow-level timeout. The returned cancel function must always be called.
func (e *defaultEngine) createRunContext(wfDef *config.WorkflowDef) (context.Context, context.CancelFunc) {
	if wfDef.Timeout != "" {
		if d, err := time.ParseDuration(wfDef.Timeout); err == nil {
			return context.WithTimeout(context.Background(), d)
		}
	}
	return context.WithCancel(context.Background())
}

func generateRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "wfr-" + hex.EncodeToString(b)
}
