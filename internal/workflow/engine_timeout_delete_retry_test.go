package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// --- Timeout tests ---

func (s *EngineSuite) TestNodeTimeoutBashNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "node-timeout",
			Nodes: []config.NodeDef{
				{ID: "slow", Type: config.NodeTypeBash, Script: "sleep 100", Timeout: "100ms"},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	s.bashRunner.On("RunBash", mock.Anything, "sleep 100", "", "").
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done() // block until node timeout cancels context
		}).
		Return("", context.DeadlineExceeded)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "node-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run failure")
	}
}

func (s *EngineSuite) TestNodeTimeoutPromptNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "prompt-timeout",
			Nodes: []config.NodeDef{
				{ID: "slow", Type: config.NodeTypePrompt, Prompt: "Think hard", Timeout: "100ms"},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		}).
		Return(nil, context.DeadlineExceeded)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "prompt-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run failure")
	}
}

func (s *EngineSuite) TestNodeTimeoutLoopNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-timeout",
			Nodes: []config.NodeDef{
				{ID: "loop", Type: config.NodeTypeLoop, Prompt: "Think", MaxIterations: 100, Timeout: "100ms"},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done() // blocks until node timeout fires
		}).
		Return(nil, context.DeadlineExceeded)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run failure")
	}
}

func (s *EngineSuite) TestNodeTimeoutNotTriggeredWhenFast() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "fast-with-timeout",
			Nodes: []config.NodeDef{
				{ID: "fast", Type: config.NodeTypeBash, Script: "echo fast", Timeout: "10s"},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo fast", "", "").Return("fast\n", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "fast-with-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for completion")
	}
}

func (s *EngineSuite) TestNodeTimeoutInvalidDurationIgnored() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "bad-node-timeout",
			Nodes: []config.NodeDef{
				{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok", Timeout: "not-a-duration"},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Return("ok\n", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-node-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for completion")
	}
}

func (s *EngineSuite) TestWorkflowTimeout() {
	s.workflows = []config.WorkflowDef{
		{
			Name:    "wf-timeout",
			Timeout: "100ms",
			Nodes: []config.NodeDef{
				{ID: "slow", Type: config.NodeTypeBash, Script: "sleep 100"},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

	var capturedErr string
	ch := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		switch run.Status {
		case db.WorkflowRunStatusCompleted, db.WorkflowRunStatusFailed, db.WorkflowRunStatusCancelled:
			capturedErr = run.ErrorText
			select {
			case ch <- run.Status:
			default:
			}
		}
	}).Return(nil)

	s.bashRunner.On("RunBash", mock.Anything, "sleep 100", "", "").
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		}).
		Return("", context.DeadlineExceeded)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "wf-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-ch:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
		require.Equal(s.T(), "workflow timeout exceeded", capturedErr)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run failure")
	}
}

func (s *EngineSuite) TestWorkflowTimeoutNotTriggeredWhenFast() {
	s.workflows = []config.WorkflowDef{
		{
			Name:    "fast-wf-timeout",
			Timeout: "10s",
			Nodes: []config.NodeDef{
				{ID: "fast", Type: config.NodeTypeBash, Script: "echo fast"},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo fast", "", "").Return("fast\n", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "fast-wf-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for completion")
	}
}

func (s *EngineSuite) TestCreateRunContextWithTimeout() {
	de := s.engine.(*defaultEngine)

	// No timeout → no deadline.
	ctx, cancel := de.createRunContext(&config.WorkflowDef{Name: "no-timeout"})
	defer cancel()
	_, hasDeadline := ctx.Deadline()
	require.False(s.T(), hasDeadline)

	// Valid timeout → has deadline.
	ctx2, cancel2 := de.createRunContext(&config.WorkflowDef{Name: "with-timeout", Timeout: "5m"})
	defer cancel2()
	deadline, hasDeadline2 := ctx2.Deadline()
	require.True(s.T(), hasDeadline2)
	require.WithinDuration(s.T(), time.Now().Add(5*time.Minute), deadline, 5*time.Second)

	// Invalid timeout → no deadline (falls back to context.WithCancel).
	ctx3, cancel3 := de.createRunContext(&config.WorkflowDef{Name: "bad-timeout", Timeout: "not-a-duration"})
	defer cancel3()
	_, hasDeadline3 := ctx3.Deadline()
	require.False(s.T(), hasDeadline3)
}

// --- DeleteRun ---

func (s *EngineSuite) TestDeleteRunHappyPath() {
	// Deleting a completed run should call DeleteWorkflowRun and broadcast the event.
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(&db.WorkflowRun{
		ID:           "r1",
		WorkflowName: "my-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusCompleted,
	}, nil)
	s.store.On("DeleteWorkflowRun", mock.Anything, "r1").Return(nil)

	err := s.engine.DeleteRun(context.Background(), "r1")
	require.NoError(s.T(), err)

	s.store.AssertCalled(s.T(), "DeleteWorkflowRun", mock.Anything, "r1")

	// Verify the broadcaster received a "deleted" event.
	s.broadcaster.mu.Lock()
	defer s.broadcaster.mu.Unlock()
	require.NotEmpty(s.T(), s.broadcaster.events)
	var found bool
	for _, ev := range s.broadcaster.events {
		if data, ok := ev.(events.WorkflowRunEventData); ok {
			if data.RunID == "r1" && data.Status == "deleted" {
				found = true
				break
			}
		}
	}
	require.True(s.T(), found, "expected a 'deleted' broadcast event for run r1")
}

func (s *EngineSuite) TestDeleteRunNotFound() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "missing").Return(nil, nil)

	err := s.engine.DeleteRun(context.Background(), "missing")
	require.ErrorContains(s.T(), err, "workflow run not found")
}

func (s *EngineSuite) TestDeleteRunGetWorkflowRunError() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(nil, fmt.Errorf("db error"))

	err := s.engine.DeleteRun(context.Background(), "r1")
	require.ErrorContains(s.T(), err, "db error")
}

func (s *EngineSuite) TestDeleteRunDeleteStoreError() {
	// When DeleteWorkflowRun fails the error should be wrapped and returned.
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(&db.WorkflowRun{
		ID:     "r1",
		Status: db.WorkflowRunStatusCompleted,
	}, nil)
	s.store.On("DeleteWorkflowRun", mock.Anything, "r1").Return(fmt.Errorf("disk full"))

	err := s.engine.DeleteRun(context.Background(), "r1")
	require.ErrorContains(s.T(), err, "deleting workflow run")
	require.ErrorContains(s.T(), err, "disk full")
}

func (s *EngineSuite) TestDeleteRunActiveRunCancelledBeforeDelete() {
	// Deleting a running run should cancel it first, then delete.
	s.store.ExpectedCalls = nil
	// GetWorkflowRun is called twice: once in DeleteRun, once in CancelRun.
	s.store.On("GetWorkflowRun", mock.Anything, "r-running").
		Return(&db.WorkflowRun{ID: "r-running", WorkflowName: "wf", ChannelID: "ch1", Status: db.WorkflowRunStatusRunning}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, "r-running").
		Return(&db.WorkflowRun{ID: "r-running", WorkflowName: "wf", ChannelID: "ch1", Status: db.WorkflowRunStatusRunning}, nil).Once()
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("DeleteWorkflowRun", mock.Anything, "r-running").Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	err := s.engine.DeleteRun(context.Background(), "r-running")
	require.NoError(s.T(), err)

	s.store.AssertCalled(s.T(), "DeleteWorkflowRun", mock.Anything, "r-running")
}

func (s *EngineSuite) TestDeleteRunPausedRunCancelledBeforeDelete() {
	// Deleting a paused run should also cancel it first.
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r-paused").
		Return(&db.WorkflowRun{ID: "r-paused", WorkflowName: "wf", ChannelID: "ch2", Status: db.WorkflowRunStatusPaused}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, "r-paused").
		Return(&db.WorkflowRun{ID: "r-paused", WorkflowName: "wf", ChannelID: "ch2", Status: db.WorkflowRunStatusPaused}, nil).Once()
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("DeleteWorkflowRun", mock.Anything, "r-paused").Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	err := s.engine.DeleteRun(context.Background(), "r-paused")
	require.NoError(s.T(), err)

	s.store.AssertCalled(s.T(), "DeleteWorkflowRun", mock.Anything, "r-paused")
}

func (s *EngineSuite) TestDeleteRunNilBroadcaster() {
	// DeleteRun should not panic when broadcaster is nil.
	e := NewEngine(s.store, s.runner, s.bashRunner, nil, func(_, _ string) []config.WorkflowDef {
		return nil
	}, "", config.WorkflowConcurrency{}, slog.Default())

	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r-nobc").Return(&db.WorkflowRun{
		ID:     "r-nobc",
		Status: db.WorkflowRunStatusFailed,
	}, nil)
	s.store.On("DeleteWorkflowRun", mock.Anything, "r-nobc").Return(nil)

	err := e.DeleteRun(context.Background(), "r-nobc")
	require.NoError(s.T(), err)
}

// --- RetryRun ---

func (s *EngineSuite) TestRetryRunHappyPath() {
	// RetryRun on a completed run should start a new run and return its ID.
	s.workflows = []config.WorkflowDef{
		{
			Name:  "retry-wf",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok"}},
		},
	}

	s.store.ExpectedCalls = nil
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// First call returns the original run; subsequent calls from the async DAG
	// (finalizeDAG, updateRunStatus) return a running run.
	s.store.On("GetWorkflowRun", mock.Anything, "original").Return(&db.WorkflowRun{
		ID:           "original",
		WorkflowName: "retry-wf",
		ChannelID:    "ch1",
		DirPath:      "/work",
		Status:       db.WorkflowRunStatusFailed,
		Inputs:       `{"key":"val"}`,
	}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(
		&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil,
	).Maybe()
	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	// Accept UpdateWorkflowRun from the async DAG execution.
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "ch1", "/work").Return("ok", nil)

	newID, err := s.engine.RetryRun(context.Background(), "original")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), newID)
	require.NotEqual(s.T(), "original", newID)
}

func (s *EngineSuite) TestRetryRunNotFound() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "missing").Return(nil, nil)

	_, err := s.engine.RetryRun(context.Background(), "missing")
	require.ErrorContains(s.T(), err, "workflow run not found")
}

func (s *EngineSuite) TestRetryRunGetWorkflowRunError() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(nil, fmt.Errorf("db error"))

	_, err := s.engine.RetryRun(context.Background(), "r1")
	require.ErrorContains(s.T(), err, "looking up run")
}

func (s *EngineSuite) TestRetryRunStillRunning() {
	// Cannot retry a run that is still active.
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r-active").Return(&db.WorkflowRun{
		ID:     "r-active",
		Status: db.WorkflowRunStatusRunning,
	}, nil)

	_, err := s.engine.RetryRun(context.Background(), "r-active")
	require.ErrorContains(s.T(), err, "cannot retry a run that is still")
}

func (s *EngineSuite) TestRetryRunStillPaused() {
	// Cannot retry a run that is paused.
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r-paused").Return(&db.WorkflowRun{
		ID:     "r-paused",
		Status: db.WorkflowRunStatusPaused,
	}, nil)

	_, err := s.engine.RetryRun(context.Background(), "r-paused")
	require.ErrorContains(s.T(), err, "cannot retry a run that is still")
}

func (s *EngineSuite) TestRetryRunInvalidInputsJSON() {
	// If the stored inputs JSON is malformed, RetryRun should return an error.
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r-bad").Return(&db.WorkflowRun{
		ID:           "r-bad",
		WorkflowName: "some-wf",
		Status:       db.WorkflowRunStatusFailed,
		Inputs:       `{INVALID`,
	}, nil)

	_, err := s.engine.RetryRun(context.Background(), "r-bad")
	require.ErrorContains(s.T(), err, "parsing original inputs")
}

func (s *EngineSuite) TestRetryRunWithNoInputs() {
	// RetryRun with an empty Inputs field should work (inputs defaults to nil map).
	s.workflows = []config.WorkflowDef{
		{
			Name:  "no-input-wf",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo hi"}},
		},
	}

	s.store.ExpectedCalls = nil
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// First call returns the original (completed) run; async DAG calls get a running run.
	s.store.On("GetWorkflowRun", mock.Anything, "r-noinput").Return(&db.WorkflowRun{
		ID:           "r-noinput",
		WorkflowName: "no-input-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusCompleted,
		Inputs:       "", // empty — no inputs stored
	}, nil).Once()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(
		&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil,
	).Maybe()
	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.bashRunner.On("RunBash", mock.Anything, "echo hi", "ch1", "").Return("hi", nil)

	newID, err := s.engine.RetryRun(context.Background(), "r-noinput")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), newID)
}

func (s *EngineSuite) TestRetryRunWorkflowNotFound() {
	// When the original workflow definition no longer exists, RetryRun should
	// propagate the error returned by StartRun.
	s.workflows = nil // no workflows defined

	s.store.ExpectedCalls = nil
	// Only one GetWorkflowRun call is made (for the original run); StartRun
	// fails immediately with "workflow not found" before any DB writes.
	s.store.On("GetWorkflowRun", mock.Anything, "r-gone").Return(&db.WorkflowRun{
		ID:           "r-gone",
		WorkflowName: "deleted-wf",
		Status:       db.WorkflowRunStatusFailed,
		Inputs:       "",
	}, nil)

	_, err := s.engine.RetryRun(context.Background(), "r-gone")
	require.ErrorContains(s.T(), err, "workflow not found")
}

// TestAcquireNodeSlotCancelledWhileBlocked covers the second ctx.Done branch in
// acquireNodeSlot: the semaphore is full, the goroutine enters the blocking
// select, and the context is cancelled while it waits.
func TestAcquireNodeSlotCancelledWhileBlocked(t *testing.T) {
	e := &defaultEngine{nodeSem: make(chan struct{}, 1)}
	// Fill the semaphore so the next acquire must wait on the second select.
	e.nodeSem <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		done <- e.acquireNodeSlot(ctx)
	}()

	// Give the goroutine time to reach the blocking select.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case ok := <-done:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("acquireNodeSlot did not return after context cancel")
	}
}

// TestPromptNodeResolvePromptError covers the ResolvePrompt error path in
// executePromptNode: neither prompt nor prompt_path set — ResolvePrompt fails.
func (s *EngineSuite) TestPromptNodeResolvePromptError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "unresolvable-prompt",
			Nodes: []config.NodeDef{{ID: "ask", Type: config.NodeTypePrompt}},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "unresolvable-prompt"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout — run should have failed because prompt could not be resolved")
	}
}

// TestRecoverRunsCheckpointUpdateStatusError covers the updateRunStatus error
// path in executeDAGFromCheckpoint: during recovery of a paused run, the
// "restore to running" write fails and finalizeDAG is invoked with that error.
func (s *EngineSuite) TestRecoverRunsCheckpointUpdateStatusError() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "cp-fail",
			Nodes: []config.NodeDef{
				{ID: "gate", Type: config.NodeTypeApproval, Message: "Go?", Timeout: "10s"},
			},
		},
	}

	pausedRun := &db.WorkflowRun{
		ID:           "wfr-cpfail",
		WorkflowName: "cp-fail",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "gate",
		Inputs:       `{}`,
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-cpfail").Return([]*db.NodeRun{
		{RunID: "wfr-cpfail", NodeID: "gate", Status: db.NodeRunStatusRunning},
	}, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// GetWorkflowRun: first call inside updateRunStatus (success path reads it),
	// second call inside finalizeDAG reads a fresh copy for the terminal write.
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-cpfail").Return(
		&db.WorkflowRun{ID: "wfr-cpfail", WorkflowName: "cp-fail", ChannelID: "ch1", Status: db.WorkflowRunStatusPaused, PausedNodeID: "gate"}, nil,
	)

	// First UpdateWorkflowRun (the restore-to-running write) fails.
	// Subsequent UpdateWorkflowRun calls (the finalize write) succeed and signal done.
	done := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(fmt.Errorf("db write failed")).Once()
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		select {
		case done <- run.Status:
		default:
		}
	}).Return(nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout — recovery should have finalized after updateRunStatus error")
	}
}
