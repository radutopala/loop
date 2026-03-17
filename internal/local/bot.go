package local

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/types"
)

const (
	// BotUserID is the user ID for the local bot.
	BotUserID = "loop-bot"

	// BotUsername is the bot name used for @mention detection on local platform.
	BotUsername = "LoopBot"

	// DefaultAuthorID is the default author for local platform messages.
	DefaultAuthorID = "local-user"
)

// LocalStore is the subset of db.Store needed by Bot.
type LocalStore interface {
	GetChannel(ctx context.Context, channelID string) (*db.Channel, error)
	UpsertChannel(ctx context.Context, ch *db.Channel) error
	DeleteChannel(ctx context.Context, channelID string) error
	InsertMessage(ctx context.Context, msg *db.Message) error
}

// Bot is the local platform bot. It implements orchestrator.Bot with
// DB-backed thread/channel operations, and api.IncomingMessageHandler
// for routing API messages through the orchestrator.
type Bot struct {
	store            LocalStore
	logger           *slog.Logger
	generateThreadID func() string

	messageHandler       bot.MessageHandler
	interactionHandler   bot.InteractionHandler
	channelDeleteHandler bot.ChannelDeleteHandler
	channelJoinHandler   bot.ChannelJoinHandler
}

// NewBot creates a new local platform Bot.
func NewBot(store LocalStore, logger *slog.Logger) *Bot {
	return &Bot{
		store:            store,
		logger:           logger,
		generateThreadID: func() string { return randutil.HexID(6) },
	}
}

// --- Lifecycle (no-ops) ---

func (b *Bot) Start(ctx context.Context) error {
	b.logger.InfoContext(ctx, "local bot started")
	return nil
}
func (b *Bot) Stop() error {
	b.logger.Info("local bot stopped")
	return nil
}
func (b *Bot) RegisterCommands(_ context.Context) error { return nil }
func (b *Bot) RemoveCommands(_ context.Context) error   { return nil }
func (b *Bot) BotUserID() string                        { return BotUserID }
func (b *Bot) IsBotUser(userID string) bool             { return userID == b.BotUserID() }

// --- Messaging (no-ops — orchestrator handles DB + EventsHub) ---

func (b *Bot) SendMessage(_ context.Context, _ *bot.OutgoingMessage) error { return nil }
func (b *Bot) SendTyping(_ context.Context, _ string) error                { return nil }
func (b *Bot) PostMessage(_ context.Context, _, _ string) error            { return nil }

// --- Stop button (no-ops — platform UI feature) ---

func (b *Bot) SendStopButton(_ context.Context, _, _ string) (string, error) { return "", nil }
func (b *Bot) RemoveStopButton(_ context.Context, _, _ string) error         { return nil }

// --- DB-backed channel/thread methods ---

func (b *Bot) GetChannelParentID(ctx context.Context, channelID string) (string, error) {
	ch, err := b.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("getting channel: %w", err)
	}
	if ch == nil {
		return "", nil
	}
	return ch.ParentID, nil
}

func (b *Bot) GetChannelName(ctx context.Context, channelID string) (string, error) {
	ch, err := b.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("getting channel: %w", err)
	}
	if ch == nil {
		return "", nil
	}
	return ch.Name, nil
}

func (b *Bot) CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error) {
	parent, err := b.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("looking up parent channel: %w", err)
	}
	if parent == nil {
		return "", fmt.Errorf("parent channel %s not found", channelID)
	}

	threadID := b.generateThreadID()

	if err := b.store.UpsertChannel(ctx, &db.Channel{
		ChannelID:   threadID,
		GuildID:     parent.GuildID,
		Name:        name,
		DirPath:     parent.DirPath,
		ParentID:    channelID,
		Platform:    parent.Platform,
		SessionID:   parent.SessionID,
		Permissions: parent.Permissions,
		Active:      true,
	}); err != nil {
		return "", fmt.Errorf("storing thread: %w", err)
	}

	// Store the initial message in the thread.
	if initialMessage != "" {
		ch, err := b.store.GetChannel(ctx, threadID)
		if err == nil && ch != nil {
			_ = b.store.InsertMessage(ctx, &db.Message{
				ChatID:     ch.ID,
				ChannelID:  threadID,
				MsgID:      b.generateThreadID(), // reuse for unique msg ID
				AuthorName: "agent",
				Content:    initialMessage,
				IsBot:      true,
				CreatedAt:  time.Now().UTC(),
			})
		}
	}

	b.logger.InfoContext(ctx, "created simple local thread", "thread_id", threadID, "name", name, "parent_id", channelID)
	return threadID, nil
}

func (b *Bot) CreateThread(ctx context.Context, channelID, name, _, message string) (string, error) {
	// mentionUserID (3rd param) is Discord-specific, ignored for local.
	return b.CreateSimpleThread(ctx, channelID, name, message)
}

func (b *Bot) DeleteThread(ctx context.Context, threadID string) error {
	if err := b.store.DeleteChannel(ctx, threadID); err != nil {
		return err
	}
	b.logger.InfoContext(ctx, "deleted local thread", "thread_id", threadID)
	return nil
}

func (b *Bot) RenameThread(ctx context.Context, threadID, name string) error {
	ch, err := b.store.GetChannel(ctx, threadID)
	if err != nil {
		return fmt.Errorf("getting thread: %w", err)
	}
	if ch == nil {
		return nil
	}
	ch.Name = name
	if err := b.store.UpsertChannel(ctx, ch); err != nil {
		return err
	}
	b.logger.InfoContext(ctx, "renamed local thread", "thread_id", threadID)
	return nil
}

func (b *Bot) InviteUserToChannel(_ context.Context, _, _ string) error { return nil }
func (b *Bot) SetChannelTopic(_ context.Context, _, _ string) error     { return nil }

// --- ChannelCreator methods (satisfy api.ChannelCreator) ---

func (b *Bot) CreateChannel(_ context.Context, _ string) (string, error) {
	return b.generateThreadID() + b.generateThreadID(), nil
}

func (b *Bot) GetOwnerUserID(_ context.Context) (string, error) {
	return "", nil
}

// --- Event handler registration ---

func (b *Bot) OnMessage(handler func(ctx context.Context, msg *bot.IncomingMessage)) {
	b.messageHandler = handler
}

func (b *Bot) OnInteraction(handler func(ctx context.Context, i *bot.Interaction)) {
	b.interactionHandler = handler
}

func (b *Bot) OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool)) {
	b.channelDeleteHandler = handler
}

func (b *Bot) OnChannelJoin(handler func(ctx context.Context, channelID string, platform types.Platform)) {
	b.channelJoinHandler = handler
}

// --- IncomingMessageHandler (absorbs orchMessageAdapter from serve.go) ---

// HandleIncomingMessage implements api.IncomingMessageHandler. It parses mentions
// and command prefixes, then routes the message through the orchestrator.
func (b *Bot) HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string, worktree bool, branch string) {
	if authorID == "" {
		authorID = DefaultAuthorID
	}

	isMention := bot.HasTextMention(content, BotUsername)
	hasPrefix := bot.HasCommandPrefix(content)

	if isMention {
		content = bot.StripTextMention(content, BotUsername)
	}
	if hasPrefix {
		content = bot.StripPrefix(content)
	}

	if b.messageHandler != nil {
		b.messageHandler(ctx, &bot.IncomingMessage{
			ChannelID:    channelID,
			AuthorID:     authorID,
			AuthorName:   authorID,
			Content:      content,
			Platform:     types.PlatformLocal,
			IsBotMention: isMention,
			HasPrefix:    hasPrefix,
			IsDM:         true,
			Timestamp:    time.Now().UTC(),
			Mode:         mode,
			Worktree:     worktree,
		Branch:       branch,
		})
	}
}

// HandleThreadCreated implements api.IncomingMessageHandler. It routes the
// initial thread message as a bot mention to trigger the agent.
func (b *Bot) HandleThreadCreated(ctx context.Context, threadID, authorID, message string) {
	if message == "" {
		return
	}
	b.HandleIncomingMessage(ctx, threadID, authorID, "@"+BotUsername+" "+message, "", false, "")
}
