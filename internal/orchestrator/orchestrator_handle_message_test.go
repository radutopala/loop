package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

// --- HandleMessage tests ---

func (s *OrchestratorSuite) TestHandleMessageUnregisteredChannel() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "ch1",
		Content:   "hello",
	})

	s.store.AssertExpectations(s.T())
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadResolved() {
	msg := &bot.IncomingMessage{
		ChannelID:    "thread1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello in thread",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	// Thread is not directly active
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil).Once()
	// Resolve thread: parent found
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
	// Parent is active
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	// Get parent channel for inheritance
	parentPerms := types.Permissions{
		Owners:  types.RoleGrant{Users: []string{"user1"}},
		Members: types.RoleGrant{Users: []string{"member1"}},
	}
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", GuildID: "g1", DirPath: "/project", Platform: types.PlatformDiscord, SessionID: "sess-parent", Permissions: parentPerms, Active: true,
	}, nil)
	// Upsert thread channel with dir_path, platform, session_id, and permissions inherited from parent
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread1" && ch.ParentID == "ch1" && ch.GuildID == "g1" &&
			ch.DirPath == "/project" && ch.Platform == "discord" && ch.SessionID == "sess-parent" &&
			len(ch.Permissions.Owners.Users) == 1 && ch.Permissions.Owners.Users[0] == "user1" &&
			len(ch.Permissions.Members.Users) == 1 && ch.Permissions.Members.Users[0] == "member1" &&
			ch.Active
	})).Return(nil)
	// Now the thread is a channel — normal flow continues
	s.store.On("GetChannel", s.ctx, "thread1").Return(&db.Channel{
		ID: 2, ChannelID: "thread1", GuildID: "g1", DirPath: "/project", ParentID: "ch1", SessionID: "sess-parent", Permissions: parentPerms, Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "thread1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "thread1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ForkSession && req.SessionID == "sess-parent"
	})).Return(&agent.AgentResponse{
		Response:  "Hi from thread!",
		SessionID: "sess-forked",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread1", "sess-forked").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "thread1" && out.Content == "Hi from thread!"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageWorktreeThread() {
	// Worktree thread — should set ParentDirPath on the agent request.
	s.store.On("IsChannelActive", s.ctx, "wt1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "wt1").Return(&db.Channel{
		ID: 3, ChannelID: "wt1", GuildID: "g1", DirPath: "/project/.worktrees/wt1",
		ParentID: "ch1", SessionID: "sess-new", Worktree: true, Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "wt1").Return(nil).Maybe()
	s.bot.On("SendStopButton", mock.Anything, "wt1", "wt1").Return("", nil).Maybe()
	s.bot.On("RemoveStopButton", mock.Anything, "wt1", "").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "wt1", 50).Return([]*db.Message{}, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "/project", SessionID: "sess-parent",
	}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/project" && req.DirPath == "/project/.worktrees/wt1" &&
			req.SystemPrompt == "" &&
			strings.Contains(req.Prompt, "IMPORTANT: Your working directory is /project/.worktrees/wt1")
	})).Return(&agent.AgentResponse{
		Response: "worktree response", SessionID: "sess-wt",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "wt1", "sess-wt").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID:    "wt1",
		GuildID:      "g1",
		AuthorName:   "user",
		Content:      "hello worktree",
		IsBotMention: true,
	})

	s.runner.AssertExpectations(s.T())
}

// TestHandleMessageThreadUnderWorktree covers a thread nested under a
// worktree channel (e.g. a scheduled task's thread): the thread row shares
// the worktree's dir_path but carries no worktree flag. The agent request
// must inherit ParentDirPath from the worktree's own parent (the root
// project channel) so the runner applies the full config merge chain —
// otherwise the root's .loop/config.json (gates, model, MCP servers) is
// silently ignored — and the prompt must carry the working-dir hint.
func (s *OrchestratorSuite) TestHandleMessageThreadUnderWorktree() {
	s.store.On("IsChannelActive", s.ctx, "task-thread").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "task-thread").Return(&db.Channel{
		ID: 4, ChannelID: "task-thread", GuildID: "g1", DirPath: "/project/.worktrees/wt1",
		ParentID: "wt1", SessionID: "sess-t", Worktree: false, Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "task-thread").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "task-thread", 50).Return([]*db.Message{}, nil)
	s.store.On("GetChannel", s.ctx, "wt1").Return(&db.Channel{
		ID: 3, ChannelID: "wt1", DirPath: "/project/.worktrees/wt1",
		ParentID: "ch1", SessionID: "sess-wt", Worktree: true,
	}, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "/project",
	}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/project" && req.DirPath == "/project/.worktrees/wt1" &&
			strings.Contains(req.Prompt, "IMPORTANT: Your working directory is /project/.worktrees/wt1")
	})).Return(&agent.AgentResponse{
		Response: "thread response", SessionID: "sess-t2",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "task-thread", "sess-t2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID:    "task-thread",
		GuildID:      "g1",
		AuthorName:   "user",
		Content:      "push the branch",
		IsBotMention: true,
	})

	s.runner.AssertExpectations(s.T())
}

// TestHandleMessageNestedWorktreeThread covers case 4: a worktree channel
// whose parent is ITSELF a worktree (created by a worktree task scheduled on
// a worktree channel). The chain must anchor at the root checkout, walking
// past the intermediate worktree.
func (s *OrchestratorSuite) TestHandleMessageNestedWorktreeThread() {
	s.store.On("IsChannelActive", s.ctx, "wt2").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "wt2").Return(&db.Channel{
		ID: 5, ChannelID: "wt2", GuildID: "g1", DirPath: "/project/.worktrees/wt1/.worktrees/wt2",
		ParentID: "wt1", SessionID: "sess-n", Worktree: true, Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "wt2").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "wt2", 50).Return([]*db.Message{}, nil)
	s.store.On("GetChannel", s.ctx, "wt1").Return(&db.Channel{
		ID: 3, ChannelID: "wt1", DirPath: "/project/.worktrees/wt1",
		ParentID: "ch1", SessionID: "sess-wt", Worktree: true,
	}, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "/project",
	}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/project" && req.DirPath == "/project/.worktrees/wt1/.worktrees/wt2"
	})).Return(&agent.AgentResponse{
		Response: "nested response", SessionID: "sess-n2",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "wt2", "sess-n2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID:    "wt2",
		GuildID:      "g1",
		AuthorName:   "user",
		Content:      "hello nested",
		IsBotMention: true,
	})

	s.runner.AssertExpectations(s.T())
}

// TestHandleMessageWorktreeRootLookupErrorFallsBack covers the fallback: when
// the root walk fails mid-chain (grandparent lookup error), a worktree
// channel still passes its immediate parent's DirPath so mounting keeps
// working.
func (s *OrchestratorSuite) TestHandleMessageWorktreeRootLookupErrorFallsBack() {
	s.store.On("IsChannelActive", s.ctx, "wt2").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "wt2").Return(&db.Channel{
		ID: 5, ChannelID: "wt2", GuildID: "g1", DirPath: "/project/.worktrees/wt1/.worktrees/wt2",
		ParentID: "wt1", SessionID: "sess-n", Worktree: true, Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "wt2").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "wt2", 50).Return([]*db.Message{}, nil)
	s.store.On("GetChannel", s.ctx, "wt1").Return(&db.Channel{
		ID: 3, ChannelID: "wt1", DirPath: "/project/.worktrees/wt1",
		ParentID: "ch1", SessionID: "sess-wt", Worktree: true,
	}, nil)
	// Grandparent lookup fails → walk returns "" → fall back to parent dir.
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("db error"))
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/project/.worktrees/wt1"
	})).Return(&agent.AgentResponse{
		Response: "resp", SessionID: "sess-n3",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "wt2", "sess-n3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID:    "wt2",
		GuildID:      "g1",
		AuthorName:   "user",
		Content:      "hello",
		IsBotMention: true,
	})

	s.runner.AssertExpectations(s.T())
}

// TestWorktreeRootForBounds covers the defensive edges of the root walk: a
// parent-id cycle exhausts the bounded loop, a channel with no parent has no
// chain, and a plain thread under a plain channel isn't a worktree chain.
func (s *OrchestratorSuite) TestWorktreeRootForBounds() {
	store := new(testutil.MockStore)
	// Cycle: two worktree channels pointing at each other.
	store.On("GetChannel", mock.Anything, "wt-a").Return(&db.Channel{
		ChannelID: "wt-a", ParentID: "wt-b", Worktree: true,
	}, nil)
	store.On("GetChannel", mock.Anything, "wt-b").Return(&db.Channel{
		ChannelID: "wt-b", ParentID: "wt-a", Worktree: true,
	}, nil)
	require.Equal(s.T(), "", worktreeRootFor(context.Background(), store,
		&db.Channel{ChannelID: "wt-a", ParentID: "wt-b", Worktree: true}))

	// Worktree with no parent: no chain to resolve.
	require.Equal(s.T(), "", worktreeRootFor(context.Background(), store,
		&db.Channel{ChannelID: "wt-orphan", Worktree: true}))

	// Plain thread with no parent id.
	require.Equal(s.T(), "", worktreeRootFor(context.Background(), store,
		&db.Channel{ChannelID: "plain"}))

	// Plain thread under a plain (non-worktree) parent: not a worktree chain.
	store.On("GetChannel", mock.Anything, "plain-parent").Return(&db.Channel{
		ChannelID: "plain-parent", DirPath: "/project",
	}, nil)
	require.Equal(s.T(), "", worktreeRootFor(context.Background(), store,
		&db.Channel{ChannelID: "t1", ParentID: "plain-parent"}))

	// Thread whose parent lookup errors.
	store.On("GetChannel", mock.Anything, "gone").Return(nil, errors.New("db error"))
	require.Equal(s.T(), "", worktreeRootFor(context.Background(), store,
		&db.Channel{ChannelID: "t2", ParentID: "gone"}))
}

func (s *OrchestratorSuite) TestHandleMessageThreadAlreadyUpserted() {
	// Second message in a thread — thread is already in DB with dir_path
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "thread1").Return(&db.Channel{
		ID: 2, ChannelID: "thread1", GuildID: "g1", DirPath: "/project", ParentID: "ch1", Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "thread1",
		GuildID:   "g1",
		Content:   "just context",
	})

	// No GetChannelParentID call — thread was already active
	s.bot.AssertNotCalled(s.T(), "GetChannelParentID", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadInactiveParent() {
	s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "thread1",
		Content:   "hello",
	})

	// Should not upsert or proceed
	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageThreadResolutionErrors() {
	tests := []struct {
		name      string
		setupMock func()
		notCalled string // method that should NOT be called
	}{
		{
			name: "parent ID error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
				s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("", errors.New("api error"))
			},
			notCalled: "GetChannel",
		},
		{
			name: "parent active check error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
				s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, errors.New("db error"))
			},
			notCalled: "GetChannel",
		},
		{
			name: "get parent channel error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
				s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("db error"))
			},
			notCalled: "UpsertChannel",
		},
		{
			name: "get parent channel nil",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
				s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
			},
			notCalled: "UpsertChannel",
		},
		{
			name: "upsert error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "thread1").Return(false, nil)
				s.bot.On("GetChannelParentID", s.ctx, "thread1").Return("ch1", nil)
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
					ID: 1, ChannelID: "ch1", GuildID: "g1", DirPath: "/project", Active: true,
				}, nil)
				s.store.On("UpsertChannel", s.ctx, mock.Anything).Return(errors.New("upsert error"))
			},
			notCalled: "InsertMessage",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setupMock()

			s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
				ChannelID: "thread1",
				Content:   "hello",
			})

			s.store.AssertNotCalled(s.T(), tc.notCalled, mock.Anything, mock.Anything)
		})
	}
}

func (s *OrchestratorSuite) TestHandleMessageDMAutoCreatesChannel() {
	msg := &bot.IncomingMessage{
		ChannelID:  "dm-ch1",
		GuildID:    "",
		AuthorID:   "user1",
		AuthorName: "Alice",
		Content:    "hello",
		MessageID:  "msg1",
		IsDM:       true,
		Timestamp:  time.Now().UTC(),
	}

	// Channel is not active
	s.store.On("IsChannelActive", s.ctx, "dm-ch1").Return(false, nil)
	// Not a thread
	s.bot.On("GetChannelParentID", s.ctx, "dm-ch1").Return("", nil)
	// Auto-create DM channel (platform comes from context, set by BotRouter in production)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "dm-ch1" && ch.Name == "DM" && ch.Active
	})).Return(nil)
	// Normal flow continues
	s.store.On("GetChannel", s.ctx, "dm-ch1").Return(&db.Channel{
		ID: 1, ChannelID: "dm-ch1", Active: true, Platform: types.PlatformLocal,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "dm-ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "dm-ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Hello from DM!",
		SessionID: "sess-dm",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "dm-ch1", "sess-dm").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "dm-ch1" && out.Content == "Hello from DM!"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageDMAutoCreateFails() {
	msg := &bot.IncomingMessage{
		ChannelID: "dm-ch1",
		GuildID:   "",
		Content:   "hello",
		IsDM:      true,
	}

	s.store.On("IsChannelActive", s.ctx, "dm-ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "dm-ch1").Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).Return(errors.New("upsert error"))

	s.orch.HandleMessage(s.ctx, msg)

	// Should not proceed
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageNonDMUnregisteredChannelDropped() {
	// Non-triggered message to an unregistered channel (not a thread either) should be dropped
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "ch1",
		Content:   "hello",
		// No trigger flags set
	})

	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageMentionAutoCreatesChannel() {
	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello bot",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("general", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ch1" && ch.GuildID == "g1" && ch.Name == "general" && ch.Active
	})).Return(nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true, Platform: types.PlatformLocal,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Hello!",
		SessionID: "sess1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.Content == "Hello!"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageMentionAutoCreateFails() {
	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		Content:      "hello bot",
		IsBotMention: true,
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("general", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).Return(errors.New("upsert error"))

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessagePrefixAutoCreatesChannel() {
	msg := &bot.IncomingMessage{
		ChannelID:  "ch1",
		AuthorID:   "user1",
		AuthorName: "Alice",
		Content:    "do something",
		MessageID:  "msg1",
		HasPrefix:  true,
		Timestamp:  time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "ch1").Return("", nil)
	s.bot.On("GetChannelName", s.ctx, "ch1").Return("dev-ops", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ch1" && ch.Name == "dev-ops" && ch.Active
	})).Return(nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true, Platform: types.PlatformDiscord,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Done!",
		SessionID: "sess1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
}

// --- HandleChannelJoin tests ---

func (s *OrchestratorSuite) TestHandleChannelJoin() {
	tests := []struct {
		name         string
		nameReturn   string
		nameErr      error
		upsertErr    error
		expectedName string
	}{
		{"success", "project-x", nil, nil, "project-x"},
		{"name lookup fails uses default", "", errors.New("api error"), nil, "channel"},
		{"empty name uses default", "", nil, nil, "channel"},
		{"upsert error", "project-x", nil, errors.New("db error"), "project-x"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.bot.On("GetChannelName", s.ctx, "ch1").Return(tc.nameReturn, tc.nameErr)
			s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
				return ch.ChannelID == "ch1" && ch.Name == tc.expectedName && ch.Platform == types.PlatformDiscord && ch.Active
			})).Return(tc.upsertErr)

			s.orch.HandleChannelJoin(s.ctx, "ch1", types.PlatformDiscord)

			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *OrchestratorSuite) TestHandleMessageEarlyErrors() {
	tests := []struct {
		name      string
		setupMock func()
		msg       *bot.IncomingMessage
		notCalled string
	}{
		{
			name: "IsChannelActive error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(false, errors.New("db err"))
			},
			msg:       &bot.IncomingMessage{ChannelID: "ch1"},
			notCalled: "GetChannel",
		},
		{
			name: "GetChannel error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("channel err"))
			},
			msg:       &bot.IncomingMessage{ChannelID: "ch1", GuildID: "g1"},
			notCalled: "InsertMessage",
		},
		{
			name: "GetChannel nil",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
			},
			msg:       &bot.IncomingMessage{ChannelID: "ch1", GuildID: "g1"},
			notCalled: "InsertMessage",
		},
		{
			name: "InsertMessage error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
				s.store.On("InsertMessage", s.ctx, mock.Anything).Return(errors.New("insert err"))
			},
			msg:       &bot.IncomingMessage{ChannelID: "ch1", IsBotMention: true},
			notCalled: "GetRecentMessages",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setupMock()

			s.orch.HandleMessage(s.ctx, tc.msg)

			s.store.AssertNotCalled(s.T(), tc.notCalled, mock.Anything, mock.Anything)
		})
	}
}

// triggeredMsg returns a standard triggered IncomingMessage for tests.
func triggeredMsg() *bot.IncomingMessage {
	return &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorName:   "Alice",
		Content:      "hello",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}
}

// setupTriggeredBase sets up common mocks for a triggered message through GetRecentMessages.
func (s *OrchestratorSuite) setupTriggeredBase() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
}

// setupTriggeredThroughRun extends setupTriggeredBase through a successful runner call.
func (s *OrchestratorSuite) setupTriggeredThroughRun(response string, sessionID string) {
	s.setupTriggeredBase()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  response,
		SessionID: sessionID,
	}, nil)
}

func (s *OrchestratorSuite) TestHandleMessageNotTriggered() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "ch1",
		GuildID:   "g1",
		Content:   "just a message",
		// Not triggered: IsBotMention=false, IsReplyToBot=false, HasPrefix=false, IsDM=false
	})

	s.store.AssertNotCalled(s.T(), "GetRecentMessages", mock.Anything, mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageTriggeredFullFlow() {
	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hello bot",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	recentMsgs := []*db.Message{
		{ID: 2, AuthorName: "Alice", Content: "hello bot", IsBot: false},
		{ID: 1, AuthorName: "Bot", Content: "hi", IsBot: true},
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	// First GetChannel (in HandleMessage)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true, SessionID: "session123"}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return(recentMsgs, nil)
	// Second GetChannel (in processTriggeredMessage for session data) — returns same object
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "ch1" && req.SessionID == "session123" && len(req.Messages) == 2 && req.Prompt == "Alice: hello bot"
	})).Return(&agent.AgentResponse{
		Response:  "Hello Alice!",
		SessionID: "session456",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "session456").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.ChannelID == "ch1" && out.Content == "Hello Alice!" && out.ReplyToMessageID == "msg1"
	})).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{2, 1}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
	s.runner.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageWithEventBroadcaster() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		GuildID:      "g1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hi",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	// Recent messages ordered DESC.
	//   m-20: newer than trigger — out of toMark window entirely.
	//   m-7:  older than trigger, but still queued (priority-bumped scenario)
	//         — must be SKIPPED so the drain queue can pick it up next.
	//   m-5:  older than trigger, non-triggered chat history — must be MARKED.
	recentMsgs := []*db.Message{
		{ID: 20, MsgID: "m-20", AuthorName: "Bob", Content: "queued newer", ChannelID: "ch1"},
		{ID: 10, MsgID: "msg1", AuthorName: "Alice", Content: "hi", ChannelID: "ch1"},
		{ID: 7, MsgID: "m-7", AuthorName: "Alice", Content: "queued older", ChannelID: "ch1", IsTriggered: true},
		{ID: 5, MsgID: "m-5", AuthorName: "Alice", Content: "old", ChannelID: "ch1"},
	}
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return(recentMsgs, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Hello!",
		SessionID: "sess1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	// Only trigger (ID:10) and non-triggered older (ID:5) should be marked.
	// m-20 is outside the window; m-7 is queued and must be left alone.
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{10, 5}).Return(nil)

	// Expect event broadcasts: user message, running status, completed status, bot message, messages processed
	eb.On("BroadcastMessageCreated", "ch1", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.AuthorName == "Alice" && d.Content == "hi" && !d.IsBot
	})).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.TriggerContent == "hi" && d.RunID != ""
	})).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "completed" && d.RunID != ""
	})).Return()
	eb.On("BroadcastMessageCreated", "ch1", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.AuthorName == "agent" && d.Content == "Hello!" && d.IsBot
	})).Return()
	// Only trigger and older message IDs should be broadcast as processed.
	eb.On("BroadcastMessagesProcessed", "ch1", events.MessagesProcessedData{
		MsgIDs: []string{"msg1", "m-5"},
	}).Return()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageWithEventBroadcasterRunError() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hi",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("runner failed"))
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.TriggerContent == "hi" && d.RunID != ""
	})).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error" && d.Error == "runner failed" && d.RunID != ""
	})).Return()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageWithEventBroadcasterAgentError() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hi",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Error: "agent broke"}, nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.TriggerContent == "hi" && d.RunID != ""
	})).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error" && d.Error == "agent broke" && d.RunID != ""
	})).Return()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageTriggeredWithNilSession() {
	msg := &bot.IncomingMessage{
		ChannelID:  "ch1",
		GuildID:    "g1",
		AuthorName: "Alice",
		Content:    "hello",
		MessageID:  "msg1",
		HasPrefix:  true,
		Timestamp:  time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == "" && len(req.Messages) == 0 && req.Prompt == "Alice: hello"
	})).Return(&agent.AgentResponse{
		Response:  "Hi!",
		SessionID: "new-session",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "new-session").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.store.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleMessageTriggeredErrors() {
	tests := []struct {
		name      string
		setupMock func()
		assertFn  func()
	}{
		{
			name: "GetRecentMessages error",
			setupMock: func() {
				s.setupTriggeredBase()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return(nil, errors.New("db err"))
			},
			assertFn: func() {
				s.runner.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything)
			},
		},
		{
			name: "GetSession error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Once()
				s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
				s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("session err")).Once()
			},
			assertFn: func() {
				s.runner.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything)
			},
		},
		{
			name: "runner error",
			setupMock: func() {
				s.setupTriggeredBase()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
				s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("runner err"))
				s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
					return out.Content == "Sorry, I encountered an error processing your request."
				})).Return(nil)
			},
			assertFn: func() { s.bot.AssertExpectations(s.T()) },
		},
		{
			name: "session limit schedules auto-continue retry",
			setupMock: func() {
				s.orch.cfg.Store(&config.Config{
					AgentRetry: config.AgentRetryConfig{SessionLimitAutoContinue: true},
				})
				s.orch.timeNow = func() time.Time {
					loc, _ := time.LoadLocation("Europe/Bucharest")
					return time.Date(2026, 6, 23, 14, 0, 0, 0, loc)
				}
				s.setupTriggeredBase()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{
					{ID: 11, MsgID: "msg1", Content: "hello"},
				}, nil)
				s.runner.On("Run", mock.Anything, mock.Anything).Return(nil,
					errors.New("claude returned error: You've hit your session limit · resets 11:30pm (Europe/Bucharest)"))
				s.scheduler.On("ListTasks", s.ctx, "ch1").Return([]*db.ScheduledTask(nil), nil)
				s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(t *db.ScheduledTask) bool {
					return t.Type == db.TaskTypeOnce && t.Prompt == "continue" &&
						t.TemplateName == sessionLimitTemplateName
				})).Return(int64(1), nil)
				s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
					return strings.Contains(out.Content, "automatically continue")
				})).Return(nil)
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{11}).Return(nil)
			},
			assertFn: func() {
				s.scheduler.AssertExpectations(s.T())
				s.bot.AssertExpectations(s.T())
			},
		},
		{
			name: "runner error marks trigger processed",
			setupMock: func() {
				eb := new(MockEventBroadcaster)
				s.orch.SetEventBroadcaster(eb)
				s.setupTriggeredBase()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{
					{ID: 11, MsgID: "msg1", Content: "hello"},
					{ID: 10, MsgID: "msg0", Content: "queued older", IsTriggered: true},
				}, nil)
				s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("runner err"))
				s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
					return out.Content == "Sorry, I encountered an error processing your request."
				})).Return(nil)
				// markTriggerProcessed marks the trigger only; the older queued
				// triggered row (msg0) must survive for the drain to pick up.
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{11}).Return(nil)
				eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return().Maybe()
				eb.On("BroadcastAgentStatus", "ch1", mock.Anything).Return().Maybe()
				eb.On("BroadcastMessagesProcessed", "ch1", events.MessagesProcessedData{
					MsgIDs: []string{"msg1"},
				}).Return()
			},
			assertFn: func() {
				s.store.AssertExpectations(s.T())
			},
		},
		{
			name: "runner error mark processed db error",
			setupMock: func() {
				s.setupTriggeredBase()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{
					{ID: 11, MsgID: "msg1", Content: "hello"},
				}, nil)
				s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("runner err"))
				s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
					return out.Content == "Sorry, I encountered an error processing your request."
				})).Return(nil)
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{11}).Return(errors.New("db err"))
			},
			assertFn: func() {
				s.store.AssertExpectations(s.T())
			},
		},
		{
			name: "agent response error",
			setupMock: func() {
				s.setupTriggeredBase()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
				s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Error: "agent broke"}, nil)
				s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
					return out.Content == "Agent error: agent broke"
				})).Return(nil)
			},
			assertFn: func() {
				s.bot.AssertCalled(s.T(), "SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
					return out.Content == "Agent error: agent broke"
				}))
			},
		},
		{
			name: "UpdateSessionID error still sends and marks",
			setupMock: func() {
				s.setupTriggeredThroughRun("ok", "data")
				s.store.On("UpdateSessionID", s.ctx, "ch1", "data").Return(errors.New("session err"))
				s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)
			},
			assertFn: func() {
				s.bot.AssertExpectations(s.T())
				s.store.AssertExpectations(s.T())
			},
		},
		{
			name: "SendResponse error still marks",
			setupMock: func() {
				s.setupTriggeredThroughRun("ok", "")
				s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
				s.bot.On("SendMessage", s.ctx, mock.Anything).Return(errors.New("send err"))
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)
			},
			assertFn: func() { s.store.AssertExpectations(s.T()) },
		},
		{
			name: "MarkProcessed error",
			setupMock: func() {
				s.setupTriggeredThroughRun("ok", "")
				s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
				s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(errors.New("mark err"))
			},
			assertFn: func() { s.store.AssertExpectations(s.T()) },
		},
		{
			name: "typing error still completes",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
				s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
				s.bot.On("SendTyping", mock.Anything, "ch1").Return(errors.New("typing err"))
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
				s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "ok"}, nil)
				s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
				s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)
			},
			assertFn: func() { s.store.AssertExpectations(s.T()) },
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setupMock()
			s.orch.HandleMessage(s.ctx, triggeredMsg())
			tc.assertFn()
		})
	}
}

func (s *OrchestratorSuite) TestHandleMessageInsertBotResponseErrors() {
	tests := []struct {
		name      string
		setupMock func()
	}{
		{
			name: "GetChannel for bot response returns error",
			setupMock: func() {
				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Once()
				s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil).Once()
				s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil).Once()
				s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "ok"}, nil)
				s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
				s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("channel err")).Once()
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)
			},
		},
		{
			name: "InsertMessage for bot response fails",
			setupMock: func() {
				// Remove the default Maybe() InsertMessage for bot messages so our specific expectation takes effect.
				filtered := s.store.ExpectedCalls[:0]
				for _, c := range s.store.ExpectedCalls {
					if c.Method != "InsertMessage" {
						filtered = append(filtered, c)
					}
				}
				s.store.ExpectedCalls = filtered

				s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
				s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
				s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil).Once()
				s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
				s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
				s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Response: "ok"}, nil)
				s.store.On("UpdateSessionID", s.ctx, "ch1", "").Return(nil)
				s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
				s.store.On("InsertMessage", s.ctx, mock.Anything).Return(errors.New("insert err")).Once()
				s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setupMock()
			s.orch.HandleMessage(s.ctx, triggeredMsg())
			s.store.AssertExpectations(s.T())
		})
	}
}

// Bot-self-triggered runs (e.g. the agent re-entering via send_message /
// create_thread MCP tools) must tag agent.status broadcasts with Trigger="bot"
// so the renderer suppresses the macOS dock bounce — those chains are indirect
// and not user-actionable.
func (s *OrchestratorSuite) TestHandleMessageBotSelfTriggerTagsBroadcastsAsBot() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	// Override the default IsBotUser(*)->false expectation so BOT-as-author
	// resolves to true. Keep the catch-all for everything else.
	s.bot = new(MockBot)
	s.bot.On("BotUserID").Return("BOT").Maybe()
	s.bot.On("IsBotUser", "BOT").Return(true).Maybe()
	s.bot.On("IsBotUser", mock.Anything).Return(false).Maybe()
	s.bot.On("SendStopButton", mock.Anything, mock.Anything, mock.Anything).Return("stop-msg-1", nil).Maybe()
	s.bot.On("RemoveStopButton", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{}, nil)
	s.orch.SetSynchronousDrain()
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "BOT",
		AuthorName:   "agent",
		Content:      "hi from agent",
		MessageID:    "msg-bot-1",
		IsBotMention: true,
		Platform:     types.PlatformDiscord,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{
		{ID: 10, MsgID: "msg-bot-1", AuthorName: "agent", Content: "hi from agent", ChannelID: "ch1"},
	}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "ack",
		SessionID: "sess1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{10}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.Trigger == "bot"
	})).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "completed" && d.Trigger == "bot"
	})).Return()
	eb.On("BroadcastMessagesProcessed", "ch1", mock.Anything).Return()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertExpectations(s.T())
}

// Bot-self-triggered agent errors must still tag the error-status broadcast
// with Trigger="bot" so the renderer doesn't bounce the dock for indirect
// chains that failed.
func (s *OrchestratorSuite) TestHandleMessageBotSelfTriggerErrorTagsBroadcastsAsBot() {
	eb := new(MockEventBroadcaster)

	s.bot = new(MockBot)
	s.bot.On("BotUserID").Return("BOT").Maybe()
	s.bot.On("IsBotUser", "BOT").Return(true).Maybe()
	s.bot.On("IsBotUser", mock.Anything).Return(false).Maybe()
	s.bot.On("SendStopButton", mock.Anything, mock.Anything, mock.Anything).Return("stop-msg-1", nil).Maybe()
	s.bot.On("RemoveStopButton", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, s.runner, s.scheduler, logger, config.Config{}, nil)
	s.orch.SetSynchronousDrain()
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "BOT",
		AuthorName:   "agent",
		Content:      "hi from agent",
		MessageID:    "msg-bot-err",
		IsBotMention: true,
		Platform:     types.PlatformDiscord,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{
		{ID: 12, MsgID: "msg-bot-err", AuthorName: "agent", Content: "hi from agent", ChannelID: "ch1"},
	}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Error: "agent broke"}, nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil).Maybe()
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{12}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.Trigger == "bot"
	})).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error" && d.Trigger == "bot" && d.Error == "agent broke"
	})).Return()
	eb.On("BroadcastMessagesProcessed", "ch1", mock.Anything).Return()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertExpectations(s.T())
}

// Real user messages must NOT carry a bot/scheduled trigger — the renderer
// relies on the empty trigger to bounce the dock when the user's reply lands.
func (s *OrchestratorSuite) TestHandleMessageUserTriggerLeavesTriggerEmpty() {
	eb := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(eb)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "user1",
		AuthorName:   "Alice",
		Content:      "hi",
		MessageID:    "msg-user-1",
		IsBotMention: true,
		Platform:     types.PlatformDiscord,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Active: true}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{
		{ID: 11, MsgID: "msg-user-1", AuthorName: "Alice", Content: "hi", ChannelID: "ch1"},
	}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "Hello!",
		SessionID: "sess2",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{11}).Return(nil)

	eb.On("BroadcastMessageCreated", "ch1", mock.Anything).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.Trigger == ""
	})).Return()
	eb.On("BroadcastAgentStatus", "ch1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "completed" && d.Trigger == ""
	})).Return()
	eb.On("BroadcastMessagesProcessed", "ch1", mock.Anything).Return()

	s.orch.HandleMessage(s.ctx, msg)

	eb.AssertExpectations(s.T())
}
