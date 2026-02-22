package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

const (
	maxMessageLen = 4000
	commandPrefix = "!loop"
	reactionEmoji = "eyes"
)

// SocketModeClient abstracts the socketmode.Client for testability.
type SocketModeClient interface {
	RunContext(ctx context.Context) error
	Ack(req socketmode.Request, payload ...any)
	Events() <-chan socketmode.Event
}

// socketModeClientAdapter wraps socketmode.Client to implement SocketModeClient.
type socketModeClientAdapter struct {
	client *socketmode.Client
}

// NewSocketModeAdapter wraps a socketmode.Client as a SocketModeClient.
func NewSocketModeAdapter(client *socketmode.Client) SocketModeClient {
	return &socketModeClientAdapter{client: client}
}

func (a *socketModeClientAdapter) RunContext(ctx context.Context) error {
	return a.client.RunContext(ctx)
}

func (a *socketModeClientAdapter) Ack(req socketmode.Request, payload ...any) {
	a.client.Ack(req, payload...)
}

func (a *socketModeClientAdapter) Events() <-chan socketmode.Event {
	return a.client.Events
}

// SlackBot implements orchestrator.Bot using the Slack API with Socket Mode.
type SlackBot struct {
	session               SlackSession
	socketClient          SocketModeClient
	logger                *slog.Logger
	botUserID             string
	botUsername           string
	messageHandlers       []MessageHandler
	interactionHandlers   []InteractionHandler
	channelDeleteHandlers []ChannelDeleteHandler
	channelJoinHandlers   []ChannelJoinHandler
	mu                    sync.RWMutex
	cancel                context.CancelFunc
	// lastMessageRef tracks the latest message per channel for emoji reactions.
	lastMessageRef sync.Map // map[string]goslack.ItemRef
}

// NewBot creates a new SlackBot with the given session, socket mode client, and logger.
func NewBot(session SlackSession, socketClient SocketModeClient, logger *slog.Logger) *SlackBot {
	return &SlackBot{
		session:      session,
		socketClient: socketClient,
		logger:       logger,
	}
}

// Start authenticates with Slack and begins listening for Socket Mode events.
func (b *SlackBot) Start(ctx context.Context) error {
	resp, err := b.session.AuthTest()
	if err != nil {
		return fmt.Errorf("slack auth test: %w", err)
	}
	b.mu.Lock()
	b.botUserID = resp.UserID
	b.botUsername = resp.User
	b.mu.Unlock()

	if err := b.session.SetUserPresence("auto"); err != nil {
		b.logger.WarnContext(ctx, "slack set presence failed", "error", err)
	}

	b.logger.InfoContext(ctx, "slack bot started", "bot_user_id", resp.UserID, "bot_username", resp.User)

	smCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancel = cancel
	b.mu.Unlock()

	go b.eventLoop(smCtx)
	go func() {
		if err := b.socketClient.RunContext(smCtx); err != nil {
			b.logger.Error("socket mode error", "error", err)
		}
	}()

	return nil
}

// Stop cancels the Socket Mode connection.
func (b *SlackBot) Stop() error {
	b.mu.RLock()
	cancel := b.cancel
	b.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// SendMessage sends one or more messages to Slack, splitting at 4000 chars.
func (b *SlackBot) SendMessage(ctx context.Context, msg *OutgoingMessage) error {
	channelID, threadTS := parseCompositeID(msg.ChannelID)
	chunks := splitMessage(msg.Content, maxMessageLen)

	for _, chunk := range chunks {
		opts := []goslack.MsgOption{goslack.MsgOptionText(chunk, false)}
		if threadTS != "" {
			opts = append(opts, goslack.MsgOptionTS(threadTS))
		}
		if _, _, err := b.session.PostMessage(channelID, opts...); err != nil {
			return fmt.Errorf("slack send message: %w", err)
		}
	}
	return nil
}

// SendTyping adds an "eyes" emoji reaction to the last received message in the
// channel and removes it when the context is cancelled.
func (b *SlackBot) SendTyping(ctx context.Context, channelID string) error {
	channelID, _ = parseCompositeID(channelID)

	val, ok := b.lastMessageRef.Load(channelID)
	if !ok {
		return nil // no tracked message to react to
	}
	ref := val.(goslack.ItemRef)

	if err := b.session.AddReaction(reactionEmoji, ref); err != nil {
		// If already_reacted, the reaction is present — still set up cleanup.
		if !strings.Contains(err.Error(), "already_reacted") {
			b.logger.Error("slack add reaction failed", "error", err, "channel_id", channelID)
			return nil // non-fatal
		}
	}

	go func() {
		<-ctx.Done()
		if err := b.session.RemoveReaction(reactionEmoji, ref); err != nil {
			b.logger.Error("slack remove reaction failed", "error", err, "channel_id", channelID)
		}
	}()

	return nil
}

// RegisterCommands is a no-op for Slack. Commands are registered via the Slack app manifest.
func (b *SlackBot) RegisterCommands(_ context.Context) error {
	b.logger.Info("slack commands are registered via the app manifest")
	return nil
}

// RemoveCommands is a no-op for Slack.
func (b *SlackBot) RemoveCommands(_ context.Context) error {
	return nil
}

// OnMessage registers a handler to be called for incoming messages.
func (b *SlackBot) OnMessage(handler MessageHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messageHandlers = append(b.messageHandlers, handler)
}

// OnInteraction registers a handler to be called for slash command interactions.
func (b *SlackBot) OnInteraction(handler InteractionHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.interactionHandlers = append(b.interactionHandlers, handler)
}

// OnChannelDelete registers a handler to be called when a channel is deleted.
func (b *SlackBot) OnChannelDelete(handler ChannelDeleteHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.channelDeleteHandlers = append(b.channelDeleteHandlers, handler)
}

// OnChannelJoin registers a handler to be called when the bot joins a channel.
func (b *SlackBot) OnChannelJoin(handler ChannelJoinHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.channelJoinHandlers = append(b.channelJoinHandlers, handler)
}

// BotUserID returns the bot's Slack user ID.
func (b *SlackBot) BotUserID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.botUserID
}

// CreateChannel creates a new public Slack channel. If the channel name is
// already taken, it looks up the existing channel and returns its ID.
func (b *SlackBot) CreateChannel(ctx context.Context, _, name string) (string, error) {
	ch, err := b.session.CreateConversation(goslack.CreateConversationParams{
		ChannelName: name,
	})
	if err != nil {
		var slackErr goslack.SlackErrorResponse
		if errors.As(err, &slackErr) && slackErr.Err == "name_taken" {
			id, lookupErr := b.findChannelByName(name)
			if lookupErr != nil {
				return "", fmt.Errorf("slack find existing channel %q: %w", name, lookupErr)
			}
			b.logger.InfoContext(ctx, "found existing slack channel", "channel_id", id, "name", name)
			return id, nil
		}
		return "", fmt.Errorf("slack create channel: %w", err)
	}
	b.logger.InfoContext(ctx, "created slack channel", "channel_id", ch.ID, "name", name)
	return ch.ID, nil
}

// findChannelByName iterates through conversations to find a channel by name.
func (b *SlackBot) findChannelByName(name string) (string, error) {
	cursor := ""
	for {
		channels, nextCursor, err := b.session.GetConversations(&goslack.GetConversationsParameters{
			Cursor:          cursor,
			ExcludeArchived: true,
			Limit:           200,
			Types:           []string{"public_channel", "private_channel"},
		})
		if err != nil {
			return "", fmt.Errorf("get conversations: %w", err)
		}
		for _, ch := range channels {
			if ch.Name == name {
				return ch.ID, nil
			}
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return "", fmt.Errorf("channel %q not found", name)
}

// InviteUserToChannel invites a user to a Slack channel.
func (b *SlackBot) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	_, err := b.session.InviteUsersToConversation(channelID, userID)
	if err != nil {
		return fmt.Errorf("slack invite user to channel: %w", err)
	}
	b.logger.InfoContext(ctx, "invited user to slack channel", "channel_id", channelID, "user_id", userID)
	return nil
}

// GetOwnerUserID returns the Slack workspace owner's user ID.
func (b *SlackBot) GetOwnerUserID(ctx context.Context) (string, error) {
	users, err := b.session.GetUsers()
	if err != nil {
		return "", fmt.Errorf("slack get users: %w", err)
	}
	for _, u := range users {
		if u.IsOwner && !u.IsBot {
			b.logger.InfoContext(ctx, "found workspace owner", "user_id", u.ID, "name", u.Name)
			return u.ID, nil
		}
	}
	return "", fmt.Errorf("no workspace owner found")
}

// SetChannelTopic sets the topic/description of a Slack channel.
func (b *SlackBot) SetChannelTopic(ctx context.Context, channelID, topic string) error {
	if _, err := b.session.SetTopicOfConversation(channelID, topic); err != nil {
		return fmt.Errorf("slack set channel topic: %w", err)
	}
	b.logger.InfoContext(ctx, "set slack channel topic", "channel_id", channelID, "topic", topic)
	return nil
}

// CreateThread creates a new thread by posting an initial message in the channel.
// Returns a composite ID "channelID:messageTS" that represents the thread.
func (b *SlackBot) CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error) {
	botID := b.BotUserID()
	var initialMsg string
	switch {
	case message != "":
		// Strip any existing bot mentions from the message to avoid duplicates.
		clean := strings.ReplaceAll(message, "<@"+botID+">", "")
		b.mu.RLock()
		username := b.botUsername
		b.mu.RUnlock()
		if username != "" {
			clean = replaceTextMention(clean, username, "")
		}
		clean = strings.TrimSpace(clean)
		initialMsg = fmt.Sprintf("<@%s> %s", botID, clean)
		if mentionUserID != "" {
			initialMsg += fmt.Sprintf(" <@%s>", mentionUserID)
		}
	case mentionUserID != "":
		initialMsg = fmt.Sprintf("*%s*\nHey <@%s>, tag me to get started!", name, mentionUserID)
	default:
		initialMsg = fmt.Sprintf("*%s*\nTag me to get started!", name)
	}

	_, ts, err := b.session.PostMessage(channelID, goslack.MsgOptionText(initialMsg, false))
	if err != nil {
		return "", fmt.Errorf("slack create thread: %w", err)
	}

	threadID := compositeID(channelID, ts)
	b.logger.InfoContext(ctx, "created slack thread", "thread_id", threadID, "name", name, "parent_id", channelID)
	return threadID, nil
}

// CreateSimpleThread creates a new thread with a plain initial message (no bot mention).
// Returns a composite "channelID:messageTS" thread ID.
func (b *SlackBot) CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error) {
	chID, _ := parseCompositeID(channelID)
	msg := initialMessage
	if msg == "" {
		msg = name
	}
	_, ts, err := b.session.PostMessage(chID, goslack.MsgOptionText(msg, false))
	if err != nil {
		return "", fmt.Errorf("slack create simple thread: %w", err)
	}
	threadID := compositeID(chID, ts)
	b.logger.InfoContext(ctx, "created simple slack thread", "thread_id", threadID, "name", name, "parent_id", channelID)
	return threadID, nil
}

// PostMessage sends a simple message to the given channel or thread.
// Text mentions of the bot (e.g. @BotName) are converted to proper Slack mentions.
func (b *SlackBot) PostMessage(ctx context.Context, channelID, content string) error {
	b.mu.RLock()
	username := b.botUsername
	userID := b.botUserID
	b.mu.RUnlock()

	if username != "" {
		slackMention := "<@" + userID + ">"
		content = replaceTextMention(content, username, slackMention)
	}

	chID, threadTS := parseCompositeID(channelID)
	opts := []goslack.MsgOption{goslack.MsgOptionText(content, false)}
	if threadTS != "" {
		opts = append(opts, goslack.MsgOptionTS(threadTS))
	}

	if _, _, err := b.session.PostMessage(chID, opts...); err != nil {
		return fmt.Errorf("slack post message: %w", err)
	}
	return nil
}

// DeleteThread deletes a thread by deleting its parent message.
func (b *SlackBot) DeleteThread(ctx context.Context, threadID string) error {
	channelID, ts := parseCompositeID(threadID)
	if ts == "" {
		return fmt.Errorf("invalid thread ID: %s (expected channelID:timestamp)", threadID)
	}
	if _, _, err := b.session.DeleteMessage(channelID, ts); err != nil {
		return fmt.Errorf("slack delete thread: %w", err)
	}
	b.logger.InfoContext(ctx, "deleted slack thread", "thread_id", threadID)
	return nil
}

// GetChannelName returns the name of a Slack channel by its ID.
func (b *SlackBot) GetChannelName(_ context.Context, channelID string) (string, error) {
	channelID, _ = parseCompositeID(channelID)
	ch, err := b.session.GetConversationInfo(&goslack.GetConversationInfoInput{
		ChannelID: channelID,
	})
	if err != nil {
		return "", fmt.Errorf("slack get channel name: %w", err)
	}
	return ch.Name, nil
}

// GetChannelParentID returns the parent channel ID for a thread (composite ID),
// or empty string if not a thread.
func (b *SlackBot) GetChannelParentID(_ context.Context, channelID string) (string, error) {
	parentID, ts := parseCompositeID(channelID)
	if ts == "" {
		return "", nil
	}
	return parentID, nil
}

// GetMemberRoles returns nil for Slack — Slack has no role equivalent.
// Slack-based access control relies on allow_users only.
func (b *SlackBot) GetMemberRoles(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

// eventLoop processes events from the Socket Mode client.
func (b *SlackBot) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-b.socketClient.Events():
			if !ok {
				return
			}
			b.handleEvent(evt)
		}
	}
}

func (b *SlackBot) handleEvent(evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		b.handleEventsAPI(evt)
	case socketmode.EventTypeSlashCommand:
		b.handleSlashCommand(evt)
	}
}

func (b *SlackBot) handleEventsAPI(evt socketmode.Event) {
	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	if evt.Request != nil {
		b.socketClient.Ack(*evt.Request)
	}

	switch ev := eventsAPIEvent.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		b.handleMessage(ev)
	case *slackevents.MemberJoinedChannelEvent:
		b.handleMemberJoinedChannel(ev)
	case *slackevents.ChannelDeletedEvent:
		b.notifyChannelDelete(ev.Channel)
	case *slackevents.GroupDeletedEvent:
		b.notifyChannelDelete(ev.Channel)
	}
}

func (b *SlackBot) handleMessage(ev *slackevents.MessageEvent) {
	// Skip subtypes (message_changed, message_deleted, etc.)
	if ev.SubType != "" {
		return
	}

	botID := b.BotUserID()

	// Self-mention loop prevention.
	if ev.User == botID && !strings.Contains(ev.Text, "<@"+botID+">") {
		return
	}

	isSelfMention := ev.User == botID
	isMention := strings.Contains(ev.Text, "<@"+botID+">")
	hasPrefix := hasCommandPrefix(ev.Text)
	isDM := ev.ChannelType == "im"
	isReply := b.isReplyToBot(ev)

	if !isMention && !hasPrefix && !isReply && !isDM {
		return
	}

	content := ev.Text
	if isMention {
		content = stripMention(content, botID)
	} else if hasPrefix {
		content = stripPrefix(content)
	}

	channelID := ev.Channel
	if ev.ThreadTimeStamp != "" && ev.ThreadTimeStamp != ev.TimeStamp {
		channelID = compositeID(ev.Channel, ev.ThreadTimeStamp)
	} else if isSelfMention && isMention {
		// Self-mention as a top-level message (e.g. from CreateThread):
		// use the message's own timestamp as the thread TS so the
		// response is posted as a reply, creating a proper Slack thread.
		channelID = compositeID(ev.Channel, ev.TimeStamp)
	}

	msg := &IncomingMessage{
		ChannelID:    channelID,
		GuildID:      "",
		AuthorID:     ev.User,
		AuthorName:   ev.User,
		Content:      content,
		MessageID:    ev.TimeStamp,
		IsBotMention: isMention,
		IsReplyToBot: isReply,
		HasPrefix:    hasPrefix,
		IsDM:         isDM,
		Timestamp:    slackTSToTime(ev.TimeStamp),
	}

	// Track message for reaction-based typing indicator.
	b.lastMessageRef.Store(ev.Channel, goslack.NewRefToMessage(ev.Channel, ev.TimeStamp))

	b.dispatchMessage(msg)
}

func (b *SlackBot) isReplyToBot(ev *slackevents.MessageEvent) bool {
	if ev.ThreadTimeStamp == "" || ev.ThreadTimeStamp == ev.TimeStamp {
		return false
	}
	// Check if the thread parent message was from the bot.
	msgs, _, _, err := b.session.GetConversationReplies(&goslack.GetConversationRepliesParameters{
		ChannelID: ev.Channel,
		Timestamp: ev.ThreadTimeStamp,
		Limit:     1,
	})
	if err != nil || len(msgs) == 0 {
		return false
	}
	return msgs[0].User == b.BotUserID() || msgs[0].BotID != ""
}

func (b *SlackBot) notifyChannelDelete(channelID string) {
	b.mu.RLock()
	handlers := make([]ChannelDeleteHandler, len(b.channelDeleteHandlers))
	copy(handlers, b.channelDeleteHandlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		go h(context.Background(), channelID, false)
	}
}

func (b *SlackBot) handleMemberJoinedChannel(ev *slackevents.MemberJoinedChannelEvent) {
	if ev.User != b.BotUserID() {
		return
	}

	b.mu.RLock()
	handlers := make([]ChannelJoinHandler, len(b.channelJoinHandlers))
	copy(handlers, b.channelJoinHandlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		go h(context.Background(), ev.Channel)
	}
}

func (b *SlackBot) handleSlashCommand(evt socketmode.Event) {
	cmd, ok := evt.Data.(goslack.SlashCommand)
	if !ok {
		return
	}
	if evt.Request != nil {
		b.socketClient.Ack(*evt.Request)
	}

	inter, errText := parseSlashCommand(cmd.ChannelID, cmd.TeamID, cmd.Text)
	if inter == nil {
		// Send error/help text back via PostMessage.
		_, _, _ = b.session.PostMessage(cmd.ChannelID, goslack.MsgOptionText(errText, false))
		return
	}

	inter.AuthorID = cmd.UserID

	b.dispatchInteraction(inter)
}

func (b *SlackBot) dispatchMessage(msg *IncomingMessage) {
	b.mu.RLock()
	handlers := make([]MessageHandler, len(b.messageHandlers))
	copy(handlers, b.messageHandlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		go h(context.Background(), msg)
	}
}

func (b *SlackBot) dispatchInteraction(inter any) {
	b.mu.RLock()
	handlers := make([]InteractionHandler, len(b.interactionHandlers))
	copy(handlers, b.interactionHandlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		go h(context.Background(), inter)
	}
}

// compositeID creates a composite channel ID for threads: "channelID:threadTS".
func compositeID(channelID, threadTS string) string {
	return channelID + ":" + threadTS
}

// parseCompositeID splits a composite channel ID into channel and thread_ts.
// If the ID is not composite, threadTS is empty.
func parseCompositeID(id string) (channelID, threadTS string) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return id, ""
}

// replaceTextMention replaces case-insensitive @username with a Slack mention.
func replaceTextMention(content, username, mention string) string {
	target := "@" + username
	idx := strings.Index(strings.ToLower(content), strings.ToLower(target))
	if idx == -1 {
		return content
	}
	return content[:idx] + mention + content[idx+len(target):]
}

func stripMention(content, botUserID string) string {
	mention := "<@" + botUserID + ">"
	content = strings.ReplaceAll(content, mention, "")
	return strings.TrimSpace(content)
}

func hasCommandPrefix(content string) bool {
	return strings.HasPrefix(strings.ToLower(content), commandPrefix)
}

func stripPrefix(content string) string {
	if len(content) <= len(commandPrefix) {
		return ""
	}
	return strings.TrimSpace(content[len(commandPrefix):])
}

// splitMessage splits a message into chunks of at most maxLen characters,
// breaking on newlines when possible.
func splitMessage(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}

	var chunks []string
	for len(content) > 0 {
		if len(content) <= maxLen {
			chunks = append(chunks, content)
			break
		}

		cutPoint := findCutPoint(content, maxLen)
		chunks = append(chunks, content[:cutPoint])
		content = content[cutPoint:]
	}
	return chunks
}

func findCutPoint(content string, maxLen int) int {
	lastNewline := strings.LastIndex(content[:maxLen], "\n")
	if lastNewline > 0 {
		return lastNewline + 1
	}
	lastSpace := strings.LastIndex(content[:maxLen], " ")
	if lastSpace > 0 {
		return lastSpace + 1
	}
	return maxLen
}

// slackTSToTime converts a Slack timestamp (e.g. "1234567890.123456") to time.Time.
func slackTSToTime(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	parts := strings.SplitN(ts, ".", 2)
	if parts[0] == "" {
		return time.Time{}
	}
	var sec int64
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return time.Time{}
		}
		sec = sec*10 + int64(c-'0')
	}
	return time.Unix(sec, 0)
}
