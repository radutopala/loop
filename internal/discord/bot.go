package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/radutopala/loop/internal/orchestrator"
)

const (
	maxMessageLen     = 2000
	typingIntervalSec = 8
	commandPrefix     = "!loop"
)

// DiscordSession abstracts the discordgo.Session methods used by the bot,
// enabling test mocking.
type DiscordSession interface {
	Open() error
	Close() error
	AddHandler(handler any) func()
	User(userID string, options ...discordgo.RequestOption) (*discordgo.User, error)
	Channel(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	ChannelMessageSend(channelID string, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageSendReply(channelID string, content string, reference *discordgo.MessageReference, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelTyping(channelID string, options ...discordgo.RequestOption) error
	ApplicationCommandCreate(appID string, guildID string, cmd *discordgo.ApplicationCommand, options ...discordgo.RequestOption) (*discordgo.ApplicationCommand, error)
	ApplicationCommands(appID string, guildID string, options ...discordgo.RequestOption) ([]*discordgo.ApplicationCommand, error)
	ApplicationCommandDelete(appID string, guildID string, cmdID string, options ...discordgo.RequestOption) error
	InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error
	InteractionResponseEdit(interaction *discordgo.Interaction, newresp *discordgo.WebhookEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
	FollowupMessageCreate(interaction *discordgo.Interaction, wait bool, data *discordgo.WebhookParams, options ...discordgo.RequestOption) (*discordgo.Message, error)
	GuildChannelCreate(guildID string, name string, ctype discordgo.ChannelType, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	ThreadStart(channelID string, name string, typ discordgo.ChannelType, archiveDuration int, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	ThreadJoin(id string, options ...discordgo.RequestOption) error
	ChannelDelete(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	GuildChannels(guildID string, options ...discordgo.RequestOption) ([]*discordgo.Channel, error)
	ChannelEdit(channelID string, data *discordgo.ChannelEdit, options ...discordgo.RequestOption) (*discordgo.Channel, error)
}

// Bot defines the interface for a Discord bot.
type Bot interface {
	Start(ctx context.Context) error
	Stop() error
	SendMessage(ctx context.Context, msg *OutgoingMessage) error
	SendTyping(ctx context.Context, channelID string) error
	RegisterCommands(ctx context.Context) error
	RemoveCommands(ctx context.Context) error
	OnMessage(handler MessageHandler)
	OnInteraction(handler InteractionHandler)
	BotUserID() string
}

// DiscordBot implements Bot using discordgo.
type DiscordBot struct {
	session               DiscordSession
	appID                 string
	logger                *slog.Logger
	botUserID             string
	botUsername           string
	messageHandlers       []MessageHandler
	interactionHandlers   []InteractionHandler
	channelDeleteHandlers []ChannelDeleteHandler
	channelJoinHandlers   []ChannelJoinHandler
	mu                    sync.RWMutex
	removeHandlers        []func()
	typingInterval        time.Duration
	pendingInteractions   map[string]*discordgo.Interaction
}

// NewBot creates a new DiscordBot with the given session, app ID, and logger.
func NewBot(session DiscordSession, appID string, logger *slog.Logger) *DiscordBot {
	return &DiscordBot{
		session:             session,
		appID:               appID,
		logger:              logger,
		typingInterval:      typingIntervalSec * time.Second,
		pendingInteractions: make(map[string]*discordgo.Interaction),
	}
}

// Start opens the Discord session and resolves the bot user ID.
func (b *DiscordBot) Start(ctx context.Context) error {
	rh1 := b.session.AddHandler(b.handleMessage)
	rh2 := b.session.AddHandler(b.handleInteraction)
	rh3 := b.session.AddHandler(b.handleThreadCreate)
	rh4 := b.session.AddHandler(b.handleThreadDelete)
	rh5 := b.session.AddHandler(b.handleChannelDelete)
	b.mu.Lock()
	b.removeHandlers = append(b.removeHandlers, rh1, rh2, rh3, rh4, rh5)
	b.mu.Unlock()

	if err := b.session.Open(); err != nil {
		return fmt.Errorf("discord session open: %w", err)
	}

	user, err := b.session.User("@me")
	if err != nil {
		return fmt.Errorf("discord get bot user: %w", err)
	}
	b.mu.Lock()
	b.botUserID = user.ID
	b.botUsername = user.Username
	b.mu.Unlock()

	b.logger.InfoContext(ctx, "discord bot started", "bot_user_id", user.ID, "bot_username", user.Username)
	return nil
}

// Stop closes the Discord session and removes event handlers.
func (b *DiscordBot) Stop() error {
	b.mu.RLock()
	handlers := b.removeHandlers
	b.mu.RUnlock()

	for _, remove := range handlers {
		remove()
	}

	b.mu.Lock()
	b.removeHandlers = nil
	b.mu.Unlock()

	return b.session.Close()
}

// SendMessage sends one or more messages to Discord, splitting at the 2000 char limit.
// If there is a pending slash command interaction for the channel, it resolves the
// deferred response instead of sending a regular message.
func (b *DiscordBot) SendMessage(ctx context.Context, msg *OutgoingMessage) error {
	b.mu.Lock()
	interaction, hasPending := b.pendingInteractions[msg.ChannelID]
	if hasPending {
		delete(b.pendingInteractions, msg.ChannelID)
	}
	b.mu.Unlock()

	chunks := splitMessage(msg.Content, maxMessageLen)

	if hasPending {
		return b.sendInteractionChunks(interaction, chunks)
	}

	return b.sendRegularChunks(msg, chunks)
}

func (b *DiscordBot) sendInteractionChunks(interaction *discordgo.Interaction, chunks []string) error {
	for i, chunk := range chunks {
		if i == 0 {
			_, err := b.session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{Content: &chunk})
			if err != nil {
				return fmt.Errorf("discord interaction edit: %w", err)
			}
		} else {
			_, err := b.session.FollowupMessageCreate(interaction, true, &discordgo.WebhookParams{Content: chunk})
			if err != nil {
				return fmt.Errorf("discord followup create: %w", err)
			}
		}
	}
	return nil
}

func (b *DiscordBot) sendRegularChunks(msg *OutgoingMessage, chunks []string) error {
	for i, chunk := range chunks {
		if msg.ReplyToMessageID != "" && i == 0 {
			ref := &discordgo.MessageReference{MessageID: msg.ReplyToMessageID}
			_, err := b.session.ChannelMessageSendReply(msg.ChannelID, chunk, ref)
			if err != nil {
				return fmt.Errorf("discord send reply: %w", err)
			}
		} else {
			_, err := b.session.ChannelMessageSend(msg.ChannelID, chunk)
			if err != nil {
				return fmt.Errorf("discord send message: %w", err)
			}
		}
	}
	return nil
}

// SendTyping sends a typing indicator that refreshes every 8 seconds
// until the context is cancelled.
func (b *DiscordBot) SendTyping(ctx context.Context, channelID string) error {
	if err := b.session.ChannelTyping(channelID); err != nil {
		return fmt.Errorf("discord typing: %w", err)
	}

	go func() {
		ticker := time.NewTicker(b.typingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := b.session.ChannelTyping(channelID); err != nil {
					b.logger.Error("discord typing refresh failed", "error", err, "channel_id", channelID)
				}
			}
		}
	}()
	return nil
}

// RegisterCommands registers the bot's slash commands with Discord.
func (b *DiscordBot) RegisterCommands(ctx context.Context) error {
	for _, cmd := range Commands() {
		created, err := b.session.ApplicationCommandCreate(b.appID, "", cmd)
		if err != nil {
			return fmt.Errorf("discord register command %q: %w", cmd.Name, err)
		}
		b.logger.InfoContext(ctx, "registered command", "name", created.Name, "id", created.ID)
	}
	return nil
}

// RemoveCommands removes all registered slash commands from Discord.
func (b *DiscordBot) RemoveCommands(ctx context.Context) error {
	cmds, err := b.session.ApplicationCommands(b.appID, "")
	if err != nil {
		return fmt.Errorf("discord list commands: %w", err)
	}
	for _, cmd := range cmds {
		if err := b.session.ApplicationCommandDelete(b.appID, "", cmd.ID); err != nil {
			return fmt.Errorf("discord delete command %q: %w", cmd.Name, err)
		}
		b.logger.InfoContext(ctx, "removed command", "name", cmd.Name, "id", cmd.ID)
	}
	return nil
}

// OnMessage registers a handler to be called for incoming messages.
func (b *DiscordBot) OnMessage(handler MessageHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messageHandlers = append(b.messageHandlers, handler)
}

// OnInteraction registers a handler to be called for slash command interactions.
func (b *DiscordBot) OnInteraction(handler InteractionHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.interactionHandlers = append(b.interactionHandlers, handler)
}

// OnChannelDelete registers a handler to be called when a channel or thread is deleted.
func (b *DiscordBot) OnChannelDelete(handler ChannelDeleteHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.channelDeleteHandlers = append(b.channelDeleteHandlers, handler)
}

// OnChannelJoin registers a handler for channel join events.
// Discord has no equivalent "bot added to channel" event (bot sees all guild channels),
// so handlers are stored but never dispatched.
func (b *DiscordBot) OnChannelJoin(handler ChannelJoinHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.channelJoinHandlers = append(b.channelJoinHandlers, handler)
}

// CreateChannel creates a new text channel in the given guild. If a text
// channel with the same name already exists, it returns the existing channel's ID.
func (b *DiscordBot) CreateChannel(ctx context.Context, guildID, name string) (string, error) {
	// Check if a channel with this name already exists.
	channels, err := b.session.GuildChannels(guildID)
	if err != nil {
		return "", fmt.Errorf("discord list channels: %w", err)
	}
	for _, ch := range channels {
		if ch.Name == name && ch.Type == discordgo.ChannelTypeGuildText {
			b.logger.InfoContext(ctx, "found existing discord channel", "channel_id", ch.ID, "name", name, "guild_id", guildID)
			return ch.ID, nil
		}
	}

	ch, err := b.session.GuildChannelCreate(guildID, name, discordgo.ChannelTypeGuildText)
	if err != nil {
		return "", fmt.Errorf("discord create channel: %w", err)
	}
	b.logger.InfoContext(ctx, "created discord channel", "channel_id", ch.ID, "name", name, "guild_id", guildID)
	return ch.ID, nil
}

// InviteUserToChannel is a no-op for Discord since channels are visible to all guild members.
func (b *DiscordBot) InviteUserToChannel(_ context.Context, _, _ string) error {
	return nil
}

// GetOwnerUserID is a no-op for Discord — channels are visible to all guild members.
func (b *DiscordBot) GetOwnerUserID(_ context.Context) (string, error) {
	return "", nil
}

// SetChannelTopic sets the topic/description of a Discord channel.
func (b *DiscordBot) SetChannelTopic(ctx context.Context, channelID, topic string) error {
	if _, err := b.session.ChannelEdit(channelID, &discordgo.ChannelEdit{Topic: topic}); err != nil {
		return fmt.Errorf("discord set channel topic: %w", err)
	}
	b.logger.InfoContext(ctx, "set discord channel topic", "channel_id", channelID, "topic", topic)
	return nil
}

// GetChannelName returns the name of a Discord channel by its ID.
func (b *DiscordBot) GetChannelName(_ context.Context, channelID string) (string, error) {
	ch, err := b.session.Channel(channelID)
	if err != nil {
		return "", fmt.Errorf("discord get channel name: %w", err)
	}
	return ch.Name, nil
}

// GetChannelParentID returns the parent channel ID for a thread, or empty string if not a thread.
func (b *DiscordBot) GetChannelParentID(ctx context.Context, channelID string) (string, error) {
	ch, err := b.session.Channel(channelID)
	if err != nil {
		return "", fmt.Errorf("discord get channel: %w", err)
	}
	if !ch.IsThread() {
		return "", nil
	}
	return ch.ParentID, nil
}

// CreateThread creates a new public thread in the given channel and sends
// an initial message mentioning the bot so users know it's active.
// If mentionUserID is non-empty, the user is also mentioned in the greeting.
func (b *DiscordBot) CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error) {
	ch, err := b.session.ThreadStart(channelID, name, discordgo.ChannelTypeGuildPublicThread, 10080)
	if err != nil {
		return "", fmt.Errorf("discord create thread: %w", err)
	}
	var initialMsg string
	switch {
	case message != "":
		botID := b.BotUserID()
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
		initialMsg = fmt.Sprintf("Hey <@%s>, tag me to get started!", mentionUserID)
	default:
		initialMsg = "Tag me to get started!"
	}
	if _, err := b.session.ChannelMessageSend(ch.ID, initialMsg); err != nil {
		b.logger.WarnContext(ctx, "sending initial thread message", "error", err, "thread_id", ch.ID)
	}
	b.logger.InfoContext(ctx, "created discord thread", "thread_id", ch.ID, "name", name, "parent_id", channelID)
	return ch.ID, nil
}

// CreateSimpleThread creates a new thread with a plain initial message (no bot mention).
// Returns the thread channel ID.
func (b *DiscordBot) CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error) {
	ch, err := b.session.ThreadStart(channelID, name, discordgo.ChannelTypeGuildPublicThread, 10080)
	if err != nil {
		return "", fmt.Errorf("discord create simple thread: %w", err)
	}
	if initialMessage != "" {
		if _, err := b.session.ChannelMessageSend(ch.ID, initialMessage); err != nil {
			b.logger.WarnContext(ctx, "sending thread initial message", "error", err, "thread_id", ch.ID)
		}
	}
	b.logger.InfoContext(ctx, "created simple discord thread", "thread_id", ch.ID, "name", name, "parent_id", channelID)
	return ch.ID, nil
}

// PostMessage sends a simple message to the given channel or thread.
// Text mentions of the bot (e.g. @LoopBot) are converted to proper Discord
// mentions so the message triggers bot processing in the target channel.
func (b *DiscordBot) PostMessage(ctx context.Context, channelID, content string) error {
	b.mu.RLock()
	username := b.botUsername
	userID := b.botUserID
	b.mu.RUnlock()

	if username != "" {
		discordMention := "<@" + userID + ">"
		content = replaceTextMention(content, username, discordMention)
	}

	_, err := b.session.ChannelMessageSend(channelID, content)
	if err != nil {
		return fmt.Errorf("discord post message: %w", err)
	}
	return nil
}

// replaceTextMention replaces case-insensitive @username with a Discord mention.
func replaceTextMention(content, username, mention string) string {
	target := "@" + username
	idx := strings.Index(strings.ToLower(content), strings.ToLower(target))
	if idx == -1 {
		return content
	}
	return content[:idx] + mention + content[idx+len(target):]
}

// DeleteThread deletes a Discord thread by its ID.
func (b *DiscordBot) DeleteThread(ctx context.Context, threadID string) error {
	if _, err := b.session.ChannelDelete(threadID); err != nil {
		return fmt.Errorf("discord delete thread: %w", err)
	}
	b.logger.InfoContext(ctx, "deleted discord thread", "thread_id", threadID)
	return nil
}

func (b *DiscordBot) handleThreadCreate(_ *discordgo.Session, c *discordgo.ThreadCreate) {
	if !c.IsThread() {
		return
	}
	if err := b.session.ThreadJoin(c.ID); err != nil {
		b.logger.Error("joining thread", "error", err, "thread_id", c.ID)
		return
	}
	b.logger.Info("joined thread", "thread_id", c.ID, "parent_id", c.ParentID)
}

func (b *DiscordBot) handleThreadDelete(_ *discordgo.Session, c *discordgo.ThreadDelete) {
	b.logger.Info("thread deleted", "thread_id", c.ID, "parent_id", c.ParentID)
	b.notifyChannelDelete(c.ID, true)
}

func (b *DiscordBot) handleChannelDelete(_ *discordgo.Session, c *discordgo.ChannelDelete) {
	if c.IsThread() {
		return
	}
	b.logger.Info("channel deleted", "channel_id", c.ID)
	b.notifyChannelDelete(c.ID, false)
}

func (b *DiscordBot) notifyChannelDelete(channelID string, isThread bool) {
	b.mu.RLock()
	handlers := make([]ChannelDeleteHandler, len(b.channelDeleteHandlers))
	copy(handlers, b.channelDeleteHandlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		go h(context.Background(), channelID, isThread)
	}
}

// BotUserID returns the bot's Discord user ID.
func (b *DiscordBot) BotUserID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.botUserID
}

func (b *DiscordBot) handleMessage(_ *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil {
		return
	}

	botID := b.BotUserID()
	// For bot-authored messages, only allow processing if the content
	// explicitly contains <@BOT_ID>. We cannot use isBotMention here because
	// Discord auto-populates m.Mentions when replying to a message, which
	// would cause infinite recursion (response replies to bot msg → Discord
	// adds bot to Mentions → triggers another runner → repeat).
	if m.Author.ID == botID && !strings.Contains(m.Content, "<@"+botID+">") {
		return
	}

	msg := parseIncomingMessage(m, botID)
	if msg == nil {
		return
	}

	b.mu.RLock()
	handlers := make([]MessageHandler, len(b.messageHandlers))
	copy(handlers, b.messageHandlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		go h(context.Background(), msg)
	}
}

func (b *DiscordBot) handleInteraction(_ *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	if err := b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		b.logger.Error("acknowledging interaction", "error", err)
	} else {
		b.mu.Lock()
		b.pendingInteractions[i.ChannelID] = i.Interaction
		b.mu.Unlock()
	}

	data := i.ApplicationCommandData()

	// Subcommands: Options[0] is the subcommand, its Options hold the params.
	// Subcommand groups: Options[0] is the group, Options[0].Options[0] is the subcommand.
	var commandName string
	var opts []*discordgo.ApplicationCommandInteractionDataOption
	switch {
	case len(data.Options) > 0 && data.Options[0].Type == discordgo.ApplicationCommandOptionSubCommandGroup:
		group := data.Options[0]
		if len(group.Options) > 0 {
			commandName = group.Name + "-" + group.Options[0].Name
			opts = group.Options[0].Options
		} else {
			commandName = group.Name
		}
	case len(data.Options) > 0 && data.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand:
		commandName = data.Options[0].Name
		opts = data.Options[0].Options
	default:
		commandName = data.Name
		opts = data.Options
	}

	options := make(map[string]string, len(opts))
	for _, o := range opts {
		options[o.Name] = fmt.Sprintf("%v", o.Value)
	}

	inter := &orchestrator.Interaction{
		ChannelID:   i.ChannelID,
		GuildID:     i.GuildID,
		CommandName: commandName,
		Options:     options,
	}

	b.mu.RLock()
	handlers := make([]InteractionHandler, len(b.interactionHandlers))
	copy(handlers, b.interactionHandlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		go h(context.Background(), inter)
	}
}

// parseIncomingMessage converts a discordgo MessageCreate into an IncomingMessage.
// Returns nil if the message has no trigger (mention, prefix, reply to bot, or DM).
// DMs (GuildID is empty) are always treated as triggered.
func parseIncomingMessage(m *discordgo.MessageCreate, botUserID string) *IncomingMessage {
	isMention := isBotMention(m, botUserID)
	hasPrefix := hasCommandPrefix(m.Content)
	isReply := isReplyToBot(m, botUserID)
	isDM := m.GuildID == ""

	if !isMention && !hasPrefix && !isReply && !isDM {
		return nil
	}

	content := m.Content
	if isMention {
		content = stripMention(content, botUserID)
	} else if hasPrefix {
		content = stripPrefix(content)
	}

	return &IncomingMessage{
		ChannelID:    m.ChannelID,
		GuildID:      m.GuildID,
		AuthorID:     m.Author.ID,
		AuthorName:   m.Author.Username,
		Content:      content,
		MessageID:    m.ID,
		IsBotMention: isMention,
		IsReplyToBot: isReply,
		HasPrefix:    hasPrefix,
		IsDM:         isDM,
		Timestamp:    m.Timestamp,
	}
}

func isBotMention(m *discordgo.MessageCreate, botUserID string) bool {
	for _, u := range m.Mentions {
		if u.ID == botUserID {
			return true
		}
	}
	// Fallback: check raw content for <@BOT_ID>. This handles the case where
	// the bot sends a self-mention via PostMessage but Discord doesn't
	// populate the Mentions slice.
	return strings.Contains(m.Content, "<@"+botUserID+">")
}

func hasCommandPrefix(content string) bool {
	lower := strings.ToLower(content)
	return strings.HasPrefix(lower, commandPrefix)
}

func isReplyToBot(m *discordgo.MessageCreate, botUserID string) bool {
	if m.MessageReference == nil {
		return false
	}
	if m.ReferencedMessage == nil {
		return false
	}
	if m.ReferencedMessage.Author == nil {
		return false
	}
	return m.ReferencedMessage.Author.ID == botUserID
}

func stripMention(content, botUserID string) string {
	mention := "<@" + botUserID + ">"
	mentionNick := "<@!" + botUserID + ">"
	content = strings.ReplaceAll(content, mention, "")
	content = strings.ReplaceAll(content, mentionNick, "")
	return strings.TrimSpace(content)
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
	// Try to cut at a newline within the limit.
	lastNewline := strings.LastIndex(content[:maxLen], "\n")
	if lastNewline > 0 {
		return lastNewline + 1
	}
	// Try to cut at a space.
	lastSpace := strings.LastIndex(content[:maxLen], " ")
	if lastSpace > 0 {
		return lastSpace + 1
	}
	// Hard cut.
	return maxLen
}
