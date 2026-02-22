package slack

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/orchestrator"
)

// parseSlashCommand parses the text from a /loop slash command into an Interaction.
// Format: /loop <subcommand> [args...]
//
// Supported subcommands:
//
//	schedule <schedule> <type> <prompt>
//	tasks
//	cancel <task_id>
//	toggle <task_id>
//	edit <task_id> [--schedule X] [--type Y] [--prompt Z]
//	status
//	stop
//	template add <name>
//	template list
//	allow user <@U...> [owner|member]
//	deny user <@U...>
//	iamtheowner
func parseSlashCommand(channelID, teamID, text string) (*orchestrator.Interaction, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, helpText()
	}

	parts := strings.Fields(text)
	subcommand := strings.ToLower(parts[0])
	args := parts[1:]

	inter := &orchestrator.Interaction{
		ChannelID: channelID,
		GuildID:   teamID,
		Options:   make(map[string]string),
	}

	switch subcommand {
	case "schedule":
		return parseSchedule(inter, args)
	case "tasks":
		inter.CommandName = "tasks"
		return inter, ""
	case "cancel":
		return parseCancel(inter, args)
	case "toggle":
		return parseToggle(inter, args)
	case "edit":
		return parseEdit(inter, args)
	case "status":
		inter.CommandName = "status"
		return inter, ""
	case "stop":
		inter.CommandName = "stop"
		return inter, ""
	case "template":
		return parseTemplate(inter, args)
	case "allow":
		return parseAllow(inter, args)
	case "deny":
		return parseDeny(inter, args)
	case "iamtheowner":
		inter.CommandName = "iamtheowner"
		return inter, ""
	default:
		return nil, fmt.Sprintf("Unknown subcommand: %s\n\n%s", subcommand, helpText())
	}
}

// parseSchedule parses: schedule <schedule> <type> <prompt>
// The type must be one of: cron, interval, once
func parseSchedule(inter *orchestrator.Interaction, args []string) (*orchestrator.Interaction, string) {
	if len(args) < 3 {
		return nil, "Usage: `/loop schedule <schedule> <type> <prompt>`\n" +
			"Types: cron, interval, once\n" +
			"Examples:\n" +
			"  `/loop schedule \"0 9 * * *\" cron Check for updates`\n" +
			"  `/loop schedule 1h interval Run health check`\n" +
			"  `/loop schedule 2026-02-15T10:00:00Z once Deploy release`"
	}

	inter.CommandName = "schedule"

	// Find the type argument (cron, interval, once) to split schedule from prompt.
	// The schedule can contain spaces (e.g., cron expressions "0 9 * * *"),
	// so we search for the type keyword from left to right.
	typeIdx := -1
	for i, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "cron" || lower == "interval" || lower == "once" {
			typeIdx = i
			break
		}
	}

	if typeIdx < 1 || typeIdx >= len(args)-1 {
		return nil, "Usage: `/loop schedule <schedule> <type> <prompt>`\nType must be one of: cron, interval, once"
	}

	inter.Options["schedule"] = strings.Join(args[:typeIdx], " ")
	inter.Options["type"] = strings.ToLower(args[typeIdx])
	inter.Options["prompt"] = strings.Join(args[typeIdx+1:], " ")

	return inter, ""
}

// parseCancel parses: cancel <task_id>
func parseCancel(inter *orchestrator.Interaction, args []string) (*orchestrator.Interaction, string) {
	if len(args) != 1 {
		return nil, "Usage: `/loop cancel <task_id>`"
	}
	if _, err := strconv.ParseInt(args[0], 10, 64); err != nil {
		return nil, "Invalid task_id: must be an integer"
	}
	inter.CommandName = "cancel"
	inter.Options["task_id"] = args[0]
	return inter, ""
}

// parseToggle parses: toggle <task_id>
func parseToggle(inter *orchestrator.Interaction, args []string) (*orchestrator.Interaction, string) {
	if len(args) != 1 {
		return nil, "Usage: `/loop toggle <task_id>`"
	}
	if _, err := strconv.ParseInt(args[0], 10, 64); err != nil {
		return nil, "Invalid task_id: must be an integer"
	}
	inter.CommandName = "toggle"
	inter.Options["task_id"] = args[0]
	return inter, ""
}

// parseEdit parses: edit <task_id> [--schedule X] [--type Y] [--prompt Z]
func parseEdit(inter *orchestrator.Interaction, args []string) (*orchestrator.Interaction, string) {
	if len(args) < 1 {
		return nil, "Usage: `/loop edit <task_id> [--schedule X] [--type Y] [--prompt Z]`"
	}
	if _, err := strconv.ParseInt(args[0], 10, 64); err != nil {
		return nil, "Invalid task_id: must be an integer"
	}
	inter.CommandName = "edit"
	inter.Options["task_id"] = args[0]

	// Parse optional flags
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--schedule":
			if i+1 >= len(rest) {
				return nil, "--schedule requires a value"
			}
			i++
			inter.Options["schedule"] = rest[i]
		case "--type":
			if i+1 >= len(rest) {
				return nil, "--type requires a value"
			}
			i++
			inter.Options["type"] = rest[i]
		case "--prompt":
			if i+1 >= len(rest) {
				return nil, "--prompt requires a value"
			}
			// Everything after --prompt is the prompt text
			inter.Options["prompt"] = strings.Join(rest[i+1:], " ")
			return inter, ""
		}
	}

	if len(inter.Options) == 1 { // only task_id
		return nil, "At least one of --schedule, --type, or --prompt is required"
	}

	return inter, ""
}

// parseTemplate parses: template add <name> | template list
func parseTemplate(inter *orchestrator.Interaction, args []string) (*orchestrator.Interaction, string) {
	if len(args) < 1 {
		return nil, "Usage: `/loop template add <name>` or `/loop template list`"
	}
	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "add":
		if len(args) < 2 {
			return nil, "Usage: `/loop template add <name>`"
		}
		inter.CommandName = "template-add"
		inter.Options["name"] = args[1]
		return inter, ""
	case "list":
		inter.CommandName = "template-list"
		return inter, ""
	default:
		return nil, "Usage: `/loop template add <name>` or `/loop template list`"
	}
}

// parseAllow parses: allow user <@USERID> [owner|member]
func parseAllow(inter *orchestrator.Interaction, args []string) (*orchestrator.Interaction, string) {
	if len(args) < 2 {
		return nil, "Usage: `/loop allow user <@U...> [owner|member]`"
	}
	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "user":
		targetID := extractUserID(args[1])
		if targetID == "" {
			return nil, "Invalid user: must be a mention like <@U123456>"
		}
		roleStr := "member"
		if len(args) >= 3 {
			r := strings.ToLower(args[2])
			if r == "owner" || r == "member" {
				roleStr = r
			}
		}
		inter.CommandName = "allow_user"
		inter.Options["target_id"] = targetID
		inter.Options["role"] = roleStr
		return inter, ""
	case "role":
		return nil, "allow role is Discord-only (role-based permissions are not supported on Slack)"
	default:
		return nil, "Usage: `/loop allow user <@U...> [owner|member]`"
	}
}

// parseDeny parses: deny user <@USERID>
func parseDeny(inter *orchestrator.Interaction, args []string) (*orchestrator.Interaction, string) {
	if len(args) < 2 {
		return nil, "Usage: `/loop deny user <@U...>`"
	}
	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "user":
		targetID := extractUserID(args[1])
		if targetID == "" {
			return nil, "Invalid user: must be a mention like <@U123456>"
		}
		inter.CommandName = "deny_user"
		inter.Options["target_id"] = targetID
		return inter, ""
	case "role":
		return nil, "deny role is Discord-only (role-based permissions are not supported on Slack)"
	default:
		return nil, "Usage: `/loop deny user <@U...>`"
	}
}

// extractUserID extracts a Slack user ID from a mention like <@U123456> or <@U123456|username>.
func extractUserID(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "<@") || !strings.HasSuffix(s, ">") {
		return ""
	}
	inner := s[2 : len(s)-1]
	// Remove optional |username suffix
	if idx := strings.Index(inner, "|"); idx != -1 {
		inner = inner[:idx]
	}
	return inner
}

func helpText() string {
	return "Available commands:\n" +
		"  `/loop schedule <schedule> <type> <prompt>` - Create a scheduled task\n" +
		"  `/loop tasks` - List all scheduled tasks\n" +
		"  `/loop cancel <task_id>` - Cancel a scheduled task\n" +
		"  `/loop toggle <task_id>` - Toggle a task on/off\n" +
		"  `/loop edit <task_id> [--schedule X] [--type Y] [--prompt Z]` - Edit a task\n" +
		"  `/loop status` - Show bot status\n" +
		"  `/loop stop` - Stop the currently running agent\n" +
		"  `/loop template add <name>` - Load a task template\n" +
		"  `/loop template list` - List available templates\n" +
		"  `/loop allow user <@U...> [owner|member]` - Grant user a role\n" +
		"  `/loop deny user <@U...>` - Remove user's role\n" +
		"  `/loop iamtheowner` - Claim ownership (initial setup only)"
}
