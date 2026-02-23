package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/readme"
	"github.com/radutopala/loop/internal/types"
)

// HandleInteraction processes a slash command interaction.
func (o *Orchestrator) HandleInteraction(ctx context.Context, inter *Interaction) {
	ch, _ := o.store.GetChannel(ctx, inter.ChannelID)
	var dbPerms db.ChannelPermissions
	dirPath := ""
	if ch != nil {
		dbPerms = ch.Permissions
		dirPath = ch.DirPath
	}
	cfgPerms := o.configPermissionsFor(dirPath)
	role := resolveRole(cfgPerms, dbPerms, inter.AuthorID, inter.AuthorRoles)

	isPermCmd := inter.CommandName == "allow_user" || inter.CommandName == "allow_role" ||
		inter.CommandName == "deny_user" || inter.CommandName == "deny_role"
	isSelfOnboard := inter.CommandName == "iamtheowner"
	switch {
	case isPermCmd:
		if role != types.RoleOwner {
			_ = o.bot.SendMessage(ctx, &OutgoingMessage{
				ChannelID: inter.ChannelID,
				Content:   "⛔ Only owners can manage permissions.",
			})
			return
		}
	case isSelfOnboard:
		// Bypass normal permission checks; handler validates bootstrap mode.
	case role == "":
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "⛔ You don't have permission to use this command.",
		})
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
	case "stop":
		o.handleStopInteraction(ctx, inter)
	case "readme":
		o.handleReadmeInteraction(ctx, inter)
	case "template-add":
		o.handleTemplateAddInteraction(ctx, inter)
	case "template-list":
		o.handleTemplateListInteraction(ctx, inter)
	case "allow_user":
		o.handlePermissionUpdate(ctx, inter, ch, "user", true)
	case "allow_role":
		o.handlePermissionUpdate(ctx, inter, ch, "role", true)
	case "deny_user":
		o.handlePermissionUpdate(ctx, inter, ch, "user", false)
	case "deny_role":
		o.handlePermissionUpdate(ctx, inter, ch, "role", false)
	case "iamtheowner":
		o.handleIAmTheOwner(ctx, inter, ch, cfgPerms, dbPerms)
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

func (o *Orchestrator) handleStatusInteraction(ctx context.Context, inter *Interaction) {
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   "Loop bot is running.",
	})
}

func (o *Orchestrator) handleStopInteraction(ctx context.Context, inter *Interaction) {
	targetChannelID := inter.ChannelID
	if v, ok := inter.Options["channel_id"]; ok && v != "" {
		targetChannelID = v
	}
	val, ok := o.activeRuns.LoadAndDelete(targetChannelID)
	if !ok {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "No active run to stop.",
		})
		return
	}
	cancel := val.(context.CancelFunc)
	cancel()
	o.logger.Info("run stopped by user", "channel_id", targetChannelID, "author_id", inter.AuthorID)
}

func (o *Orchestrator) handleReadmeInteraction(ctx context.Context, inter *Interaction) {
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   readme.Content,
	})
}

func (o *Orchestrator) handleTemplateAddInteraction(ctx context.Context, inter *Interaction) {
	name := inter.Options["name"]

	var tmpl *config.TaskTemplate
	for i := range o.cfg.TaskTemplates {
		if o.cfg.TaskTemplates[i].Name == name {
			tmpl = &o.cfg.TaskTemplates[i]
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

	prompt, err := tmpl.ResolvePrompt(o.cfg.LoopDir)
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
	if len(o.cfg.TaskTemplates) == 0 {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "No templates configured.",
		})
		return
	}

	var sb strings.Builder
	sb.WriteString("Available templates:\n")
	for _, t := range o.cfg.TaskTemplates {
		fmt.Fprintf(&sb, "- **%s** [%s] `%s` — %s\n", t.Name, t.Type, t.Schedule, t.Description)
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   sb.String(),
	})
}

// handlePermissionUpdate is a parameterized handler for allow_user, allow_role, deny_user, deny_role.
// targetType is "user" or "role"; allow true grants a role, false revokes all grants.
func (o *Orchestrator) handlePermissionUpdate(ctx context.Context, inter *Interaction, ch *db.Channel, targetType string, allow bool) {
	if ch == nil {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "⛔ Channel not registered.",
		})
		return
	}
	targetID := inter.Options["target_id"]
	perms := ch.Permissions

	// slicePtr returns a pointer to the Users or Roles slice in a ChannelRoleGrant.
	slicePtr := func(g *db.ChannelRoleGrant) *[]string {
		if targetType == "role" {
			return &g.Roles
		}
		return &g.Users
	}

	owners := slicePtr(&perms.Owners)
	members := slicePtr(&perms.Members)
	*owners = removeString(*owners, targetID)
	*members = removeString(*members, targetID)

	if allow {
		roleStr := inter.Options["role"]
		if roleStr == "" {
			roleStr = "member"
		}
		if roleStr == "owner" {
			*owners = appendUnique(*owners, targetID)
		} else {
			*members = appendUnique(*members, targetID)
		}
		if err := o.store.UpdateChannelPermissions(ctx, inter.ChannelID, perms); err != nil {
			o.logger.Error("updating channel permissions", "error", err)
			_ = o.bot.SendMessage(ctx, &OutgoingMessage{
				ChannelID: inter.ChannelID,
				Content:   "Failed to update permissions.",
			})
			return
		}
		mention := fmt.Sprintf("<@%s>", targetID)
		if targetType == "role" {
			mention = fmt.Sprintf("Role <@&%s>", targetID)
		}
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   fmt.Sprintf("✅ %s granted %s role.", mention, roleStr),
		})
		return
	}

	// Deny: remove from all lists (already done above).
	if err := o.store.UpdateChannelPermissions(ctx, inter.ChannelID, perms); err != nil {
		o.logger.Error("updating channel permissions", "error", err)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to update permissions.",
		})
		return
	}
	mention := fmt.Sprintf("<@%s>", targetID)
	if targetType == "role" {
		mention = fmt.Sprintf("Role <@&%s>", targetID)
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   fmt.Sprintf("✅ %s removed from channel permissions.", mention),
	})
}

func (o *Orchestrator) handleIAmTheOwner(ctx context.Context, inter *Interaction, ch *db.Channel, cfgPerms config.PermissionsConfig, dbPerms db.ChannelPermissions) {
	if !cfgPerms.IsEmpty() || !dbPerms.IsEmpty() {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "⛔ An owner is already configured. Use `/loop allow_user` to manage permissions.",
		})
		return
	}
	if ch == nil {
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "⛔ Channel not registered.",
		})
		return
	}
	perms := ch.Permissions
	perms.Owners.Users = appendUnique(perms.Owners.Users, inter.AuthorID)
	if err := o.store.UpdateChannelPermissions(ctx, inter.ChannelID, perms); err != nil {
		o.logger.Error("updating channel permissions", "error", err)
		_ = o.bot.SendMessage(ctx, &OutgoingMessage{
			ChannelID: inter.ChannelID,
			Content:   "Failed to update permissions.",
		})
		return
	}
	_ = o.bot.SendMessage(ctx, &OutgoingMessage{
		ChannelID: inter.ChannelID,
		Content:   fmt.Sprintf("✅ <@%s> is now the owner of this channel.", inter.AuthorID),
	})
}
