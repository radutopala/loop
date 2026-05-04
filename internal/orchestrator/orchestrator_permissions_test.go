package orchestrator

import (
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// --- Permissions tests ---

func (s *OrchestratorSuite) TestConfigPermissionsForEmptyConfig() {
	// Default zero-value config → empty permissions.
	s.orch.cfg.Store(&config.Config{})
	perms := s.orch.configPermissionsFor("")
	require.True(s.T(), perms.IsEmpty())
}

func (s *OrchestratorSuite) TestConfigPermissionsForGlobalNoProjectConfig() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}},
	})
	// Empty dirPath → global permissions returned directly.
	perms := s.orch.configPermissionsFor("")
	require.Equal(s.T(), []string{"U1"}, perms.Owners.Users)
}

func (s *OrchestratorSuite) TestConfigPermissionsForWithDirPath() {
	s.orch.loadProjectConfig = func(_ string, mainCfg *config.Config) (*config.Config, error) {
		return mainCfg, nil
	}

	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}},
	})
	// Non-existent project config → LoadProjectConfig returns global config → global permissions returned.
	perms := s.orch.configPermissionsFor("/some/project")
	require.Equal(s.T(), []string{"U1"}, perms.Owners.Users)
}

func (s *OrchestratorSuite) TestConfigPermissionsForLoadError() {
	s.orch.loadProjectConfig = func(_ string, _ *config.Config) (*config.Config, error) {
		return nil, errors.New("permission denied")
	}

	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}},
	})
	// Read error (not ErrNotExist) → LoadProjectConfig returns error → global permissions returned as fallback.
	perms := s.orch.configPermissionsFor("/some/project")
	require.Equal(s.T(), []string{"U1"}, perms.Owners.Users)
}

func (s *OrchestratorSuite) TestConfigPermissionsForProjectOverridesGlobal() {
	s.orch.loadProjectConfig = func(_ string, _ *config.Config) (*config.Config, error) {
		merged := config.Config{
			Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"U2"}}},
		}
		return &merged, nil
	}

	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}},
	})
	perms := s.orch.configPermissionsFor("/project")
	require.Equal(s.T(), []string{"U2"}, perms.Owners.Users)
}

func (s *OrchestratorSuite) TestResolveRole() {
	tests := []struct {
		name        string
		cfgPerms    types.Permissions
		dbPerms     types.Permissions
		authorID    string
		authorRoles []string
		expected    types.Role
	}{
		{
			name:     "bootstrap: both empty → owner",
			cfgPerms: types.Permissions{},
			dbPerms:  types.Permissions{},
			expected: types.RoleOwner,
		},
		{
			name:     "cfg owner only",
			cfgPerms: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}},
			dbPerms:  types.Permissions{},
			authorID: "U1",
			expected: types.RoleOwner,
		},
		{
			name:     "db owner only",
			cfgPerms: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}},
			dbPerms:  types.Permissions{Owners: types.RoleGrant{Users: []string{"U2"}}},
			authorID: "U2",
			expected: types.RoleOwner,
		},
		{
			name:     "cfg member only",
			cfgPerms: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}, Members: types.RoleGrant{Users: []string{"U2"}}},
			dbPerms:  types.Permissions{},
			authorID: "U2",
			expected: types.RoleMember,
		},
		{
			name:     "db member wins when cfg empty",
			cfgPerms: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}},
			dbPerms:  types.Permissions{Members: types.RoleGrant{Users: []string{"U3"}}},
			authorID: "U3",
			expected: types.RoleMember,
		},
		{
			name:     "denied when not in any list",
			cfgPerms: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}},
			dbPerms:  types.Permissions{},
			authorID: "U99",
			expected: "",
		},
		{
			name:        "cfg owner by role",
			cfgPerms:    types.Permissions{Owners: types.RoleGrant{Roles: []string{"admin"}}},
			dbPerms:     types.Permissions{},
			authorID:    "U5",
			authorRoles: []string{"admin"},
			expected:    types.RoleOwner,
		},
		{
			name:     "db owner beats cfg member",
			cfgPerms: types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}, Members: types.RoleGrant{Users: []string{"U2"}}},
			dbPerms:  types.Permissions{Owners: types.RoleGrant{Users: []string{"U2"}}},
			authorID: "U2",
			expected: types.RoleOwner,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			role := resolveRole(tc.cfgPerms, tc.dbPerms, tc.authorID, tc.authorRoles)
			require.Equal(s.T(), tc.expected, role)
		})
	}
}

func (s *OrchestratorSuite) TestHandleMessagePermissionDenied() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"allowed-user"}}},
	})

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "denied-user",
		Content:      "hello bot",
		IsBotMention: true,
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// No runner call or response sent.
	s.runner.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything)
	s.bot.AssertNotCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageLocalPlatformBypassesPermissions() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"allowed-user"}}},
	})

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "local-user",
		AuthorName:   "local-user",
		Content:      "hello bot",
		MessageID:    "msg-local",
		Platform:     types.PlatformLocal,
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response: "Hello local!", SessionID: "s1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// The runner should have been called despite local-user not being in the owners list.
	s.runner.AssertCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessageBotSelfMentionBypassesPermissions() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"allowed-user"}}},
	})
	// Replace the default IsBotUser(mock.Anything)->false with a specific one for "BOT"->true.
	filtered := s.bot.ExpectedCalls[:0]
	for _, c := range s.bot.ExpectedCalls {
		if c.Method != "IsBotUser" {
			filtered = append(filtered, c)
		}
	}
	s.bot.ExpectedCalls = filtered
	s.bot.On("IsBotUser", "BOT").Return(true)

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "BOT", // same as BotUserID()
		AuthorName:   "LoopBot",
		Content:      "audit the codebase",
		MessageID:    "msg-bot",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response: "Done!", SessionID: "s1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	// The runner should have been called despite the bot not being in the owners list.
	s.runner.AssertCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleMessagePermissionAllowed() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"allowed-user"}}},
	})

	msg := &bot.IncomingMessage{
		ChannelID:    "ch1",
		AuthorID:     "allowed-user",
		AuthorName:   "Alice",
		Content:      "hello bot",
		MessageID:    "msg1",
		IsBotMention: true,
		Timestamp:    time.Now().UTC(),
	}

	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", Active: true,
	}, nil)
	s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
	s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
	s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response: "Hello!", SessionID: "s1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{}).Return(nil)

	s.orch.HandleMessage(s.ctx, msg)

	s.runner.AssertCalled(s.T(), "Run", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleInteractionPermissionDenied() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"allowed-user"}}},
	})

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ You don't have permission to use this command."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "denied-user",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
	s.scheduler.AssertNotCalled(s.T(), "ListTasks", mock.Anything, mock.Anything)
}

func (s *OrchestratorSuite) TestHandleInteractionLocalPlatformBypassesPermissions() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"allowed-user"}}},
	})

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return([]*db.ScheduledTask{}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "No scheduled tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "local-user",
		Platform:    types.PlatformLocal,
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionPermissionAllowed() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"allowed-user"}}},
	})

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return([]*db.ScheduledTask{}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "No scheduled tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "allowed-user",
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionPermissionByRole() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Roles: []string{"admin-role"}}},
	})

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.scheduler.On("ListTasks", s.ctx, "ch1").Return([]*db.ScheduledTask{}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "No scheduled tasks."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "some-user",
		AuthorRoles: []string{"admin-role"},
	})

	s.store.AssertExpectations(s.T())
	s.scheduler.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionGetChannelNil() {
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{Owners: types.RoleGrant{Users: []string{"allowed-user"}}},
	})

	// Channel not found — dirPath will be empty, permissions come from global.
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ You don't have permission to use this command."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "tasks",
		AuthorID:    "denied-user",
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionPermCmdRequiresOwner() {
	// Member user cannot manage permissions.
	s.orch.cfg.Store(&config.Config{
		Permissions: types.Permissions{
			Owners:  types.RoleGrant{Users: []string{"owner-user"}},
			Members: types.RoleGrant{Users: []string{"member-user"}},
		},
	})

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ Only owners can manage permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		AuthorID:    "member-user",
		Options:     map[string]string{"target_id": "U99", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", types.Permissions{
		Members: types.RoleGrant{Users: []string{"U99"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ <@U99> granted member role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserOwner() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1", DirPath: "",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", types.Permissions{
		Owners: types.RoleGrant{Users: []string{"U99"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ <@U99> granted owner role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99", "role": "owner"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserChannelNotRegistered() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", types.Permissions{
		Owners: types.RoleGrant{Roles: []string{"R1"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ Role <@&R1> granted owner role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1", "role": "owner"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyUserSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{
			Owners:  types.RoleGrant{Users: []string{"owner-user"}},
			Members: types.RoleGrant{Users: []string{"U99"}},
		},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.MatchedBy(func(p types.Permissions) bool {
		// U99 removed from Members; owner-user remains in Owners
		return len(p.Owners.Users) == 1 && p.Owners.Users[0] == "owner-user" &&
			len(p.Members.Users) == 0
	})).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ <@U99> removed from channel permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_user",
		AuthorID:    "owner-user",
		Options:     map[string]string{"target_id": "U99"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyRoleSuccess() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{
			Owners: types.RoleGrant{Users: []string{"owner-user"}, Roles: []string{"R1"}},
		},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.MatchedBy(func(p types.Permissions) bool {
		// R1 removed from Owners.Roles; owner-user remains in Owners.Users
		return len(p.Owners.Users) == 1 && p.Owners.Users[0] == "owner-user" &&
			len(p.Owners.Roles) == 0
	})).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ Role <@&R1> removed from channel permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_role",
		AuthorID:    "owner-user",
		Options:     map[string]string{"target_id": "R1"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestAppendUnique() {
	// Value not present — appends it.
	result := appendUnique([]string{"a", "b"}, "c")
	require.Equal(s.T(), []string{"a", "b", "c"}, result)

	// Value already present — no duplicate added.
	result = appendUnique([]string{"a", "b", "c"}, "b")
	require.Equal(s.T(), []string{"a", "b", "c"}, result)
}

func (s *OrchestratorSuite) TestHandleInteractionAllowUserDefaultRole() {
	// When "role" option is absent, it defaults to "member".
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", types.Permissions{
		Members: types.RoleGrant{Users: []string{"U99"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ <@U99> granted member role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_user",
		Options:     map[string]string{"target_id": "U99"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleChannelNotRegistered() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1", "role": "owner"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1", "role": "owner"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleMember() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", types.Permissions{
		Members: types.RoleGrant{Roles: []string{"R1"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ Role <@&R1> granted member role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1", "role": "member"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionAllowRoleDefaultRole() {
	// When "role" option is absent, it defaults to "member".
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", types.Permissions{
		Members: types.RoleGrant{Roles: []string{"R1"}},
	}).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "✅ Role <@&R1> granted member role."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "allow_role",
		Options:     map[string]string{"target_id": "R1"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyUserChannelNotRegistered() {
	// Bootstrap mode (no config perms, no db perms) → everyone is RoleOwner.
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_user",
		Options:     map[string]string{"target_id": "U99"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyUserStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{
			Owners: types.RoleGrant{Users: []string{"owner-user"}},
		},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_user",
		AuthorID:    "owner-user",
		Options:     map[string]string{"target_id": "U99"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyRoleChannelNotRegistered() {
	// Bootstrap mode (no config perms, no db perms) → everyone is RoleOwner.
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "⛔ Channel not registered."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_role",
		Options:     map[string]string{"target_id": "R1"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestHandleInteractionDenyRoleStoreError() {
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ID: 1, ChannelID: "ch1",
		Permissions: types.Permissions{
			Owners: types.RoleGrant{Users: []string{"owner-user"}, Roles: []string{"R1"}},
		},
	}, nil)
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", mock.Anything).Return(errors.New("db err"))
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return out.Content == "Failed to update permissions."
	})).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "deny_role",
		AuthorID:    "owner-user",
		Options:     map[string]string{"target_id": "R1"},
	})

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}
