package slack

import (
	"context"
	"errors"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
)

// --- Event Handling ---

func (s *BotSuite) TestHandleMessageMentionInChannel() {
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "<@U123BOT> hello",
		TimeStamp:   "1234567890.000001",
		Channel:     "C123",
		ChannelType: "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.Equal(s.T(), "C123", msg.ChannelID)
		require.Equal(s.T(), "hello", msg.Content)
		require.True(s.T(), msg.IsBotMention)
		require.Equal(s.T(), "U456", msg.AuthorID)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

func (s *BotSuite) TestHandleMessageMentionInThread() {
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received <- msg
	})

	s.session.On("GetConversationReplies", mock.AnythingOfType("*slack.GetConversationRepliesParameters")).
		Return([]goslack.Message{{Msg: goslack.Msg{User: "U123BOT"}}}, false, "", nil)

	ev := &slackevents.MessageEvent{
		User:            "U456",
		Text:            "<@U123BOT> hello",
		TimeStamp:       "1234567891.000001",
		ThreadTimeStamp: "1234567890.000001",
		Channel:         "C123",
		ChannelType:     "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.Equal(s.T(), "C123:1234567890.000001", msg.ChannelID)
		require.True(s.T(), msg.IsBotMention)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

func (s *BotSuite) TestHandleMessageWithPrefix() {
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "!loop status",
		TimeStamp:   "1234567890.000001",
		Channel:     "C123",
		ChannelType: "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.HasPrefix)
		require.Equal(s.T(), "status", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

func (s *BotSuite) TestHandleMessageDM() {
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:        "U456",
		Text:        "hello",
		TimeStamp:   "1234567890.000001",
		Channel:     "D123",
		ChannelType: "im",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.IsDM)
		require.Equal(s.T(), "hello", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

func (s *BotSuite) TestHandleMessageIgnored() {
	tests := []struct {
		name string
		ev   *slackevents.MessageEvent
	}{
		{"subtype", &slackevents.MessageEvent{User: "U456", Text: "edited", TimeStamp: "1234567890.000001", Channel: "C123", SubType: "message_changed"}},
		{"no_trigger", &slackevents.MessageEvent{User: "U456", Text: "just a random message", TimeStamp: "1234567890.000001", Channel: "C123", ChannelType: "channel"}},
		{"self_no_mention", &slackevents.MessageEvent{User: "U123BOT", Text: "hello from bot", Channel: "C123", TimeStamp: "1234567890.000001"}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			received := make(chan *bot.IncomingMessage, 1)
			s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) { received <- msg })
			s.bot.handleMessage(tt.ev)

			select {
			case <-received:
				s.Fail("should not have received message")
			case <-time.After(50 * time.Millisecond):
				// expected
			}
		})
	}
}

func (s *BotSuite) TestHandleMessageReplyToBot() {
	s.session.On("GetConversationReplies", mock.AnythingOfType("*slack.GetConversationRepliesParameters")).Return(
		[]goslack.Message{{Msg: goslack.Msg{User: "U123BOT"}}},
		false, "", nil,
	)

	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received <- msg
	})

	ev := &slackevents.MessageEvent{
		User:            "U456",
		Text:            "follow up",
		TimeStamp:       "1234567891.000001",
		ThreadTimeStamp: "1234567890.000001",
		Channel:         "C123",
		ChannelType:     "channel",
	}
	s.bot.handleMessage(ev)

	select {
	case msg := <-received:
		require.True(s.T(), msg.IsReplyToBot)
		require.Equal(s.T(), "C123:1234567890.000001", msg.ChannelID)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

// --- Slash Commands ---

func (s *BotSuite) TestHandleSlashCommandDispatched() {
	tests := []struct {
		name     string
		cmd      goslack.SlashCommand
		wantCmd  string
		wantOpts map[string]string
		wantAuth string
	}{
		{"schedule", goslack.SlashCommand{ChannelID: "C123", TeamID: "T123", Text: "schedule 0 9 * * * cron Check for updates"},
			"schedule", map[string]string{"schedule": "0 9 * * *", "type": "cron", "prompt": "Check for updates"}, ""},
		{"tasks", goslack.SlashCommand{ChannelID: "C123", TeamID: "T123", Text: "tasks"},
			"tasks", nil, ""},
		{"author_id", goslack.SlashCommand{ChannelID: "C123", TeamID: "T123", UserID: "U789", Text: "tasks"},
			"tasks", nil, "U789"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			b := NewBot(session, sc, testLogger())
			b.botUserID = "U123BOT"

			received := make(chan *bot.Interaction, 1)
			b.OnInteraction(func(_ context.Context, i *bot.Interaction) { received <- i })
			sc.On("Ack", mock.Anything, mock.Anything).Return()

			evt := socketmode.Event{
				Type: socketmode.EventTypeSlashCommand, Data: tt.cmd, Request: &socketmode.Request{},
			}
			b.handleSlashCommand(evt)

			select {
			case inter := <-received:
				require.Equal(s.T(), tt.wantCmd, inter.CommandName)
				for k, v := range tt.wantOpts {
					require.Equal(s.T(), v, inter.Options[k], "option %s", k)
				}
				if tt.wantAuth != "" {
					require.Equal(s.T(), tt.wantAuth, inter.AuthorID)
				}
			case <-time.After(time.Second):
				s.Fail("timeout waiting for interaction")
			}
		})
	}
}

func (s *BotSuite) TestHandleSlashCommandHelp() {
	// Empty text should send help back
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()
	s.session.On("PostMessage", "C123", mock.Anything).Return("C123", "1234.5678", nil)

	evt := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: goslack.SlashCommand{
			ChannelID: "C123",
			TeamID:    "T123",
			Text:      "",
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleSlashCommand(evt)

	time.Sleep(50 * time.Millisecond)
	s.session.AssertCalled(s.T(), "PostMessage", "C123", mock.Anything)
}

func (s *BotSuite) TestHandleEventsAPIChannelMention() {
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received <- msg
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: string(slackevents.Message),
				Data: &slackevents.MessageEvent{
					User:        "U456",
					Text:        "<@U123BOT> test",
					TimeStamp:   "1234567890.000001",
					Channel:     "C123",
					ChannelType: "channel",
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEvent(evt)

	select {
	case msg := <-received:
		require.True(s.T(), msg.IsBotMention)
		require.Equal(s.T(), "test", msg.Content)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message")
	}
}

// --- Command Parsing ---

func (s *BotSuite) TestParseSlashCommand() {
	tests := []struct {
		name       string
		input      string
		wantCmd    string
		wantOpts   map[string]string
		wantErrSub string // non-empty means error expected
	}{
		// schedule
		{"schedule_cron", "schedule 0 9 * * * cron Check updates", "schedule", map[string]string{"schedule": "0 9 * * *", "type": "cron", "prompt": "Check updates"}, ""},
		{"schedule_interval", "schedule 1h interval Run checks", "schedule", map[string]string{"schedule": "1h", "type": "interval", "prompt": "Run checks"}, ""},
		{"schedule_too_few", "schedule foo bar", "", nil, "Usage:"},
		{"schedule_type_at_start", "schedule cron prompt text", "", nil, "Usage:"},
		{"schedule_type_at_end", "schedule daily cron", "", nil, "Usage:"},
		{"schedule_no_type", "schedule daily something prompt", "", nil, "Usage:"},
		// task
		{"task", "task 74", "task", map[string]string{"task_id": "74"}, ""},
		{"task_invalid_id", "task abc", "", nil, "Invalid task_id"},
		{"task_no_args", "task", "", nil, "Usage:"},
		// cancel
		{"cancel", "cancel 42", "cancel", map[string]string{"task_id": "42"}, ""},
		{"cancel_invalid_id", "cancel abc", "", nil, "Invalid task_id"},
		{"cancel_no_args", "cancel", "", nil, "Usage:"},
		// toggle
		{"toggle", "toggle 7", "toggle", map[string]string{"task_id": "7"}, ""},
		{"toggle_no_args", "toggle", "", nil, "Usage:"},
		{"toggle_invalid_id", "toggle abc", "", nil, "Invalid task_id"},
		// edit
		{"edit_all_flags", "edit 5 --schedule 1h --type interval --prompt New task prompt text", "edit", map[string]string{"task_id": "5", "schedule": "1h", "type": "interval", "prompt": "New task prompt text"}, ""},
		{"edit_no_flags", "edit 5", "", nil, "At least one of"},
		{"edit_no_args", "edit", "", nil, "Usage:"},
		{"edit_invalid_id", "edit abc", "", nil, "Invalid task_id"},
		{"edit_schedule_no_val", "edit 1 --schedule", "", nil, "--schedule requires a value"},
		{"edit_type_no_val", "edit 1 --type", "", nil, "--type requires a value"},
		{"edit_prompt_no_val", "edit 1 --prompt", "", nil, "--prompt requires a value"},
		{"edit_with_prompt", "edit 1 --prompt hello world", "edit", map[string]string{"prompt": "hello world"}, ""},
		{"edit_with_type", "edit 1 --type cron", "edit", map[string]string{"type": "cron"}, ""},
		// simple commands
		{"status", "status", "status", nil, ""},
		{"stop", "stop", "stop", nil, ""},
		{"readme", "readme", "readme", nil, ""},
		{"iamtheowner", "iamtheowner", "iamtheowner", nil, ""},
		{"tasks", "tasks", "tasks", nil, ""},
		// template
		{"template_add", "template add daily-check", "template-add", map[string]string{"name": "daily-check"}, ""},
		{"template_list", "template list", "template-list", nil, ""},
		{"template_no_args", "template", "", nil, "Usage:"},
		{"template_unknown_sub", "template foo", "", nil, "Usage:"},
		{"template_add_no_name", "template add", "", nil, "Usage:"},
		// shortcuts / shortcut
		{"shortcuts", "shortcuts", "shortcuts", nil, ""},
		{"shortcut_name", "shortcut my-shortcut", "shortcut", map[string]string{"name": "my-shortcut"}, ""},
		{"shortcut_no_args", "shortcut", "", nil, "Usage:"},
		// workflow
		{"workflow_list", "workflow list", "workflows", nil, ""},
		{"workflow_runs", "workflow runs", "workflow-runs", nil, ""},
		{"workflow_run", "workflow run my-flow", "workflow-run", map[string]string{"name": "my-flow"}, ""},
		{"workflow_cancel", "workflow cancel run-42", "workflow-cancel", map[string]string{"run_id": "run-42"}, ""},
		{"workflow_delete", "workflow delete run-42", "workflow-delete", map[string]string{"run_id": "run-42"}, ""},
		{"workflow_retry", "workflow retry run-42", "workflow-retry", map[string]string{"run_id": "run-42"}, ""},
		{"workflow_no_args", "workflow", "", nil, "Usage:"},
		{"workflow_run_no_name", "workflow run", "", nil, "Usage:"},
		{"workflow_cancel_no_id", "workflow cancel", "", nil, "Usage:"},
		{"workflow_delete_no_id", "workflow delete", "", nil, "Usage:"},
		{"workflow_retry_no_id", "workflow retry", "", nil, "Usage:"},
		{"workflow_unknown_sub", "workflow foo", "", nil, "Usage:"},
		// errors
		{"empty", "", "", nil, "Available commands"},
		{"unknown", "foo", "", nil, "Unknown subcommand: foo"},
		// help text checks
		{"help_has_task", "", "", nil, "/loop task"},
		{"help_has_stop", "", "", nil, "/loop stop"},
		{"help_has_readme", "", "", nil, "/loop readme"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			inter, errText := parseSlashCommand("C123", "T123", tt.input)
			if tt.wantErrSub != "" {
				require.Contains(s.T(), errText, tt.wantErrSub)
				return
			}
			require.Empty(s.T(), errText)
			require.NotNil(s.T(), inter)
			require.Equal(s.T(), tt.wantCmd, inter.CommandName)
			require.Equal(s.T(), "C123", inter.ChannelID)
			require.Equal(s.T(), "T123", inter.GuildID)
			for k, v := range tt.wantOpts {
				require.Equal(s.T(), v, inter.Options[k], "option %s", k)
			}
		})
	}
}

func (s *BotSuite) TestEventLoopChannelClosed() {
	session := new(MockSession)
	events := make(chan socketmode.Event, 1)
	sc := &MockSocketModeClient{events: events}
	bot := NewBot(session, sc, testLogger())
	bot.botUserID = "U123BOT"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		bot.eventLoop(ctx)
		close(done)
	}()

	// Close the events channel to trigger the !ok path.
	close(events)

	select {
	case <-done:
		// eventLoop exited correctly
	case <-time.After(time.Second):
		s.Fail("eventLoop did not exit when events channel closed")
	}
}

func (s *BotSuite) TestHandleEventInvalidData() {
	// All should return without panic
	for _, evt := range []socketmode.Event{
		{Type: socketmode.EventTypeSlashCommand, Data: "not a slash command"},
		{Type: socketmode.EventTypeEventsAPI, Data: "not an events api event"},
		{Type: socketmode.EventType("unknown"), Data: nil},
	} {
		s.bot.handleEvent(evt)
	}
}

func (s *BotSuite) TestHandleEventBothPaths() {
	// Exercise both case branches through handleEvent

	// EventsAPI path
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()
	evtAPI := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "unknown",
				Data: nil,
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEvent(evtAPI)

	// SlashCommand path (with invalid data, exercises both switch branches)
	evtCmd := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: "invalid",
	}
	s.bot.handleEvent(evtCmd)
}

func (s *BotSuite) TestEventLoopProcessesEvent() {
	// Test that eventLoop processes events from the channel
	session := new(MockSession)
	events := make(chan socketmode.Event, 10)
	sc := &MockSocketModeClient{events: events}
	b := NewBot(session, sc, testLogger())
	b.botUserID = "U123BOT"

	received := make(chan *bot.IncomingMessage, 1)
	b.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received <- msg
	})

	sc.On("Ack", mock.Anything, mock.Anything).Return()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go b.eventLoop(ctx)

	// Send a mention via message event through the channel.
	events <- socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: string(slackevents.Message),
				Data: &slackevents.MessageEvent{
					User:        "U456",
					Text:        "<@U123BOT> hello from event loop",
					TimeStamp:   "1234567890.000001",
					Channel:     "C123",
					ChannelType: "channel",
				},
			},
		},
		Request: &socketmode.Request{},
	}

	select {
	case msg := <-received:
		require.Equal(s.T(), "hello from event loop", msg.Content)
		require.True(s.T(), msg.IsBotMention)
	case <-time.After(time.Second):
		s.Fail("timeout waiting for message from eventLoop")
	}
}

func (s *BotSuite) TestStartRunContextError() {
	// Test that Start logs socket mode errors
	session := new(MockSession)
	session.On("AuthTest").Return(&goslack.AuthTestResponse{
		UserID: "U123BOT",
		User:   "loopbot",
	}, nil)
	session.On("SetUserPresence", "auto").Return(nil)

	// Create a mock socket client that returns an error from RunContext
	errSc := &errorSocketModeClient{
		events: make(chan socketmode.Event),
		runErr: errors.New("socket error"),
	}

	bot := NewBot(session, errSc, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	err := bot.Start(ctx)
	require.NoError(s.T(), err)

	// Give the goroutine time to execute RunContext and log the error.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Give time for cleanup.
	time.Sleep(50 * time.Millisecond)
}

// errorSocketModeClient is a SocketModeClient that returns an error from RunContext.
type errorSocketModeClient struct {
	events chan socketmode.Event
	runErr error
}

func (e *errorSocketModeClient) RunContext(_ context.Context) error {
	return e.runErr
}

func (e *errorSocketModeClient) Ack(_ socketmode.Request, _ ...any) {}

func (e *errorSocketModeClient) Events() <-chan socketmode.Event {
	return e.events
}

// --- Additional bot edge cases ---

func (s *BotSuite) TestSendTypingRemoveReactionErrors() {
	for _, removeErr := range []string{"remove error", "no_reaction"} {
		s.Run(removeErr, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			ref := goslack.NewRefToMessage("C123", "1234.5678")
			bot.lastMessageRef.Store("C123", ref)

			session.On("AddReaction", "eyes", ref).Return(nil)
			session.On("RemoveReaction", "eyes", ref).Return(errors.New(removeErr))

			ctx, cancel := context.WithCancel(context.Background())
			err := bot.SendTyping(ctx, "C123")
			require.NoError(s.T(), err)

			cancel()
			time.Sleep(50 * time.Millisecond)
			session.AssertCalled(s.T(), "RemoveReaction", "eyes", ref)
		})
	}
}

func (s *BotSuite) TestIsReplyToBotFalse() {
	tests := []struct {
		name    string
		replies []goslack.Message
		err     error
	}{
		{"api_error", nil, errors.New("api error")},
		{"empty_replies", []goslack.Message{}, nil},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			session := new(MockSession)
			sc := newMockSocketClient()
			bot := NewBot(session, sc, testLogger())
			bot.botUserID = "U123BOT"

			ev := &slackevents.MessageEvent{
				User: "U456", Channel: "C123",
				ThreadTimeStamp: "1234567890.000001", TimeStamp: "1234567890.000002",
			}
			session.On("GetConversationReplies", mock.Anything).Return(tt.replies, false, "", tt.err)
			require.False(s.T(), bot.isReplyToBot(ev))
		})
	}
}

func (s *BotSuite) TestHandleEventsAPIUnknownInnerEvent() {
	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "unknown_type",
				Data: nil,
			},
		},
		Request: &socketmode.Request{},
	}
	// Should not panic
	s.bot.handleEventsAPI(evt)
}

func (s *BotSuite) TestHandleEventsAPIMessageEvent() {
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		received <- msg
	})

	s.socketClient.On("Ack", mock.Anything, mock.Anything).Return()

	// Use a DM to exercise the message event path.
	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: string(slackevents.Message),
				Data: &slackevents.MessageEvent{
					User:        "U456",
					Text:        "hello via events api",
					TimeStamp:   "1234567890.000001",
					Channel:     "D123",
					ChannelType: "im",
				},
			},
		},
		Request: &socketmode.Request{},
	}
	s.bot.handleEventsAPI(evt)

	select {
	case msg := <-received:
		require.Equal(s.T(), "hello via events api", msg.Content)
		require.True(s.T(), msg.IsDM)
	case <-time.After(time.Second):
		s.Fail("timeout")
	}
}

func (s *BotSuite) TestHandleEventsAPINilRequest() {
	// No Request to Ack
	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "unknown_type",
				Data: nil,
			},
		},
		Request: nil,
	}
	s.bot.handleEventsAPI(evt)
}
