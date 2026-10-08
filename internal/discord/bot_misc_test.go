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
	"github.com/radutopala/loop/internal/events"
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

// --- Ask/plan cards ---

func (s *BotSuite) TestSendCards() {
	ask := events.AskUserQuestionEventData{ToolUseID: "toolu_1", Questions: []events.AskUserQuestion{{Question: "Which one?"}}}
	plan := events.ExitPlanModeEventData{ToolUseID: "toolu_1", Plan: "# Plan"}
	ref := &discordgo.MessageReference{MessageID: "1001"}
	tests := []struct {
		name       string
		send       func() error
		text       string
		textErr    error
		buttons    int
		buttonsErr error
		wantErr    string
	}{
		{
			name:    "ask",
			send:    func() error { return s.bot.SendAskCard(context.Background(), "ch1", "1001", ask) },
			text:    bot.FormatAskCard(ask, "**"),
			buttons: 1,
		},
		{
			name:    "plan",
			send:    func() error { return s.bot.SendPlanCard(context.Background(), "ch1", "1001", plan) },
			text:    bot.FormatPlanCard(plan, "**"),
			buttons: 2,
		},
		{
			name: "card without an ID gets no buttons",
			send: func() error {
				return s.bot.SendPlanCard(context.Background(), "ch1", "1001", events.ExitPlanModeEventData{Plan: "# Plan"})
			},
			text: bot.FormatPlanCard(events.ExitPlanModeEventData{Plan: "# Plan"}, "**"),
		},
		{
			name:    "text fails",
			send:    func() error { return s.bot.SendAskCard(context.Background(), "ch1", "1001", ask) },
			text:    bot.FormatAskCard(ask, "**"),
			textErr: errors.New("boom"), wantErr: "discord send reply: boom",
		},
		{
			name:    "buttons fail",
			send:    func() error { return s.bot.SendPlanCard(context.Background(), "ch1", "1001", plan) },
			text:    bot.FormatPlanCard(plan, "**"),
			buttons: 2, buttonsErr: errors.New("boom"), wantErr: "discord send card buttons: boom",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.session.On("ChannelMessageSendReply", "ch1", tc.text, ref, mock.Anything).Return(&discordgo.Message{}, tc.textErr).Once()
			if tc.buttons > 0 {
				s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
					return data.Content == "" && len(data.Components) == 1 &&
						len(data.Components[0].(discordgo.ActionsRow).Components) == tc.buttons
				}), mock.Anything).Return(&discordgo.Message{ID: "btn-1"}, tc.buttonsErr).Once()
			}

			err := tc.send()

			s.session.AssertExpectations(s.T())
			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
				require.Empty(s.T(), s.bot.openCards)
				return
			}
			require.NoError(s.T(), err)
			if tc.buttons > 0 {
				require.Equal(s.T(), map[string]openCard{"ch1": {cardID: "toolu_1", messageID: "btn-1"}}, s.bot.openCards)
			} else {
				require.Empty(s.T(), s.bot.openCards)
			}
		})
	}
}

func (s *BotSuite) TestCardComponents() {
	btns := []bot.CardButton{
		{Label: "Approve", Choice: "approve", Style: bot.CardButtonPrimary},
		{Label: "Reject", Choice: "reject", Style: bot.CardButtonDanger},
	}
	for i := 1; i <= 4; i++ {
		btns = append(btns, bot.CardButton{Label: "O", Choice: string(rune('0' + i))})
	}
	rows := cardComponents("ch1", "toolu_1", btns)
	require.Len(s.T(), rows, 2)
	first := rows[0].(discordgo.ActionsRow).Components
	require.Len(s.T(), first, maxButtonsPerRow)
	require.Equal(s.T(), discordgo.Button{Label: "Approve", Style: discordgo.PrimaryButton, CustomID: "card:ch1:toolu_1:approve"}, first[0])
	require.Equal(s.T(), discordgo.Button{Label: "Reject", Style: discordgo.DangerButton, CustomID: "card:ch1:toolu_1:reject"}, first[1])
	require.Equal(s.T(), discordgo.Button{Label: "O", Style: discordgo.SecondaryButton, CustomID: "card:ch1:toolu_1:1"}, first[2])
	second := rows[1].(discordgo.ActionsRow).Components
	require.Equal(s.T(), []discordgo.MessageComponent{
		discordgo.Button{Label: "O", Style: discordgo.SecondaryButton, CustomID: "card:ch1:toolu_1:4"},
	}, second)
}

func (s *BotSuite) TestCloseCard() {
	tests := []struct {
		name     string
		cardID   string
		editErr  error
		wantCall bool
		wantOpen bool
		wantErr  string
	}{
		{name: "closes the open card", cardID: "toolu_1", wantCall: true},
		{name: "other card is left open", cardID: "toolu_old", wantOpen: true},
		{name: "edit fails", cardID: "toolu_1", wantCall: true, editErr: errors.New("boom"), wantErr: "discord close card: boom"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.bot.openCards = map[string]openCard{"ch1": {cardID: "toolu_1", messageID: "btn-1"}}
			if tc.wantCall {
				s.session.On("ChannelMessageEditComplex", mock.MatchedBy(func(e *discordgo.MessageEdit) bool {
					return e.ID == "btn-1" && e.Channel == "ch1" && *e.Content == "› Approved — <@u1>" &&
						len(*e.Components) == 0 && e.AllowedMentions != nil && len(e.AllowedMentions.Parse) == 0
				}), mock.Anything).Return(&discordgo.Message{}, tc.editErr).Once()
			}

			err := s.bot.CloseCard(context.Background(), "ch1", tc.cardID, "Approved", "u1")

			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			_, open := s.bot.openCards["ch1"]
			require.Equal(s.T(), tc.wantOpen, open)
			s.session.AssertExpectations(s.T())
		})
	}
}

func (s *BotSuite) TestCloseCardNothingOpen() {
	require.NoError(s.T(), s.bot.CloseCard(context.Background(), "ch1", "toolu_1", "Approved", "u1"))
	s.session.AssertNotCalled(s.T(), "ChannelMessageEditComplex", mock.Anything, mock.Anything)
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

func (s *BotSuite) TestSendApprovalRendersDiffBlock() {
	s.session.On("ChannelMessageSendComplex", "ch1", mock.MatchedBy(func(data *discordgo.MessageSend) bool {
		return strings.HasSuffix(data.Content, "\n```diff\n+[core]\n+\tfsmonitor = <x>\n```") &&
			!strings.Contains(data.Content, "`diff`:")
	}), mock.Anything).Return(&discordgo.Message{ID: "m4"}, nil)

	_, err := s.bot.SendApproval(context.Background(), "ch1", bot.ApprovalPrompt{
		ID:      "req-g",
		Target:  "write /w/.git/config",
		Details: map[string]string{"diff": "+[core]\n+\tfsmonitor = <x>\n"},
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
	}), mock.Anything).Return(&discordgo.Message{ID: "1001"}, nil)

	msgID, err := s.bot.SendStopButton(context.Background(), "ch1", "my-channel")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "1001", msgID)
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

func (s *BotSuite) TestHandleIncomingMessageDelayedNoop() {
	s.bot.HandleIncomingMessageDelayed(context.Background(), "", "", "", "", "", 0)
}
