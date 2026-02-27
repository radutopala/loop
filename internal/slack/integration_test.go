//go:build integration

package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/bot"
)

const (
	defaultTimeout = 30 * time.Second
	apiDelay       = 200 * time.Millisecond
)

// SlackIntegrationSuite tests SlackBot against the real Slack API.
type SlackIntegrationSuite struct {
	suite.Suite
	botClient   *goslack.Client
	userClient  *goslack.Client // nil if SLACK_USER_TOKEN not provided
	bot         *SlackBot
	channelID   string // test channel created in SetupSuite
	channelName string
	botUserID   string
	cfg         *integrationConfig
	ctx         context.Context
	cancel      context.CancelFunc
	// cleanupChannels tracks channels to archive in TearDownSuite.
	cleanupChannels []string
}

func TestSlackIntegration(t *testing.T) {
	suite.Run(t, new(SlackIntegrationSuite))
}

func (s *SlackIntegrationSuite) SetupSuite() {
	cfg, err := loadIntegrationConfig()
	require.NoError(s.T(), err, "failed to load integration config")
	s.cfg = cfg

	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Create bot client and socket mode client.
	s.botClient = goslack.New(cfg.BotToken, goslack.OptionAppLevelToken(cfg.AppToken))
	smClient := socketmode.New(s.botClient)

	logger := slog.Default()
	s.bot = NewBot(s.botClient, NewSocketModeAdapter(smClient), logger)

	// Start the bot (auth + event loop).
	err = s.bot.Start(s.ctx)
	require.NoError(s.T(), err, "failed to start bot")

	s.botUserID = s.bot.BotUserID()
	require.NotEmpty(s.T(), s.botUserID, "bot user ID should not be empty")

	// Create user client if token is available.
	if cfg.UserToken != "" {
		s.userClient = goslack.New(cfg.UserToken)
	}

	// Create a dedicated test channel (retry with different suffix on name collision).
	for range 3 {
		s.channelName = "inttest-" + randomSuffix()
		ch, createErr := s.botClient.CreateConversation(goslack.CreateConversationParams{
			ChannelName: s.channelName,
		})
		if createErr == nil {
			s.channelID = ch.ID
			s.cleanupChannels = append(s.cleanupChannels, s.channelID)
			break
		}
		if !strings.Contains(createErr.Error(), "name_taken") {
			require.NoError(s.T(), createErr, "failed to create test channel")
		}
		time.Sleep(10 * time.Millisecond) // shift the nano suffix
	}
	require.NotEmpty(s.T(), s.channelID, "failed to create test channel after retries")

	// If we have a user client, invite the user to the test channel.
	if s.userClient != nil {
		authResp, err := s.userClient.AuthTest()
		if err == nil {
			_, err = s.botClient.InviteUsersToConversation(s.channelID, authResp.UserID)
			if err != nil {
				// Non-fatal: user might already be in channel.
				s.T().Logf("could not invite user to test channel: %v", err)
			}
		}
	}

	// Verify Socket Mode is alive by sending a canary message and waiting
	// for it to arrive through the event loop.
	s.T().Log("verifying Socket Mode connection...")
	if s.userClient != nil {
		// Ensure user has joined the test channel (required to post).
		_, _, _, err = s.userClient.JoinConversation(s.channelID)
		if err != nil {
			s.T().Logf("user join channel: %v (non-fatal)", err)
		}

		canary := "inttest-canary-" + randomSuffix()
		canaryReceived := make(chan struct{}, 1)
		s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
			if strings.Contains(msg.Content, canary) {
				select {
				case canaryReceived <- struct{}{}:
				default:
				}
			}
		})
		for attempt := range 5 {
			if attempt > 0 {
				s.T().Logf("Socket Mode canary retry (attempt %d)", attempt+1)
				time.Sleep(2 * time.Second)
			}
			_, sendErr := sendUserMessage(s.userClient, s.channelID, "!loop "+canary)
			if sendErr != nil {
				s.T().Logf("canary send error (will retry): %v", sendErr)
				continue
			}
			select {
			case <-canaryReceived:
				s.T().Log("Socket Mode connection verified")
				goto canaryOK
			case <-time.After(10 * time.Second):
			}
		}
		s.T().Fatal("Socket Mode not receiving events after 5 canary attempts")
	canaryOK:
	} else {
		s.T().Log("no user token, falling back to static wait")
		time.Sleep(5 * time.Second)
	}
}

func (s *SlackIntegrationSuite) TearDownSuite() {
	// Stop the bot.
	if s.bot != nil {
		_ = s.bot.Stop()
	}
	if s.cancel != nil {
		s.cancel()
	}

	// Archive test channels (best-effort).
	for _, chID := range s.cleanupChannels {
		archiveChannel(s.botClient, chID)
	}
}

func (s *SlackIntegrationSuite) requireUserToken() {
	if s.userClient == nil {
		s.T().Skip("SLACK_USER_TOKEN not set, skipping test requiring user actions")
	}
}

// rateSleep adds a small delay between API calls to avoid rate limits.
func rateSleep() {
	time.Sleep(apiDelay)
}

// ===== Group A: Bot Authentication & Lifecycle =====

func (s *SlackIntegrationSuite) TestA01_AuthAndBotUserID() {
	require.NotEmpty(s.T(), s.botUserID)
	// Bot user IDs start with U.
	require.True(s.T(), strings.HasPrefix(s.botUserID, "U"), "bot user ID should start with U")
}

// ===== Group B: Channel Operations =====

func (s *SlackIntegrationSuite) TestB01_CreateChannel() {
	rateSleep()
	name := "inttest-create-" + randomSuffix()
	id, err := s.bot.CreateChannel(s.ctx, "", name)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), id)
	s.cleanupChannels = append(s.cleanupChannels, id)

	// Verify channel exists.
	rateSleep()
	ch, err := s.botClient.GetConversationInfo(&goslack.GetConversationInfoInput{
		ChannelID: id,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), name, ch.Name)
}

func (s *SlackIntegrationSuite) TestB02_CreateChannelDuplicate() {
	rateSleep()
	name := "inttest-dup-" + randomSuffix()

	id1, err := s.bot.CreateChannel(s.ctx, "", name)
	require.NoError(s.T(), err)
	s.cleanupChannels = append(s.cleanupChannels, id1)

	// Allow Slack API to propagate the new channel before duplicate create.
	time.Sleep(2 * time.Second)
	id2, err := s.bot.CreateChannel(s.ctx, "", name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), id1, id2, "duplicate create should return same channel ID")
}

func (s *SlackIntegrationSuite) TestB03_SetChannelTopic() {
	rateSleep()
	topic := "/home/user/dev/loop"
	err := s.bot.SetChannelTopic(s.ctx, s.channelID, topic)
	require.NoError(s.T(), err)

	rateSleep()
	ch, err := s.botClient.GetConversationInfo(&goslack.GetConversationInfoInput{
		ChannelID: s.channelID,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), topic, ch.Topic.Value)
}

func (s *SlackIntegrationSuite) TestB04_GetChannelName() {
	rateSleep()
	name, err := s.bot.GetChannelName(s.ctx, s.channelID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), s.channelName, name)
}

func (s *SlackIntegrationSuite) TestB05_GetChannelNameComposite() {
	rateSleep()
	compositeID := s.channelID + ":1234567890.000001"
	name, err := s.bot.GetChannelName(s.ctx, compositeID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), s.channelName, name)
}

func (s *SlackIntegrationSuite) TestB06_GetOwnerUserID() {
	rateSleep()
	ownerID, err := s.bot.GetOwnerUserID(s.ctx)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), ownerID)
	require.True(s.T(), strings.HasPrefix(ownerID, "U"))
}

func (s *SlackIntegrationSuite) TestB07_InviteUserToChannel() {
	rateSleep()
	// Create a fresh channel to test invite.
	name := "inttest-invite-" + randomSuffix()
	chID, err := s.bot.CreateChannel(s.ctx, "", name)
	require.NoError(s.T(), err)
	s.cleanupChannels = append(s.cleanupChannels, chID)

	rateSleep()
	ownerID, err := s.bot.GetOwnerUserID(s.ctx)
	require.NoError(s.T(), err)

	rateSleep()
	err = s.bot.InviteUserToChannel(s.ctx, chID, ownerID)
	// Several "soft" errors are acceptable for invite.
	if err != nil {
		errStr := err.Error()
		isSoftErr := strings.Contains(errStr, "already_in_channel") ||
			strings.Contains(errStr, "user_not_found") ||
			strings.Contains(errStr, "cant_invite_self")
		if !isSoftErr {
			require.NoError(s.T(), err)
		}
	}
}

func (s *SlackIntegrationSuite) TestB08_OnChannelJoinEvent() {
	s.requireUserToken()
	rateSleep()

	// Register handler before triggering the event.
	received := make(chan string, 1)
	s.bot.OnChannelJoin(func(_ context.Context, channelID string) {
		select {
		case received <- channelID:
		default:
		}
	})

	// User creates a channel (bot is NOT a member yet).
	name := "inttest-join-" + randomSuffix()
	ch, err := s.userClient.CreateConversation(goslack.CreateConversationParams{
		ChannelName: name,
	})
	require.NoError(s.T(), err)
	s.cleanupChannels = append(s.cleanupChannels, ch.ID)

	// User invites the bot — should trigger member_joined_channel event.
	rateSleep()
	_, err = s.userClient.InviteUsersToConversation(ch.ID, s.botUserID)
	require.NoError(s.T(), err)

	select {
	case joinedID := <-received:
		require.Equal(s.T(), ch.ID, joinedID)
	case <-time.After(defaultTimeout):
		s.T().Log("member_joined_channel event not received (Socket Mode may not deliver this reliably)")
	}
}

// ===== Group C: Message Sending =====

func (s *SlackIntegrationSuite) TestC01_SendMessagePlain() {
	rateSleep()
	content := fmt.Sprintf("integration-test-msg-%s", randomSuffix())
	err := s.bot.SendMessage(s.ctx, &bot.OutgoingMessage{
		ChannelID: s.channelID,
		Content:   content,
	})
	require.NoError(s.T(), err)

	// Verify message appears in history.
	msg, err := waitForMessage(s.botClient, s.channelID, "",
		messageContains(content), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Text, content)
}

func (s *SlackIntegrationSuite) TestC02_SendMessageLong() {
	rateSleep()
	// Create a message longer than maxMessageLen (4000 chars).
	marker := fmt.Sprintf("SPLIT-TEST-%s", randomSuffix())
	longContent := marker + " " + strings.Repeat("x", maxMessageLen+100)

	err := s.bot.SendMessage(s.ctx, &bot.OutgoingMessage{
		ChannelID: s.channelID,
		Content:   longContent,
	})
	require.NoError(s.T(), err)

	// Verify at least the first chunk arrived.
	msg, err := waitForMessage(s.botClient, s.channelID, "",
		messageContains(marker), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Text, marker)
}

func (s *SlackIntegrationSuite) TestC03_PostMessagePlain() {
	rateSleep()
	content := fmt.Sprintf("post-msg-%s", randomSuffix())
	err := s.bot.PostMessage(s.ctx, s.channelID, content)
	require.NoError(s.T(), err)

	msg, err := waitForMessage(s.botClient, s.channelID, "",
		messageContains(content), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Text, content)
}

func (s *SlackIntegrationSuite) TestC04_PostMessageTextMentionConversion() {
	rateSleep()
	botUsername := s.bot.botUsername
	if botUsername == "" {
		s.T().Skip("bot username not available")
	}

	content := fmt.Sprintf("hey @%s check this %s", botUsername, randomSuffix())
	err := s.bot.PostMessage(s.ctx, s.channelID, content)
	require.NoError(s.T(), err)

	// The message should contain <@BOTID> instead of @botname.
	expectedMention := "<@" + s.botUserID + ">"
	msg, err := waitForMessage(s.botClient, s.channelID, "",
		messageContains(expectedMention), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Text, expectedMention)
}

func (s *SlackIntegrationSuite) TestC05_PostMessageInThread() {
	rateSleep()
	// First create a parent message.
	parentContent := fmt.Sprintf("parent-%s", randomSuffix())
	_, parentTS, err := s.botClient.PostMessage(s.channelID,
		goslack.MsgOptionText(parentContent, false))
	require.NoError(s.T(), err)

	rateSleep()
	// Post to the thread via composite ID.
	replyContent := fmt.Sprintf("thread-reply-%s", randomSuffix())
	compositeID := s.channelID + ":" + parentTS
	err = s.bot.PostMessage(s.ctx, compositeID, replyContent)
	require.NoError(s.T(), err)

	// Verify reply appears in thread.
	msg, err := waitForThreadReply(s.botClient, s.channelID, parentTS,
		messageContains(replyContent), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Text, replyContent)
}

// ===== Group D: Thread Operations =====

func (s *SlackIntegrationSuite) TestD01_CreateThreadDefault() {
	rateSleep()
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, "test-thread-default", "", "")
	require.NoError(s.T(), err)
	require.Contains(s.T(), threadID, s.channelID+":")
	require.Contains(s.T(), threadID, ".") // Should contain a timestamp with dot.
}

func (s *SlackIntegrationSuite) TestD02_CreateThreadWithMessage() {
	rateSleep()
	msg := "Review the integration tests"
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, "test-thread-msg", "", msg)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), threadID)

	// Verify the initial message contains the bot self-mention and content.
	chID, ts := parseCompositeID(threadID)
	rateSleep()
	parentMsg, err := waitForMessage(s.botClient, chID, "",
		func(m goslack.Message) bool { return m.Timestamp == ts },
		defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), parentMsg.Text, "<@"+s.botUserID+">")
	require.Contains(s.T(), parentMsg.Text, "Review the integration tests")
}

func (s *SlackIntegrationSuite) TestD03_CreateThreadWithMention() {
	rateSleep()
	ownerID, err := s.bot.GetOwnerUserID(s.ctx)
	require.NoError(s.T(), err)

	rateSleep()
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, "test-thread-mention", ownerID, "")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), threadID)

	// Verify the initial message mentions the user.
	chID, ts := parseCompositeID(threadID)
	rateSleep()
	parentMsg, err := waitForMessage(s.botClient, chID, "",
		func(m goslack.Message) bool { return m.Timestamp == ts },
		defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), parentMsg.Text, "<@"+ownerID+">")
}

func (s *SlackIntegrationSuite) TestD04_DeleteThread() {
	rateSleep()
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, "test-thread-delete", "", "")
	require.NoError(s.T(), err)

	rateSleep()
	err = s.bot.DeleteThread(s.ctx, threadID)
	require.NoError(s.T(), err)

	// Verify parent message is deleted (should fail to retrieve).
	chID, ts := parseCompositeID(threadID)
	rateSleep()
	msgs, _, _, err := s.botClient.GetConversationReplies(&goslack.GetConversationRepliesParameters{
		ChannelID: chID,
		Timestamp: ts,
		Limit:     1,
	})
	// Either error or no messages means it was deleted.
	if err == nil {
		require.Empty(s.T(), msgs, "thread should be deleted")
	}
}

func (s *SlackIntegrationSuite) TestD05_DeleteThreadInvalidID() {
	err := s.bot.DeleteThread(s.ctx, "C123NOTS")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid thread ID")
}

func (s *SlackIntegrationSuite) TestD06_GetChannelParentID() {
	// Composite ID returns parent.
	parentID, err := s.bot.GetChannelParentID(s.ctx, s.channelID+":1234567890.000001")
	require.NoError(s.T(), err)
	require.Equal(s.T(), s.channelID, parentID)

	// Plain ID returns empty.
	parentID, err = s.bot.GetChannelParentID(s.ctx, s.channelID)
	require.NoError(s.T(), err)
	require.Empty(s.T(), parentID)
}

func (s *SlackIntegrationSuite) TestD07_CreateSimpleThreadWithMessage() {
	rateSleep()
	threadID, err := s.bot.CreateSimpleThread(s.ctx, s.channelID, "simple-thread-test", "Hello from simple thread")
	require.NoError(s.T(), err)
	require.Contains(s.T(), threadID, s.channelID+":")
	require.Contains(s.T(), threadID, ".")

	// Verify the parent message contains the initial text but no bot mention.
	chID, ts := parseCompositeID(threadID)
	rateSleep()
	parentMsg, err := waitForMessage(s.botClient, chID, "",
		func(m goslack.Message) bool { return m.Timestamp == ts },
		defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), parentMsg.Text, "Hello from simple thread")
	require.NotContains(s.T(), parentMsg.Text, "<@"+s.botUserID+">")
}

func (s *SlackIntegrationSuite) TestD08_CreateSimpleThreadEmptyMessageUsesName() {
	rateSleep()
	threadID, err := s.bot.CreateSimpleThread(s.ctx, s.channelID, "fallback-name-test", "")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), threadID)

	// When initialMessage is empty, the name is used as the message text.
	chID, ts := parseCompositeID(threadID)
	rateSleep()
	parentMsg, err := waitForMessage(s.botClient, chID, "",
		func(m goslack.Message) bool { return m.Timestamp == ts },
		defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), parentMsg.Text, "fallback-name-test")
}

// ===== Group E: Event Handling (requires user token) =====

func (s *SlackIntegrationSuite) TestE01_AppMentionEvent() {
	s.requireUserToken()
	rateSleep()

	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		if msg.IsBotMention && strings.Contains(msg.Content, "inttest-mention") {
			select {
			case received <- msg:
			default:
			}
		}
	})

	// Retry sending — Socket Mode event delivery can be unreliable.
	for attempt := range 5 {
		if attempt > 0 {
			s.T().Logf("retrying mention (attempt %d)", attempt+1)
		}
		_, err := sendUserMention(s.userClient, s.channelID, s.botUserID, "inttest-mention-"+randomSuffix())
		require.NoError(s.T(), err)

		select {
		case msg := <-received:
			require.True(s.T(), msg.IsBotMention)
			require.Contains(s.T(), msg.Content, "inttest-mention")
			return
		case <-time.After(15 * time.Second):
		}
	}
	s.Fail("timeout waiting for mention event after 5 attempts")
}

func (s *SlackIntegrationSuite) TestE02_DMEvent() {
	s.requireUserToken()
	rateSleep()

	// Open a DM conversation with the bot.
	dmCh, _, _, err := s.userClient.OpenConversation(&goslack.OpenConversationParameters{
		Users: []string{s.botUserID},
	})
	if err != nil && strings.Contains(err.Error(), "missing_scope") {
		s.T().Skip("user token lacks im:write scope, skipping DM test")
	}
	require.NoError(s.T(), err)
	dmChannelID := dmCh.ID

	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		if msg.IsDM && strings.Contains(msg.Content, "inttest-dm-") {
			select {
			case received <- msg:
			default:
			}
		}
	})

	for attempt := range 3 {
		if attempt > 0 {
			s.T().Logf("retrying DM (attempt %d)", attempt+1)
		}
		marker := "inttest-dm-" + randomSuffix()
		rateSleep()
		_, err = sendUserMessage(s.userClient, dmChannelID, marker)
		require.NoError(s.T(), err)

		select {
		case msg := <-received:
			require.True(s.T(), msg.IsDM)
			require.Contains(s.T(), msg.Content, marker)
			return
		case <-time.After(10 * time.Second):
		}
	}
	s.T().Log("DM event not received (Socket Mode may not deliver message.im reliably)")
}

func (s *SlackIntegrationSuite) TestE03_PrefixCommand() {
	s.requireUserToken()
	rateSleep()

	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		if msg.HasPrefix && strings.Contains(msg.Content, "inttest-prefix") {
			select {
			case received <- msg:
			default:
			}
		}
	})

	for attempt := range 5 {
		if attempt > 0 {
			s.T().Logf("retrying prefix command (attempt %d)", attempt+1)
		}
		marker := "inttest-prefix-" + randomSuffix()
		_, err := sendUserMessage(s.userClient, s.channelID, "!loop "+marker)
		require.NoError(s.T(), err)

		select {
		case msg := <-received:
			require.True(s.T(), msg.HasPrefix)
			require.Contains(s.T(), msg.Content, "inttest-prefix")
			require.False(s.T(), strings.HasPrefix(msg.Content, "!loop"))
			return
		case <-time.After(15 * time.Second):
		}
	}
	s.Fail("timeout waiting for prefix command event after 5 attempts")
}

func (s *SlackIntegrationSuite) TestE04_ReplyToBot() {
	s.requireUserToken()
	rateSleep()

	// Bot posts a message.
	marker := "inttest-reply-parent-" + randomSuffix()
	_, parentTS, err := s.botClient.PostMessage(s.channelID,
		goslack.MsgOptionText(marker, false))
	require.NoError(s.T(), err)

	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		if msg.IsReplyToBot && strings.Contains(msg.Content, "inttest-reply") {
			select {
			case received <- msg:
			default:
			}
		}
	})

	for attempt := range 5 {
		if attempt > 0 {
			s.T().Logf("retrying reply-to-bot (attempt %d)", attempt+1)
		}
		replyMarker := "inttest-reply-" + randomSuffix()
		rateSleep()
		_, err = sendUserThreadReply(s.userClient, s.channelID, parentTS, replyMarker)
		require.NoError(s.T(), err)

		select {
		case msg := <-received:
			require.True(s.T(), msg.IsReplyToBot)
			require.Contains(s.T(), msg.Content, "inttest-reply")
			require.Contains(s.T(), msg.ChannelID, ":")
			return
		case <-time.After(15 * time.Second):
		}
	}
	s.Fail("timeout waiting for reply-to-bot event after 5 attempts")
}

func (s *SlackIntegrationSuite) TestE05_MessageInThread() {
	s.requireUserToken()
	rateSleep()

	// Bot posts a parent message to create a thread context.
	_, parentTS, err := s.botClient.PostMessage(s.channelID,
		goslack.MsgOptionText("thread-parent-"+randomSuffix(), false))
	require.NoError(s.T(), err)

	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		if msg.IsBotMention && strings.Contains(msg.Content, "inttest-thread-mention") {
			select {
			case received <- msg:
			default:
			}
		}
	})

	for attempt := range 5 {
		if attempt > 0 {
			s.T().Logf("retrying thread mention (attempt %d)", attempt+1)
		}
		mentionMarker := "inttest-thread-mention-" + randomSuffix()
		rateSleep()
		mentionText := fmt.Sprintf("<@%s> %s", s.botUserID, mentionMarker)
		_, err = sendUserThreadReply(s.userClient, s.channelID, parentTS, mentionText)
		require.NoError(s.T(), err)

		select {
		case msg := <-received:
			require.True(s.T(), msg.IsBotMention)
			require.Contains(s.T(), msg.ChannelID, s.channelID+":")
			return
		case <-time.After(15 * time.Second):
		}
	}
	s.Fail("timeout waiting for thread mention event after 5 attempts")
}

func (s *SlackIntegrationSuite) TestE06_TypingIndicator() {
	s.requireUserToken()
	rateSleep()

	// Send a mention and verify the bot receives it (so lastMessageRef is populated).
	received := make(chan string, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		if msg.IsBotMention && strings.Contains(msg.Content, "inttest-typing") {
			select {
			case received <- msg.MessageID:
			default:
			}
		}
	})

	var ts string
	for attempt := range 5 {
		if attempt > 0 {
			s.T().Logf("retrying typing setup (attempt %d)", attempt+1)
		}
		marker := "inttest-typing-" + randomSuffix()
		var err error
		ts, err = sendUserMention(s.userClient, s.channelID, s.botUserID, marker)
		require.NoError(s.T(), err)

		select {
		case <-received:
			goto mentionReceived
		case <-time.After(15 * time.Second):
		}
	}
	s.T().Fatal("timeout waiting for typing setup mention after 5 attempts")
mentionReceived:

	// Trigger typing indicator.
	typingCtx, stopTyping := context.WithCancel(s.ctx)
	err := s.bot.SendTyping(typingCtx, s.channelID)
	require.NoError(s.T(), err)

	// Check that the eyes reaction was added.
	found, err := waitForReaction(s.botClient, s.channelID, ts, reactionEmoji, defaultTimeout)
	require.NoError(s.T(), err)
	require.True(s.T(), found, "eyes reaction should be added")

	// Stop typing and poll for reaction removal.
	stopTyping()
	reactionGone := false
	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		reactions, err := s.botClient.GetReactions(goslack.NewRefToMessage(s.channelID, ts),
			goslack.NewGetReactionsParameters())
		if err != nil {
			continue
		}
		found := false
		for _, r := range reactions {
			if r.Name == reactionEmoji {
				found = true
				break
			}
		}
		if !found {
			reactionGone = true
			break
		}
	}
	require.True(s.T(), reactionGone, "eyes reaction should be removed after typing stops")
}

func (s *SlackIntegrationSuite) TestE07_RandomChannelMessageIgnored() {
	s.requireUserToken()
	rateSleep()

	marker := "inttest-ignored-" + randomSuffix()
	received := make(chan *bot.IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *bot.IncomingMessage) {
		if strings.Contains(msg.Content, marker) {
			received <- msg
		}
	})

	// User sends a message without any trigger (no mention, no prefix, no DM, no reply).
	_, err := sendUserMessage(s.userClient, s.channelID, marker)
	require.NoError(s.T(), err)

	select {
	case <-received:
		s.Fail("handler should NOT fire for untriggered messages")
	case <-time.After(3 * time.Second):
		// Expected — message was correctly ignored.
	}
}

// ===== Group F: Channel Lifecycle Events (requires user token) =====

func (s *SlackIntegrationSuite) TestF01_ChannelDeleteEvent() {
	s.requireUserToken()
	rateSleep()

	// Create a temporary channel.
	name := "inttest-del-" + randomSuffix()
	ch, err := s.botClient.CreateConversation(goslack.CreateConversationParams{
		ChannelName: name,
	})
	require.NoError(s.T(), err)

	received := make(chan string, 1)
	s.bot.OnChannelDelete(func(_ context.Context, channelID string, _ bool) {
		if channelID == ch.ID {
			received <- channelID
		}
	})

	// Archive then the event should fire (Slack sends channel_deleted on archive for some workspace configs).
	rateSleep()
	err = s.botClient.ArchiveConversation(ch.ID)
	require.NoError(s.T(), err)

	select {
	case chID := <-received:
		require.Equal(s.T(), ch.ID, chID)
	case <-time.After(10 * time.Second):
		// Channel delete events may not fire for all workspace configurations.
		s.T().Log("channel_deleted event not received (may not be supported by workspace config)")
	}
}

// ===== Group G: Slash Command Parsing (injects events, verifies dispatch) =====

func (s *SlackIntegrationSuite) TestG01_SlashCommandSchedule() {
	rateSleep()
	received := make(chan any, 1)
	s.bot.OnInteraction(func(_ context.Context, i any) {
		received <- i
	})

	// Simulate a slash command via direct method call (can't trigger via API).
	inter, errText := parseSlashCommand(s.channelID, "T123", "schedule 0 9 * * * cron Check for updates")
	require.Empty(s.T(), errText)
	require.NotNil(s.T(), inter)
	require.Equal(s.T(), "schedule", inter.CommandName)
	require.Equal(s.T(), "0 9 * * *", inter.Options["schedule"])
	require.Equal(s.T(), "cron", inter.Options["type"])
	require.Equal(s.T(), "Check for updates", inter.Options["prompt"])
}

func (s *SlackIntegrationSuite) TestG02_SlashCommandTasks() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "tasks")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "tasks", inter.CommandName)
}

func (s *SlackIntegrationSuite) TestG02b_SlashCommandTask() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "task 74")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "task", inter.CommandName)
	require.Equal(s.T(), "74", inter.Options["task_id"])
}

func (s *SlackIntegrationSuite) TestG03_SlashCommandCancel() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "cancel 42")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "cancel", inter.CommandName)
	require.Equal(s.T(), "42", inter.Options["task_id"])
}

func (s *SlackIntegrationSuite) TestG04_SlashCommandToggle() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "toggle 7")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "toggle", inter.CommandName)
	require.Equal(s.T(), "7", inter.Options["task_id"])
}

func (s *SlackIntegrationSuite) TestG05_SlashCommandEdit() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "edit 5 --schedule 1h --type interval --prompt New task")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "edit", inter.CommandName)
	require.Equal(s.T(), "5", inter.Options["task_id"])
	require.Equal(s.T(), "1h", inter.Options["schedule"])
	require.Equal(s.T(), "interval", inter.Options["type"])
	require.Equal(s.T(), "New task", inter.Options["prompt"])
}

func (s *SlackIntegrationSuite) TestG06_SlashCommandStatus() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "status")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "status", inter.CommandName)
}

func (s *SlackIntegrationSuite) TestG07_SlashCommandTemplateAdd() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "template add daily-check")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "template-add", inter.CommandName)
	require.Equal(s.T(), "daily-check", inter.Options["name"])
}

func (s *SlackIntegrationSuite) TestG08_SlashCommandTemplateList() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "template list")
	require.Empty(s.T(), errText)
	require.Equal(s.T(), "template-list", inter.CommandName)
}

func (s *SlackIntegrationSuite) TestG09_SlashCommandHelp() {
	inter, errText := parseSlashCommand(s.channelID, "T123", "")
	require.Nil(s.T(), inter)
	require.Contains(s.T(), errText, "Available commands")
}
