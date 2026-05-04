package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/workflow"
)

// --- OrchestratorCommandsSuite: tests for commands.go functions ---

type OrchestratorCommandsSuite struct {
	suite.Suite
	store *testutil.MockStore
	bot   *MockBot
	sched *testutil.MockScheduler
	orch  *Orchestrator
	ctx   context.Context
}

func TestOrchestratorCommandsSuite(t *testing.T) {
	suite.Run(t, new(OrchestratorCommandsSuite))
}

func (s *OrchestratorCommandsSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.bot = new(MockBot)
	s.sched = new(testutil.MockScheduler)
	s.ctx = context.Background()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, new(MockRunner), s.sched, logger, config.Config{}, nil)

	// Default: bot inserts are noisy in command tests; allow silently.
	s.store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.IsBot
	})).Return(nil).Maybe()
	// GetChannel for storeBotMessage calls.
	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
}

// sendMessageMatcher returns a mock.MatchedBy predicate that checks content contains the given substring.
func sendMessageContains(sub string) any {
	return mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, sub)
	})
}

// --- SetWorkflowEngine ---

func (s *OrchestratorCommandsSuite) TestSetWorkflowEngine() {
	require.Nil(s.T(), s.orch.workflowEngine)
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	require.Same(s.T(), wfe, s.orch.workflowEngine)
}

// --- channelConfig ---

func (s *OrchestratorCommandsSuite) TestChannelConfigNilChannel() {
	s.orch.cfg.Store(&config.Config{LoopDir: "/loop"})
	cfg := s.orch.channelConfig(nil)
	require.Equal(s.T(), "/loop", cfg.LoopDir)
}

func (s *OrchestratorCommandsSuite) TestChannelConfigNoDirPath() {
	s.orch.cfg.Store(&config.Config{LoopDir: "/loop"})
	ch := &db.Channel{ChannelID: "ch1", DirPath: ""}
	cfg := s.orch.channelConfig(ch)
	require.Equal(s.T(), "/loop", cfg.LoopDir)
}

func (s *OrchestratorCommandsSuite) TestChannelConfigWithDirPath() {
	base := &config.Config{LoopDir: "/loop", PromptShortcuts: []config.PromptShortcut{{Name: "global", Prompt: "global prompt"}}}
	s.orch.cfg.Store(base)
	s.orch.loadProjectConfig = func(dirPath string, mainCfg *config.Config) (*config.Config, error) {
		merged := *mainCfg
		merged.LoopDir = "/project/loop"
		return &merged, nil
	}
	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	cfg := s.orch.channelConfig(ch)
	require.Equal(s.T(), "/project/loop", cfg.LoopDir)
}

func (s *OrchestratorCommandsSuite) TestChannelConfigLoadProjectConfigError() {
	base := &config.Config{LoopDir: "/loop"}
	s.orch.cfg.Store(base)
	s.orch.loadProjectConfig = func(_ string, _ *config.Config) (*config.Config, error) {
		return nil, errors.New("load failed")
	}
	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	cfg := s.orch.channelConfig(ch)
	// Falls back to global config.
	require.Equal(s.T(), "/loop", cfg.LoopDir)
}

// --- handleShortcutsInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleShortcutsInteractionNoShortcuts() {
	s.orch.cfg.Store(&config.Config{PromptShortcuts: nil})
	s.bot.On("SendMessage", s.ctx, sendMessageContains("No prompt shortcuts configured.")).Return(nil)

	s.orch.handleShortcutsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutsInteractionListsShortcuts() {
	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{
			{Name: "review", Description: "Review code", Prompt: "Review this"},
			{Name: "test", Prompt: "Run tests please"},
		},
	})
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "Available shortcuts:") &&
			strings.Contains(out.Content, "**review**") &&
			strings.Contains(out.Content, "Review code") &&
			strings.Contains(out.Content, "**test**") &&
			strings.Contains(out.Content, "Run tests please")
	})).Return(nil)

	s.orch.handleShortcutsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutsInteractionDescriptionTruncatedFromPrompt() {
	longPrompt := strings.Repeat("x", 100)
	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{
			{Name: "long", Prompt: longPrompt},
		},
	})
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		// Prompt is truncated to 60 chars when no description.
		return strings.Contains(out.Content, "**long**") &&
			!strings.Contains(out.Content, longPrompt)
	})).Return(nil)

	s.orch.handleShortcutsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

// --- handleShortcutInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleShortcutInteractionNoName() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleShortcutInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": ""},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutInteractionUnknown() {
	s.orch.cfg.Store(&config.Config{PromptShortcuts: []config.PromptShortcut{{Name: "review", Prompt: "Review this"}}})
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Unknown shortcut: missing")).Return(nil)

	s.orch.handleShortcutInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": "missing"},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutInteractionHappyPath() {
	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{
			{Name: "review", Prompt: "Please review the code"},
		},
	})
	s.bot.On("HandleIncomingMessage", s.ctx, "ch1", "user1", "Please review the code", "").Once()

	s.orch.handleShortcutInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		AuthorID:  "user1",
		Options:   map[string]string{"name": "review"},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutInteractionResolveError() {
	// PromptShortcut with both Prompt and PromptPath set triggers a resolve error.
	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{
			{Name: "bad", Prompt: "inline", PromptPath: "file.md"},
		},
	})
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to resolve shortcut prompt:")).Return(nil)

	s.orch.handleShortcutInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		AuthorID:  "user1",
		Options:   map[string]string{"name": "bad"},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowsInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionNoEngine() {
	// workflowEngine is nil by default.
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionListError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListWorkflows", s.ctx, "", "").Return(nil, errors.New("db error"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to list workflows.")).Return(nil)

	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionEmpty() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListWorkflows", s.ctx, "", "").Return([]config.WorkflowDef{}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("No workflows configured.")).Return(nil)

	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionListsWorkflows() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListWorkflows", s.ctx, "/project", "").Return([]config.WorkflowDef{
		{Name: "fix-issue", Description: "Fix a GitHub issue"},
		{Name: "validate", Nodes: []config.NodeDef{{}, {}}},
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "Available workflows:") &&
			strings.Contains(out.Content, "**fix-issue**") &&
			strings.Contains(out.Content, "Fix a GitHub issue") &&
			strings.Contains(out.Content, "**validate**") &&
			strings.Contains(out.Content, "2 nodes")
	})).Return(nil)

	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, ch)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionWithParentDirPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)

	// Override the maybe GetChannel to return a parent channel.
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{ChannelID: "parent-ch", DirPath: "/parent"}, nil)
	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	wfe.On("ListWorkflows", s.ctx, "/project", "/parent").Return([]config.WorkflowDef{
		{Name: "deploy", Description: "Deploy app"},
	}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("**deploy**")).Return(nil)

	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project", ParentID: "parent-ch"}
	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, ch)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowRunInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowRunInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunInteractionNoName() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleWorkflowRunInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": ""},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunInteractionHappyPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("StartRun", s.ctx, workflow.StartRunOptions{
		WorkflowName: "fix-issue",
		ChannelID:    "ch1",
		DirPath:      "/project",
	}).Return("run-abc", nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, `"fix-issue"`) &&
			strings.Contains(out.Content, "run-abc")
	})).Return(nil)

	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	s.orch.handleWorkflowRunInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": "fix-issue"},
	}, ch)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunInteractionStartError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("StartRun", s.ctx, mock.Anything).Return("", errors.New("workflow not found"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to start workflow:")).Return(nil)

	s.orch.handleWorkflowRunInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": "missing"},
	}, nil)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowCancelInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowCancelInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowCancelInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowCancelInteractionNoRunID() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleWorkflowCancelInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": ""},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowCancelInteractionHappyPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("CancelRun", s.ctx, "run-123").Return(nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-123 cancelled")).Return(nil)

	s.orch.handleWorkflowCancelInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-123"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowCancelInteractionError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("CancelRun", s.ctx, "run-123").Return(errors.New("not found"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to cancel workflow run:")).Return(nil)

	s.orch.handleWorkflowCancelInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-123"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowDeleteInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowDeleteInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowDeleteInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowDeleteInteractionNoRunID() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleWorkflowDeleteInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": ""},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowDeleteInteractionHappyPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("DeleteRun", s.ctx, "run-456").Return(nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-456 deleted")).Return(nil)

	s.orch.handleWorkflowDeleteInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-456"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowDeleteInteractionError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("DeleteRun", s.ctx, "run-456").Return(errors.New("not found"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to delete workflow run:")).Return(nil)

	s.orch.handleWorkflowDeleteInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-456"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowRetryInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRetryInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowRetryInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRetryInteractionNoRunID() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleWorkflowRetryInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": ""},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRetryInteractionHappyPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("RetryRun", s.ctx, "run-old").Return("run-new", nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-new")).Return(nil)

	s.orch.handleWorkflowRetryInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-old"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRetryInteractionError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("RetryRun", s.ctx, "run-old").Return("", errors.New("run not found"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to retry workflow run:")).Return(nil)

	s.orch.handleWorkflowRetryInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-old"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowRunsInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunsInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowRunsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunsInteractionListError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListRuns", s.ctx, "", 10, 0).Return(nil, errors.New("db error"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to list workflow runs.")).Return(nil)

	s.orch.handleWorkflowRunsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunsInteractionEmpty() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListRuns", s.ctx, "", 10, 0).Return([]*db.WorkflowRun{}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("No recent workflow runs.")).Return(nil)

	s.orch.handleWorkflowRunsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunsInteractionListsRuns() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListRuns", s.ctx, "", 10, 0).Return([]*db.WorkflowRun{
		{ID: "run-abc", WorkflowName: "fix-issue", Status: "completed", StartedAt: time.Now().Add(-5 * time.Minute)},
		{ID: "run-def", WorkflowName: "validate", Status: "running", StartedAt: time.Now().Add(-1 * time.Minute)},
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "Recent workflow runs:") &&
			strings.Contains(out.Content, "**fix-issue**") &&
			strings.Contains(out.Content, "run-abc") &&
			strings.Contains(out.Content, "**validate**") &&
			strings.Contains(out.Content, "run-def")
	})).Return(nil)

	s.orch.handleWorkflowRunsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- HandleInteraction dispatch for new commands ---

func (s *OrchestratorCommandsSuite) TestHandleInteractionShortcuts() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{{Name: "deploy", Prompt: "Deploy the app"}},
	})
	s.bot.On("SendMessage", s.ctx, sendMessageContains("**deploy**")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "shortcuts",
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionShortcut() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{{Name: "review", Prompt: "Review the code"}},
	})
	s.bot.On("HandleIncomingMessage", s.ctx, "ch1", "user1", "Review the code", "").Once()

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		AuthorID:    "user1",
		CommandName: "shortcut",
		Options:     map[string]string{"name": "review"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflows() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListWorkflows", s.ctx, "", "").Return([]config.WorkflowDef{
		{Name: "my-flow", Description: "My workflow"},
	}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("**my-flow**")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflows",
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowRun() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("StartRun", s.ctx, workflow.StartRunOptions{
		WorkflowName: "deploy",
		ChannelID:    "ch1",
		DirPath:      "",
	}).Return("run-xyz", nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-xyz")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-run",
		Options:     map[string]string{"name": "deploy"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowCancel() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("CancelRun", s.ctx, "run-to-cancel").Return(nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-to-cancel cancelled")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-cancel",
		Options:     map[string]string{"run_id": "run-to-cancel"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowDelete() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("DeleteRun", s.ctx, "run-to-delete").Return(nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-to-delete deleted")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-delete",
		Options:     map[string]string{"run_id": "run-to-delete"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowRetry() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("RetryRun", s.ctx, "run-old").Return("run-new-2", nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-new-2")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-retry",
		Options:     map[string]string{"run_id": "run-old"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowRuns() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListRuns", s.ctx, "", 10, 0).Return([]*db.WorkflowRun{
		{ID: "run-1", WorkflowName: "build", Status: "completed", StartedAt: time.Now().Add(-10 * time.Minute)},
	}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("**build**")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-runs",
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}
