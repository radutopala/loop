package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// --- recovery ---

func (s *EngineSuite) TestRecoverRunsNoStaleRuns() {
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, []db.WorkflowRunStatus{
		db.WorkflowRunStatusRunning, db.WorkflowRunStatusPaused,
	}).Return(nil, nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
}

func (s *EngineSuite) TestRecoverRunsListError() {
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("db down"))

	err := s.engine.RecoverRuns(context.Background())
	require.ErrorContains(s.T(), err, "listing stale runs")
}

func (s *EngineSuite) TestRecoverRunsFailStaleRunning() {
	// A running workflow whose definition is gone should be marked as failed on
	// recovery via failStaleRun (single transactional Store call).
	staleRun := &db.WorkflowRun{
		ID:           "wfr-stale",
		WorkflowName: "test",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       `{}`,
	}
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{staleRun}, nil)
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-stale",
		"server restarted while workflow was running", "server restarted", mock.Anything).Return(nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "MarkRunFailedWithStaleNodes",
		mock.Anything, "wfr-stale", "server restarted while workflow was running", "server restarted", mock.Anything)
}

func (s *EngineSuite) TestRecoverRunsPausedWorkflowNotFound() {
	// When the workflow definition is no longer available, the paused run should be failed.
	pausedRun := &db.WorkflowRun{
		ID:           "wfr-paused-nf",
		WorkflowName: "deleted-workflow",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "gate",
		Inputs:       `{}`,
	}
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-paused-nf", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.workflows = nil // no workflows defined

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "MarkRunFailedWithStaleNodes", mock.Anything, "wfr-paused-nf", mock.Anything, mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestRecoverRunsPausedNodeRunListError() {
	// When listing node runs fails, the paused run should be failed.
	pausedRun := &db.WorkflowRun{
		ID:           "wfr-paused-nle",
		WorkflowName: "test-wf",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "gate",
		Inputs:       `{}`,
	}
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-paused-nle").Return(nil, fmt.Errorf("node list error"))
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-paused-nle", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.workflows = []config.WorkflowDef{
		{
			Name:  "test-wf",
			Nodes: []config.NodeDef{{ID: "gate", Type: config.NodeTypeApproval, Message: "Go?"}},
		},
	}

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "MarkRunFailedWithStaleNodes", mock.Anything, "wfr-paused-nle", mock.Anything, mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestRecoverRunsPausedBadInputs() {
	// When inputs JSON is malformed, the paused run should be failed.
	pausedRun := &db.WorkflowRun{
		ID:           "wfr-paused-bad",
		WorkflowName: "test-wf",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "gate",
		Inputs:       `{INVALID`,
	}
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-paused-bad").Return(nil, nil)
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-paused-bad", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.workflows = []config.WorkflowDef{
		{
			Name:  "test-wf",
			Nodes: []config.NodeDef{{ID: "gate", Type: config.NodeTypeApproval, Message: "Go?"}},
		},
	}

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "MarkRunFailedWithStaleNodes", mock.Anything, "wfr-paused-bad", mock.Anything, mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestRecoverRunsPausedResumeApproval() {
	// A paused workflow with a completed bash node and a paused approval node
	// should be recovered: the approval node re-enters the wait loop, and after
	// resume, downstream nodes execute.
	s.workflows = []config.WorkflowDef{
		{
			Name: "recover-wf",
			Nodes: []config.NodeDef{
				{ID: "check", Type: config.NodeTypeBash, Script: "echo pre"},
				{ID: "approve", Type: config.NodeTypeApproval, DependsOn: []string{"check"}, Message: "Approve?", Timeout: "10s"},
				{ID: "deploy", Type: config.NodeTypeBash, DependsOn: []string{"approve"}, Script: "echo deploying"},
			},
		},
	}

	pausedRun := &db.WorkflowRun{
		ID:           "wfr-recover",
		WorkflowName: "recover-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "approve",
		Inputs:       `{}`,
	}

	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-recover", NodeID: "check", Status: db.NodeRunStatusSuccess, Output: "pre"},
		{RunID: "wfr-recover", NodeID: "approve", Status: db.NodeRunStatusRunning},
		{RunID: "wfr-recover", NodeID: "deploy", Status: db.NodeRunStatusPending},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-recover").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// GetWorkflowRun: used by updateRunStatus and finalizeDAG. Return with
	// PausedNodeID="approve" so ResumeRun can form the composite key.
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-recover").Return(
		&db.WorkflowRun{ID: "wfr-recover", Status: db.WorkflowRunStatusRunning, PausedNodeID: "approve", WorkflowName: "recover-wf", ChannelID: "ch1"}, nil,
	)

	done := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		if run.Status == db.WorkflowRunStatusCompleted || run.Status == db.WorkflowRunStatusFailed {
			select {
			case done <- run.Status:
			default:
			}
		}
	}).Return(nil)

	s.bashRunner.On("RunBash", mock.Anything, "echo deploying", "ch1", "").Return("deployed", nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	// Wait for the approval node to re-enter the wait loop.
	time.Sleep(200 * time.Millisecond)

	// Resume the approval.
	err = s.engine.ResumeRun(context.Background(), "wfr-recover", "approved")
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered run to complete")
	}

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo deploying", "ch1", "")
}

func (s *EngineSuite) TestFailStaleRunStoreError() {
	// MarkRunFailedWithStaleNodes error is logged, not fatal.
	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{
		{ID: "wfr-err", WorkflowName: "wf", Status: db.WorkflowRunStatusRunning},
	}, nil)
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-err", mock.Anything, mock.Anything, mock.Anything).
		Return(fmt.Errorf("mark failed"))

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
}

func (s *EngineSuite) TestRecoverRunsCheckpointWithStaleRunningNode() {
	// A paused workflow where a non-paused node was running at restart time.
	// The stale running node should be marked as failed, and the DAG should
	// complete as failed because of the pre-failed node.
	s.workflows = []config.WorkflowDef{
		{
			Name: "stale-node-wf",
			Nodes: []config.NodeDef{
				{ID: "a", Type: config.NodeTypeBash, Script: "echo a"},
				{ID: "b", Type: config.NodeTypeBash, Script: "echo b"},
				{ID: "approve", Type: config.NodeTypeApproval, DependsOn: []string{"a"}, Message: "Go?", Timeout: "5s"},
				{ID: "final", Type: config.NodeTypeBash, DependsOn: []string{"approve", "b"}, Script: "echo done"},
			},
		},
	}

	pausedRun := &db.WorkflowRun{
		ID:           "wfr-stalenode",
		WorkflowName: "stale-node-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "approve",
		Inputs:       `{}`,
	}

	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-stalenode", NodeID: "a", Status: db.NodeRunStatusSuccess, Output: "a"},
		{RunID: "wfr-stalenode", NodeID: "b", Status: db.NodeRunStatusRunning},       // stale running
		{RunID: "wfr-stalenode", NodeID: "approve", Status: db.NodeRunStatusRunning}, // paused node
		{RunID: "wfr-stalenode", NodeID: "final", Status: db.NodeRunStatusPending},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-stalenode").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-stalenode").Return(
		&db.WorkflowRun{ID: "wfr-stalenode", Status: db.WorkflowRunStatusRunning, PausedNodeID: "approve", WorkflowName: "stale-node-wf", ChannelID: "ch1"}, nil,
	)

	done := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		if run.Status == db.WorkflowRunStatusCompleted || run.Status == db.WorkflowRunStatusFailed {
			select {
			case done <- run.Status:
			default:
			}
		}
	}).Return(nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	// Wait for approval node to pause.
	time.Sleep(200 * time.Millisecond)

	// Resume approval.
	err = s.engine.ResumeRun(context.Background(), "wfr-stalenode", "ok")
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		// The DAG should fail because node "b" was pre-failed.
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered run to complete")
	}
}

func (s *EngineSuite) TestFailStaleRunNilBroadcaster() {
	// Verify failStaleRun works when broadcaster is nil.
	e := NewEngine(s.store, s.runner, s.bashRunner, nil, func(_, _ string) []config.WorkflowDef {
		return nil
	}, "", config.WorkflowConcurrency{}, slog.Default())

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{
		{ID: "wfr-nobc", WorkflowName: "wf", Status: db.WorkflowRunStatusRunning},
	}, nil)
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-nobc", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err := e.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
}

func (s *EngineSuite) TestRunConcurrencyLimit() {
	// With MaxConcurrentRuns=1, the second StartRun should block until the first completes.
	s.workflows = []config.WorkflowDef{
		{Name: "wf", Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo hi"}}},
	}

	e := NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, func(_, _ string) []config.WorkflowDef {
		return s.workflows
	}, "", config.WorkflowConcurrency{MaxConcurrentRuns: 1}, slog.Default())

	// Block the bash runner so the first run doesn't complete immediately.
	bashBlock := make(chan struct{})
	s.bashRunner.On("RunBash", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { <-bashBlock }).
		Return("ok", nil)
	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)

	// Start first run — should acquire the semaphore slot.
	runID1, err := e.StartRun(context.Background(), StartRunOptions{WorkflowName: "wf"})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), runID1)

	// Start second run in a goroutine — should block on the semaphore.
	run2Started := make(chan string, 1)
	go func() {
		id, _ := e.StartRun(context.Background(), StartRunOptions{WorkflowName: "wf"})
		run2Started <- id
	}()

	// Give time for run2 to attempt; it should NOT start yet.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-run2Started:
		s.T().Fatal("second run should not start while first is running")
	default:
		// expected
	}

	// Unblock the first run.
	close(bashBlock)

	// The second run should now start.
	select {
	case id := <-run2Started:
		require.NotEmpty(s.T(), id)
	case <-time.After(5 * time.Second):
		s.T().Fatal("second run did not start after first completed")
	}
}

func (s *EngineSuite) TestNodeConcurrencyLimit() {
	// With MaxConcurrentNodes=1, parallel nodes should execute sequentially.
	s.workflows = []config.WorkflowDef{
		{Name: "wf", Nodes: []config.NodeDef{
			{ID: "a", Type: config.NodeTypeBash, Script: "echo a"},
			{ID: "b", Type: config.NodeTypeBash, Script: "echo b"},
		}},
	}

	e := NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, func(_, _ string) []config.WorkflowDef {
		return s.workflows
	}, "", config.WorkflowConcurrency{MaxConcurrentNodes: 1}, slog.Default())

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	s.bashRunner.On("RunBash", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			cur := concurrent.Add(1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			concurrent.Add(-1)
		}).
		Return("ok", nil)
	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

	done := s.waitForRunStatus()
	_, err := e.StartRun(context.Background(), StartRunOptions{WorkflowName: "wf"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timed out waiting for run to complete")
	}

	require.Equal(s.T(), int32(1), maxConcurrent.Load(), "at most 1 node should run concurrently")
}

func (s *EngineSuite) TestRecoverPausedRunSemaphoreFull() {
	// When the run semaphore is full, recovery should fail the paused run instead.
	s.workflows = []config.WorkflowDef{
		{Name: "wf", Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeApproval, Message: "approve?"}}},
	}

	e := NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, func(_, _ string) []config.WorkflowDef {
		return s.workflows
	}, "", config.WorkflowConcurrency{MaxConcurrentRuns: 1}, slog.Default())

	// Fill the semaphore so recovery can't acquire.
	de := e.(*defaultEngine)
	de.runSem <- struct{}{}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{
		{ID: "wfr-paused", WorkflowName: "wf", Status: db.WorkflowRunStatusPaused, PausedNodeID: "n1", Inputs: "{}"},
	}, nil)
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-paused").Return(
		&db.WorkflowRun{ID: "wfr-paused", WorkflowName: "wf", Status: db.WorkflowRunStatusPaused}, nil,
	)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-paused").Return(nil, nil)
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-paused", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err := e.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	// Verify the run was failed via the atomic helper, not recovered.
	s.store.AssertCalled(s.T(), "MarkRunFailedWithStaleNodes", mock.Anything, "wfr-paused", mock.Anything, mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestStartRunSemaphoreContextCancelled() {
	// If context is cancelled while waiting for the run semaphore, StartRun should return.
	s.workflows = []config.WorkflowDef{
		{Name: "wf", Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo"}}},
	}

	e := NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, func(_, _ string) []config.WorkflowDef {
		return s.workflows
	}, "", config.WorkflowConcurrency{MaxConcurrentRuns: 1}, slog.Default())

	// Fill the semaphore.
	de := e.(*defaultEngine)
	de.runSem <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := e.StartRun(ctx, StartRunOptions{WorkflowName: "wf"})
	require.Error(s.T(), err)
	require.ErrorIs(s.T(), err, context.Canceled)
}

func (s *EngineSuite) TestNodeSlotCancelledDuringDAG() {
	// When a child node is dispatched AFTER the context is cancelled (via its
	// parent finishing on cancel), its goroutine hits the fast-path in
	// acquireNodeSlot and exits via the ctx.Done branch deterministically —
	// covering the "acquireNodeSlot returned false" return in dag.go.
	s.workflows = []config.WorkflowDef{
		{Name: "wf", Nodes: []config.NodeDef{
			{ID: "slow", Type: config.NodeTypeBash, Script: "sleep 10"},
			{ID: "child", Type: config.NodeTypeBash, Script: "echo never", DependsOn: []string{"slow"}},
		}},
	}

	e := NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, func(_, _ string) []config.WorkflowDef {
		return s.workflows
	}, "", config.WorkflowConcurrency{MaxConcurrentNodes: 1}, slog.Default())

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

	bashStarted := make(chan struct{}, 1)
	s.bashRunner.On("RunBash", mock.Anything, "sleep 10", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			bashStarted <- struct{}{}
			<-args.Get(0).(context.Context).Done()
		}).
		Return("", context.Canceled)
	s.bashRunner.On("RunBash", mock.Anything, "echo never", mock.Anything, mock.Anything).Return("never", nil)

	done := s.waitForRunStatus()
	runID, err := e.StartRun(context.Background(), StartRunOptions{WorkflowName: "wf"})
	require.NoError(s.T(), err)

	<-bashStarted
	err = e.CancelRun(context.Background(), runID)
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout — run should have been cancelled")
	}

	// child's goroutine is dispatched after slow finishes (post-cancel) and
	// must exit at acquireNodeSlot without calling RunBash.
	s.bashRunner.AssertNotCalled(s.T(), "RunBash", mock.Anything, "echo never", mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestNodeSlotCancelledDuringCheckpoint() {
	// Covers the same acquireNodeSlot-cancelled branch, but on the
	// executeDAGFromCheckpoint (resume) path: a paused approval node is
	// recovered and then cancelled, causing its dependent child to be
	// dispatched after cancel — the child exits via the ctx.Done fast-path
	// in acquireNodeSlot, covering the return branch in dag.go.
	s.workflows = []config.WorkflowDef{
		{Name: "ck-wf", Nodes: []config.NodeDef{
			{ID: "approve", Type: config.NodeTypeApproval, Message: "ok?", Timeout: "1h"},
			{ID: "child", Type: config.NodeTypeBash, Script: "echo never", DependsOn: []string{"approve"}},
		}},
	}

	e := NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, func(_, _ string) []config.WorkflowDef {
		return s.workflows
	}, "", config.WorkflowConcurrency{MaxConcurrentNodes: 1}, slog.Default())

	pausedRun := &db.WorkflowRun{
		ID:           "wfr-ck",
		WorkflowName: "ck-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "approve",
		Inputs:       `{}`,
	}
	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-ck", NodeID: "approve", Status: db.NodeRunStatusRunning},
		{RunID: "wfr-ck", NodeID: "child", Status: db.NodeRunStatusPending},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-ck").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-ck").Return(
		&db.WorkflowRun{ID: "wfr-ck", Status: db.WorkflowRunStatusRunning, PausedNodeID: "approve", WorkflowName: "ck-wf", ChannelID: "ch1"}, nil,
	)

	paused := make(chan struct{}, 1)
	terminal := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		switch run.Status {
		case db.WorkflowRunStatusPaused:
			select {
			case paused <- struct{}{}:
			default:
			}
		case db.WorkflowRunStatusCompleted, db.WorkflowRunStatusFailed, db.WorkflowRunStatusCancelled:
			select {
			case terminal <- run.Status:
			default:
			}
		}
	}).Return(nil)

	err := e.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered approval node to pause")
	}

	err = e.CancelRun(context.Background(), "wfr-ck")
	require.NoError(s.T(), err)

	select {
	case <-terminal:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for cancelled run to finalize")
	}

	s.bashRunner.AssertNotCalled(s.T(), "RunBash", mock.Anything, "echo never", mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestFinalizeDAGAlreadyTerminal() {
	// When finalizeDAG reads a run that has already been marked terminal
	// (e.g. by CancelRun), it should use the DB's status for the broadcast
	// instead of overwriting it. This covers the "default" branch in finalizeDAG.
	s.workflows = []config.WorkflowDef{
		{Name: "wf", Nodes: []config.NodeDef{
			{ID: "fast", Type: config.NodeTypeBash, Script: "echo done"},
		}},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.bashRunner.On("RunBash", mock.Anything, "echo done", mock.Anything, mock.Anything).Return("done", nil)

	// Override default GetWorkflowRun to return "cancelled" — simulating
	// CancelRun having already written a terminal status before finalizeDAG runs.
	for i, call := range s.store.ExpectedCalls {
		if call.Method == "GetWorkflowRun" {
			s.store.ExpectedCalls = append(s.store.ExpectedCalls[:i], s.store.ExpectedCalls[i+1:]...)
			break
		}
	}
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(
		&db.WorkflowRun{Status: db.WorkflowRunStatusCancelled, ErrorText: "cancelled by user"}, nil,
	).Maybe()

	// finalizeDAG will skip UpdateWorkflowRun (already terminal), so signal
	// completion via the BroadcastWorkflowRunCompleted event instead.
	completedCh := make(chan string, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		if run.Status != db.WorkflowRunStatusRunning {
			select {
			case completedCh <- string(run.Status):
			default:
			}
		}
	}).Return(nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "wf"})
	require.NoError(s.T(), err)

	// Wait for the broadcast — finalizeDAG emits BroadcastWorkflowRunCompleted
	// even when it skips the DB write.
	require.Eventually(s.T(), func() bool {
		s.broadcaster.mu.Lock()
		defer s.broadcaster.mu.Unlock()
		for _, ev := range s.broadcaster.events {
			if data, ok := ev.(events.WorkflowRunEventData); ok && data.Status == string(db.WorkflowRunStatusCancelled) {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "expected cancelled broadcast from finalizeDAG")
}

func (s *EngineSuite) TestApprovalNodeResumeStatusWriteError() {
	// If updateRunStatus fails when restoring running status after resume,
	// the approval node should return an error and the run should fail.
	s.workflows = []config.WorkflowDef{
		{
			Name: "approval-resume-err",
			Nodes: []config.NodeDef{
				{ID: "approve", Type: config.NodeTypeApproval, Message: "Approve?", Timeout: "10s"},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Clear default GetWorkflowRun mock.
	for _, call := range s.store.ExpectedCalls {
		if call.Method == "GetWorkflowRun" {
			call.Unset()
		}
	}

	var pauseWritten atomic.Bool
	// Call sequence for GetWorkflowRun:
	// 1. updateRunStatus (pause) — succeed
	// 2. ResumeRun — succeed (needs PausedNodeID)
	// 3. updateRunStatus (resume) — fail
	// 4. finalizeDAG — succeed
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) { pauseWritten.Store(true) }).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusRunning, PausedNodeID: "approve"}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusPaused, PausedNodeID: "approve"}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("db went away")).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil).Maybe()

	done := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		if run.Status == db.WorkflowRunStatusFailed || run.Status == db.WorkflowRunStatusCompleted {
			select {
			case done <- run.Status:
			default:
			}
		}
	}).Return(nil)

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-resume-err"})
	require.NoError(s.T(), err)

	// Wait for the approval node to pause.
	require.Eventually(s.T(), pauseWritten.Load, 5*time.Second, 10*time.Millisecond)

	// Resume — this should trigger the error path in updateRunStatus.
	err = s.engine.ResumeRun(context.Background(), runID, "looks good")
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout — run should have failed due to resume status write error")
	}
}
