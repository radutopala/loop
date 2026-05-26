package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// --- loop body (child execution) tests ---

// TestExecuteLoopWithBodyRunsChildrenInOrder verifies a body with two bash
// children runs both children, in declaration order, every iteration up to
// max_iterations when the condition never fires.
func (s *EngineSuite) TestExecuteLoopWithBodyRunsChildrenInOrder() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-body",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 2,
					Body: []*config.NodeDef{
						{ID: "first", Type: config.NodeTypeBash, Script: "echo first"},
						{ID: "second", Type: config.NodeTypeBash, Script: "echo second"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	var firstCalls, secondCalls atomic.Int32
	s.bashRunner.On("RunBash", mock.Anything, "echo first", "", "").Return("ok-1", nil).Run(func(_ mock.Arguments) { firstCalls.Add(1) })
	s.bashRunner.On("RunBash", mock.Anything, "echo second", "", "").Return("ok-2", nil).Run(func(_ mock.Arguments) { secondCalls.Add(1) })

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-body"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	require.Equal(s.T(), int32(2), firstCalls.Load())
	require.Equal(s.T(), int32(2), secondCalls.Load())
}

// TestExecuteLoopBodyBreaksOnReviewNoComments verifies a review body child
// emitting `{"no_comments":true}` flips `.Review.NoComments` and the loop's
// condition exits before max_iterations.
func (s *EngineSuite) TestExecuteLoopBodyBreaksOnReviewNoComments() {
	s.workflows = []config.WorkflowDef{
		{
			// Must match a name in reviewParsedWorkflows; otherwise the loop-body
			// executor skips parseReviewOutput and the condition never sees
			// .Review.NoComments flip true.
			Name: "review-loop",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 5,
					Condition:     "{{ if .Review.NoComments }}true{{ end }}",
					Body: []*config.NodeDef{
						{ID: "review", Type: config.NodeTypeBash, Script: "loop review run"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	var calls atomic.Int32
	s.bashRunner.On("RunBash", mock.Anything, "loop review run", "", "").Return(`{"status":"ready","no_comments":true,"comments":[]}`, nil).Run(func(_ mock.Arguments) { calls.Add(1) })

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "review-loop"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	require.Equal(s.T(), int32(1), calls.Load(), "loop should exit after one iteration when no_comments=true")
}

// TestExecuteLoopBodyBreaksOnSameAsPrev verifies two iterations with the same
// comment IDs flips SameAsPrev and the loop exits via the
// `{{ or .Review.NoComments .Review.SameAsPrev }}` condition.
func (s *EngineSuite) TestExecuteLoopBodyBreaksOnSameAsPrev() {
	s.workflows = []config.WorkflowDef{
		{
			// review-fix-loop is in reviewParsedWorkflows; the gate would
			// otherwise skip parseReviewOutput and SameAsPrev stays false.
			Name: "review-fix-loop",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 5,
					Condition:     "{{ if or .Review.NoComments .Review.SameAsPrev }}true{{ end }}",
					Body: []*config.NodeDef{
						{ID: "review", Type: config.NodeTypeBash, Script: "loop review run"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	var calls atomic.Int32
	// Same comment ID set every call — first iter sets PrevIDs=nil so
	// SameAsPrev=false; second iter sees PrevIDs=["x"] equal to current → break.
	s.bashRunner.On("RunBash", mock.Anything, "loop review run", "", "").
		Return(`{"status":"ready","no_comments":false,"comments":[{"id":"x"}]}`, nil).
		Run(func(_ mock.Arguments) { calls.Add(1) })

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "review-fix-loop"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	require.Equal(s.T(), int32(2), calls.Load(), "loop should exit after second iteration when comment IDs match")
}

// TestExecuteLoopBodyChildErrorPropagates verifies that a body child failure
// stops the loop and fails the run, with no further iterations.
func (s *EngineSuite) TestExecuteLoopBodyChildErrorPropagates() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-err",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 3,
					Body: []*config.NodeDef{
						{ID: "boom", Type: config.NodeTypeBash, Script: "false"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	var calls atomic.Int32
	s.bashRunner.On("RunBash", mock.Anything, "false", "", "").
		Return("", fmt.Errorf("bash failed")).
		Run(func(_ mock.Arguments) { calls.Add(1) })

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-err"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	require.Equal(s.T(), int32(1), calls.Load(), "loop should stop on first child error")
}

// TestExecuteLoopBodyWhenGatesChild verifies a child gated by `when` is
// skipped when the template returns false. The skipped child must still emit
// a Skipped node_run row.
func (s *EngineSuite) TestExecuteLoopBodyWhenGatesChild() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-when",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 1,
					Body: []*config.NodeDef{
						{ID: "always", Type: config.NodeTypeBash, Script: "echo always"},
						{ID: "skipme", Type: config.NodeTypeBash, When: "{{ if false }}true{{ end }}", Script: "echo never"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	skippedCh := make(chan struct{}, 1)
	s.store.On("UpsertNodeRun", mock.Anything, mock.MatchedBy(func(nr *db.NodeRun) bool {
		return nr != nil && nr.NodeID == "skipme" && nr.Status == db.NodeRunStatusSkipped
	})).Run(func(_ mock.Arguments) {
		select {
		case skippedCh <- struct{}{}:
		default:
		}
	}).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo always", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-when"})
	require.NoError(s.T(), err)

	select {
	case <-skippedCh:
	case <-time.After(5 * time.Second):
		s.T().Fatal("expected skipme node to be persisted as skipped")
	}

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	// The skipped child's script should never have been invoked.
	s.bashRunner.AssertNotCalled(s.T(), "RunBash", mock.Anything, "echo never", "", "")
}

// TestExecuteLoopBodyUnknownChildTypeFailsAtStartRun verifies the validator
// rejects unsupported body-child types at StartRun for known-bad shapes.
func (s *EngineSuite) TestExecuteLoopBodyUnknownChildTypeFailsAtStartRun() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-bad-child",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 1,
					Body: []*config.NodeDef{
						{ID: "bad", Type: "weird"},
					},
				},
			},
		},
	}

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-bad-child"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "weird")
}

// TestExecuteLoopBodyUnknownChildTypeExecutorDefault drives the executor's
// defensive `default` branch directly. The resume path
// (executeDAGFromCheckpoint) replays a pinned workflow_def without re-running
// validateWorkflowDef, so a stored definition with an unsupported body-child
// type (manual DB edit, pre-validator install) can reach the executor — the
// default branch must surface a real error rather than silently persisting
// the child as Success with empty output.
func (s *EngineSuite) TestExecuteLoopBodyUnknownChildTypeExecutorDefault() {
	e := s.engine.(*defaultEngine)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

	loopNode := &config.NodeDef{
		ID:   "loop",
		Type: config.NodeTypeLoop,
		Body: []*config.NodeDef{{ID: "bad", Type: "weird"}},
	}
	run := &db.WorkflowRun{ID: "r1"}
	runCtx := &RunContext{}
	var mu sync.Mutex

	output, err := e.executeLoopBody(context.Background(), run, loopNode, runCtx, &mu, 0)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unsupported body child type")
	require.Contains(s.T(), err.Error(), "weird")
	require.Empty(s.T(), output)
}

// TestLoopBodyMaxIterFromInputs verifies the loop reads max_iterations from
// runCtx.Inputs when the NodeDef itself doesn't pin a value. The FE's
// max-iter input drives the cap through this path.
func (s *EngineSuite) TestLoopBodyMaxIterFromInputs() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-input-cap",
			Nodes: []config.NodeDef{
				{
					ID:   "loop",
					Type: config.NodeTypeLoop,
					Body: []*config.NodeDef{
						{ID: "tick", Type: config.NodeTypeBash, Script: "echo tick"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	var calls atomic.Int32
	s.bashRunner.On("RunBash", mock.Anything, "echo tick", "", "").
		Return("ok", nil).
		Run(func(_ mock.Arguments) { calls.Add(1) })

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{
		WorkflowName: "loop-input-cap",
		Inputs:       map[string]string{"max_iterations": "2"},
	})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	require.Equal(s.T(), int32(2), calls.Load())
}

// TestLoopBodyMaxIterInvalidInputFallsBackToDefault verifies that a non-numeric
// max_iterations input falls back to the default of 10 (or the node's value).
// Pinning the node's MaxIterations to 1 to keep the test short while still
// covering the strconv.Atoi failure branch.
func (s *EngineSuite) TestLoopBodyMaxIterInvalidInputFallsBackToDefault() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-bad-input",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 1,
					Body: []*config.NodeDef{
						{ID: "tick", Type: config.NodeTypeBash, Script: "echo tick"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo tick", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{
		WorkflowName: "loop-bad-input",
		Inputs:       map[string]string{"max_iterations": "not-a-number"},
	})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
}

// TestLoopBodyPromptChild verifies a prompt-type body child invokes the
// agent runner and surfaces its response as the iteration's last output.
func (s *EngineSuite) TestLoopBodyPromptChild() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-prompt-body",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 1,
					Body: []*config.NodeDef{
						{ID: "ask", Type: config.NodeTypePrompt, Prompt: "say hi"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	var promptCalls atomic.Int32
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		promptCalls.Add(1)
	}).Return(&agent.AgentResponse{Response: "hi"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-prompt-body"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	require.Equal(s.T(), int32(1), promptCalls.Load())
}

// TestLoopBodyContextCancelBetweenChildren verifies that cancelling the run
// from inside the first child's bash invocation short-circuits the body via
// the ctx.Err() guard before the second child runs. CancelRun triggers the
// internal run context's cancel function, which executeLoopBody observes on
// the next iteration of its child loop.
func (s *EngineSuite) TestLoopBodyContextCancelBetweenChildren() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-ctx-cancel",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 1,
					Body: []*config.NodeDef{
						{ID: "first", Type: config.NodeTypeBash, Script: "echo first"},
						{ID: "second", Type: config.NodeTypeBash, Script: "echo second"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	var runID atomic.Value
	s.bashRunner.On("RunBash", mock.Anything, "echo first", "", "").
		Return("ok", nil).
		Run(func(_ mock.Arguments) {
			if id := runID.Load(); id != nil {
				_ = s.engine.CancelRun(context.Background(), id.(string))
			}
		})
	s.bashRunner.On("RunBash", mock.Anything, "echo second", "", "").Return("never", nil).Maybe()

	id, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-ctx-cancel"})
	require.NoError(s.T(), err)
	runID.Store(id)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
	s.bashRunner.AssertNotCalled(s.T(), "RunBash", mock.Anything, "echo second", "", "")
}

// TestLoopBodyUpsertNodeRunErrorsAreLogged drives all three UpsertNodeRun
// error-log branches in executeLoopBody (skip path, start path, finish path)
// by configuring the store to return an error for any UpsertNodeRun call.
// The run still completes — these errors are observability-only.
func (s *EngineSuite) TestLoopBodyUpsertNodeRunErrorsAreLogged() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-upsert-fail",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 1,
					Body: []*config.NodeDef{
						{ID: "tick", Type: config.NodeTypeBash, Script: "echo tick"},
						{ID: "gated", Type: config.NodeTypeBash, When: "{{ if false }}true{{ end }}", Script: "echo gated"},
					},
				},
			},
		},
	}

	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(
		&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil,
	).Maybe()
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// All UpsertNodeRun calls inside the body return an error so the three
	// log branches all fire. The top-level run's UpsertNodeRun also fails;
	// that lands in different code (executeNode) and is allowed to fail.
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(errors.New("db down"))
	done := s.waitForTerminalStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo tick", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-upsert-fail"})
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
}

// TestLoopBodyChildTimeoutAppliesAndCancels verifies a body child's
// `timeout:` declaration is honored: the child's bash invocation sees a
// context with a deadline matching the timeout, and the per-iteration
// cancel is explicitly invoked (rather than deferred — defer in the for-loop
// would stack one cancel per iteration). With timeout="50ms", the ctx that
// reaches RunBash must have a deadline less than 100ms from now.
func (s *EngineSuite) TestLoopBodyChildTimeoutAppliesAndCancels() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-child-timeout",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 1,
					Body: []*config.NodeDef{
						{ID: "slow", Type: config.NodeTypeBash, Timeout: "50ms", Script: "echo slow"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	var observedDeadline time.Time
	s.bashRunner.On("RunBash", mock.MatchedBy(func(ctx context.Context) bool {
		// The childCtx wrap must have set a deadline. Without the wrap the
		// ctx would be the run ctx, which has no deadline (or a much later
		// one — runReviewPollTimeout default is 30m, but here the run ctx
		// is the engine's root background ctx with no deadline at all).
		d, ok := ctx.Deadline()
		if ok {
			observedDeadline = d
		}
		return ok && time.Until(d) < 200*time.Millisecond
	}), "echo slow", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-child-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
	require.False(s.T(), observedDeadline.IsZero(), "child ctx must have a deadline set")
}

// TestLoopBodyChildTimeoutInvalidDurationIgnored verifies a malformed
// timeout string (`time.ParseDuration` error) is swallowed without
// failing the run — the child runs with the inherited run ctx instead,
// preserving the same behavior as omitting `timeout:` entirely.
func (s *EngineSuite) TestLoopBodyChildTimeoutInvalidDurationIgnored() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-child-bad-timeout",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 1,
					Body: []*config.NodeDef{
						{ID: "ok", Type: config.NodeTypeBash, Timeout: "not-a-duration", Script: "echo ok"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	s.bashRunner.On("RunBash", mock.MatchedBy(func(ctx context.Context) bool {
		// Bad timeout string is silently ignored — child runs without a per-child deadline.
		_, hasDeadline := ctx.Deadline()
		return !hasDeadline
	}), "echo ok", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-child-bad-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
}

// TestLoopBodyConditionTemplateErrorContinues verifies that a bad condition
// template logs a warning and continues iterating (rather than failing the
// run) — same recovery semantics as the legacy self-prompt loop.
func (s *EngineSuite) TestLoopBodyConditionTemplateErrorContinues() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-bad-cond",
			Nodes: []config.NodeDef{
				{
					ID:            "loop",
					Type:          config.NodeTypeLoop,
					MaxIterations: 2,
					Condition:     "{{.Bad", // unparseable template
					Body: []*config.NodeDef{
						{ID: "tick", Type: config.NodeTypeBash, Script: "echo tick"},
					},
				},
			},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	var calls atomic.Int32
	s.bashRunner.On("RunBash", mock.Anything, "echo tick", "", "").
		Return("ok", nil).
		Run(func(_ mock.Arguments) { calls.Add(1) })

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-bad-cond"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	require.Equal(s.T(), int32(2), calls.Load(), "bad condition template must not abort the loop")
}
