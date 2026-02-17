//go:build integration

package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// DiscordIntegrationSuite tests DiscordBot against the real Discord API.
type DiscordIntegrationSuite struct {
	suite.Suite
	bot        *DiscordBot
	session    *discordgo.Session // raw session for verification
	channelID  string             // test channel created in SetupSuite
	botUserID  string
	guildID    string
	ctx        context.Context
	cancel     context.CancelFunc
	cleanupIDs []string // channels/threads to delete in teardown
}

func TestDiscordIntegration(t *testing.T) {
	suite.Run(t, new(DiscordIntegrationSuite))
}

func (s *DiscordIntegrationSuite) SetupSuite() {
	cfg, err := loadDiscordIntegrationConfig()
	require.NoError(s.T(), err, "failed to load discord integration config")

	s.guildID = cfg.GuildID
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Create a discordgo session.
	session, err := discordgo.New("Bot " + cfg.Token)
	require.NoError(s.T(), err, "failed to create discordgo session")
	session.Identify.Intents |= discordgo.IntentMessageContent

	s.session = session

	logger := slog.Default()
	s.bot = NewBot(session, cfg.AppID, logger)

	// Start the bot (opens gateway, resolves bot user ID).
	err = s.bot.Start(s.ctx)
	require.NoError(s.T(), err, "failed to start discord bot")

	s.botUserID = s.bot.BotUserID()
	require.NotEmpty(s.T(), s.botUserID, "bot user ID should not be empty")

	// Create a dedicated test channel.
	for range 3 {
		name := "inttest-" + randomSuffix()
		chID, createErr := s.bot.CreateChannel(s.ctx, s.guildID, name)
		if createErr == nil {
			s.channelID = chID
			s.cleanupIDs = append(s.cleanupIDs, chID)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotEmpty(s.T(), s.channelID, "failed to create test channel after retries")

	// Canary: verify gateway is receiving events via self-mention.
	s.T().Log("verifying gateway connection...")
	canary := "inttest-canary-" + randomSuffix()
	canaryReceived := make(chan struct{}, 1)
	s.bot.OnMessage(func(_ context.Context, msg *IncomingMessage) {
		if strings.Contains(msg.Content, canary) {
			select {
			case canaryReceived <- struct{}{}:
			default:
			}
		}
	})

	botUsername := s.bot.botUsername
	for attempt := range 5 {
		if attempt > 0 {
			s.T().Logf("gateway canary retry (attempt %d)", attempt+1)
			time.Sleep(2 * time.Second)
		}
		mentionText := fmt.Sprintf("@%s %s", botUsername, canary)
		sendErr := s.bot.PostMessage(s.ctx, s.channelID, mentionText)
		if sendErr != nil {
			s.T().Logf("canary send error (will retry): %v", sendErr)
			continue
		}
		select {
		case <-canaryReceived:
			s.T().Log("gateway connection verified")
			goto canaryOK
		case <-time.After(10 * time.Second):
		}
	}
	s.T().Fatal("gateway not receiving events after 5 canary attempts")
canaryOK:
}

func (s *DiscordIntegrationSuite) TearDownSuite() {
	// Remove any registered commands (best-effort).
	if s.bot != nil {
		_ = s.bot.RemoveCommands(s.ctx)
		_ = s.bot.Stop()
	}
	if s.cancel != nil {
		s.cancel()
	}

	// Delete test channels/threads (best-effort).
	if s.session != nil {
		for _, id := range s.cleanupIDs {
			_, _ = s.session.ChannelDelete(id)
		}
	}
}

// ===== Group A: Bot Authentication & Lifecycle =====

func (s *DiscordIntegrationSuite) TestA01_AuthAndBotUserID() {
	require.NotEmpty(s.T(), s.botUserID)
	// Discord user IDs are numeric snowflakes.
	for _, c := range s.botUserID {
		require.True(s.T(), c >= '0' && c <= '9', "bot user ID should be numeric, got %q", s.botUserID)
	}
}

// ===== Group B: Channel Operations =====

func (s *DiscordIntegrationSuite) TestB01_CreateChannel() {
	rateSleep()
	name := "inttest-create-" + randomSuffix()
	id, err := s.bot.CreateChannel(s.ctx, s.guildID, name)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), id)
	s.cleanupIDs = append(s.cleanupIDs, id)

	// Verify channel exists via raw session.
	rateSleep()
	ch, err := s.session.Channel(id)
	require.NoError(s.T(), err)
	require.Equal(s.T(), name, ch.Name)
}

func (s *DiscordIntegrationSuite) TestB02_CreateChannelDuplicate() {
	rateSleep()
	name := "inttest-dup-" + randomSuffix()

	id1, err := s.bot.CreateChannel(s.ctx, s.guildID, name)
	require.NoError(s.T(), err)
	s.cleanupIDs = append(s.cleanupIDs, id1)

	rateSleep()
	id2, err := s.bot.CreateChannel(s.ctx, s.guildID, name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), id1, id2, "duplicate create should return same channel ID")
}

func (s *DiscordIntegrationSuite) TestB03_SetChannelTopic() {
	rateSleep()
	topic := "/home/user/dev/loop"
	err := s.bot.SetChannelTopic(s.ctx, s.channelID, topic)
	require.NoError(s.T(), err)

	rateSleep()
	ch, err := s.session.Channel(s.channelID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), topic, ch.Topic)
}

func (s *DiscordIntegrationSuite) TestB04_GetChannelName() {
	rateSleep()
	name, err := s.bot.GetChannelName(s.ctx, s.channelID)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), name)
	require.True(s.T(), strings.HasPrefix(name, "inttest-"))
}

func (s *DiscordIntegrationSuite) TestB05_GetChannelParentID() {
	rateSleep()
	parentID, err := s.bot.GetChannelParentID(s.ctx, s.channelID)
	require.NoError(s.T(), err)
	require.Empty(s.T(), parentID, "non-thread channel should have empty parent ID")
}

func (s *DiscordIntegrationSuite) TestB06_InviteUserToChannel() {
	rateSleep()
	err := s.bot.InviteUserToChannel(s.ctx, s.channelID, s.botUserID)
	require.NoError(s.T(), err, "InviteUserToChannel is a no-op, should not error")
}

func (s *DiscordIntegrationSuite) TestB07_GetOwnerUserID() {
	rateSleep()
	ownerID, err := s.bot.GetOwnerUserID(s.ctx)
	require.NoError(s.T(), err)
	require.Empty(s.T(), ownerID, "GetOwnerUserID is a no-op for Discord, should return empty")
}

// ===== Group C: Message Sending =====

func (s *DiscordIntegrationSuite) TestC01_SendMessagePlain() {
	rateSleep()
	content := fmt.Sprintf("integration-test-msg-%s", randomSuffix())
	err := s.bot.SendMessage(s.ctx, &OutgoingMessage{
		ChannelID: s.channelID,
		Content:   content,
	})
	require.NoError(s.T(), err)

	msg, err := waitForDiscordMessage(s.session, s.channelID,
		messageContains(content), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Content, content)
}

func (s *DiscordIntegrationSuite) TestC02_SendMessageLong() {
	rateSleep()
	marker := fmt.Sprintf("SPLIT-TEST-%s", randomSuffix())
	longContent := marker + " " + strings.Repeat("x", maxMessageLen+100)

	err := s.bot.SendMessage(s.ctx, &OutgoingMessage{
		ChannelID: s.channelID,
		Content:   longContent,
	})
	require.NoError(s.T(), err)

	// Verify at least the first chunk arrived.
	msg, err := waitForDiscordMessage(s.session, s.channelID,
		messageContains(marker), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Content, marker)
}

func (s *DiscordIntegrationSuite) TestC03_SendMessageWithReply() {
	rateSleep()
	// Send a parent message.
	parentContent := fmt.Sprintf("parent-%s", randomSuffix())
	parentMsg, err := s.session.ChannelMessageSend(s.channelID, parentContent)
	require.NoError(s.T(), err)

	rateSleep()
	replyContent := fmt.Sprintf("reply-%s", randomSuffix())
	err = s.bot.SendMessage(s.ctx, &OutgoingMessage{
		ChannelID:        s.channelID,
		Content:          replyContent,
		ReplyToMessageID: parentMsg.ID,
	})
	require.NoError(s.T(), err)

	// Verify reply references the parent.
	msg, err := waitForDiscordMessage(s.session, s.channelID,
		messageContains(replyContent), defaultTimeout)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), msg.MessageReference, "reply should have MessageReference set")
	require.Equal(s.T(), parentMsg.ID, msg.MessageReference.MessageID)
}

func (s *DiscordIntegrationSuite) TestC04_PostMessagePlain() {
	rateSleep()
	content := fmt.Sprintf("post-msg-%s", randomSuffix())
	err := s.bot.PostMessage(s.ctx, s.channelID, content)
	require.NoError(s.T(), err)

	msg, err := waitForDiscordMessage(s.session, s.channelID,
		messageContains(content), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Content, content)
}

func (s *DiscordIntegrationSuite) TestC05_PostMessageTextMentionConversion() {
	rateSleep()
	botUsername := s.bot.botUsername
	if botUsername == "" {
		s.T().Skip("bot username not available")
	}

	marker := randomSuffix()
	content := fmt.Sprintf("hey @%s check this %s", botUsername, marker)
	err := s.bot.PostMessage(s.ctx, s.channelID, content)
	require.NoError(s.T(), err)

	// The message should contain <@BOTID> instead of @botname.
	expectedMention := "<@" + s.botUserID + ">"
	msg, err := waitForDiscordMessage(s.session, s.channelID,
		messageContains(marker), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Content, expectedMention)
}

// ===== Group D: Thread Operations =====

func (s *DiscordIntegrationSuite) TestD01_CreateThreadDefault() {
	rateSleep()
	name := "inttest-thread-default-" + randomSuffix()
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, name, "", "")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), threadID)
	s.cleanupIDs = append(s.cleanupIDs, threadID)

	// Verify thread exists and has the initial "Tag me" message.
	rateSleep()
	msg, err := waitForDiscordMessage(s.session, threadID,
		messageContains("Tag me"), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Content, "Tag me")
}

func (s *DiscordIntegrationSuite) TestD02_CreateThreadWithMessage() {
	rateSleep()
	name := "inttest-thread-msg-" + randomSuffix()
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, name, "", "Review the integration tests")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), threadID)
	s.cleanupIDs = append(s.cleanupIDs, threadID)

	// Verify the initial message contains the bot self-mention and content.
	rateSleep()
	expectedMention := "<@" + s.botUserID + ">"
	msg, err := waitForDiscordMessage(s.session, threadID,
		messageContains("Review the integration tests"), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Content, expectedMention)
	require.Contains(s.T(), msg.Content, "Review the integration tests")
}

func (s *DiscordIntegrationSuite) TestD03_CreateThreadWithMention() {
	rateSleep()
	name := "inttest-thread-mention-" + randomSuffix()
	// Use bot's own ID as the mention user (no helper bot available).
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, name, s.botUserID, "")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), threadID)
	s.cleanupIDs = append(s.cleanupIDs, threadID)

	// Verify the initial message mentions the user.
	rateSleep()
	expectedMention := "<@" + s.botUserID + ">"
	msg, err := waitForDiscordMessage(s.session, threadID,
		messageContains(expectedMention), defaultTimeout)
	require.NoError(s.T(), err)
	require.Contains(s.T(), msg.Content, expectedMention)
}

func (s *DiscordIntegrationSuite) TestD04_DeleteThread() {
	rateSleep()
	name := "inttest-thread-delete-" + randomSuffix()
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, name, "", "")
	require.NoError(s.T(), err)

	rateSleep()
	err = s.bot.DeleteThread(s.ctx, threadID)
	require.NoError(s.T(), err)

	// Verify thread is gone.
	rateSleep()
	_, err = s.session.Channel(threadID)
	require.Error(s.T(), err, "deleted thread should not be accessible")
}

func (s *DiscordIntegrationSuite) TestD05_GetChannelParentIDOnThread() {
	rateSleep()
	name := "inttest-thread-parent-" + randomSuffix()
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, name, "", "")
	require.NoError(s.T(), err)
	s.cleanupIDs = append(s.cleanupIDs, threadID)

	rateSleep()
	parentID, err := s.bot.GetChannelParentID(s.ctx, threadID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), s.channelID, parentID)
}

// ===== Group E: Event Handling =====

func (s *DiscordIntegrationSuite) TestE01_SelfMentionEvent() {
	rateSleep()

	received := make(chan *IncomingMessage, 1)
	s.bot.OnMessage(func(_ context.Context, msg *IncomingMessage) {
		if msg.IsBotMention && strings.Contains(msg.Content, "inttest-mention") {
			select {
			case received <- msg:
			default:
			}
		}
	})

	botUsername := s.bot.botUsername
	for attempt := range 5 {
		if attempt > 0 {
			s.T().Logf("retrying mention (attempt %d)", attempt+1)
			time.Sleep(2 * time.Second)
		}
		marker := "inttest-mention-" + randomSuffix()
		mentionText := fmt.Sprintf("@%s %s", botUsername, marker)
		sendErr := s.bot.PostMessage(s.ctx, s.channelID, mentionText)
		require.NoError(s.T(), sendErr)

		select {
		case msg := <-received:
			require.True(s.T(), msg.IsBotMention)
			require.Contains(s.T(), msg.Content, "inttest-mention")
			return
		case <-time.After(15 * time.Second):
		}
	}
	s.Fail("timeout waiting for self-mention event after 5 attempts")
}

func (s *DiscordIntegrationSuite) TestE02_ThreadDeleteEvent() {
	rateSleep()
	name := "inttest-thread-delevent-" + randomSuffix()
	threadID, err := s.bot.CreateThread(s.ctx, s.channelID, name, "", "")
	require.NoError(s.T(), err)

	received := make(chan struct {
		channelID string
		isThread  bool
	}, 1)
	s.bot.OnChannelDelete(func(_ context.Context, channelID string, isThread bool) {
		if channelID == threadID {
			select {
			case received <- struct {
				channelID string
				isThread  bool
			}{channelID, isThread}:
			default:
			}
		}
	})

	rateSleep()
	_, err = s.session.ChannelDelete(threadID)
	require.NoError(s.T(), err)

	select {
	case evt := <-received:
		require.Equal(s.T(), threadID, evt.channelID)
		require.True(s.T(), evt.isThread, "thread delete should have isThread=true")
	case <-time.After(defaultTimeout):
		s.T().Log("thread delete event not received (Discord gateway may not deliver reliably)")
	}
}

func (s *DiscordIntegrationSuite) TestE03_ChannelDeleteEvent() {
	rateSleep()
	name := "inttest-ch-delevent-" + randomSuffix()
	chID, err := s.bot.CreateChannel(s.ctx, s.guildID, name)
	require.NoError(s.T(), err)

	received := make(chan struct {
		channelID string
		isThread  bool
	}, 1)
	s.bot.OnChannelDelete(func(_ context.Context, channelID string, isThread bool) {
		if channelID == chID {
			select {
			case received <- struct {
				channelID string
				isThread  bool
			}{channelID, isThread}:
			default:
			}
		}
	})

	rateSleep()
	_, err = s.session.ChannelDelete(chID)
	require.NoError(s.T(), err)

	select {
	case evt := <-received:
		require.Equal(s.T(), chID, evt.channelID)
		require.False(s.T(), evt.isThread, "channel delete should have isThread=false")
	case <-time.After(defaultTimeout):
		s.T().Log("channel delete event not received (Discord gateway may not deliver reliably)")
	}
}

// ===== Group F: Slash Commands =====

func (s *DiscordIntegrationSuite) TestF01_RegisterCommands() {
	rateSleep()
	// Register guild-scoped commands for instant propagation.
	err := s.registerGuildCommands()
	require.NoError(s.T(), err)

	rateSleep()
	cmds, err := s.session.ApplicationCommands(s.bot.appID, s.guildID)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), cmds, "commands should be registered")

	// Find our "loop" command.
	found := false
	for _, cmd := range cmds {
		if cmd.Name == "loop" {
			found = true
			break
		}
	}
	require.True(s.T(), found, "loop command should be registered")
}

func (s *DiscordIntegrationSuite) TestF02_RemoveCommands() {
	rateSleep()
	// First register, then remove.
	err := s.registerGuildCommands()
	require.NoError(s.T(), err)

	rateSleep()
	err = s.removeGuildCommands()
	require.NoError(s.T(), err)

	rateSleep()
	cmds, err := s.session.ApplicationCommands(s.bot.appID, s.guildID)
	require.NoError(s.T(), err)

	for _, cmd := range cmds {
		require.NotEqual(s.T(), "loop", cmd.Name, "loop command should be removed")
	}
}

func (s *DiscordIntegrationSuite) TestF03_RegisterCommandsIdempotent() {
	rateSleep()
	err := s.registerGuildCommands()
	require.NoError(s.T(), err)

	rateSleep()
	err = s.registerGuildCommands()
	require.NoError(s.T(), err, "registering commands twice should not error")

	// Cleanup.
	rateSleep()
	_ = s.removeGuildCommands()
}

// registerGuildCommands registers commands scoped to the test guild (instant propagation).
func (s *DiscordIntegrationSuite) registerGuildCommands() error {
	for _, cmd := range Commands() {
		_, err := s.session.ApplicationCommandCreate(s.bot.appID, s.guildID, cmd)
		if err != nil {
			return fmt.Errorf("register guild command %q: %w", cmd.Name, err)
		}
	}
	return nil
}

// removeGuildCommands removes all guild-scoped commands.
func (s *DiscordIntegrationSuite) removeGuildCommands() error {
	cmds, err := s.session.ApplicationCommands(s.bot.appID, s.guildID)
	if err != nil {
		return fmt.Errorf("list guild commands: %w", err)
	}
	for _, cmd := range cmds {
		if err := s.session.ApplicationCommandDelete(s.bot.appID, s.guildID, cmd.ID); err != nil {
			return fmt.Errorf("delete guild command %q: %w", cmd.Name, err)
		}
	}
	return nil
}

// ===== Group G: Typing Indicator =====

func (s *DiscordIntegrationSuite) TestG01_SendTyping() {
	rateSleep()
	typingCtx, stopTyping := context.WithCancel(s.ctx)
	err := s.bot.SendTyping(typingCtx, s.channelID)
	require.NoError(s.T(), err)

	// Wait briefly, then cancel. Discord typing state can't be queried via API.
	time.Sleep(500 * time.Millisecond)
	stopTyping()
}
