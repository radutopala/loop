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

// --- Lifecycle: fan out to all bots ---

func (r *BotRouter) Start(ctx context.Context) error {
	var errs []string
	for p, b := range r.bots {
		if err := b.Start(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("starting bots: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (r *BotRouter) Stop() error {
	var errs []string
	for p, b := range r.bots {
		if err := b.Stop(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("stopping bots: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (r *BotRouter) RegisterCommands(ctx context.Context) error {
	var errs []string
	for p, b := range r.bots {
		if err := b.RegisterCommands(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("registering commands: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (r *BotRouter) RemoveCommands(ctx context.Context) error {
	var errs []string
	for p, b := range r.bots {
		if err := b.RemoveCommands(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("removing commands: %s", strings.Join(errs, "; "))
	}
	return nil
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
	return r.botForChannel(ctx, msg.ChannelID).SendMessage(ctx, msg)
}

func (r *BotRouter) SendTyping(ctx context.Context, channelID string) error {
	return r.botForChannel(ctx, channelID).SendTyping(ctx, channelID)
}

func (r *BotRouter) SendStopButton(ctx context.Context, channelID, runID string) (string, error) {
	return r.botForChannel(ctx, channelID).SendStopButton(ctx, channelID, runID)
}

func (r *BotRouter) RemoveStopButton(ctx context.Context, channelID, messageID string) error {
	return r.botForChannel(ctx, channelID).RemoveStopButton(ctx, channelID, messageID)
}

func (r *BotRouter) SetChannelTopic(ctx context.Context, channelID, topic string) error {
	return r.botForChannel(ctx, channelID).SetChannelTopic(ctx, channelID, topic)
}

func (r *BotRouter) DeleteThread(ctx context.Context, threadID string) error {
	return r.botForChannel(ctx, threadID).DeleteThread(ctx, threadID)
}

func (r *BotRouter) RenameThread(ctx context.Context, threadID, name string) error {
	return r.botForChannel(ctx, threadID).RenameThread(ctx, threadID, name)
}

func (r *BotRouter) PostMessage(ctx context.Context, channelID, content string) error {
	return r.botForChannel(ctx, channelID).PostMessage(ctx, channelID, content)
}

func (r *BotRouter) GetChannelParentID(ctx context.Context, channelID string) (string, error) {
	return r.botForChannel(ctx, channelID).GetChannelParentID(ctx, channelID)
}

func (r *BotRouter) GetChannelName(ctx context.Context, channelID string) (string, error) {
	return r.botForChannel(ctx, channelID).GetChannelName(ctx, channelID)
}

func (r *BotRouter) CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error) {
	return r.botForChannel(ctx, channelID).CreateThread(ctx, channelID, name, mentionUserID, message)
}

func (r *BotRouter) CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error) {
	return r.botForChannel(ctx, channelID).CreateSimpleThread(ctx, channelID, name, initialMessage)
}

func (r *BotRouter) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	return r.botForChannel(ctx, channelID).InviteUserToChannel(ctx, channelID, userID)
}

// --- API message routing: route to channel's platform bot ---

func (r *BotRouter) HandleIncomingMessage(ctx context.Context, channelID, authorID, content string) {
	if b := r.botForChannel(ctx, channelID); b != nil {
		b.HandleIncomingMessage(ctx, channelID, authorID, content)
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
