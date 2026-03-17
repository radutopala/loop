package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// ChannelStore is the subset of db.Store needed by BotRouter for channel lookups.
type ChannelStore interface {
	GetChannel(ctx context.Context, channelID string) (*db.Channel, error)
}

// BotRouter implements Bot by routing calls to the correct platform-specific bot
// based on channel platform from the database.
type BotRouter struct {
	bots   map[types.Platform]Bot
	store  ChannelStore
	logger *slog.Logger
}

// NewBotRouter creates a BotRouter wrapping the given platform bots.
func NewBotRouter(bots map[types.Platform]Bot, store ChannelStore, logger *slog.Logger) *BotRouter {
	return &BotRouter{
		bots:   bots,
		store:  store,
		logger: logger,
	}
}

// botForChannel looks up the channel's platform in the DB and returns the
// corresponding bot.
func (r *BotRouter) botForChannel(ctx context.Context, channelID string) Bot {
	ch, err := r.store.GetChannel(ctx, channelID)
	if err == nil && ch != nil {
		if b, ok := r.bots[ch.Platform]; ok {
			return b
		}
	}
	return nil
}

// BotFor returns the bot for the given platform, or nil if not found.
func (r *BotRouter) BotFor(p types.Platform) Bot {
	return r.bots[p]
}

// forEachBot runs fn against every registered bot and collects errors.
func (r *BotRouter) forEachBot(fn func(types.Platform, Bot) error, operation string) error {
	var errs []string
	for p, b := range r.bots {
		if err := fn(p, b); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", operation, strings.Join(errs, "; "))
	}
	return nil
}

// withBot resolves the bot for a channel, returning an error if none is found.
func (r *BotRouter) withBot(ctx context.Context, channelID, method string) (Bot, error) {
	b := r.botForChannel(ctx, channelID)
	if b == nil {
		return nil, r.noBotErr(method, channelID)
	}
	return b, nil
}

// --- Lifecycle: fan out to all bots ---

func (r *BotRouter) Start(ctx context.Context) error {
	return r.forEachBot(func(_ types.Platform, b Bot) error { return b.Start(ctx) }, "starting bots")
}

func (r *BotRouter) Stop() error {
	return r.forEachBot(func(_ types.Platform, b Bot) error { return b.Stop() }, "stopping bots")
}

func (r *BotRouter) RegisterCommands(ctx context.Context) error {
	return r.forEachBot(func(_ types.Platform, b Bot) error { return b.RegisterCommands(ctx) }, "registering commands")
}

func (r *BotRouter) RemoveCommands(ctx context.Context) error {
	return r.forEachBot(func(_ types.Platform, b Bot) error { return b.RemoveCommands(ctx) }, "removing commands")
}

// --- Inbound handlers: register on all bots ---

func (r *BotRouter) OnMessage(handler func(ctx context.Context, msg *bot.IncomingMessage)) {
	for _, b := range r.bots {
		b.OnMessage(handler)
	}
}

func (r *BotRouter) OnInteraction(handler func(ctx context.Context, i *bot.Interaction)) {
	for _, b := range r.bots {
		b.OnInteraction(handler)
	}
}

func (r *BotRouter) OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool)) {
	for _, b := range r.bots {
		b.OnChannelDelete(handler)
	}
}

func (r *BotRouter) OnChannelJoin(handler func(ctx context.Context, channelID string, platform types.Platform)) {
	for _, b := range r.bots {
		b.OnChannelJoin(handler)
	}
}

// --- Channel-specific calls: route to channel's platform bot ---

func (r *BotRouter) SendMessage(ctx context.Context, msg *bot.OutgoingMessage) error {
	b, err := r.withBot(ctx, msg.ChannelID, "SendMessage")
	if err != nil {
		return err
	}
	return b.SendMessage(ctx, msg)
}

func (r *BotRouter) SendTyping(ctx context.Context, channelID string) error {
	b, err := r.withBot(ctx, channelID, "SendTyping")
	if err != nil {
		return err
	}
	return b.SendTyping(ctx, channelID)
}

func (r *BotRouter) SendStopButton(ctx context.Context, channelID, runID string) (string, error) {
	b, err := r.withBot(ctx, channelID, "SendStopButton")
	if err != nil {
		return "", err
	}
	return b.SendStopButton(ctx, channelID, runID)
}

func (r *BotRouter) RemoveStopButton(ctx context.Context, channelID, messageID string) error {
	b, err := r.withBot(ctx, channelID, "RemoveStopButton")
	if err != nil {
		return err
	}
	return b.RemoveStopButton(ctx, channelID, messageID)
}

func (r *BotRouter) SetChannelTopic(ctx context.Context, channelID, topic string) error {
	b, err := r.withBot(ctx, channelID, "SetChannelTopic")
	if err != nil {
		return err
	}
	return b.SetChannelTopic(ctx, channelID, topic)
}

func (r *BotRouter) DeleteThread(ctx context.Context, threadID string) error {
	b, err := r.withBot(ctx, threadID, "DeleteThread")
	if err != nil {
		return err
	}
	return b.DeleteThread(ctx, threadID)
}

func (r *BotRouter) RenameThread(ctx context.Context, threadID, name string) error {
	b, err := r.withBot(ctx, threadID, "RenameThread")
	if err != nil {
		return err
	}
	return b.RenameThread(ctx, threadID, name)
}

func (r *BotRouter) PostMessage(ctx context.Context, channelID, content string) error {
	b, err := r.withBot(ctx, channelID, "PostMessage")
	if err != nil {
		return err
	}
	return b.PostMessage(ctx, channelID, content)
}

func (r *BotRouter) GetChannelParentID(ctx context.Context, channelID string) (string, error) {
	b, err := r.withBot(ctx, channelID, "GetChannelParentID")
	if err != nil {
		return "", err
	}
	return b.GetChannelParentID(ctx, channelID)
}

func (r *BotRouter) GetChannelName(ctx context.Context, channelID string) (string, error) {
	b, err := r.withBot(ctx, channelID, "GetChannelName")
	if err != nil {
		return "", err
	}
	return b.GetChannelName(ctx, channelID)
}

func (r *BotRouter) CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error) {
	b, err := r.withBot(ctx, channelID, "CreateThread")
	if err != nil {
		return "", err
	}
	return b.CreateThread(ctx, channelID, name, mentionUserID, message)
}

func (r *BotRouter) CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error) {
	b, err := r.withBot(ctx, channelID, "CreateSimpleThread")
	if err != nil {
		return "", err
	}
	return b.CreateSimpleThread(ctx, channelID, name, initialMessage)
}

func (r *BotRouter) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	b, err := r.withBot(ctx, channelID, "InviteUserToChannel")
	if err != nil {
		return err
	}
	return b.InviteUserToChannel(ctx, channelID, userID)
}

// noBotErr logs a warning and returns an error when no bot is found for a channel.
func (r *BotRouter) noBotErr(method, channelID string) error {
	r.logger.Warn("no bot found for channel", "method", method, "channel_id", channelID)
	return fmt.Errorf("no bot found for channel %s", channelID)
}

// --- API message routing: route to channel's platform bot ---

func (r *BotRouter) HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string, worktree bool, branch string) {
	if b := r.botForChannel(ctx, channelID); b != nil {
		b.HandleIncomingMessage(ctx, channelID, authorID, content, mode, worktree, branch)
	}
}

func (r *BotRouter) HandleThreadCreated(ctx context.Context, threadID, authorID, message string) {
	if b := r.botForChannel(ctx, threadID); b != nil {
		b.HandleThreadCreated(ctx, threadID, authorID, message)
	}
}

// --- No-channel methods: use BotFor(platform) directly instead ---

func (r *BotRouter) BotUserID() string { return "" }

// IsBotUser returns true if userID matches ANY bot's user ID.
func (r *BotRouter) IsBotUser(userID string) bool {
	for _, b := range r.bots {
		if b.IsBotUser(userID) {
			return true
		}
	}
	return false
}
