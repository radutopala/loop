package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

func (s *EngineSuite) TestStartRunWorkflowNotFound() {
	s.workflows = nil
	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "nonexistent"})
	require.ErrorContains(s.T(), err, "workflow not found")
}

func (s *EngineSuite) TestStartRunMissingRequiredInput() {
	s.workflows = []config.WorkflowDef{
		{
			Name:   "test",
			Inputs: map[string]config.WorkflowInput{"url": {Required: true}},
			Nodes:  []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok"}},
		},
	}
	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "test"})
	require.ErrorContains(s.T(), err, "missing required input: url")
}

// TestStartRunSeedsBlankDefaultInputs guards the `<no value>` fix: a declared
// input with a blank default must be seeded so `{{.Inputs.pr}}` renders "" (→
// shell-quoted ”) rather than Go's map-miss sentinel `<no value>`, which would
// splice into a bash node as `--pr <no value>` and break the shell.
func (s *EngineSuite) TestStartRunSeedsBlankDefaultInputs() {
	s.workflows = []config.WorkflowDef{
		{
			Name:   "blank-input",
			Inputs: map[string]config.WorkflowInput{"pr": {Default: ""}},
			Nodes:  []config.NodeDef{{ID: "n", Type: config.NodeTypeBash, Script: "loop review run --pr {{.Inputs.pr}} --wait"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	// Blank pr → shell-quoted '' in the rendered script, never "<no value>".
	s.bashRunner.On("RunBash", mock.Anything, "loop review run --pr '' --wait", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "blank-input"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)
	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "loop review run --pr '' --wait", "", "")
}

func (s *EngineSuite) TestStartRunSingleBashNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "hello",
			Nodes: []config.NodeDef{{ID: "greet", Type: config.NodeTypeBash, Script: "echo hello"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo hello", "", "").Return("hello\n", nil)

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "hello"})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), runID)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo hello", "", "")
}

func (s *EngineSuite) TestStartRunSinglePromptNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "prompt-test",
			Nodes: []config.NodeDef{{ID: "ask", Type: config.NodeTypePrompt, Prompt: "Say hi"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "Hello!"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "prompt-test"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// Verify the prompt was passed correctly.
	calls := s.runner.Calls
	require.NotEmpty(s.T(), calls)
	req := calls[0].Arguments.Get(1).(*agent.AgentRequest)
	require.Equal(s.T(), "Say hi", req.Prompt)
}

func (s *EngineSuite) TestStartRunLinearChain() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "chain",
			Nodes: []config.NodeDef{
				{ID: "diff", Type: config.NodeTypeBash, Script: "git diff"},
				{ID: "review", Type: config.NodeTypePrompt, DependsOn: []string{"diff"}, Prompt: "Review: {{.NodeOutputs.diff}}"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "git diff", "", "").Return("+added line", nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "LGTM"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "chain"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// Verify the prompt included the bash output.
	calls := s.runner.Calls
	require.NotEmpty(s.T(), calls)
	req := calls[0].Arguments.Get(1).(*agent.AgentRequest)
	require.Equal(s.T(), "Review: +added line", req.Prompt)
}

func (s *EngineSuite) TestStartRunParallelFanOut() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "parallel",
			Nodes: []config.NodeDef{
				{ID: "test", Type: config.NodeTypeBash, Script: "make test"},
				{ID: "lint", Type: config.NodeTypeBash, Script: "make lint"},
				{ID: "report", Type: config.NodeTypePrompt, DependsOn: []string{"test", "lint"}, Prompt: "T:{{.NodeOutputs.test}} L:{{.NodeOutputs.lint}}"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "make test", "", "").Return("PASS", nil)
	s.bashRunner.On("RunBash", mock.Anything, "make lint", "", "").Return("OK", nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "All good"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "parallel"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// Verify the report prompt got both outputs.
	calls := s.runner.Calls
	require.NotEmpty(s.T(), calls)
	req := calls[0].Arguments.Get(1).(*agent.AgentRequest)
	require.Equal(s.T(), "T:PASS L:OK", req.Prompt)
}

func (s *EngineSuite) TestStartRunBashNodeFailure() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "fail",
			Nodes: []config.NodeDef{{ID: "boom", Type: config.NodeTypeBash, Script: "exit 1"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "exit 1", "", "").Return("", fmt.Errorf("exit code 1"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "fail"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestStartRunInputDefaults() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "defaults",
			Inputs: map[string]config.WorkflowInput{
				"branch": {Default: "main"},
			},
			Nodes: []config.NodeDef{{ID: "run", Type: config.NodeTypeBash, Script: "echo {{.Inputs.branch}}"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo 'main'", "", "").Return("main", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "defaults"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo 'main'", "", "")
}

func (s *EngineSuite) TestStartRunInputOverridesDefault() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "override",
			Inputs: map[string]config.WorkflowInput{
				"branch": {Default: "main"},
			},
			Nodes: []config.NodeDef{{ID: "run", Type: config.NodeTypeBash, Script: "echo {{.Inputs.branch}}"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo 'develop'", "", "").Return("develop", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{
		WorkflowName: "override",
		Inputs:       map[string]string{"branch": "develop"},
	})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo 'develop'", "", "")
}

// TestStartRunSkipsEmptyStringInputs covers the explicit empty-string skip
// in StartRun. External callers (CLI, MCP, future automation) routinely
// emit `{"branch": ""}` for unset optional fields; without the skip those
// would wipe the configured default and surface as downstream
// strconv/parse failures (or, for max_iterations, a 0-cap loop).
func (s *EngineSuite) TestStartRunSkipsEmptyStringInputs() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "empty-input-defaults",
			Inputs: map[string]config.WorkflowInput{
				"branch": {Default: "main"},
			},
			Nodes: []config.NodeDef{{ID: "run", Type: config.NodeTypeBash, Script: "echo {{.Inputs.branch}}"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	// Should run "echo 'main'" — the empty-string input was skipped so the
	// default survives. The value is shell-quoted by renderBashScript.
	s.bashRunner.On("RunBash", mock.Anything, "echo 'main'", "", "").Return("main", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{
		WorkflowName: "empty-input-defaults",
		Inputs:       map[string]string{"branch": ""},
	})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo 'main'", "", "")
}

func (s *EngineSuite) TestCancelRun() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "cancel-test",
			Nodes: []config.NodeDef{{ID: "slow", Type: config.NodeTypeBash, Script: "sleep 10"}},
		},
	}

	s.expectRunPersistence()
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)

	s.bashRunner.On("RunBash", mock.Anything, "sleep 10", "", "").
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		}).
		Return("", fmt.Errorf("cancelled"))

	// Unset the default GetWorkflowRun mock (shares one pointer across calls)
	// and register one-shot returns so each concurrent caller gets a distinct
	// struct, preventing a data race between CancelRun and finalizeDAG.
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

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "cancel-test"})
	require.NoError(s.T(), err)

	err = s.engine.CancelRun(context.Background(), runID)
	require.NoError(s.T(), err)
}

func (s *EngineSuite) TestCancelRunNotFound() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "missing").Return(nil, nil)

	err := s.engine.CancelRun(context.Background(), "missing")
	require.ErrorContains(s.T(), err, "workflow run not found")
}

func (s *EngineSuite) TestCancelRunAlreadyCompleted() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "done").Return(&db.WorkflowRun{
		ID:     "done",
		Status: db.WorkflowRunStatusCompleted,
	}, nil)

	err := s.engine.CancelRun(context.Background(), "done")
	require.NoError(s.T(), err)
}

func (s *EngineSuite) TestGetRun() {
	s.store.ExpectedCalls = nil
	run := &db.WorkflowRun{ID: "r1", WorkflowName: "test", Status: db.WorkflowRunStatusCompleted}
	nodeRuns := []*db.NodeRun{{RunID: "r1", NodeID: "n1", Status: db.NodeRunStatusSuccess}}

	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(run, nil)
	s.store.On("ListNodeRuns", mock.Anything, "r1").Return(nodeRuns, nil)

	gotRun, gotNodes, err := s.engine.GetRun(context.Background(), "r1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "r1", gotRun.ID)
	require.Len(s.T(), gotNodes, 1)
}

func (s *EngineSuite) TestGetRunNotFound() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "missing").Return(nil, nil)

	run, nodes, err := s.engine.GetRun(context.Background(), "missing")
	require.NoError(s.T(), err)
	require.Nil(s.T(), run)
	require.Nil(s.T(), nodes)
}

func (s *EngineSuite) TestListRuns() {
	runs := []*db.WorkflowRun{{ID: "r1"}, {ID: "r2"}}
	s.store.On("ListWorkflowRuns", mock.Anything, "ch1", 20, 0).Return(runs, nil)

	got, err := s.engine.ListRuns(context.Background(), "ch1", 20, 0)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 2)
}

func (s *EngineSuite) TestListRunsDefaultLimit() {
	s.store.On("ListWorkflowRuns", mock.Anything, "", 50, 0).Return(nil, nil)

	_, err := s.engine.ListRuns(context.Background(), "", 0, 0)
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "ListWorkflowRuns", mock.Anything, "", 50, 0)
}

func (s *EngineSuite) TestListRunsWithOffset() {
	runs := []*db.WorkflowRun{{ID: "r3"}}
	s.store.On("ListWorkflowRuns", mock.Anything, "ch1", 20, 40).Return(runs, nil)

	got, err := s.engine.ListRuns(context.Background(), "ch1", 20, 40)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
}

func (s *EngineSuite) TestListWorkflows() {
	s.workflows = []config.WorkflowDef{
		{Name: "wf1"},
		{Name: "wf2"},
	}

	got, err := s.engine.ListWorkflows(context.Background(), "", "")
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 2)
	require.Equal(s.T(), "wf1", got[0].Name)
}

func (s *EngineSuite) TestWhenConditionSkipsNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "when-test",
			Nodes: []config.NodeDef{
				{ID: "always", Type: config.NodeTypeBash, Script: "echo yes"},
				{ID: "never", Type: config.NodeTypeBash, Script: "echo no", When: "false"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo yes", "", "").Return("yes", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "when-test"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// "never" node should not have called RunBash with "echo no".
	s.bashRunner.AssertNotCalled(s.T(), "RunBash", mock.Anything, "echo no", mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestTriggerRuleAllDone() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "all-done",
			Nodes: []config.NodeDef{
				{ID: "fail", Type: config.NodeTypeBash, Script: "exit 1"},
				{ID: "report", Type: config.NodeTypePrompt, DependsOn: []string{"fail"}, TriggerRule: "all_done", Prompt: "Report"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "exit 1", "", "").Return("", fmt.Errorf("exit 1"))
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "Done"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "all-done"})
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

	// The report node should have run despite dependency failure.
	s.runner.AssertCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestTriggerRuleAllSuccessSkips() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "skip-on-fail",
			Nodes: []config.NodeDef{
				{ID: "fail", Type: config.NodeTypeBash, Script: "exit 1"},
				{ID: "report", Type: config.NodeTypePrompt, DependsOn: []string{"fail"}, Prompt: "Report"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "exit 1", "", "").Return("", fmt.Errorf("exit 1"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "skip-on-fail"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)

	// Report node should NOT run since dependency failed.
	s.runner.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestBroadcasterEventsEmitted() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "events",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo hi"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo hi", "", "").Return("hi", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "events"})
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

	// Expect: run_started, node_started, node_completed, run_completed = 4 events.
	// BroadcastWorkflowRunCompleted runs after the terminal UpdateWorkflowRun that
	// unblocks `done`, so poll until all 4 are observed rather than reading once.
	require.Eventually(s.T(), func() bool {
		s.broadcaster.mu.Lock()
		defer s.broadcaster.mu.Unlock()
		return len(s.broadcaster.events) >= 4
	}, 2*time.Second, 10*time.Millisecond)
}

func (s *EngineSuite) TestUnsupportedNodeType() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "bad-type",
			Nodes: []config.NodeDef{{ID: "n1", Type: "unknown"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-type"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestPromptNodeAgentError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "agent-err",
			Nodes: []config.NodeDef{{ID: "ask", Type: config.NodeTypePrompt, Prompt: "test"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "partial", Error: "agent failed"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "agent-err"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestPromptNodeWithSystemPrompt() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "sys-prompt",
			Nodes: []config.NodeDef{{
				ID: "ask", Type: config.NodeTypePrompt,
				Prompt:       "Hello",
				SystemPrompt: "You are a {{.Inputs.role}}",
			}},
			Inputs: map[string]config.WorkflowInput{"role": {}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "Hi"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{
		WorkflowName: "sys-prompt",
		Inputs:       map[string]string{"role": "helper"},
	})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	calls := s.runner.Calls
	require.NotEmpty(s.T(), calls)
	req := calls[0].Arguments.Get(1).(*agent.AgentRequest)
	require.Equal(s.T(), "You are a helper", req.SystemPrompt)
}

func (s *EngineSuite) TestPromptNodeSystemPromptTemplateError() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "bad-sys",
			Nodes: []config.NodeDef{{
				ID: "ask", Type: config.NodeTypePrompt,
				Prompt:       "Hello",
				SystemPrompt: "{{.Bad",
			}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-sys"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestPromptNodeTemplateError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "bad-prompt",
			Nodes: []config.NodeDef{{ID: "ask", Type: config.NodeTypePrompt, Prompt: "{{.Bad"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-prompt"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestBashNodeScriptTemplateError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "bad-script",
			Nodes: []config.NodeDef{{ID: "run", Type: config.NodeTypeBash, Script: "{{.Bad"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-script"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestTriggerRuleOneSuccess() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "one-success",
			Nodes: []config.NodeDef{
				{ID: "a", Type: config.NodeTypeBash, Script: "exit 1"},
				{ID: "b", Type: config.NodeTypeBash, Script: "echo ok"},
				{ID: "report", Type: config.NodeTypePrompt, DependsOn: []string{"a", "b"}, TriggerRule: "one_success", Prompt: "Report"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "exit 1", "", "").Return("", fmt.Errorf("exit 1"))
	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Return("ok", nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "Done"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "one-success"})
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	// Report node should have run because at least one dependency succeeded.
	s.runner.AssertCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestWhenConditionTemplateError() {
	s.workflows = []config.WorkflowDef{
		{
			Name: "bad-when",
			Nodes: []config.NodeDef{
				{ID: "run", Type: config.NodeTypeBash, Script: "echo ok", When: "{{.Bad"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-when"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		// Node should still run (defaults to true on template error).
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo ok", "", "")
}

func (s *EngineSuite) TestGetRunGetWorkflowRunError() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(nil, fmt.Errorf("db error"))

	_, _, err := s.engine.GetRun(context.Background(), "r1")
	require.ErrorContains(s.T(), err, "db error")
}

func (s *EngineSuite) TestGetRunListNodeRunsError() {
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(&db.WorkflowRun{ID: "r1"}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "r1").Return(nil, fmt.Errorf("db error"))

	_, _, err := s.engine.GetRun(context.Background(), "r1")
	require.ErrorContains(s.T(), err, "db error")
}

func (s *EngineSuite) TestStartRunCreateWorkflowRunError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "test",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok"}},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("db error"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "test"})
	require.ErrorContains(s.T(), err, "creating workflow run")
}

func (s *EngineSuite) TestCancelRunGetWorkflowRunError() {
	s.store.ExpectedCalls = nil
	s.store.On("GetWorkflowRun", mock.Anything, "r1").Return(nil, fmt.Errorf("db error"))

	err := s.engine.CancelRun(context.Background(), "r1")
	require.ErrorContains(s.T(), err, "db error")
}

func (s *EngineSuite) TestPromptNodeRunnerError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "runner-err",
			Nodes: []config.NodeDef{{ID: "ask", Type: config.NodeTypePrompt, Prompt: "test"}},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("connection refused"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "runner-err"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
}

func (s *EngineSuite) TestTriggerRuleOneSuccessNoneSucceeded() {
	// Test one_success when no dependency succeeded — downstream should be skipped.
	s.workflows = []config.WorkflowDef{
		{
			Name: "one-none",
			Nodes: []config.NodeDef{
				{ID: "a", Type: config.NodeTypeBash, Script: "exit 1"},
				{ID: "report", Type: config.NodeTypePrompt, DependsOn: []string{"a"}, TriggerRule: "one_success", Prompt: "Report"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "exit 1", "", "").Return("", fmt.Errorf("exit 1"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "one-none"})
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	// Report node should NOT have run because no dependencies succeeded.
	s.runner.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestTriggerRuleUnknown() {
	// Unknown trigger rule defaults to true (runs the node).
	s.workflows = []config.WorkflowDef{
		{
			Name: "unknown-rule",
			Nodes: []config.NodeDef{
				{ID: "a", Type: config.NodeTypeBash, Script: "echo ok"},
				{ID: "report", Type: config.NodeTypePrompt, DependsOn: []string{"a"}, TriggerRule: "custom_rule", Prompt: "Report"},
			},
		},
	}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Return("ok", nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "Done"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "unknown-rule"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	s.runner.AssertCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *EngineSuite) TestTriggerRuleAllDoneWithPendingDep() {
	// all_done should not fire if a dependency is still running/pending.
	// We test this by having two deps — one runs, one "stays running" —
	// but since in our DAG the downstream only fires after deps finish,
	// we test by having a dep that fails at bash + one that succeeds.
	// The "all_done" case with non-terminal status is hard to reach via
	// the engine since the DAG only decrements in-degree after completion.
	// Instead we unit-test checkTriggerRule directly.
	e := s.engine.(*defaultEngine)

	nodeStatus := map[string]db.NodeRunStatus{
		"a": db.NodeRunStatusSuccess,
		"b": db.NodeRunStatusRunning, // still running
	}
	node := &config.NodeDef{
		ID:          "report",
		DependsOn:   []string{"a", "b"},
		TriggerRule: "all_done",
	}
	require.False(s.T(), e.checkTriggerRule(node, nodeStatus))
}

func (s *EngineSuite) TestUpdateWorkflowRunErrorLogging() {
	// Test that UpdateWorkflowRun error in executeDAG is handled (just logs).
	s.workflows = []config.WorkflowDef{
		{
			Name:  "update-err",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok"}},
		},
	}

	s.expectRunPersistence()

	// Make UpdateWorkflowRun fail.
	doneCh := make(chan struct{}, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		select {
		case doneCh <- struct{}{}:
		default:
		}
	}).Return(fmt.Errorf("db error"))

	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "update-err"})
	require.NoError(s.T(), err)

	// Wait for executeDAG to attempt the final update.
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
}

func (s *EngineSuite) TestUpsertNodeRunErrorLogging() {
	// Test that UpsertNodeRun error is handled (just logs, doesn't crash).
	s.workflows = []config.WorkflowDef{
		{
			Name:  "upsert-log-err",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok"}},
		},
	}

	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Every UpsertNodeRun call during DAG execution fails — covers the
	// error log paths in executeNode (running) and completeNode (terminal).
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(fmt.Errorf("db error"))

	done := s.waitForRunStatus()
	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "upsert-log-err"})
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
}

func (s *EngineSuite) TestDeleteWorkflowRunOnStore() {
	// Exercise the DeleteWorkflowRun mock method for coverage.
	s.store.On("DeleteWorkflowRun", mock.Anything, "r1").Return(nil)
	err := s.store.DeleteWorkflowRun(context.Background(), "r1")
	require.NoError(s.T(), err)
}

// --- unit tests for helpers ---

func TestRenderTemplate(t *testing.T) {
	rc := &RunContext{
		Inputs:      map[string]string{"branch": "main"},
		NodeOutputs: map[string]string{"diff": "+hello"},
	}

	tests := []struct {
		name     string
		tmpl     string
		expected string
	}{
		{"empty", "", ""},
		{"plain", "hello", "hello"},
		{"input", "{{.Inputs.branch}}", "main"},
		{"node output", "Review: {{.NodeOutputs.diff}}", "Review: +hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderTemplate(tt.tmpl, rc)
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestRenderTemplateError(t *testing.T) {
	rc := &RunContext{}
	_, err := renderTemplate("{{.Bad", rc)
	require.Error(t, err)
}

func TestRenderTemplateExecuteError(t *testing.T) {
	rc := &RunContext{}
	// Calling a method that doesn't exist triggers an execute error.
	_, err := renderTemplate("{{.Inputs.Missing | len}}", rc)
	require.Error(t, err)
}

// TestShellQuote covers the POSIX single-quote escape used to neutralize
// shell metacharacters in user-controlled values that flow into bash node
// scripts.
func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		out  string
	}{
		{"empty", "", "''"},
		{"plain", "main", "'main'"},
		{"semicolon", "a; rm -rf /", "'a; rm -rf /'"},
		{"single quote", "it's", `'it'\''s'`},
		{"backticks and $", "`whoami`$(id)", "'`whoami`$(id)'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.out, shellQuote(tt.in))
		})
	}
}

// TestRenderBashScriptShellQuotesUserValues asserts that values from Inputs,
// NodeOutputs, ChannelID and Review.CommentsJSON are shell-quoted before
// interpolation into bash node scripts — closing the command-injection sink
// at /bin/sh -c (CodeQL go/command-injection).
func TestRenderBashScriptShellQuotesUserValues(t *testing.T) {
	rc := &RunContext{
		Inputs:      map[string]string{"branch": "; rm -rf /"},
		NodeOutputs: map[string]string{"diff": "$(whoami)"},
		ChannelID:   "ch'1",
		Review:      ReviewState{CommentsJSON: `[{"id":"a"}]`},
	}

	out, err := renderBashScript(
		"echo {{.Inputs.branch}} {{.NodeOutputs.diff}} {{.ChannelID}} {{.Review.CommentsJSON}}",
		rc,
	)
	require.NoError(t, err)
	require.Equal(t, `echo '; rm -rf /' '$(whoami)' 'ch'\''1' '[{"id":"a"}]'`, out)
}

// TestRenderBashScriptEmptyTemplate covers the early-return path.
func TestRenderBashScriptEmptyTemplate(t *testing.T) {
	out, err := renderBashScript("", &RunContext{})
	require.NoError(t, err)
	require.Empty(t, out)
}

// TestRenderBashScriptNilMaps covers shellQuoteMap's nil-input branch — the
// engine constructs RunContext with non-nil maps in practice, but the helper
// must remain defensive so partial test/usage paths don't panic.
func TestRenderBashScriptNilMaps(t *testing.T) {
	out, err := renderBashScript("echo hi", &RunContext{})
	require.NoError(t, err)
	require.Equal(t, "echo hi", out)
}
