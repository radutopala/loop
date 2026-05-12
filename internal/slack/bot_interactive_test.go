package slack

import (
	"context"
	"errors"
	"sync"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
)

// --- Stop button tests ---

func (s *BotSuite) TestSendStopButton() {
	tests := []struct {
		name      string
		channelID string
		postErr   error
		wantID    string
		wantErr   bool
	}{
		{"success", "C123", nil, "1234567890.123456", false},
		{"error", "C123", errors.New("post failed"), "", true},
		{"thread", "C123:1111111111.000", nil, "1234567890.999", false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.postErr != nil {
				session.On("PostMessage", "C123", mock.Anything).Return("", "", tt.postErr)
			} else {
				session.On("PostMessage", "C123", mock.Anything).Return("C123", tt.wantID, nil)
			}

			msgID, err := bot.SendStopButton(context.Background(), tt.channelID, "run-1")
			if tt.wantErr {
				require.Error(s.T(), err)
				require.Empty(s.T(), msgID)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.wantID, msgID)
			}
		})
	}
}

func (s *BotSuite) TestRemoveStopButton() {
	tests := []struct {
		name      string
		channelID string
		delErr    error
		wantErr   bool
	}{
		{"success", "C123", nil, false},
		{"error", "C123", errors.New("delete failed"), true},
		{"thread", "C123:1111111111.000", nil, false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			if tt.delErr != nil {
				session.On("DeleteMessage", "C123", "1234567890.123456").Return("", "", tt.delErr)
			} else {
				session.On("DeleteMessage", "C123", "1234567890.123456").Return("C123", "1234567890.123456", nil)
			}

			err := bot.RemoveStopButton(context.Background(), tt.channelID, "1234567890.123456")
			if tt.wantErr {
				require.Error(s.T(), err)
			} else {
				require.NoError(s.T(), err)
			}
		})
	}
}

func (s *BotSuite) TestSendApprovalRendersThreeButtons() {
	var captured goslack.Blocks
	s.session.On("PostMessage", "C123", mock.MatchedBy(func(opts []goslack.MsgOption) bool {
		_, values, err := goslack.UnsafeApplyMsgOptions("xoxb-test", "C123", "https://slack.test/", opts...)
		if err != nil {
			return false
		}
		blob := values.Get("blocks")
		if blob == "" {
			return false
		}
		return captured.UnmarshalJSON([]byte(blob)) == nil
	})).Return("C123", "1234567890.000001", nil)

	msgID, err := s.bot.SendApproval(context.Background(), "C123", bot.ApprovalPrompt{
		ID:      "req-1",
		Kind:    "execve",
		Target:  "git push origin main",
		Message: "write-side git op",
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "1234567890.000001", msgID)
	s.session.AssertExpectations(s.T())

	require.Len(s.T(), captured.BlockSet, 2)
	section, ok := captured.BlockSet[0].(*goslack.SectionBlock)
	require.True(s.T(), ok)
	require.Contains(s.T(), section.Text.Text, "git push origin main")
	require.Contains(s.T(), section.Text.Text, "write-side git op")

	actions, ok := captured.BlockSet[1].(*goslack.ActionBlock)
	require.True(s.T(), ok)
	require.Equal(s.T(), "gate_actions:req-1", actions.BlockID)
	require.Len(s.T(), actions.Elements.ElementSet, 3)

	btns := make([]*goslack.ButtonBlockElement, 3)
	for i, elt := range actions.Elements.ElementSet {
		btn, ok := elt.(*goslack.ButtonBlockElement)
		require.True(s.T(), ok)
		btns[i] = btn
	}

	require.Equal(s.T(), "gate:req-1:once", btns[0].ActionID)
	require.Equal(s.T(), "Allow once", btns[0].Text.Text)
	require.Equal(s.T(), goslack.StylePrimary, btns[0].Style)

	require.Equal(s.T(), "gate:req-1:session", btns[1].ActionID)
	require.Equal(s.T(), "Allow for session", btns[1].Text.Text)
	require.Equal(s.T(), goslack.Style(""), btns[1].Style)

	require.Equal(s.T(), "gate:req-1:deny", btns[2].ActionID)
	require.Equal(s.T(), "Deny", btns[2].Text.Text)
	require.Equal(s.T(), goslack.StyleDanger, btns[2].Style)
}

func (s *BotSuite) TestSendApprovalThreadedChannel() {
	s.session.On("PostMessage", "C123", mock.MatchedBy(func(opts []goslack.MsgOption) bool {
		_, values, err := goslack.UnsafeApplyMsgOptions("xoxb-test", "C123", "https://slack.test/", opts...)
		if err != nil {
			return false
		}
		return values.Get("thread_ts") == "1111.2222"
	})).Return("C123", "1234.5678", nil)

	msgID, err := s.bot.SendApproval(context.Background(), "C123:1111.2222", bot.ApprovalPrompt{ID: "r", Target: "docker ps"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "1234.5678", msgID)
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestSendApprovalRendersDetailsInHeader() {
	var captured goslack.Blocks
	s.session.On("PostMessage", "C123", mock.MatchedBy(func(opts []goslack.MsgOption) bool {
		_, values, err := goslack.UnsafeApplyMsgOptions("xoxb-test", "C123", "https://slack.test/", opts...)
		if err != nil {
			return false
		}
		blob := values.Get("blocks")
		return blob != "" && captured.UnmarshalJSON([]byte(blob)) == nil
	})).Return("C123", "1234567890.000002", nil)

	_, err := s.bot.SendApproval(context.Background(), "C123", bot.ApprovalPrompt{
		ID:     "req-d",
		Target: "POST /containers/create",
		Details: map[string]string{
			"image":      "alpine:3.20",
			"privileged": "true",
		},
	})
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())

	require.Len(s.T(), captured.BlockSet, 2)
	section, ok := captured.BlockSet[0].(*goslack.SectionBlock)
	require.True(s.T(), ok)
	require.Contains(s.T(), section.Text.Text, "> `image`: alpine:3.20")
	require.Contains(s.T(), section.Text.Text, "> `privileged`: true")
}

func (s *BotSuite) TestSendApprovalError() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("post failed"))

	msgID, err := s.bot.SendApproval(context.Background(), "C123", bot.ApprovalPrompt{ID: "r", Target: "git push"})
	require.Error(s.T(), err)
	require.Empty(s.T(), msgID)
}

func (s *BotSuite) TestRemoveApprovalSuccess() {
	s.session.On("DeleteMessage", "C123", "1234.5678").Return("C123", "1234.5678", nil)
	require.NoError(s.T(), s.bot.RemoveApproval(context.Background(), "C123:1111.2222", "1234.5678"))
	s.session.AssertExpectations(s.T())
}

func (s *BotSuite) TestRemoveApprovalError() {
	s.session.On("DeleteMessage", "C123", "1234.5678").Return("", "", errors.New("delete failed"))
	require.Error(s.T(), s.bot.RemoveApproval(context.Background(), "C123", "1234.5678"))
}

// --- handleGateAction ---

type recordingSlackResolver struct {
	mu    sync.Mutex
	calls []slackResolverCall
	err   error
}

type slackResolverCall struct {
	reqID    string
	decision string
	actorID  string
}

func (r *recordingSlackResolver) Resolve(reqID, decision, actorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, slackResolverCall{reqID, decision, actorID})
	return r.err
}

func (r *recordingSlackResolver) snapshot() []slackResolverCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]slackResolverCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func (s *BotSuite) TestHandleInteractiveGateAction() {
	resolver := &recordingSlackResolver{}
	s.bot.SetApprovalResolver(resolver)
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			Channel: goslack.Channel{
				GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C123"}},
			},
			Team: goslack.Team{ID: "T123"},
			User: goslack.User{ID: "U-actor"},
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{
					{ActionID: "gate:req-7:session"},
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleInteractive(evt)

	calls := resolver.snapshot()
	require.Len(s.T(), calls, 1)
	require.Equal(s.T(), slackResolverCall{reqID: "req-7", decision: "session", actorID: "U-actor"}, calls[0])
}

func (s *BotSuite) TestHandleInteractiveGateActionMalformed() {
	resolver := &recordingSlackResolver{}
	s.bot.SetApprovalResolver(resolver)
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{{ActionID: "gate:oops"}},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleInteractive(evt)
	require.Empty(s.T(), resolver.snapshot())
}

func (s *BotSuite) TestHandleInteractiveGateActionNoResolver() {
	// No SetApprovalResolver — must not panic.
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{{ActionID: "gate:r1:once"}},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleInteractive(evt)
}

func (s *BotSuite) TestHandleInteractiveGateActionResolverError() {
	resolver := &recordingSlackResolver{err: errors.New("late click")}
	s.bot.SetApprovalResolver(resolver)
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			User: goslack.User{ID: "U-x"},
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{{ActionID: "gate:r1:once"}},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleInteractive(evt)
	require.Len(s.T(), resolver.snapshot(), 1)
}

// --- handleInteractive tests ---

func (s *BotSuite) TestHandleInteractiveStopAction() {
	received := make(chan *bot.Interaction, 1)
	s.bot.OnInteraction(func(_ context.Context, i *bot.Interaction) {
		received <- i
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			Channel: goslack.Channel{
				GroupConversation: goslack.GroupConversation{
					Conversation: goslack.Conversation{ID: "C123"},
				},
			},
			Team: goslack.Team{ID: "T123"},
			User: goslack.User{ID: "U456"},
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{
					{ActionID: "stop:target-ch"},
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleInteractive(evt)

	select {
	case inter := <-received:
		require.Equal(s.T(), "stop", inter.CommandName)
		require.Equal(s.T(), "target-ch", inter.Options["channel_id"])
		require.Equal(s.T(), "C123", inter.ChannelID)
		require.Equal(s.T(), "T123", inter.GuildID)
		require.Equal(s.T(), "U456", inter.AuthorID)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for interaction")
	}
}

func (s *BotSuite) TestHandleInteractiveIgnored() {
	tests := []struct {
		name string
		evt  socketmode.Event
	}{
		{"non_block_actions", socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    goslack.InteractionCallback{Type: goslack.InteractionTypeDialogSubmission},
			Request: &socketmode.Request{},
		}},
		{"non_stop_action", socketmode.Event{
			Type: socketmode.EventTypeInteractive,
			Data: goslack.InteractionCallback{
				Type:           goslack.InteractionTypeBlockActions,
				ActionCallback: goslack.ActionCallbacks{BlockActions: []*goslack.BlockAction{{ActionID: "other:something"}}},
			},
			Request: &socketmode.Request{},
		}},
		{"invalid_data", socketmode.Event{
			Type: socketmode.EventTypeInteractive,
			Data: "not-a-callback",
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			b := NewBot(session, sc, testLogger())
			b.botUserID = "U123BOT"

			called := false
			b.OnInteraction(func(_ context.Context, _ *bot.Interaction) { called = true })
			sc.On("Ack", mock.Anything, mock.Anything).Return()

			b.handleInteractive(tt.evt)
			require.False(s.T(), called)
		})
	}
}

func (s *BotSuite) TestHandleEventInteractiveType() {
	received := make(chan *bot.Interaction, 1)
	s.bot.OnInteraction(func(_ context.Context, i *bot.Interaction) {
		received <- i
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			Channel: goslack.Channel{
				GroupConversation: goslack.GroupConversation{
					Conversation: goslack.Conversation{ID: "C123"},
				},
			},
			Team: goslack.Team{ID: "T123"},
			User: goslack.User{ID: "U456"},
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{
					{ActionID: "stop:ch-1"},
				},
			},
		},
		Request: &socketmode.Request{},
	}
	// Call handleEvent directly to cover the EventTypeInteractive branch
	s.bot.handleEvent(evt)

	select {
	case inter := <-received:
		require.Equal(s.T(), "stop", inter.CommandName)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for interaction")
	}
}

func (s *BotSuite) TestHandleIncomingMessageNoop() {
	// HandleIncomingMessage is a no-op stub — just verify it doesn't panic.
	s.bot.HandleIncomingMessage(context.Background(), "", "", "", "")
}

func (s *BotSuite) TestHandleIncomingMessageWithPriorityNoop() {
	s.bot.HandleIncomingMessageWithPriority(context.Background(), "", "", "", "", 0)
}
