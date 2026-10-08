package slack

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
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
	// slack-go ≥ 0.18 no longer exposes blocks via UnsafeApplyMsgOptions, so
	// block content is asserted on approvalBlocks directly; the mock only
	// verifies the message is posted to the right channel.
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234567890.000001", nil)

	prompt := bot.ApprovalPrompt{
		ID:      "req-1",
		Kind:    "execve",
		Target:  "git push origin main",
		Message: "write-side git op",
	}
	msgID, err := s.bot.SendApproval(context.Background(), "C123", prompt)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "1234567890.000001", msgID)
	s.session.AssertExpectations(s.T())

	section, actions := approvalBlocks(prompt)
	require.Contains(s.T(), section.Text.Text, "git push origin main")
	require.Contains(s.T(), section.Text.Text, "write-side git op")

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
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234567890.000002", nil)

	prompt := bot.ApprovalPrompt{
		ID:     "req-d",
		Target: "POST /containers/create",
		Details: map[string]string{
			"image":      "alpine:3.20",
			"privileged": "true",
		},
	}
	_, err := s.bot.SendApproval(context.Background(), "C123", prompt)
	require.NoError(s.T(), err)
	s.session.AssertExpectations(s.T())

	section, _ := approvalBlocks(prompt)
	require.Contains(s.T(), section.Text.Text, "> `image`: alpine:3.20")
	require.Contains(s.T(), section.Text.Text, "> `privileged`: true")
}

func (s *BotSuite) TestApprovalBlocksRendersEscapedDiff() {
	section, _ := approvalBlocks(bot.ApprovalPrompt{
		ID:      "req-g",
		Target:  "write /w/.git/hooks/pre-commit",
		Details: map[string]string{"diff": "+curl <https://x|docs> && rm a > b\n"},
	})
	require.True(s.T(), strings.HasSuffix(section.Text.Text,
		"\n```diff\n+curl &lt;https://x|docs&gt; &amp;&amp; rm a &gt; b\n```"), section.Text.Text)
}

func (s *BotSuite) TestApprovalBlocksLongDiffPointsToDesktop() {
	section, _ := approvalBlocks(bot.ApprovalPrompt{
		ID:      "req-g",
		Target:  "write /w/.git/hooks/pre-commit",
		Details: map[string]string{"diff": strings.Repeat("+echo hi\n", 400)},
	})
	require.True(s.T(), strings.HasSuffix(section.Text.Text, bot.ApprovalDiffUnavailable))
}

func (s *BotSuite) TestSendApprovalError() {
	s.session.On("PostMessage", "C123", mock.Anything).Return("", "", errors.New("post failed"))

	msgID, err := s.bot.SendApproval(context.Background(), "C123", bot.ApprovalPrompt{ID: "r", Target: "git push"})
	require.Error(s.T(), err)
	require.Empty(s.T(), msgID)
}

func (s *BotSuite) TestSendCards() {
	ask := events.AskUserQuestionEventData{ToolUseID: "toolu_1", Questions: []events.AskUserQuestion{{Question: "Which one?"}}}
	plan := events.ExitPlanModeEventData{ToolUseID: "toolu_1", Plan: "# Plan"}
	tests := []struct {
		name       string
		send       func() error
		textErr    error
		buttons    bool
		buttonsErr error
		wantErr    string
	}{
		{name: "ask", send: func() error { return s.bot.SendAskCard(context.Background(), "C123:1111.2222", "m1", ask) }, buttons: true},
		{name: "plan", send: func() error { return s.bot.SendPlanCard(context.Background(), "C123:1111.2222", "m1", plan) }, buttons: true},
		{
			name: "card without an ID gets no buttons",
			send: func() error {
				return s.bot.SendPlanCard(context.Background(), "C123:1111.2222", "m1", events.ExitPlanModeEventData{Plan: "# Plan"})
			},
		},
		{
			name:    "text fails",
			send:    func() error { return s.bot.SendAskCard(context.Background(), "C123:1111.2222", "m1", ask) },
			textErr: errors.New("boom"), wantErr: "slack send message: boom",
		},
		{
			name:    "buttons fail",
			send:    func() error { return s.bot.SendPlanCard(context.Background(), "C123:1111.2222", "m1", plan) },
			buttons: true, buttonsErr: errors.New("boom"), wantErr: "slack send card buttons: boom",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1111.3333", tc.textErr).Once()
			if tc.buttons {
				s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1111.4444", tc.buttonsErr).Once()
			}

			err := tc.send()

			s.session.AssertExpectations(s.T())
			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
				require.Empty(s.T(), s.bot.openCards)
				return
			}
			require.NoError(s.T(), err)
			if tc.buttons {
				require.Equal(s.T(), map[string]openCard{"C123:1111.2222": {cardID: "toolu_1", ts: "1111.4444"}}, s.bot.openCards)
			} else {
				require.Empty(s.T(), s.bot.openCards)
			}
		})
	}
}

func (s *BotSuite) TestCardActionBlock() {
	block := cardActionBlock("C123", "toolu_1", []bot.CardButton{
		{Label: "A", Choice: "1"},
		{Label: "Approve", Choice: "approve", Style: bot.CardButtonPrimary},
		{Label: "Reject", Choice: "reject", Style: bot.CardButtonDanger},
	})
	require.Equal(s.T(), "card_actions:toolu_1", block.BlockID)
	require.Len(s.T(), block.Elements.ElementSet, 3)
	var ids, labels []string
	var styles []goslack.Style
	for _, el := range block.Elements.ElementSet {
		btn, ok := el.(*goslack.ButtonBlockElement)
		require.True(s.T(), ok)
		ids = append(ids, btn.ActionID)
		labels = append(labels, btn.Text.Text)
		styles = append(styles, btn.Style)
	}
	require.Equal(s.T(), []string{"card:C123:toolu_1:1", "card:C123:toolu_1:approve", "card:C123:toolu_1:reject"}, ids)
	require.Equal(s.T(), []string{"A", "Approve", "Reject"}, labels)
	require.Equal(s.T(), []goslack.Style{"", goslack.StylePrimary, goslack.StyleDanger}, styles)
}

func (s *BotSuite) TestCardClosedBlock() {
	block := cardClosedBlock("<b> & co", "U1")
	require.Len(s.T(), block.ContextElements.Elements, 1)
	text, ok := block.ContextElements.Elements[0].(*goslack.TextBlockObject)
	require.True(s.T(), ok)
	require.Equal(s.T(), "› &lt;b&gt; &amp; co — <@U1>", text.Text)
}

func (s *BotSuite) TestCloseCard() {
	tests := []struct {
		name      string
		cardID    string
		updateErr error
		wantCall  bool
		wantOpen  bool
		wantErr   string
	}{
		{name: "closes the open card", cardID: "toolu_1", wantCall: true},
		{name: "other card is left open", cardID: "toolu_old", wantOpen: true},
		{name: "update fails", cardID: "toolu_1", wantCall: true, updateErr: errors.New("boom"), wantErr: "slack close card: boom"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.bot.openCards = map[string]openCard{"C123:1111.2222": {cardID: "toolu_1", ts: "1111.4444"}}
			if tc.wantCall {
				s.session.On("UpdateMessage", "C123", "1111.4444", mock.Anything).Return("C123", "1111.4444", "", tc.updateErr).Once()
			}

			err := s.bot.CloseCard(context.Background(), "C123:1111.2222", tc.cardID, "Approved", "U1")

			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			_, open := s.bot.openCards["C123:1111.2222"]
			require.Equal(s.T(), tc.wantOpen, open)
			s.session.AssertExpectations(s.T())
		})
	}
}

func (s *BotSuite) TestCloseCardNothingOpen() {
	require.NoError(s.T(), s.bot.CloseCard(context.Background(), "C123", "toolu_1", "Approved", "U1"))
	s.session.AssertNotCalled(s.T(), "UpdateMessage", mock.Anything, mock.Anything, mock.Anything)
}

func (s *BotSuite) TestHandleInteractiveCardAction() {
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, m *bot.IncomingMessage) {
		received <- m
	})
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	s.bot.handleInteractive(socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: goslack.InteractionCallback{
			Type: goslack.InteractionTypeBlockActions,
			User: goslack.User{ID: "U456"},
			ActionCallback: goslack.ActionCallbacks{
				BlockActions: []*goslack.BlockAction{
					{ActionID: bot.CardActionID("C123:1111.2222", "toolu_1", "approve")},
				},
			},
		},
		Request: &socketmode.Request{},
	})

	select {
	case msg := <-received:
		require.Equal(s.T(), "C123:1111.2222", msg.ChannelID)
		require.Equal(s.T(), "U456", msg.AuthorID)
		require.Equal(s.T(), "U456", msg.AuthorName)
		require.Equal(s.T(), "approve", msg.Content)
		require.Equal(s.T(), "toolu_1", msg.CardID)
		require.True(s.T(), msg.IsBotMention)
		require.Equal(s.T(), types.PlatformSlack, msg.Platform)
		require.False(s.T(), msg.Timestamp.IsZero())
	case <-time.After(time.Second):
		s.Fail("timeout waiting for card click")
	}
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

func (s *BotSuite) TestHandleIncomingMessageDelayedNoop() {
	s.bot.HandleIncomingMessageDelayed(context.Background(), "", "", "", "", "", 0)
}
