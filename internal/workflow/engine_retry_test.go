package workflow

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// --- retry ---

func (s *EngineSuite) TestRetryBashNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "retry-test",
			Nodes: []config.NodeDef{
				{ID: "flaky", Type: config.NodeTypeBash, Script: "flaky-cmd",
					Retry: &config.RetryConfig{MaxRetries: 2, BackoffBase: "10ms", BackoffMax: "50ms"}},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	// First call fails, second succeeds.
	s.bashRunner.On("RunBash", mock.Anything, "flaky-cmd", "", "").Return("", fmt.Errorf("flaky")).Once()
	s.bashRunner.On("RunBash", mock.Anything, "flaky-cmd", "", "").Return("success", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-test"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// Verify at least 2 calls were made (first fails, second succeeds).
	s.bashRunner.AssertNumberOfCalls(s.T(), "RunBash", 2)
}

func (s *EngineSuite) TestRetryExhausted() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "retry-exhausted",
			Nodes: []config.NodeDef{
				{ID: "fail", Type: config.NodeTypeBash, Script: "fail-cmd",
					Retry: &config.RetryConfig{MaxRetries: 1, BackoffBase: "10ms"}},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "fail-cmd", "", "").Return("", fmt.Errorf("always fails"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-exhausted"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestRetryNoConfig() {
	// Without retry config, failure on first attempt.
	s.workflows = []config.WorkflowDef{
		{
			Name:  "no-retry",
			Nodes: []config.NodeDef{{ID: "fail", Type: config.NodeTypeBash, Script: "fail"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "fail", "", "").Return("", fmt.Errorf("error")).Once()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "no-retry"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)

	// Only one attempt.
	s.bashRunner.AssertNumberOfCalls(s.T(), "RunBash", 1)
}

func (s *EngineSuite) TestLoopNodeCancelledDuringIteration() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-cancel",
			Nodes: []config.NodeDef{
				{ID: "loop", Type: config.NodeTypeLoop, Prompt: "Again", MaxIterations: 100},
			},
		},
	}

	s.expectRunPersistence()
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)

	// Use one-shot returns so CancelRun and finalizeDAG get distinct pointers
	// (avoids data race on the shared mock return value).
	for _, call := range s.store.ExpectedCalls {
		if call.Method == "GetWorkflowRun" {
			call.Unset()
		}
	}
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil).Maybe()

	var callCount atomic.Int32
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callCount.Add(1)
		time.Sleep(5 * time.Millisecond) // slow down so cancel can interrupt
	}).Return(&agent.AgentResponse{Response: "ok"}, nil)

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-cancel"})
	require.NoError(s.T(), err)

	// Let a few iterations run.
	time.Sleep(50 * time.Millisecond)

	err = s.engine.CancelRun(context.Background(), runID)
	require.NoError(s.T(), err)

	// Give time for cancellation to propagate.
	time.Sleep(200 * time.Millisecond)

	// Should have stopped before 100 iterations.
	require.Less(s.T(), int(callCount.Load()), 100)
}

func (s *EngineSuite) TestApprovalNodeCancelledWhileWaiting() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "approval-cancel",
			Nodes: []config.NodeDef{
				{ID: "approve", Type: config.NodeTypeApproval, Message: "Approve?", Timeout: "1h"},
			},
		},
	}

	s.expectRunPersistence()
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)

	// Use one-shot returns so CancelRun and finalizeDAG get distinct pointers.
	for _, call := range s.store.ExpectedCalls {
		if call.Method == "GetWorkflowRun" {
			call.Unset()
		}
	}
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusPaused}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusPaused}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusPaused}, nil).Maybe()

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-cancel"})
	require.NoError(s.T(), err)

	// Wait for approval node to pause.
	time.Sleep(200 * time.Millisecond)

	// Cancel while waiting for approval.
	err = s.engine.CancelRun(context.Background(), runID)
	require.NoError(s.T(), err)

	// Give time for cancellation to propagate.
	time.Sleep(200 * time.Millisecond)
}

func (s *EngineSuite) TestApprovalNodePauseStatusWriteError() {
	// If updateRunStatus fails when trying to set paused status, the approval
	// node should fail rather than blocking forever.
	s.workflows = []config.WorkflowDef{
		{
			Name: "approval-db-err",
			Nodes: []config.NodeDef{
				{ID: "approve", Type: config.NodeTypeApproval, Message: "Approve?", Timeout: "10s"},
			},
		},
	}

	s.expectRunPersistence()
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	for _, call := range s.store.ExpectedCalls {
		if call.Method == "GetWorkflowRun" {
			call.Unset()
		}
	}
	// First GetWorkflowRun call (from updateRunStatus in approval pause) fails.
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("db connection lost")).Once()
	// Subsequent calls (from finalizeDAG) succeed so the final status can be written.
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

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-db-err"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestResumeRunAlreadyResumed() {
	// Manually set up a pending approval and resume it twice.
	// ResumeRun reads PausedNodeID from DB to form composite key "run:node".
	s.store.ExpectedCalls = nil
	e := s.engine.(*defaultEngine)
	ch := make(chan string, 1)
	e.pendingApprovals.Store("test-run:gate", ch)
	s.store.On("GetWorkflowRun", mock.Anything, "test-run").Return(
		&db.WorkflowRun{ID: "test-run", Status: db.WorkflowRunStatusPaused, PausedNodeID: "gate"}, nil,
	).Maybe()

	// First resume succeeds.
	err := s.engine.ResumeRun(context.Background(), "test-run", "ok")
	require.NoError(s.T(), err)

	// Second resume fails — channel already has a value.
	err = s.engine.ResumeRun(context.Background(), "test-run", "again")
	require.ErrorContains(s.T(), err, "approval already resumed")

	e.pendingApprovals.Delete("test-run:gate")
}

func (s *EngineSuite) TestRetryBackoffMaxCapped() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "retry-cap",
			Nodes: []config.NodeDef{
				{ID: "fail", Type: config.NodeTypeBash, Script: "fail",
					Retry: &config.RetryConfig{MaxRetries: 3, BackoffBase: "5ms", BackoffMax: "10ms"}},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "fail", "", "").Return("", fmt.Errorf("error"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-cap"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)

	// 1 initial + 3 retries = 4 calls.
	s.bashRunner.AssertNumberOfCalls(s.T(), "RunBash", 4)
}

func (s *EngineSuite) TestRetryUpsertNodeRunError() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "retry-upsert-err",
			Nodes: []config.NodeDef{
				{ID: "fail", Type: config.NodeTypeBash, Script: "fail",
					Retry: &config.RetryConfig{MaxRetries: 1, BackoffBase: "1ms"}},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// First UpsertNodeRun (running attempt 1) succeeds; the retry-attempt
	// update fails to cover the error log path in executeWithRetry.
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil).Once()
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(fmt.Errorf("upsert failed"))
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "fail", "", "").Return("", fmt.Errorf("error"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-upsert-err"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestRetryCancelledDuringBackoff() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "retry-cancel",
			Nodes: []config.NodeDef{
				{ID: "fail", Type: config.NodeTypeBash, Script: "fail",
					Retry: &config.RetryConfig{MaxRetries: 5, BackoffBase: "10s"}},
			},
		},
	}

	s.expectRunPersistence()
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)

	// Use one-shot returns so CancelRun and finalizeDAG get distinct pointers.
	for _, call := range s.store.ExpectedCalls {
		if call.Method == "GetWorkflowRun" {
			call.Unset()
		}
	}
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).
		Return(&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil).Maybe()

	s.bashRunner.On("RunBash", mock.Anything, "fail", "", "").Return("", fmt.Errorf("error"))

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-cancel"})
	require.NoError(s.T(), err)

	// Wait for first failure + start of backoff delay.
	time.Sleep(200 * time.Millisecond)

	err = s.engine.CancelRun(context.Background(), runID)
	require.NoError(s.T(), err)

	// Give time for cancellation.
	time.Sleep(200 * time.Millisecond)
}

func (s *EngineSuite) TestUpdateRunStatusGetError() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(nil, fmt.Errorf("db down"))

	e := s.engine.(*defaultEngine)
	// Should return an error and log it but not panic.
	err := e.updateRunStatus(context.Background(), "r1", db.WorkflowRunStatusPaused, "n1")
	require.Error(s.T(), err)
}

func (s *EngineSuite) TestUpdateRunStatusGetNil() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(nil, nil)

	e := s.engine.(*defaultEngine)
	err := e.updateRunStatus(context.Background(), "r1", db.WorkflowRunStatusPaused, "n1")
	require.Error(s.T(), err)
}

func (s *EngineSuite) TestUpdateRunStatusUpdateError() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(&db.WorkflowRun{
		ID: "r1", Status: db.WorkflowRunStatusRunning,
	}, nil)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(fmt.Errorf("write failed"))

	e := s.engine.(*defaultEngine)
	err := e.updateRunStatus(context.Background(), "r1", db.WorkflowRunStatusPaused, "n1")
	require.Error(s.T(), err)
}

func (s *EngineSuite) TestExecuteDAGFinalWriteGetError() {
	// When GetWorkflowRun fails during the final status write in executeDAG,
	// the error is logged but the run completes.
	s.workflows = []config.WorkflowDef{
		{
			Name:  "final-err",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok"}},
		},
	}

	s.store.ExpectedCalls = nil
	s.expectRunPersistence()
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// GetWorkflowRun returns error — the final write in executeDAG will log it.
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("db gone"))
	// UpdateWorkflowRun may still be called; accept it.
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)

	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "final-err"})
	require.NoError(s.T(), err)

	// Wait for DAG to finish (logs error instead of panicking).
	time.Sleep(300 * time.Millisecond)
}
