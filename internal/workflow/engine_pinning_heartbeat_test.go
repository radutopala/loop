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
)

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
	s.store.On("CreateWorkflowRunWithNodes", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
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

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	// Block the bash runner long enough for the initial heartbeat to fire.
	s.bashRunner.On("RunBash", mock.Anything, "slow", "", "").Run(func(_ mock.Arguments) {
		time.Sleep(100 * time.Millisecond)
	}).Return("done", nil)

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "hb-test"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

	// The initial heartbeat should have fired at least once.
	s.store.AssertCalled(s.T(), "UpdateNodeHeartbeat", mock.Anything, mock.Anything, "slow", 0)
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
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

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
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

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
	s.expectRunPersistence()
	s.store.On("GetWorkflowRun", mock.Anything, mock.Anything).Return(
		&db.WorkflowRun{Status: db.WorkflowRunStatusRunning}, nil,
	)
	// Heartbeat returns an error — should be logged but not fail the node.
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("db locked"))

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

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

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
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)

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
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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

	s.awaitStatus(done, db.WorkflowRunStatusFailed)
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
	s.store.On("UpdateNodeHeartbeat", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-bad-inp", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "MarkRunFailedWithStaleNodes", mock.Anything, "wfr-bad-inp", mock.Anything, mock.Anything, mock.Anything)
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
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-nle", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err := s.engine.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "MarkRunFailedWithStaleNodes", mock.Anything, "wfr-nle", mock.Anything, mock.Anything, mock.Anything)
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
	s.store.On("MarkRunFailedWithStaleNodes", mock.Anything, "wfr-sem", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	err := e.RecoverRuns(context.Background())
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "MarkRunFailedWithStaleNodes", mock.Anything, "wfr-sem", mock.Anything, mock.Anything, mock.Anything)

	// Clean up: drain semaphore.
	<-de.runSem
}
