package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// Bot represents the Discord bot interface.
type Bot interface {
	Start(ctx context.Context) error
	Stop() error
	SendMessage(ctx context.Context, msg *OutgoingMessage) error
	SendTyping(ctx context.Context, channelID string) error
	RegisterCommands(ctx context.Context) error
	RemoveCommands(ctx context.Context) error
	OnMessage(handler func(ctx context.Context, msg *IncomingMessage))
	OnInteraction(handler func(ctx context.Context, i any))
	OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool))
	BotUserID() string
	CreateChannel(ctx context.Context, guildID, name string) (string, error)
	CreateThread(ctx context.Context, channelID, name string) (string, error)
	GetChannelParentID(ctx context.Context, channelID string) (string, error)
}

// IncomingMessage from Discord.
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
}

// OutgoingMessage to Discord.
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

// Interaction represents a Discord slash command interaction.
type Interaction struct {
	ChannelID   string
	GuildID     string
	CommandName string
	Options     map[string]string
}

// Orchestrator coordinates all components of the loop bot.
type Orchestrator struct {
	store            db.Store
	bot              Bot
	runner           Runner
	scheduler        Scheduler
	queue            *ChannelQueue
	logger           *slog.Logger
	typingInterval   time.Duration
	templates        []config.TaskTemplate
	containerTimeout time.Duration
	loopDir          string
}

// New creates a new Orchestrator.
func New(store db.Store, bot Bot, runner Runner, scheduler Scheduler, logger *slog.Logger, templates []config.TaskTemplate, containerTimeout time.Duration, loopDir string) *Orchestrator {
	return &Orchestrator{
		store:            store,
		bot:              bot,
		runner:           runner,
		scheduler:        scheduler,
		queue:            NewChannelQueue(),
		logger:           logger,
		typingInterval:   typingRefreshInterval,
		templates:        templates,
		containerTimeout: containerTimeout,
		loopDir:          loopDir,
	}
}

// Start registers handlers, slash commands, and starts the bot and scheduler.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.bot.OnMessage(o.HandleMessage)
	o.bot.OnInteraction(o.HandleInteraction)
	o.bot.OnChannelDelete(o.HandleChannelDelete)

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

// HandleMessage processes an incoming Discord message.
func (o *Orchestrator) HandleMessage(ctx context.Context, msg *IncomingMessage) {
	active, err := o.store.IsChannelActive(ctx, msg.ChannelID)
	if err != nil {
		o.logger.Error("checking channel active", "error", err, "channel_id", msg.ChannelID)
		return
	}
	if !active {
		if !o.resolveThread(ctx, msg.ChannelID) {
			return
		}
	}

	channel, err := o.store.GetChannel(ctx, msg.ChannelID)
	if err != nil || channel == nil {
		o.logger.Error("getting channel", "error", err, "channel_id", msg.ChannelID)
		return
	}

	discordMsgID := msg.MessageID
	if discordMsgID == "" {
		discordMsgID = generateMessageID()
	}

	if err := o.store.InsertMessage(ctx, &db.Message{
		ChatID:       channel.ID,
		ChannelID:    msg.ChannelID,
		DiscordMsgID: discordMsgID,
		AuthorID:     msg.AuthorID,
		AuthorName:   msg.AuthorName,
		Content:      msg.Content,
		CreatedAt:    msg.Timestamp,
	}); err != nil {
		o.logger.Error("inserting message", "error", err, "channel_id", msg.ChannelID)
		return
	}

	o.logger.Info("incoming message",
		"channel_id", msg.ChannelID,
		"author", msg.AuthorName,
		"content", msg.Content,
		"triggered", msg.IsBotMention || msg.IsReplyToBot || msg.HasPrefix || msg.IsDM,
	)

	triggered := msg.IsBotMention || msg.IsReplyToBot || msg.HasPrefix || msg.IsDM
	if !triggered {
		return
	}

	o.processTriggeredMessage(ctx, msg)
}

// resolveThread checks if channelID is a thread with an active parent channel.
// If so, it upserts the thread as a channel inheriting from the parent and returns true.
func (o *Orchestrator) resolveThread(ctx context.Context, channelID string) bool {
	parentID, err := o.bot.GetChannelParentID(ctx, channelID)
	if err != nil {
		o.logger.Error("getting channel parent", "error", err, "channel_id", channelID)
		return false
	}
	if parentID == "" {
		return false
	}

	parentActive, err := o.store.IsChannelActive(ctx, parentID)
	if err != nil {
		o.logger.Error("checking parent channel active", "error", err, "parent_id", parentID)
		return false
	}
	if !parentActive {
		return false
	}

	parent, err := o.store.GetChannel(ctx, parentID)
	if err != nil || parent == nil {
		o.logger.Error("getting parent channel", "error", err, "parent_id", parentID)
		return false
	}

	if err := o.store.UpsertChannel(ctx, &db.Channel{
		ChannelID: channelID,
		GuildID:   parent.GuildID,
		DirPath:   parent.DirPath,
		ParentID:  parentID,
		SessionID: parent.SessionID,
		Active:    true,
	}); err != nil {
		o.logger.Error("upserting thread channel", "error", err, "channel_id", channelID)
		return false
	}

	o.logger.Info("resolved thread to parent channel",
		"thread_id", channelID,
		"parent_id", parentID,
	)
	return true
}

func (o *Orchestrator) processTriggeredMessage(ctx context.Context, msg *IncomingMessage) {
	o.queue.Acquire(msg.ChannelID)
	defer o.queue.Release(msg.ChannelID)

	typingCtx, stopTyping := context.WithCancel(ctx)
	defer stopTyping()
	go o.refreshTyping(typingCtx, msg.ChannelID)

	recent, err := o.store.GetRecentMessages(ctx, msg.ChannelID, recentMessageLimit)
	if err != nil {
		o.logger.Error("getting recent messages", "error", err, "channel_id", msg.ChannelID)
		return
	}

	channel, err := o.store.GetChannel(ctx, msg.ChannelID)
	if err != nil {
		o.logger.Error("getting channel", "error", err, "channel_id", msg.ChannelID)
		return
	}

	req := o.buildAgentRequest(msg.ChannelID, recent, channel)
	req.Prompt = fmt.Sprintf("%s: %s", msg.AuthorName, msg.Content)

	// Fork the session on the first thread message so the thread gets its
	// own session while inheriting the parent's context.
	if channel.ParentID != "" && req.SessionID != "" {
		parent, err := o.store.GetChannel(ctx, channel.ParentID)
		if err == nil && parent != nil && channel.SessionID == parent.SessionID {
			req.ForkSession = true
		}
	}

	runCtx, runCancel := context.WithTimeout(ctx, o.containerTimeout)
	defer runCancel()

	resp, err := o.runner.Run(runCtx, req)
	if err != nil {
		o.logger.Error("running agent", "error", err, "channel_id", msg.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          "Sorry, I encountered an error processing your request.",
			ReplyToMessageID: msg.MessageID,
		})
		return
	}

	if resp.Error != "" {
		o.logger.Error("agent returned error", "error", resp.Error, "channel_id", msg.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID:        msg.ChannelID,
			Content:          fmt.Sprintf("Agent error: %s", resp.Error),
			ReplyToMessageID: msg.MessageID,
		})
		return
	}

	if err := o.store.UpdateSessionID(ctx, msg.ChannelID, resp.SessionID); err != nil {
		o.logger.Error("updating session data", "error", err, "channel_id", msg.ChannelID)
	}

	o.logger.Info("outgoing message",
		"channel_id", msg.ChannelID,
		"content", resp.Response,
	)

	if err := o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID:        msg.ChannelID,
		Content:          resp.Response,
		ReplyToMessageID: msg.MessageID,
	}); err != nil {
		o.logger.Error("sending response", "error", err, "channel_id", msg.ChannelID)
	}

	ch, err := o.store.GetChannel(ctx, msg.ChannelID)
	if err == nil && ch != nil {
		if insertErr := o.store.InsertMessage(ctx, &db.Message{
			ChatID:       ch.ID,
			ChannelID:    msg.ChannelID,
			DiscordMsgID: generateMessageID(),
			AuthorName:   "assistant",
			Content:      resp.Response,
			IsBot:        true,
			CreatedAt:    time.Now().UTC(),
		}); insertErr != nil {
			o.logger.Error("inserting bot response", "error", insertErr, "channel_id", msg.ChannelID)
		}
	}

	ids := make([]int64, len(recent))
	for i, m := range recent {
		ids[i] = m.ID
	}
	if err := o.store.MarkMessagesProcessed(ctx, ids); err != nil {
		o.logger.Error("marking messages processed", "error", err, "channel_id", msg.ChannelID)
	}
}

func (o *Orchestrator) refreshTyping(ctx context.Context, channelID string) {
	if err := o.bot.SendTyping(ctx, channelID); err != nil {
		o.logger.Error("sending typing indicator", "error", err, "channel_id", channelID)
	}

	ticker := time.NewTicker(o.typingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := o.bot.SendTyping(ctx, channelID); err != nil {
				o.logger.Error("refreshing typing indicator", "error", err, "channel_id", channelID)
			}
		}
	}
}

func (o *Orchestrator) buildAgentRequest(channelID string, recent []*db.Message, channel *db.Channel) *agent.AgentRequest {
	var messages []agent.AgentMessage
	// Reverse so oldest first
	for i := len(recent) - 1; i >= 0; i-- {
		m := recent[i]
		role := "user"
		if m.IsBot {
			role = "assistant"
		}
		messages = append(messages, agent.AgentMessage{
			Role:    role,
			Content: fmt.Sprintf("%s: %s", m.AuthorName, m.Content),
		})
	}

	sessionID := ""
	if channel != nil {
		sessionID = channel.SessionID
	}

	dirPath := ""
	if channel != nil {
		dirPath = channel.DirPath
	}

	return &agent.AgentRequest{
		SessionID: sessionID,
		Messages:  messages,
		ChannelID: channelID,
		DirPath:   dirPath,
	}
}

// HandleInteraction processes a Discord slash command interaction.
func (o *Orchestrator) HandleInteraction(ctx context.Context, interaction any) {
	inter, ok := interaction.(*Interaction)
	if !ok {
		o.logger.Error("invalid interaction type")
		return
	}

	switch inter.CommandName {
	case "schedule":
		o.handleScheduleInteraction(ctx, inter)
	case "tasks":
		o.handleTasksInteraction(ctx, inter)
	case "cancel":
		o.handleCancelInteraction(ctx, inter)
	case "toggle":
		o.handleToggleInteraction(ctx, inter)
	case "edit":
		o.handleEditInteraction(ctx, inter)
	case "status":
		o.handleStatusInteraction(ctx, inter)
	case "template-add":
		o.handleTemplateAddInteraction(ctx, inter)
	case "template-list":
		o.handleTemplateListInteraction(ctx, inter)
	default:
		o.logger.Warn("unknown command", "command", inter.CommandName)
	}
}

func (o *Orchestrator) handleScheduleInteraction(ctx context.Context, inter *Interaction) {
	task := &db.ScheduledTask{
		ChannelID: inter.ChannelID,
		GuildID:   inter.GuildID,
		Schedule:  inter.Options["schedule"],
		Prompt:    inter.Options["prompt"],
		Type:      db.TaskType(inter.Options["type"]),
		Enabled:   true,
	}

	id, err := o.scheduler.AddTask(ctx, task)
	if err != nil {
		o.logger.Error("adding scheduled task", "error", err, "channel_id", inter.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to schedule task.",
		})
		return
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   fmt.Sprintf("Task scheduled (ID: %d).", id),
	})
}

func (o *Orchestrator) handleTasksInteraction(ctx context.Context, inter *Interaction) {
	tasks, err := o.scheduler.ListTasks(ctx, inter.ChannelID)
	if err != nil {
		o.logger.Error("listing tasks", "error", err, "channel_id", inter.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to list tasks.",
		})
		return
	}

	if len(tasks) == 0 {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "No scheduled tasks.",
		})
		return
	}

	now := time.Now().UTC()
	var sb strings.Builder
	sb.WriteString("Scheduled tasks:\n")
	for _, t := range tasks {
		status := "enabled"
		if !t.Enabled {
			status = "disabled"
		}
		var schedule string
		if t.Type == db.TaskTypeOnce {
			schedule = t.NextRunAt.Local().Format("2006-01-02 15:04 MST")
		} else {
			schedule = fmt.Sprintf("`%s`", t.Schedule)
		}
		nextRun := formatDuration(t.NextRunAt.Sub(now))
		fmt.Fprintf(&sb, "- **ID %d** [%s] [%s] %s — %s (next: %s)\n", t.ID, t.Type, status, schedule, t.Prompt, nextRun)
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   sb.String(),
	})
}

func (o *Orchestrator) handleCancelInteraction(ctx context.Context, inter *Interaction) {
	idStr := inter.Options["task_id"]
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Invalid task ID.",
		})
		return
	}

	if err := o.scheduler.RemoveTask(ctx, taskID); err != nil {
		o.logger.Error("removing task", "error", err, "channel_id", inter.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to cancel task.",
		})
		return
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   fmt.Sprintf("Task %d cancelled.", taskID),
	})
}

func (o *Orchestrator) handleToggleInteraction(ctx context.Context, inter *Interaction) {
	idStr := inter.Options["task_id"]
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Invalid task ID.",
		})
		return
	}

	newEnabled, err := o.scheduler.ToggleTask(ctx, taskID)
	if err != nil {
		o.logger.Error("toggling task", "error", err, "channel_id", inter.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to toggle task.",
		})
		return
	}

	state := "disabled"
	if newEnabled {
		state = "enabled"
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   fmt.Sprintf("Task %d %s.", taskID, state),
	})
}

func (o *Orchestrator) handleEditInteraction(ctx context.Context, inter *Interaction) {
	idStr := inter.Options["task_id"]
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Invalid task ID.",
		})
		return
	}

	var schedule, taskType, prompt *string
	if v, ok := inter.Options["schedule"]; ok {
		schedule = &v
	}
	if v, ok := inter.Options["type"]; ok {
		taskType = &v
	}
	if v, ok := inter.Options["prompt"]; ok {
		prompt = &v
	}

	if schedule == nil && taskType == nil && prompt == nil {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "At least one of schedule, type, or prompt is required.",
		})
		return
	}

	if err := o.scheduler.EditTask(ctx, taskID, schedule, taskType, prompt); err != nil {
		o.logger.Error("editing task", "error", err, "channel_id", inter.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to edit task.",
		})
		return
	}

	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   fmt.Sprintf("Task %d updated.", taskID),
	})
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

func (o *Orchestrator) handleStatusInteraction(ctx context.Context, inter *Interaction) {
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   "Loop bot is running.",
	})
}

func (o *Orchestrator) handleTemplateAddInteraction(ctx context.Context, inter *Interaction) {
	name := inter.Options["name"]

	var tmpl *config.TaskTemplate
	for i := range o.templates {
		if o.templates[i].Name == name {
			tmpl = &o.templates[i]
			break
		}
	}
	if tmpl == nil {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   fmt.Sprintf("Unknown template: %s", name),
		})
		return
	}

	existing, err := o.store.GetScheduledTaskByTemplateName(ctx, inter.ChannelID, name)
	if err != nil {
		o.logger.Error("checking template", "error", err, "channel_id", inter.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to check existing templates.",
		})
		return
	}
	if existing != nil {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   fmt.Sprintf("Template '%s' already loaded (task ID: %d).", name, existing.ID),
		})
		return
	}

	prompt, err := tmpl.ResolvePrompt(o.loopDir)
	if err != nil {
		o.logger.Error("resolving template prompt", "error", err, "template", name)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   fmt.Sprintf("Failed to resolve template prompt: %v", err),
		})
		return
	}

	task := &db.ScheduledTask{
		ChannelID:    inter.ChannelID,
		GuildID:      inter.GuildID,
		Schedule:     tmpl.Schedule,
		Prompt:       prompt,
		Type:         db.TaskType(tmpl.Type),
		Enabled:      true,
		TemplateName: name,
	}

	id, err := o.scheduler.AddTask(ctx, task)
	if err != nil {
		o.logger.Error("adding template task", "error", err, "channel_id", inter.ChannelID)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to add template task.",
		})
		return
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   fmt.Sprintf("Template '%s' loaded (task ID: %d).", name, id),
	})
}

func (o *Orchestrator) handleTemplateListInteraction(ctx context.Context, inter *Interaction) {
	if len(o.templates) == 0 {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "No templates configured.",
		})
		return
	}

	var sb strings.Builder
	sb.WriteString("Available templates:\n")
	for _, t := range o.templates {
		fmt.Fprintf(&sb, "- **%s** [%s] `%s` — %s\n", t.Name, t.Type, t.Schedule, t.Description)
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   sb.String(),
	})
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
