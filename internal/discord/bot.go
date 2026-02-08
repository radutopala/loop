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
	session             DiscordSession
	appID               string
	logger              *slog.Logger
	botUserID           string
	messageHandlers     []MessageHandler
	interactionHandlers []InteractionHandler
	mu                  sync.RWMutex
	removeHandlers      []func()
	typingInterval      time.Duration
	pendingInteractions map[string]*discordgo.Interaction
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
	b.mu.Lock()
	b.removeHandlers = append(b.removeHandlers, rh1, rh2)
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
	b.mu.Unlock()

	b.logger.InfoContext(ctx, "discord bot started", "bot_user_id", user.ID)
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

// CreateChannel creates a new text channel in the given guild.
func (b *DiscordBot) CreateChannel(ctx context.Context, guildID, name string) (string, error) {
	ch, err := b.session.GuildChannelCreate(guildID, name, discordgo.ChannelTypeGuildText)
	if err != nil {
		return "", fmt.Errorf("discord create channel: %w", err)
	}
	b.logger.InfoContext(ctx, "created discord channel", "channel_id", ch.ID, "name", name, "guild_id", guildID)
	return ch.ID, nil
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
	if m.Author.ID == botID {
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
		h(context.Background(), msg)
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
	var commandName string
	var opts []*discordgo.ApplicationCommandInteractionDataOption
	if len(data.Options) > 0 && data.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		commandName = data.Options[0].Name
		opts = data.Options[0].Options
	} else {
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
		h(context.Background(), inter)
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
	return false
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
