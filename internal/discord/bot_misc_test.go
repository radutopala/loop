package discord

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
)

// --- PostMessage ---

func (s *BotSuite) TestPostMessage() {
	tests := []struct {
		name        string
		content     string
		botUserID   string
		botUsername string
		expectedMsg string
		sendErr     error
		wantErr     string
	}{
		{name: "success", content: "hello", expectedMsg: "hello"},
		{name: "converts text mention", content: "@LoopBot check the last commit", botUserID: "bot-123", botUsername: "LoopBot", expectedMsg: "<@bot-123> check the last commit"},
		{name: "converts text mention case insensitive", content: "@loopbot check commits", botUserID: "bot-123", botUsername: "LoopBot", expectedMsg: "<@bot-123> check commits"},
		{name: "error", content: "hello", expectedMsg: "hello", sendErr: errors.New("send failed"), wantErr: "discord post message"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			if tc.botUserID != "" {
				b.botUserID = tc.botUserID
				b.botUsername = tc.botUsername
			}
			var ret *discordgo.Message
			if tc.sendErr == nil {
				ret = &discordgo.Message{}
			}
			session.On("ChannelMessageSend", "ch-1", tc.expectedMsg, mock.Anything).Return(ret, tc.sendErr)
			err := b.PostMessage(context.Background(), "ch-1", tc.content)
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			session.AssertExpectations(s.T())
		})
	}
}

// --- CreateSimpleThread tests ---

func (s *BotSuite) TestCreateSimpleThread() {
	tests := []struct {
		name    string
		title   string
		message string
		setup   func(*MockSession)
		wantID  string
		wantErr string
	}{
		{
			name: "success", title: "task output", message: "First turn content",
			setup: func(ss *MockSession) {
				ss.On("ThreadStart", "ch-1", "task output", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
					Return(&discordgo.Channel{ID: "thread-1"}, nil)
				ss.On("ChannelMessageSend", "thread-1", "First turn content", mock.Anything).Return(&discordgo.Message{}, nil)
			},
			wantID: "thread-1",
		},
		{
			name: "empty message", title: "task name", message: "",
			setup: func(ss *MockSession) {
				ss.On("ThreadStart", "ch-1", "task name", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
					Return(&discordgo.Channel{ID: "thread-2"}, nil)
			},
			wantID: "thread-2",
		},
		{
			name: "start error", title: "task", message: "content",
			setup: func(ss *MockSession) {
				ss.On("ThreadStart", "ch-1", "task", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
					Return(nil, errors.New("thread start failed"))
			},
			wantErr: "discord create simple thread",
		},
		{
			name: "message send error", title: "task", message: "content",
			setup: func(ss *MockSession) {
				ss.On("ThreadStart", "ch-1", "task", discordgo.ChannelTypeGuildPublicThread, 10080, mock.Anything).
					Return(&discordgo.Channel{ID: "thread-3"}, nil)
				ss.On("ChannelMessageSend", "thread-3", "content", mock.Anything).Return(nil, errors.New("send failed"))
			},
			wantID: "thread-3", // message send error is logged but doesn't fail creation
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			session := new(MockSession)
			b := NewBot(session, "app-1", "g-1", slog.New(slog.NewTextHandler(discard{}, nil)))
			tc.setup(session)
			threadID, err := b.CreateSimpleThread(context.Background(), "ch-1", tc.title, tc.message)
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
				require.Empty(s.T(), threadID)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tc.wantID, threadID)
			}
			session.AssertExpectations(s.T())
		})
	}
}

// --- Stop button tests ---

func (s *BotSuite) TestSendStopButtonSuccess() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
		return data.Content == "Processing..." && len(data.Components) == 1
	}), mock.Anything).Return(&discordgo.Message{ID: "stop-msg-1"}, nil)

	msgID, err := s.bot.SendStopButton(context.Background(), "ch1", "run-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "stop-msg-1", msgID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendStopButtonError() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.Anything, mock.Anything).Return(nil, errors.New("send failed"))

	msgID, err := s.bot.SendStopButton(context.Background(), "ch1", "run-1")
	require.Error(s.T(), err)
	require.Equal(s.T(), "", msgID)
}

func (s *BotSuite) TestRemoveStopButtonSuccess() {
	s.session.On("ChannelMessageDelete", "ch1", "stop-msg-1", mock.Anything).Return(nil)

	err := s.bot.RemoveStopButton(context.Background(), "ch1", "stop-msg-1")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestRemoveStopButtonError() {
	s.session.On("ChannelMessageDelete", "ch1", "stop-msg-1", mock.Anything).Return(errors.New("delete failed"))

	err := s.bot.RemoveStopButton(context.Background(), "ch1", "stop-msg-1")
	require.Error(s.T(), err)
}

func (s *BotSuite) TestSendApprovalRendersThreeButtons() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
		if !strings.Contains(data.Content, "git push origin main") {
			return false
		}
		if !strings.Contains(data.Content, "write-side git op") {
			return false
		}
		if len(data.Components) != 1 {
			return false
		}
		row, ok := data.Components[0].(discordgo.ActionsRow)
		if !ok || len(row.Components) != 3 {
			return false
		}
		once, _ := row.Components[0].(discordgo.Button)
		session, _ := row.Components[1].(discordgo.Button)
		deny, _ := row.Components[2].(discordgo.Button)
		return once.CustomID == "gate:req-1:once" && once.Style == discordgo.PrimaryButton && once.Label == "Allow once" &&
			session.CustomID == "gate:req-1:session" && session.Style == discordgo.SecondaryButton && session.Label == "Allow for session" &&
			deny.CustomID == "gate:req-1:deny" && deny.Style == discordgo.DangerButton && deny.Label == "Deny"
	}), mock.Anything).Return(&discordgo.Message{ID: "approval-msg-1"}, nil)

	msgID, err := s.bot.SendApproval(context.Background(), "ch1", bot.ApprovalPrompt{
		ID:      "req-1",
		Kind:    "execve",
		Target:  "git push origin main",
		Message: "write-side git op",
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "approval-msg-1", msgID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendApprovalOmitsMessageLineWhenEmpty() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
		return !strings.Contains(data.Content, "\n")
	}), mock.Anything).Return(&discordgo.Message{ID: "m2"}, nil)

	_, err := s.bot.SendApproval(context.Background(), "ch1", bot.ApprovalPrompt{ID: "r2", Target: "docker ps"})
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendApprovalRendersDetailsAsQuotedLines() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
		// Keys must be alphabetically sorted in the rendered output.
		return strings.Contains(data.Content, "> `image`: alpine:3.20") &&
			strings.Contains(data.Content, "> `privileged`: true") &&
			strings.Index(data.Content, "image") < strings.Index(data.Content, "privileged")
	}), mock.Anything).Return(&discordgo.Message{ID: "m3"}, nil)

	_, err := s.bot.SendApproval(context.Background(), "ch1", bot.ApprovalPrompt{
		ID:     "req-d",
		Target: "POST /containers/create",
		Details: map[string]string{
			"image":      "alpine:3.20",
			"privileged": "true",
		},
	})
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendApprovalError() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.Anything, mock.Anything).Return(nil, errors.New("send failed"))

	msgID, err := s.bot.SendApproval(context.Background(), "ch1", bot.ApprovalPrompt{ID: "r"})
	require.Error(s.T(), err)
	require.Equal(s.T(), "", msgID)
}

func (s *BotSuite) TestRemoveApprovalSuccess() {
	s.session.On("ChannelMessageDelete", "ch1", "approval-msg-1", mock.Anything).Return(nil)

	err := s.bot.RemoveApproval(context.Background(), "ch1", "approval-msg-1")
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestRemoveApprovalError() {
	s.session.On("ChannelMessageDelete", "ch1", "approval-msg-1", mock.Anything).Return(errors.New("delete failed"))

	err := s.bot.RemoveApproval(context.Background(), "ch1", "approval-msg-1")
	require.Error(s.T(), err)
}

// --- handleGateComponent ---

type recordingResolver struct {
	mu    sync.Mutex
	calls []resolverCall
	err   error
}

type resolverCall struct {
	reqID    string
	decision string
	actorID  string
}

func (r *recordingResolver) Resolve(reqID, decision, actorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, resolverCall{reqID, decision, actorID})
	return r.err
}

func (r *recordingResolver) snapshot() []resolverCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]resolverCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func (s *BotSuite) TestHandleGateComponentGuildUser() {
	resolver := &recordingResolver{}
	s.bot.SetApprovalResolver(resolver)
	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			GuildID:   "g-1",
			Type:      discordgo.InteractionMessageComponent,
			Member:    &discordgo.Member{User: &discordgo.User{ID: "user-1"}},
			Data:      discordgo.MessageComponentInteractionData{CustomID: "gate:req-123:session"},
		},
	}
	s.bot.handleInteraction(nil, ic)

	calls := resolver.snapshot()
	require.Len(s.T(), calls, 1)
	require.Equal(s.T(), resolverCall{reqID: "req-123", decision: "session", actorID: "user-1"}, calls[0])
}

func (s *BotSuite) TestHandleGateComponentDMUser() {
	resolver := &recordingResolver{}
	s.bot.SetApprovalResolver(resolver)
	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "dm-1",
			Type:      discordgo.InteractionMessageComponent,
			User:      &discordgo.User{ID: "dm-user"},
			Data:      discordgo.MessageComponentInteractionData{CustomID: "gate:req-7:deny"},
		},
	}
	s.bot.handleInteraction(nil, ic)

	calls := resolver.snapshot()
	require.Len(s.T(), calls, 1)
	require.Equal(s.T(), "dm-user", calls[0].actorID)
	require.Equal(s.T(), "deny", calls[0].decision)
}

func (s *BotSuite) TestHandleGateComponentMalformedCustomID() {
	resolver := &recordingResolver{}
	s.bot.SetApprovalResolver(resolver)
	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionMessageComponent,
			User:      &discordgo.User{ID: "u"},
			Data:      discordgo.MessageComponentInteractionData{CustomID: "gate:oops"},
		},
	}
	s.bot.handleInteraction(nil, ic)

	require.Empty(s.T(), resolver.snapshot())
}

func (s *BotSuite) TestHandleGateComponentNoResolver() {
	// No SetApprovalResolver call — missing resolver must not panic.
	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionMessageComponent,
			User:      &discordgo.User{ID: "u"},
			Data:      discordgo.MessageComponentInteractionData{CustomID: "gate:req-1:once"},
		},
	}
	// Should complete without panic; nothing to assert beyond that.
	s.bot.handleInteraction(nil, ic)
}

func (s *BotSuite) TestHandleGateComponentResolverError() {
	resolver := &recordingResolver{err: errors.New("late click")}
	s.bot.SetApprovalResolver(resolver)
	s.session.On("InteractionRespond", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ic := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ChannelID: "ch-1",
			Type:      discordgo.InteractionMessageComponent,
			User:      &discordgo.User{ID: "u"},
			Data:      discordgo.MessageComponentInteractionData{CustomID: "gate:req-1:once"},
		},
	}
	// Error is logged but does not panic or surface.
	s.bot.handleInteraction(nil, ic)

	calls := resolver.snapshot()
	require.Len(s.T(), calls, 1)
}

func (s *BotSuite) TestSendStopButtonCustomID() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
		if len(data.Components) != 1 {
			return false
		}
		row, ok := data.Components[0].(discordgo.ActionsRow)
		if !ok || len(row.Components) != 1 {
			return false
		}
		btn, ok := row.Components[0].(discordgo.Button)
		if !ok {
			return false
		}
		return btn.CustomID == "stop:my-channel" && btn.Style == discordgo.DangerButton && btn.Label == "Stop"
	}), mock.Anything).Return(&discordgo.Message{ID: "msg-1"}, nil)

	msgID, err := s.bot.SendStopButton(context.Background(), "ch1", "my-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "msg-1", msgID)
	s.session.AssertExpectations(s.T())
}

// --- Verify Bot interface compliance ---

func (s *BotSuite) TestBotInterfaceCompliance() {
	var _ Bot = (*DiscordBot)(nil)
}

func (s *BotSuite) TestHandleIncomingMessageNoop() {
	// HandleIncomingMessage is a no-op stub — just verify it doesn't panic.
	s.bot.HandleIncomingMessage(context.Background(), "", "", "", "")
}

func (s *BotSuite) TestHandleIncomingMessageWithPriorityNoop() {
	s.bot.HandleIncomingMessageWithPriority(context.Background(), "", "", "", "", 0)
}
