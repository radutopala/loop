package workflow

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
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
