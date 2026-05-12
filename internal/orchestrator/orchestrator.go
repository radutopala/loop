package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/types"
	"github.com/radutopala/loop/internal/workflow"
)

// Bot represents the chat platform bot interface (Discord or Slack).
type Bot interface {
	Start(ctx context.Context) error
	Stop() error
	SendMessage(ctx context.Context, msg *bot.OutgoingMessage) error
	SendTyping(ctx context.Context, channelID string) error
	SendStopButton(ctx context.Context, channelID, runID string) (messageID string, err error)
	RemoveStopButton(ctx context.Context, channelID, messageID string) error
	SendApproval(ctx context.Context, channelID string, prompt bot.ApprovalPrompt) (messageID string, err error)
	RemoveApproval(ctx context.Context, channelID, messageID string) error
	RegisterCommands(ctx context.Context) error
	RemoveCommands(ctx context.Context) error
	OnMessage(handler func(ctx context.Context, msg *bot.IncomingMessage))
	OnInteraction(handler func(ctx context.Context, i *bot.Interaction))
	OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool))
	OnChannelJoin(handler func(ctx context.Context, channelID string, platform types.Platform))
	BotUserID() string
	IsBotUser(userID string) bool
	InviteUserToChannel(ctx context.Context, channelID, userID string) error
	SetChannelTopic(ctx context.Context, channelID, topic string) error
	CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error)
	PostMessage(ctx context.Context, channelID, content string) error
	DeleteThread(ctx context.Context, threadID string) error
	RenameThread(ctx context.Context, threadID, name string) error
	GetChannelParentID(ctx context.Context, channelID string) (string, error)
	GetChannelName(ctx context.Context, channelID string) (string, error)
	CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error)
	HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string)
	HandleIncomingMessageWithPriority(ctx context.Context, channelID, authorID, content, mode string, priority int)
	HandleThreadCreated(ctx context.Context, threadID, authorID, message string)
}

// Runner runs Claude agent in a container.
type Runner interface {
	Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error)
	Cleanup(ctx context.Context) error
}

// WorkflowEngine is the subset of workflow.Engine used by the orchestrator for chat commands.
type WorkflowEngine interface {
	StartRun(ctx context.Context, opts workflow.StartRunOptions) (string, error)
	CancelRun(ctx context.Context, runID string) error
	DeleteRun(ctx context.Context, runID string) error
	RetryRun(ctx context.Context, runID string) (string, error)
	ListRuns(ctx context.Context, channelID string, limit, offset int) ([]*db.WorkflowRun, error)
	ListWorkflows(ctx context.Context, dirPath, parentDirPath string) ([]config.WorkflowDef, error)
}

// Orchestrator coordinates all components of the loop bot.
type Orchestrator struct {
	store             db.Store
	bot               Bot
	runner            Runner
	scheduler         scheduler.Scheduler
	events            events.Broadcaster
	workflowEngine    WorkflowEngine
	channelLocks      sync.Map // map[channelID]*sync.Mutex — serialises per-channel drain loops
	activeRuns        sync.Map // map[channelID]context.CancelFunc
	activeRunMsgIDs   sync.Map // map[channelID]string — msg_id of the row currently running
	logger            *slog.Logger
	typingInterval    time.Duration
	cfg               atomic.Pointer[config.Config]
	configLoad        func() (*config.Config, error)
	loadProjectConfig func(string, *config.Config) (*config.Config, error)
	removeMCPConfig   func(string, string) error
}

// defaultRemoveMCPConfig delegates to bot.RemoveMCPConfig.
// Defined at package level to avoid parameter shadowing in New().
func defaultRemoveMCPConfig(dirPath, channelID string) error {
	return bot.RemoveMCPConfig(dirPath, channelID)
}

// New creates a new Orchestrator.
func New(store db.Store, bot Bot, runner Runner, sched scheduler.Scheduler, logger *slog.Logger, cfg config.Config, configLoad func() (*config.Config, error)) *Orchestrator {
	o := &Orchestrator{
		store:             store,
		bot:               bot,
		runner:            runner,
		scheduler:         sched,
		logger:            logger,
		typingInterval:    TypingInterval,
		configLoad:        configLoad,
		loadProjectConfig: config.LoadProjectConfig,
		removeMCPConfig:   defaultRemoveMCPConfig,
	}
	o.cfg.Store(&cfg)
	return o
}

// currentConfig returns a fresh config by calling configLoad, falling back
// to the last-known-good config on error or when configLoad is nil.
func (o *Orchestrator) currentConfig() *config.Config {
	if o.configLoad == nil {
		return o.cfg.Load()
	}
	fresh, err := o.configLoad()
	if err != nil {
		return o.cfg.Load()
	}
	o.cfg.Store(fresh)
	return fresh
}

// ActiveChatChannelIDs returns the set of channel IDs that have an active
// chat agent run (as opposed to just having a running container for terminal use).
func (o *Orchestrator) ActiveChatChannelIDs() map[string]struct{} {
	result := make(map[string]struct{})
	o.activeRuns.Range(func(key, _ any) bool {
		result[key.(string)] = struct{}{}
		return true
	})
	return result
}

// ActiveRunsMap returns a pointer to the activeRuns sync.Map so the
// TaskExecutor can register task runs for stop button support.
func (o *Orchestrator) ActiveRunsMap() *sync.Map {
	return &o.activeRuns
}

// ActiveRunMessageID returns the msg_id of the message currently being
// processed on the given channel, or empty string if no run is active.
// Used by the API layer for interrupt diagnostics and by the FE to know
// which row to label as "processing".
func (o *Orchestrator) ActiveRunMessageID(channelID string) string {
	val, ok := o.activeRunMsgIDs.Load(channelID)
	if !ok {
		return ""
	}
	return val.(string)
}

// CancelActiveRun cancels the active agent run for a channel, if any.
// Returns true if a run was cancelled.
func (o *Orchestrator) CancelActiveRun(channelID string) bool {
	val, ok := o.activeRuns.LoadAndDelete(channelID)
	if !ok {
		return false
	}
	cancel := val.(context.CancelFunc)
	cancel()
	o.logger.Info("active run cancelled via interrupt", "channel_id", channelID)
	return true
}

// SetEventBroadcaster configures the event broadcaster for real-time event streaming.
func (o *Orchestrator) SetEventBroadcaster(eb events.Broadcaster) {
	o.events = eb
}

// SetWorkflowEngine configures the workflow engine for chat commands.
func (o *Orchestrator) SetWorkflowEngine(we WorkflowEngine) {
	o.workflowEngine = we
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

// TypingInterval is the default interval between typing indicator refreshes.
const TypingInterval = 8 * time.Second

// HandleChannelJoin auto-registers a channel when the bot is added to it.
func (o *Orchestrator) HandleChannelJoin(ctx context.Context, channelID string, platform types.Platform) {
	name := o.resolveChannelName(ctx, channelID, false)
	if err := o.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: channelID,
		Name:      name,
		Platform:  platform,
		Active:    true,
	}); err != nil {
		o.logger.Error("auto-creating channel on join", "error", err, "channel_id", channelID, "platform", platform)
		return
	}
	o.logger.Info("auto-created channel on bot join", "channel_id", channelID, "platform", platform, "name", name)
}

// configPermissionsFor returns the effective Permissions for the given dirPath.
// Project config overrides global when present; falls back to global on error.
func (o *Orchestrator) configPermissionsFor(dirPath string) types.Permissions {
	cfg := o.currentConfig()
	if dirPath == "" {
		return cfg.Permissions
	}
	merged, err := o.loadProjectConfig(dirPath, cfg)
	if err != nil {
		return cfg.Permissions
	}
	return merged.Permissions
}

// resolveRole returns the effective role for the given author by merging config and DB grants.
// Bootstrap rule: if both config and DB are empty, everyone is RoleOwner.
// Otherwise the more privileged role (owner > member) from either source wins.
func resolveRole(cfgPerms, dbPerms types.Permissions, authorID string, authorRoles []string) types.Role {
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
// MCP config files are cleaned up on a best-effort basis unless keep_mcp_configs is set.
func (o *Orchestrator) HandleChannelDelete(ctx context.Context, channelID string, isThread bool) {
	keepMCPConfigs := o.currentConfig().KeepMCPConfigs
	if isThread {
		ch, err := o.store.GetChannel(ctx, channelID)
		if err != nil {
			o.logger.Error("looking up thread for MCP cleanup", "error", err, "thread_id", channelID)
		}
		if ch != nil && !keepMCPConfigs {
			if err := o.removeMCPConfig(ch.DirPath, channelID); err != nil {
				o.logger.Warn("removing MCP config for thread", "error", err, "thread_id", channelID)
			}
		}
		if err := o.store.DeleteChannel(ctx, channelID); err != nil {
			o.logger.Error("deleting thread from db", "error", err, "thread_id", channelID)
			return
		}
		o.logger.Info("deleted thread from db", "thread_id", channelID)
		return
	}

	// Look up channel for MCP cleanup.
	ch, err := o.store.GetChannel(ctx, channelID)
	if err != nil {
		o.logger.Error("looking up channel for MCP cleanup", "error", err, "channel_id", channelID)
	}
	if ch != nil && !keepMCPConfigs {
		// Clean up MCP configs for child threads.
		childIDs, err := o.store.ListChannelIDsByParentID(ctx, channelID)
		if err != nil {
			o.logger.Warn("listing child threads for MCP cleanup", "error", err, "channel_id", channelID)
		}
		for _, childID := range childIDs {
			if err := o.removeMCPConfig(ch.DirPath, childID); err != nil {
				o.logger.Warn("removing MCP config for child thread", "error", err, "thread_id", childID)
			}
		}
		// Clean up MCP config for the channel itself.
		if err := o.removeMCPConfig(ch.DirPath, channelID); err != nil {
			o.logger.Warn("removing MCP config for channel", "error", err, "channel_id", channelID)
		}
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
	return "ask-" + randutil.HexID(16)
}
