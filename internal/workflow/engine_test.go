package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/testutil"
)

// --- mocks ---

type mockRunner struct {
	mock.Mock
}

func (m *mockRunner) Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*agent.AgentResponse), args.Error(1)
}

type mockBashRunner struct {
	mock.Mock
}

func (m *mockBashRunner) RunBash(ctx context.Context, script, channelID, dirPath string) (string, error) {
	args := m.Called(ctx, script, channelID, dirPath)
	return args.String(0), args.Error(1)
}

type mockBroadcaster struct {
	mu     sync.Mutex
	events []any
}

func (m *mockBroadcaster) BroadcastWorkflowRunStarted(data events.WorkflowRunEventData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, data)
}
func (m *mockBroadcaster) BroadcastWorkflowRunCompleted(data events.WorkflowRunEventData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, data)
}
func (m *mockBroadcaster) BroadcastWorkflowRunPaused(data events.WorkflowRunEventData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, data)
}
func (m *mockBroadcaster) BroadcastWorkflowNodeStarted(data events.WorkflowNodeEventData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, data)
}
func (m *mockBroadcaster) BroadcastWorkflowNodeCompleted(data events.WorkflowNodeEventData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, data)
}

// --- suite ---

type EngineSuite struct {
	suite.Suite
	store       *testutil.MockStore
	runner      *mockRunner
	bashRunner  *mockBashRunner
	broadcaster *mockBroadcaster
	workflows   []config.WorkflowDef
	engine      Engine
}

func TestEngineSuite(t *testing.T) {
	suite.Run(t, new(EngineSuite))
}

func (s *EngineSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.runner = new(mockRunner)
	s.bashRunner = new(mockBashRunner)
	s.broadcaster = &mockBroadcaster{}
	s.workflows = nil
	s.engine = NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, func(_, _ string) []config.WorkflowDef {
		return s.workflows
	}, "", config.WorkflowConcurrency{}, slog.Default())

	// Default GetWorkflowRun mock — used by updateRunStatus, executeDAG final
	// write, and ResumeRun. Returns running with PausedNodeID so approval tests
	// can call ResumeRun. Tests that clear ExpectedCalls must re-add this.
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(
		&db.WorkflowRun{Status: db.WorkflowRunStatusRunning, PausedNodeID: "approve"}, nil,
	).Maybe()

	// Default heartbeat mock — the heartbeat goroutine fires from executeNode
	// and its timing is non-deterministic in tests.
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

// waitForRunStatus sets up UpdateWorkflowRun to signal on a channel when the
// run reaches any non-running status (including paused).
func (s *EngineSuite) waitForRunStatus() chan db.WorkflowRunStatus {
	ch := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		if run.Status != db.WorkflowRunStatusRunning {
			select {
			case ch <- run.Status:
			default:
			}
		}
	}).Return(nil)
	return ch
}

// waitForTerminalStatus sets up UpdateWorkflowRun to signal only on truly terminal
// statuses (completed, failed, cancelled), ignoring paused.
func (s *EngineSuite) waitForTerminalStatus() chan db.WorkflowRunStatus {
	ch := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		switch run.Status {
		case db.WorkflowRunStatusCompleted, db.WorkflowRunStatusFailed, db.WorkflowRunStatusCancelled:
			select {
			case ch <- run.Status:
			default:
			}
		}
	}).Return(nil)
	return ch
}

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

func (s *EngineSuite) TestStartRunSingleBashNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "hello",
			Nodes: []config.NodeDef{{ID: "greet", Type: config.NodeTypeBash, Script: "echo hello"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo hello", "", "").Return("hello\n", nil)

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "hello"})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), runID)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo hello", "", "")
}

func (s *EngineSuite) TestStartRunSinglePromptNode() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "prompt-test",
			Nodes: []config.NodeDef{{ID: "ask", Type: config.NodeTypePrompt, Prompt: "Say hi"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "Hello!"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "prompt-test"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "git diff", "", "").Return("+added line", nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "LGTM"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "chain"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "make test", "", "").Return("PASS", nil)
	s.bashRunner.On("RunBash", mock.Anything, "make lint", "", "").Return("OK", nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "All good"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "parallel"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "exit 1", "", "").Return("", fmt.Errorf("exit code 1"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "fail"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo main", "", "").Return("main", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "defaults"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo main", "", "")
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo develop", "", "").Return("develop", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{
		WorkflowName: "override",
		Inputs:       map[string]string{"branch": "develop"},
	})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo develop", "", "")
}

func (s *EngineSuite) TestCancelRun() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "cancel-test",
			Nodes: []config.NodeDef{{ID: "slow", Type: config.NodeTypeBash, Script: "sleep 10"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo yes", "", "").Return("yes", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "when-test"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "exit 1", "", "").Return("", fmt.Errorf("exit 1"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "skip-on-fail"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo hi", "", "").Return("hi", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "events"})
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

	s.broadcaster.mu.Lock()
	defer s.broadcaster.mu.Unlock()
	// Expect: run_started, node_started, node_completed, run_completed = 4 events.
	require.GreaterOrEqual(s.T(), len(s.broadcaster.events), 4)
}

func (s *EngineSuite) TestUnsupportedNodeType() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "bad-type",
			Nodes: []config.NodeDef{{ID: "n1", Type: "unknown"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-type"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}
}

func (s *EngineSuite) TestPromptNodeAgentError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "agent-err",
			Nodes: []config.NodeDef{{ID: "ask", Type: config.NodeTypePrompt, Prompt: "test"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "partial", Error: "agent failed"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "agent-err"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "Hi"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{
		WorkflowName: "sys-prompt",
		Inputs:       map[string]string{"role": "helper"},
	})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-sys"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
}

func (s *EngineSuite) TestPromptNodeTemplateError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "bad-prompt",
			Nodes: []config.NodeDef{{ID: "ask", Type: config.NodeTypePrompt, Prompt: "{{.Bad"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-prompt"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
}

func (s *EngineSuite) TestBashNodeScriptTemplateError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "bad-script",
			Nodes: []config.NodeDef{{ID: "run", Type: config.NodeTypeBash, Script: "{{.Bad"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad-script"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(fmt.Errorf("db error"))

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("connection refused"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "runner-err"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Return("ok", nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "Done"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "unknown-rule"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	// Succeed on initial UpsertNodeRun (in StartRun), fail during DAG execution.
	callCount := 0
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		callCount++
	}).Return(nil).Once()
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

func (s *EngineSuite) TestStartRunUpsertNodeRunError() {
	s.workflows = []config.WorkflowDef{
		{
			Name:  "upsert-err",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(fmt.Errorf("db error"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "upsert-err"})
	require.ErrorContains(s.T(), err, "creating node run")
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)

	s.bashRunner.On("RunBash", mock.Anything, "echo pre", "", "").Return("pre", nil)
	s.bashRunner.On("RunBash", mock.Anything, "echo deploying", "", "").Return("deployed", nil)

	done := make(chan db.WorkflowRunStatus, 1)
	updateCalls := 0
	s.store.ExpectedCalls = nil // clear existing mocks
	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run completion")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-timeout"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for run failure")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-bad-msg"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForTerminalStatus()

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "approval-default"})
	require.NoError(s.T(), err)

	time.Sleep(200 * time.Millisecond)

	// Resume with empty string — should default to "approved".
	err = s.engine.ResumeRun(context.Background(), runID, "")
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	callCount := 0
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		callCount++
	}).Return(&agent.AgentResponse{Response: "iteration result"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-test"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	callCount := 0
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		callCount++
	}).Return(&agent.AgentResponse{Response: "done"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-stop"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("agent error"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-err"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	callCount := 0
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		callCount++
	}).Return(&agent.AgentResponse{Response: "ok"}, nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-bad-cond"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	// Should run all iterations despite bad condition (condition error → continue).
	require.Equal(s.T(), 2, callCount)
}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	// First call fails, second succeeds.
	s.bashRunner.On("RunBash", mock.Anything, "flaky-cmd", "", "").Return("", fmt.Errorf("flaky")).Once()
	s.bashRunner.On("RunBash", mock.Anything, "flaky-cmd", "", "").Return("success", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-test"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "fail-cmd", "", "").Return("", fmt.Errorf("always fails"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-exhausted"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
}

func (s *EngineSuite) TestRetryNoConfig() {
	// Without retry config, failure on first attempt.
	s.workflows = []config.WorkflowDef{
		{
			Name:  "no-retry",
			Nodes: []config.NodeDef{{ID: "fail", Type: config.NodeTypeBash, Script: "fail"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "fail", "", "").Return("", fmt.Errorf("error")).Once()

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "no-retry"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

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

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout — approval node should have failed due to DB error")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "fail", "", "").Return("", fmt.Errorf("error"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-cap"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	// First 2 UpsertNodeRun calls succeed (initial creation + node start),
	// then fail for the retry attempt update (covers the error log path).
	var upsertCalls atomic.Int32
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		upsertCalls.Add(1)
	}).Return(nil).Times(2)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(fmt.Errorf("upsert failed"))
	done := s.waitForRunStatus()

	s.bashRunner.On("RunBash", mock.Anything, "fail", "", "").Return("", fmt.Errorf("error"))

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "retry-upsert-err"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
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
	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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
	// A running workflow should be marked as failed on recovery.
	staleRun := &db.WorkflowRun{
		ID:           "wfr-stale",
		WorkflowName: "test",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       `{}`,
	}
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{staleRun}, nil)

	// failStaleRun updates the run and its node runs.
	var capturedRun *db.WorkflowRun
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		capturedRun = args.Get(1).(*db.WorkflowRun)
	}).Return(nil)

	pendingNode := &db.NodeRun{RunID: "wfr-stale", NodeID: "n1", Status: db.NodeRunStatusPending}
	runningNode := &db.NodeRun{RunID: "wfr-stale", NodeID: "n2", Status: db.NodeRunStatusRunning}
	doneNode := &db.NodeRun{RunID: "wfr-stale", NodeID: "n3", Status: db.NodeRunStatusSuccess}
	s.store.On("ListNodeRuns", mock.Anything, "wfr-stale").Return([]*db.NodeRun{pendingNode, runningNode, doneNode}, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	require.NotNil(s.T(), capturedRun)
	require.Equal(s.T(), db.WorkflowRunStatusFailed, capturedRun.Status)
	require.Equal(s.T(), "server restarted while workflow was running", capturedRun.ErrorText)
	require.NotNil(s.T(), capturedRun.FinishedAt)

	// Verify pending and running nodes were failed, but success node was not touched.
	s.store.AssertNumberOfCalls(s.T(), "UpsertNodeRun", 2)
}

func (s *EngineSuite) TestRecoverRunsFailStaleListNodeError() {
	staleRun := &db.WorkflowRun{
		ID:           "wfr-stale2",
		WorkflowName: "test",
		Status:       db.WorkflowRunStatusRunning,
	}
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{staleRun}, nil)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-stale2").Return(nil, fmt.Errorf("node list error"))

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	// Should still succeed — node list error is logged, not returned.
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
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-paused-nf").Return(nil, nil)

	s.workflows = nil // no workflows defined

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	// Should have fallen back to failStaleRun.
	s.store.AssertCalled(s.T(), "UpdateWorkflowRun", mock.Anything, mock.MatchedBy(func(r *db.WorkflowRun) bool {
		return r.ID == "wfr-paused-nf" && r.Status == db.WorkflowRunStatusFailed
	}))
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
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-paused-nle").Return(nil, fmt.Errorf("node list error"))

	s.workflows = []config.WorkflowDef{
		{
			Name:  "test-wf",
			Nodes: []config.NodeDef{{ID: "gate", Type: config.NodeTypeApproval, Message: "Go?"}},
		},
	}

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "UpdateWorkflowRun", mock.Anything, mock.MatchedBy(func(r *db.WorkflowRun) bool {
		return r.ID == "wfr-paused-nle" && r.Status == db.WorkflowRunStatusFailed
	}))
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
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-paused-bad").Return(nil, nil)

	s.workflows = []config.WorkflowDef{
		{
			Name:  "test-wf",
			Nodes: []config.NodeDef{{ID: "gate", Type: config.NodeTypeApproval, Message: "Go?"}},
		},
	}

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "UpdateWorkflowRun", mock.Anything, mock.MatchedBy(func(r *db.WorkflowRun) bool {
		return r.ID == "wfr-paused-bad" && r.Status == db.WorkflowRunStatusFailed
	}))
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

func (s *EngineSuite) TestFailStaleRunUpdateError() {
	// UpdateWorkflowRun error is logged, not fatal.
	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{
		{ID: "wfr-err", WorkflowName: "wf", Status: db.WorkflowRunStatusRunning},
	}, nil)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(fmt.Errorf("update failed"))
	s.store.On("ListNodeRuns", mock.Anything, "wfr-err").Return(nil, nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
}

func (s *EngineSuite) TestFailStaleRunUpsertNodeError() {
	// UpsertNodeRun error is logged, not fatal.
	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{
		{ID: "wfr-nrerr", WorkflowName: "wf", Status: db.WorkflowRunStatusRunning},
	}, nil)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-nrerr").Return([]*db.NodeRun{
		{RunID: "wfr-nrerr", NodeID: "n1", Status: db.NodeRunStatusPending},
	}, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(fmt.Errorf("upsert failed"))

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
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-nobc").Return(nil, nil)

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
	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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
	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-paused").Return(nil, nil)

	err := e.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	// Verify the run was failed, not recovered.
	s.store.AssertCalled(s.T(), "UpdateWorkflowRun", mock.Anything, mock.MatchedBy(func(run *db.WorkflowRun) bool {
		return run.ID == "wfr-paused" && run.Status == db.WorkflowRunStatusFailed
	}))
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

func TestAcquireReleaseNodeSlotNil(t *testing.T) {
	// acquireNodeSlot/releaseNodeSlot should be no-ops with nil semaphore.
	e := &defaultEngine{} // nodeSem is nil
	require.True(t, e.acquireNodeSlot(context.Background()))
	e.releaseNodeSlot()
	// No panic = pass.
}

func TestAcquireNodeSlotCancelledReturnsFalse(t *testing.T) {
	e := &defaultEngine{nodeSem: make(chan struct{}, 1)}
	// Fill the semaphore so acquireNodeSlot would block.
	e.nodeSem <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.False(t, e.acquireNodeSlot(ctx))
}

func TestTruncateOutput(t *testing.T) {
	require.Equal(t, "abc", truncateOutput("abc", 10))
	require.Equal(t, "abcde...", truncateOutput("abcdefghij", 5))
}

// --- version pinning ---

func (s *EngineSuite) TestStartRunSnapshotsWorkflowDef() {
	// StartRun should serialize the workflow definition into the DB run record.
	s.workflows = []config.WorkflowDef{
		{
			Name:        "pin-test",
			Description: "test workflow",
			Nodes:       []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo hi"}},
		},
	}

	var savedRun *db.WorkflowRun
	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		savedRun = args.Get(1).(*db.WorkflowRun)
	}).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()
	s.bashRunner.On("RunBash", mock.Anything, "echo hi", "", "").Return("hi", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "pin-test"})
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	require.NotNil(s.T(), savedRun)
	require.Contains(s.T(), savedRun.WorkflowDef, `"pin-test"`)
	require.Contains(s.T(), savedRun.WorkflowDef, `"echo hi"`)
}

func (s *EngineSuite) TestResolveWorkflowDefPinnedPreferred() {
	// When WorkflowDef is set on the run, resolveWorkflowDef should use it.
	e := s.engine.(*defaultEngine)

	run := &db.WorkflowRun{
		WorkflowName: "old-name",
		WorkflowDef:  `{"name":"pinned","nodes":[{"id":"n1","type":"bash","script":"echo pinned"}]}`,
	}

	wfDef, err := e.resolveWorkflowDef(run)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "pinned", wfDef.Name)
	require.Len(s.T(), wfDef.Nodes, 1)
	require.Equal(s.T(), "echo pinned", wfDef.Nodes[0].Script)
}

func (s *EngineSuite) TestResolveWorkflowDefFallbackToLive() {
	// When WorkflowDef is empty (legacy run), fall back to live config.
	s.workflows = []config.WorkflowDef{
		{Name: "live-wf", Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo live"}}},
	}
	e := s.engine.(*defaultEngine)

	run := &db.WorkflowRun{WorkflowName: "live-wf", WorkflowDef: ""}

	wfDef, err := e.resolveWorkflowDef(run)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "live-wf", wfDef.Name)
}

func (s *EngineSuite) TestResolveWorkflowDefInvalidJSON() {
	e := s.engine.(*defaultEngine)
	run := &db.WorkflowRun{WorkflowDef: "not json"}

	_, err := e.resolveWorkflowDef(run)
	require.ErrorContains(s.T(), err, "parsing pinned workflow definition")
}

// --- heartbeat ---

func (s *EngineSuite) TestHeartbeatFiresDuringNodeExecution() {
	// Verify that UpdateNodeHeartbeat is called during node execution.
	s.workflows = []config.WorkflowDef{
		{
			Name:  "hb-test",
			Nodes: []config.NodeDef{{ID: "slow", Type: config.NodeTypeBash, Script: "slow"}},
		},
	}

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	done := s.waitForRunStatus()

	// Block the bash runner long enough for the initial heartbeat to fire.
	s.bashRunner.On("RunBash", mock.Anything, "slow", "", "").Run(func(_ mock.Arguments) {
		time.Sleep(100 * time.Millisecond)
	}).Return("done", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "hb-test"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	// The initial heartbeat should have fired at least once.
	s.store.AssertCalled(s.T(), "UpdateNodeHeartbeat", mock.Anything, mock.Anything, "slow")
}

func (s *EngineSuite) TestRecoverPausedRunUsePinnedDef() {
	// Recovery should use the pinned workflow definition from the DB.
	pinnedDef := `{"name":"pinned-wf","nodes":[{"id":"approve","type":"approval","message":"ok?","timeout":"5s"},{"id":"done","type":"bash","depends_on":["approve"],"script":"echo done"}]}`

	pausedRun := &db.WorkflowRun{
		ID:           "wfr-pinned",
		WorkflowName: "pinned-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "approve",
		Inputs:       `{}`,
		WorkflowDef:  pinnedDef,
	}

	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-pinned", NodeID: "approve", Status: db.NodeRunStatusRunning},
		{RunID: "wfr-pinned", NodeID: "done", Status: db.NodeRunStatusPending},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-pinned").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-pinned").Return(
		&db.WorkflowRun{ID: "wfr-pinned", Status: db.WorkflowRunStatusRunning, PausedNodeID: "approve", WorkflowName: "pinned-wf", ChannelID: "ch1"}, nil,
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

	s.bashRunner.On("RunBash", mock.Anything, "echo done", "ch1", "").Return("done", nil)

	// No workflows in live config — recovery must use pinned def.
	s.workflows = nil

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	// Wait for approval node to pause.
	time.Sleep(200 * time.Millisecond)

	// Resume approval.
	err = s.engine.ResumeRun(context.Background(), "wfr-pinned", "approved")
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered run to complete")
	}

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo done", "ch1", "")
}

func (s *EngineSuite) TestCheckpointNodeNotInDBTreatedAsPending() {
	// When recovering from checkpoint, a node in the workflow definition that
	// has no corresponding DB record should be treated as pending and executed.
	s.workflows = []config.WorkflowDef{
		{
			Name: "checkpoint-missing",
			Nodes: []config.NodeDef{
				{ID: "a", Type: config.NodeTypeBash, Script: "echo a"},
				{ID: "b", Type: config.NodeTypeBash, DependsOn: []string{"a"}, Script: "echo b"},
			},
		},
	}

	pausedRun := &db.WorkflowRun{
		ID:           "wfr-missing",
		WorkflowName: "checkpoint-missing",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusPaused,
		PausedNodeID: "a", // doesn't matter, just needs to be non-empty for recovery
		Inputs:       `{}`,
	}

	// Only node "a" has a DB record (success); node "b" is NOT in DB at all.
	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-missing", NodeID: "a", Status: db.NodeRunStatusSuccess, Output: "a"},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{pausedRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-missing").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-missing").Return(
		&db.WorkflowRun{ID: "wfr-missing", Status: db.WorkflowRunStatusRunning, PausedNodeID: "a", WorkflowName: "checkpoint-missing", ChannelID: "ch1"}, nil,
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

	s.bashRunner.On("RunBash", mock.Anything, "echo b", "ch1", "").Return("b", nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered run to complete")
	}

	// Node "b" should have been executed (it was not in DB, treated as pending).
	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo b", "ch1", "")
}

func (s *EngineSuite) TestHeartbeatErrorPaths() {
	// Verify that heartbeat error paths (initial + ticker) are exercised
	// without breaking node execution.
	s.workflows = []config.WorkflowDef{
		{
			Name:  "hb-err",
			Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypeBash, Script: "echo ok"}},
		},
	}

	// Use a very short heartbeat interval so the ticker fires during the test.
	s.engine.(*defaultEngine).heartbeatInterval = 10 * time.Millisecond

	s.store.ExpectedCalls = nil
	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(
		&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil,
	)
	// Heartbeat returns an error — should be logged but not fail the node.
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("db locked"))

	done := make(chan db.WorkflowRunStatus, 1)
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		if run.Status != db.WorkflowRunStatusRunning {
			select {
			case done <- run.Status:
			default:
			}
		}
	}).Return(nil)

	// Make bash runner slow enough that the ticker fires at least once.
	s.bashRunner.On("RunBash", mock.Anything, "echo ok", "", "").Run(func(_ mock.Arguments) {
		time.Sleep(50 * time.Millisecond)
	}).Return("ok", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "hb-err"})
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout")
	}

	// The heartbeat should have been attempted multiple times (initial + ticker).
	calls := 0
	for _, call := range s.store.Calls {
		if call.Method == "UpdateNodeHeartbeat" {
			calls++
		}
	}
	require.GreaterOrEqual(s.T(), calls, 2, "expected at least 2 heartbeat calls (initial + ticker)")
}

// --- heartbeat-based stale node detection ---

func (s *EngineSuite) TestIsNodeHeartbeatFresh() {
	e := s.engine.(*defaultEngine)

	// No heartbeat → stale.
	require.False(s.T(), e.isNodeHeartbeatFresh(&db.NodeRun{LastHeartbeatAt: nil}))

	// Recent heartbeat → fresh.
	recent := time.Now().Add(-5 * time.Second)
	require.True(s.T(), e.isNodeHeartbeatFresh(&db.NodeRun{LastHeartbeatAt: &recent}))

	// Old heartbeat → stale.
	old := time.Now().Add(-5 * time.Minute)
	require.False(s.T(), e.isNodeHeartbeatFresh(&db.NodeRun{LastHeartbeatAt: &old}))
}

func (s *EngineSuite) TestRecoverRunningRunFreshHeartbeatReExecutes() {
	// A running workflow where a node has a fresh heartbeat should be recovered:
	// the fresh node gets re-executed instead of failed.
	s.workflows = []config.WorkflowDef{
		{
			Name: "recover-running",
			Nodes: []config.NodeDef{
				{ID: "a", Type: config.NodeTypeBash, Script: "echo a"},
				{ID: "b", Type: config.NodeTypeBash, DependsOn: []string{"a"}, Script: "echo b"},
			},
		},
	}

	freshHB := time.Now().Add(-2 * time.Second)
	runningRun := &db.WorkflowRun{
		ID:           "wfr-fresh",
		WorkflowName: "recover-running",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       `{}`,
	}

	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-fresh", NodeID: "a", Status: db.NodeRunStatusRunning, LastHeartbeatAt: &freshHB},
		{RunID: "wfr-fresh", NodeID: "b", Status: db.NodeRunStatusPending},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{runningRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-fresh").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-fresh").Return(
		&db.WorkflowRun{ID: "wfr-fresh", Status: db.WorkflowRunStatusRunning, WorkflowName: "recover-running", ChannelID: "ch1"}, nil,
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

	// Both nodes should be re-/executed.
	s.bashRunner.On("RunBash", mock.Anything, "echo a", "ch1", "").Return("a", nil)
	s.bashRunner.On("RunBash", mock.Anything, "echo b", "ch1", "").Return("b", nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusCompleted, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered run to complete")
	}

	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo a", "ch1", "")
	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo b", "ch1", "")
}

func (s *EngineSuite) TestRecoverRunningRunStaleHeartbeatFails() {
	// A running workflow where a node has a stale heartbeat — node should be
	// failed and the workflow should complete as failed.
	s.workflows = []config.WorkflowDef{
		{
			Name: "recover-stale",
			Nodes: []config.NodeDef{
				{ID: "a", Type: config.NodeTypeBash, Script: "echo a"},
				{ID: "b", Type: config.NodeTypeBash, DependsOn: []string{"a"}, Script: "echo b"},
			},
		},
	}

	staleHB := time.Now().Add(-5 * time.Minute)
	runningRun := &db.WorkflowRun{
		ID:           "wfr-stale-hb",
		WorkflowName: "recover-stale",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       `{}`,
	}

	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-stale-hb", NodeID: "a", Status: db.NodeRunStatusRunning, LastHeartbeatAt: &staleHB},
		{RunID: "wfr-stale-hb", NodeID: "b", Status: db.NodeRunStatusPending},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{runningRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-stale-hb").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-stale-hb").Return(
		&db.WorkflowRun{ID: "wfr-stale-hb", Status: db.WorkflowRunStatusRunning, WorkflowName: "recover-stale", ChannelID: "ch1"}, nil,
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

	select {
	case status := <-done:
		// Node "a" stale → failed. Node "b" depends on "a" → skipped. Workflow fails.
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered run to complete")
	}
}

func (s *EngineSuite) TestRecoverRunningRunNoHeartbeatFails() {
	// A running workflow where a node has no heartbeat (nil) — treated as stale.
	s.workflows = []config.WorkflowDef{
		{
			Name: "recover-nohb",
			Nodes: []config.NodeDef{
				{ID: "only", Type: config.NodeTypeBash, Script: "echo x"},
			},
		},
	}

	runningRun := &db.WorkflowRun{
		ID:           "wfr-nohb",
		WorkflowName: "recover-nohb",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       `{}`,
	}

	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-nohb", NodeID: "only", Status: db.NodeRunStatusRunning, LastHeartbeatAt: nil},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{runningRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-nohb").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-nohb").Return(
		&db.WorkflowRun{ID: "wfr-nohb", Status: db.WorkflowRunStatusRunning, WorkflowName: "recover-nohb", ChannelID: "ch1"}, nil,
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

	select {
	case status := <-done:
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered run to complete")
	}
}

func (s *EngineSuite) TestRecoverRunningRunMixedNodes() {
	// Mixed scenario: one completed node, one running with fresh heartbeat,
	// one running with stale heartbeat, one pending. The fresh node gets
	// re-executed, the stale one fails, and the pending one may or may not
	// execute depending on dependencies.
	s.workflows = []config.WorkflowDef{
		{
			Name: "recover-mixed",
			Nodes: []config.NodeDef{
				{ID: "done", Type: config.NodeTypeBash, Script: "echo done"},
				{ID: "fresh", Type: config.NodeTypeBash, Script: "echo fresh"},
				{ID: "stale", Type: config.NodeTypeBash, Script: "echo stale"},
				{ID: "final", Type: config.NodeTypeBash, DependsOn: []string{"done", "fresh", "stale"}, Script: "echo final"},
			},
		},
	}

	freshHB := time.Now().Add(-2 * time.Second)
	staleHB := time.Now().Add(-5 * time.Minute)
	runningRun := &db.WorkflowRun{
		ID:           "wfr-mixed",
		WorkflowName: "recover-mixed",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       `{}`,
	}

	nodeRuns := []*db.NodeRun{
		{RunID: "wfr-mixed", NodeID: "done", Status: db.NodeRunStatusSuccess, Output: "done"},
		{RunID: "wfr-mixed", NodeID: "fresh", Status: db.NodeRunStatusRunning, LastHeartbeatAt: &freshHB},
		{RunID: "wfr-mixed", NodeID: "stale", Status: db.NodeRunStatusRunning, LastHeartbeatAt: &staleHB},
		{RunID: "wfr-mixed", NodeID: "final", Status: db.NodeRunStatusPending},
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{runningRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-mixed").Return(nodeRuns, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetWorkflowRun", mock.Anything, "wfr-mixed").Return(
		&db.WorkflowRun{ID: "wfr-mixed", Status: db.WorkflowRunStatusRunning, WorkflowName: "recover-mixed", ChannelID: "ch1"}, nil,
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

	// Only "fresh" should be re-executed. "done" was already completed. "stale" stays failed.
	s.bashRunner.On("RunBash", mock.Anything, "echo fresh", "ch1", "").Return("fresh", nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)

	select {
	case status := <-done:
		// "stale" node was failed → "final" depends on it → skipped → workflow fails.
		require.Equal(s.T(), db.WorkflowRunStatusFailed, status)
	case <-time.After(5 * time.Second):
		s.T().Fatal("timeout waiting for recovered run to complete")
	}

	// "fresh" was re-executed, "done" was not (already completed).
	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo fresh", "ch1", "")
	s.bashRunner.AssertNotCalled(s.T(), "RunBash", mock.Anything, "echo done", "ch1", "")
}

func (s *EngineSuite) TestRecoverRunningRunBadInputsFallsBack() {
	// recoverRunningRun with bad inputs falls back to failStaleRun.
	s.workflows = []config.WorkflowDef{
		{Name: "bad-inputs-wf", Nodes: []config.NodeDef{{ID: "a", Type: config.NodeTypeBash, Script: "echo a"}}},
	}

	runningRun := &db.WorkflowRun{
		ID:           "wfr-bad-inp",
		WorkflowName: "bad-inputs-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       `INVALID JSON`,
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{runningRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-bad-inp").Return([]*db.NodeRun{
		{RunID: "wfr-bad-inp", NodeID: "a", Status: db.NodeRunStatusPending},
	}, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

	var capturedStatus db.WorkflowRunStatus
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		capturedStatus = run.Status
	}).Return(nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), db.WorkflowRunStatusFailed, capturedStatus)
}

func (s *EngineSuite) TestRecoverRunningRunNodeListErrorFallsBack() {
	// recoverRunningRun with node list error falls back to failStaleRun.
	s.workflows = []config.WorkflowDef{
		{Name: "nle-wf", Nodes: []config.NodeDef{{ID: "a", Type: config.NodeTypeBash, Script: "echo a"}}},
	}

	runningRun := &db.WorkflowRun{
		ID:           "wfr-nle",
		WorkflowName: "nle-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{runningRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-nle").Return(nil, fmt.Errorf("db error"))
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

	var capturedStatus db.WorkflowRunStatus
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		capturedStatus = run.Status
	}).Return(nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), db.WorkflowRunStatusFailed, capturedStatus)
}

func (s *EngineSuite) TestRecoverRunningRunSemaphoreFullFallsBack() {
	// When run semaphore is full, recoverRunningRun should fall back to failStaleRun.
	s.workflows = []config.WorkflowDef{
		{Name: "sem-wf", Nodes: []config.NodeDef{{ID: "a", Type: config.NodeTypeBash, Script: "echo a"}}},
	}

	// Create engine with a size-1 semaphore and fill it.
	e := NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, func(_, _ string) []config.WorkflowDef {
		return s.workflows
	}, "", config.WorkflowConcurrency{MaxConcurrentRuns: 1}, slog.Default())
	de := e.(*defaultEngine)
	de.runSem <- struct{}{} // fill the semaphore

	runningRun := &db.WorkflowRun{
		ID:           "wfr-sem",
		WorkflowName: "sem-wf",
		ChannelID:    "ch1",
		Status:       db.WorkflowRunStatusRunning,
		Inputs:       `{}`,
	}

	s.store.ExpectedCalls = nil
	s.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return([]*db.WorkflowRun{runningRun}, nil)
	s.store.On("ListNodeRuns", mock.Anything, "wfr-sem").Return([]*db.NodeRun{
		{RunID: "wfr-sem", NodeID: "a", Status: db.NodeRunStatusPending},
	}, nil)
	s.store.On("UpsertNodeRun", mock.Anything, mock.Anything).Return(nil)

	var capturedStatus db.WorkflowRunStatus
	s.store.On("UpdateWorkflowRun", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		run := args.Get(1).(*db.WorkflowRun)
		capturedStatus = run.Status
	}).Return(nil)

	err := e.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), db.WorkflowRunStatusFailed, capturedStatus)

	// Clean up: drain semaphore.
	<-de.runSem
}

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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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

	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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
	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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
	s.store.On("CreateWorkflowRun", mock.Anything, mock.Anything).Return(nil)
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
