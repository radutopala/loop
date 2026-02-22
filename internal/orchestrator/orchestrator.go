package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// Bot represents the chat platform bot interface (Discord or Slack).
type Bot interface {
	Start(ctx context.Context) error
	Stop() error
	SendMessage(ctx context.Context, msg *OutgoingMessage) error
	SendTyping(ctx context.Context, channelID string) error
	SendStopButton(ctx context.Context, channelID, runID string) (messageID string, err error)
	RemoveStopButton(ctx context.Context, channelID, messageID string) error
	RegisterCommands(ctx context.Context) error
	RemoveCommands(ctx context.Context) error
	OnMessage(handler func(ctx context.Context, msg *IncomingMessage))
	OnInteraction(handler func(ctx context.Context, i any))
	OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool))
	OnChannelJoin(handler func(ctx context.Context, channelID string))
	BotUserID() string
	CreateChannel(ctx context.Context, guildID, name string) (string, error)
	InviteUserToChannel(ctx context.Context, channelID, userID string) error
	GetOwnerUserID(ctx context.Context) (string, error)
	SetChannelTopic(ctx context.Context, channelID, topic string) error
	CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error)
	PostMessage(ctx context.Context, channelID, content string) error
	DeleteThread(ctx context.Context, threadID string) error
	GetChannelParentID(ctx context.Context, channelID string) (string, error)
	GetChannelName(ctx context.Context, channelID string) (string, error)
	CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error)
	GetMemberRoles(ctx context.Context, guildID, userID string) ([]string, error)
}

// IncomingMessage from the chat platform.
type IncomingMessage struct {
	ChannelID    string
	GuildID      string
	AuthorID     string
	AuthorName   string
	Content      string
	MessageID    string
	IsBotMention bool
	IsReplyToBot bool
	HasPrefix    bool
	IsDM         bool
	Timestamp    time.Time
	AuthorRoles  []string // role IDs for permission checking (Discord only)
}

// OutgoingMessage to the chat platform.
type OutgoingMessage struct {
	ChannelID        string
	Content          string
	ReplyToMessageID string
}

// Runner runs Claude agent in a container.
type Runner interface {
	Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error)
	Cleanup(ctx context.Context) error
}

// Scheduler manages scheduled tasks.
type Scheduler interface {
	Start(ctx context.Context) error
	Stop() error
	AddTask(ctx context.Context, task *db.ScheduledTask) (int64, error)
	RemoveTask(ctx context.Context, taskID int64) error
	ListTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error)
	SetTaskEnabled(ctx context.Context, taskID int64, enabled bool) error
	ToggleTask(ctx context.Context, taskID int64) (bool, error)
	EditTask(ctx context.Context, taskID int64, schedule, taskType, prompt *string) error
}

// Interaction represents a slash command interaction.
type Interaction struct {
	ChannelID   string
	GuildID     string
	CommandName string
	Options     map[string]string
	AuthorID    string   // user who invoked the command
	AuthorRoles []string // role IDs (Discord only)
}

// Orchestrator coordinates all components of the loop bot.
type Orchestrator struct {
	store          db.Store
	bot            Bot
	runner         Runner
	scheduler      Scheduler
	queue          *ChannelQueue
	activeRuns     sync.Map // map[channelID]context.CancelFunc
	logger         *slog.Logger
	typingInterval time.Duration
	platform       types.Platform
	cfg            config.Config
}

// New creates a new Orchestrator.
func New(store db.Store, bot Bot, runner Runner, scheduler Scheduler, logger *slog.Logger, platform types.Platform, cfg config.Config) *Orchestrator {
	return &Orchestrator{
		store:          store,
		bot:            bot,
		runner:         runner,
		scheduler:      scheduler,
		queue:          NewChannelQueue(),
		logger:         logger,
		typingInterval: typingRefreshInterval,
		platform:       platform,
		cfg:            cfg,
	}
}

// Start registers handlers, slash commands, and starts the bot and scheduler.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.bot.OnMessage(o.HandleMessage)
	o.bot.OnInteraction(o.HandleInteraction)
	o.bot.OnChannelDelete(o.HandleChannelDelete)
	o.bot.OnChannelJoin(o.HandleChannelJoin)

	if err := o.bot.RegisterCommands(ctx); err != nil {
		return fmt.Errorf("registering commands: %w", err)
	}

	if err := o.bot.Start(ctx); err != nil {
		return fmt.Errorf("starting bot: %w", err)
	}

	if err := o.scheduler.Start(ctx); err != nil {
		return fmt.Errorf("starting scheduler: %w", err)
	}

	o.logger.Info("orchestrator started")
	return nil
}

// Stop gracefully shuts down the bot, scheduler, and runner.
func (o *Orchestrator) Stop() error {
	o.logger.Info("orchestrator stopping")

	var errs []string

	if err := o.scheduler.Stop(); err != nil {
		errs = append(errs, fmt.Sprintf("scheduler: %v", err))
	}

	if err := o.bot.Stop(); err != nil {
		errs = append(errs, fmt.Sprintf("bot: %v", err))
	}

	if err := o.runner.Cleanup(context.Background()); err != nil {
		errs = append(errs, fmt.Sprintf("runner cleanup: %v", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

const recentMessageLimit = 50
const typingRefreshInterval = 8 * time.Second

// HandleChannelJoin auto-registers a channel when the bot is added to it.
func (o *Orchestrator) HandleChannelJoin(ctx context.Context, channelID string) {
	name := o.resolveChannelName(ctx, channelID, false)
	if err := o.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: channelID,
		Name:      name,
		Platform:  o.platform,
		Active:    true,
	}); err != nil {
		o.logger.Error("auto-creating channel on join", "error", err, "channel_id", channelID)
		return
	}
	o.logger.Info("auto-created channel on bot join", "channel_id", channelID, "name", name)
}

// configPermissionsFor returns the effective PermissionsConfig for the given dirPath.
// Project config overrides global when present; falls back to global on error.
func (o *Orchestrator) configPermissionsFor(dirPath string) config.PermissionsConfig {
	if dirPath == "" {
		return o.cfg.Permissions
	}
	cfg, err := config.LoadProjectConfig(dirPath, &o.cfg)
	if err != nil {
		return o.cfg.Permissions
	}
	return cfg.Permissions
}

// resolveRole returns the effective role for the given author by merging config and DB grants.
// Bootstrap rule: if both config and DB are empty, everyone is RoleOwner.
// Otherwise the more privileged role (owner > member) from either source wins.
func resolveRole(cfgPerms config.PermissionsConfig, dbPerms db.ChannelPermissions, authorID string, authorRoles []string) types.Role {
	if cfgPerms.IsEmpty() && dbPerms.IsEmpty() {
		return types.RoleOwner // bootstrap: no restrictions configured
	}
	cfgRole := cfgPerms.GetRole(authorID, authorRoles)
	dbRole := dbPerms.GetRole(authorID, authorRoles)
	if cfgRole == types.RoleOwner || dbRole == types.RoleOwner {
		return types.RoleOwner
	}
	if cfgRole == types.RoleMember || dbRole == types.RoleMember {
		return types.RoleMember
	}
	return ""
}

// appendUnique appends v to s if not already present.
func appendUnique(s []string, v string) []string {
	for _, item := range s {
		if item == v {
			return s
		}
	}
	return append(s, v)
}

// removeString removes all occurrences of v from s.
func removeString(s []string, v string) []string {
	out := s[:0:0]
	for _, item := range s {
		if item != v {
			out = append(out, item)
		}
	}
	return out
}

// resolveChannelName returns the channel name from the platform API,
// falling back to "DM" for DMs or "channel" if the lookup fails.
func (o *Orchestrator) resolveChannelName(ctx context.Context, channelID string, isDM bool) string {
	if isDM {
		return "DM"
	}
	name, err := o.bot.GetChannelName(ctx, channelID)
	if err != nil || name == "" {
		return "channel"
	}
	return name
}

// HandleChannelDelete removes a deleted channel or thread from the database.
// For channels (not threads), it also removes all child threads.
func (o *Orchestrator) HandleChannelDelete(ctx context.Context, channelID string, isThread bool) {
	if isThread {
		if err := o.store.DeleteChannel(ctx, channelID); err != nil {
			o.logger.Error("deleting thread from db", "error", err, "thread_id", channelID)
			return
		}
		o.logger.Info("deleted thread from db", "thread_id", channelID)
		return
	}

	if err := o.store.DeleteChannelsByParentID(ctx, channelID); err != nil {
		o.logger.Error("deleting child threads from db", "error", err, "channel_id", channelID)
	}
	if err := o.store.DeleteChannel(ctx, channelID); err != nil {
		o.logger.Error("deleting channel from db", "error", err, "channel_id", channelID)
		return
	}
	o.logger.Info("deleted channel and child threads from db", "channel_id", channelID)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "due now"
	}
	if d < time.Minute {
		return fmt.Sprintf("in %ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("in %dh", h)
	}
	return fmt.Sprintf("in %dh%dm", h, m)
}

func generateMessageID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "ask-" + hex.EncodeToString(b)
}

// truncateString truncates s to maxLen characters, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
