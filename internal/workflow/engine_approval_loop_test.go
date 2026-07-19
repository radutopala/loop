package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// --- approval node ---

func (s *EngineSuite) TestApprovalNodePauseAndResume() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "approval-test",
			Nodes: []config.NodeDef{
				{ID: "check", Type: config.NodeTypeBash, Script: "echo pre"},
				{ID: "approve", Type: config.NodeTypeApproval, DependsOn: []string{"check"}, Message: "Please approve: {{.NodeOutputs.check}}", Timeout: "10s"},
				{ID: "deploy", Type: config.NodeTypeBash, DependsOn: []string{"approve"}, Script: "echo deploying"},
			},
		},
	}

	s.expectRunPersistence()
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)

	s.bashRunner.On("RunBash", mock.Anything, "echo pre", "", "").Return("pre", nil)
	s.bashRunner.On("RunBash", mock.Anything, "echo deploying", "", "").Return("deployed", nil)

	done := make(chan db.WorkflowRunStatus, 1)
	updateCalls := 0
	s.store.ExpectedCalls = nil // clear existing mocks
	s.expectRunPersistence()
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// GetWorkflowRun: used by updateRunStatus, executeDAG final write, and
	// ResumeRun (to look up PausedNodeID). Return PausedNodeID="approve" so
	// ResumeRun can form the correct composite approval key.
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(
		&db.WorkflowRun{Status: db.WorkflowRunStatusRunning, PausedNodeID: "approve"}, nil,
	)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		updateCalls++
		if run.Status == db.WorkflowRunStatusCompleted || run.Status == db.WorkflowRunStatusFailed {
			select {
			case done <- run.Status:
			default:
			}
		}
	}).Return(nil)

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-test"})
	require.NoError(s.T(), err)

	// Wait for the approval node to pause.
	time.Sleep(200 * time.Millisecond)

	// Resume with a response.
	err = s.engine.ResumeRun(context.Background(), runID, "looks good")
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// Verify deploy ran after approval.
	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo deploying", "", "")
}

func (s *EngineSuite) TestApprovalNodeTimeout() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "approval-timeout",
			Nodes: []config.NodeDef{
				{ID: "approve", Type: config.NodeTypeApproval, Message: "Approve?", Timeout: "100ms"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForTerminalStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-timeout"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestApprovalNodeMessageTemplateError() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "approval-bad-msg",
			Nodes: []config.NodeDef{
				{ID: "approve", Type: config.NodeTypeApproval, Message: "{{.Bad", Timeout: "100ms"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForTerminalStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-bad-msg"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestApprovalNodeDefaultResponse() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "approval-default",
			Nodes: []config.NodeDef{
				{ID: "approve", Type: config.NodeTypeApproval, Message: "Go?", Timeout: "5s"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForTerminalStatus()

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-default"})
	require.NoError(s.T(), err)

	time.Sleep(200 * time.Millisecond)

	// Resume with empty string — should default to "approved".
	err = s.engine.ResumeRun(context.Background(), runID, "")
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)
}

func (s *EngineSuite) TestResumeRunNotPending() {
	err := s.engine.ResumeRun(context.Background(), "nonexistent", "ok")
	require.ErrorContains(s.T(), err, "no pending approval")
}

func (s *EngineSuite) TestResumeRunNotFound() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "missing").Return(nil, nil)

	err := s.engine.ResumeRun(context.Background(), "missing", "ok")
	require.ErrorContains(s.T(), err, "workflow run not found")
}

func (s *EngineSuite) TestResumeRunGetError() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(nil, fmt.Errorf("db error"))

	err := s.engine.ResumeRun(context.Background(), "r1", "ok")
	require.ErrorContains(s.T(), err, "looking up run")
}

func (s *EngineSuite) TestResumeRunNoPausedNode() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(&db.WorkflowRun{
		ID: "r1", Status: db.WorkflowRunStatusRunning,
	}, nil)

	err := s.engine.ResumeRun(context.Background(), "r1", "ok")
	require.ErrorContains(s.T(), err, "no pending approval")
}

// --- loop node ---

func (s *EngineSuite) TestLoopNodeIterates() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-test",
			Nodes: []config.NodeDef{
				{ID: "loop", Type: config.NodeTypeLoop, Prompt: "Generate next", MaxIterations: 3},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	callCount := 0
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		callCount++
	}).Return(&agent.AgentResponse{Response: "iteration result"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-test"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	require.Equal(s.T(), 3, callCount)
}

func (s *EngineSuite) TestLoopNodeStopsOnCondition() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-stop",
			Nodes: []config.NodeDef{
				{ID: "loop", Type: config.NodeTypeLoop, Prompt: "Next", MaxIterations: 10, Condition: `{{if eq .NodeOutputs.loop "done"}}true{{end}}`},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	callCount := 0
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		callCount++
	}).Return(&agent.AgentResponse{Response: "done"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-stop"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// Should stop after first iteration since condition is immediately "true".
	require.Equal(s.T(), 1, callCount)
}

func (s *EngineSuite) TestLoopNodeDefaultMaxIterations() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-default",
			Nodes: []config.NodeDef{
				{ID: "loop", Type: config.NodeTypeLoop, Prompt: "Again"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	callCount := 0
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		callCount++
	}).Return(&agent.AgentResponse{Response: "ok"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-default"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(10 * time.Second):
		s.T().Fatal("timeout")
	}

	require.Equal(s.T(), 10, callCount) // default max_iterations
}

func (s *EngineSuite) TestLoopNodePromptError() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-err",
			Nodes: []config.NodeDef{
				{ID: "loop", Type: config.NodeTypeLoop, Prompt: "Test", MaxIterations: 3},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("agent error"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-err"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestLoopNodeConditionTemplateError() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "loop-bad-cond",
			Nodes: []config.NodeDef{
				{ID: "loop", Type: config.NodeTypeLoop, Prompt: "Test", MaxIterations: 2, Condition: "{{.Bad"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	callCount := 0
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		callCount++
	}).Return(&agent.AgentResponse{Response: "ok"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-bad-cond"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// Should run all iterations despite bad condition (condition error → continue).
	require.Equal(s.T(), 2, callCount)
}
