package discord

import (
	"github.com/bwmarrin/discordgo"
)

// Commands returns the slash command definitions for the bot.
func Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "loop",
			Description: "Loop AI assistant commands",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "schedule",
					Description: "Schedule a task",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "schedule",
							Description: "Schedule expression (cron, interval, or timestamp)",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "prompt",
							Description: "The prompt to execute",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "type",
							Description: "Schedule type",
							Required:    true,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "cron", Value: "cron"},
								{Name: "interval", Value: "interval"},
								{Name: "once", Value: "once"},
								{Name: "manual", Value: "manual"},
							},
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "tasks",
					Description: "List scheduled tasks",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "task",
					Description: "Show full details of a scheduled task",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionInteger,
							Name:        "task_id",
							Description: "ID of the task to show",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "cancel",
					Description: "Cancel a scheduled task",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionInteger,
							Name:        "task_id",
							Description: "ID of the task to cancel",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "toggle",
					Description: "Toggle a scheduled task on or off",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionInteger,
							Name:        "task_id",
							Description: "The ID of the task to toggle",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "edit",
					Description: "Edit a scheduled task",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionInteger,
							Name:        "task_id",
							Description: "The ID of the task to edit",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "schedule",
							Description: "New schedule expression (cron or duration)",
							Required:    false,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "type",
							Description: "New schedule type",
							Required:    false,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "cron", Value: "cron"},
								{Name: "interval", Value: "interval"},
								{Name: "once", Value: "once"},
								{Name: "manual", Value: "manual"},
							},
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "prompt",
							Description: "New prompt to execute",
							Required:    false,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "shortcuts",
					Description: "List available prompt shortcuts",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "shortcut",
					Description: "Execute a prompt shortcut",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "name",
							Description: "Name of the shortcut to execute",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
					Name:        "workflow",
					Description: "Manage workflows",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "list",
							Description: "List available workflow definitions",
						},
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "runs",
							Description: "List recent workflow runs",
						},
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "run",
							Description: "Start a workflow run",
							Options: []*discordgo.ApplicationCommandOption{
								{
									Type:        discordgo.ApplicationCommandOptionString,
									Name:        "name",
									Description: "Name of the workflow to run",
									Required:    true,
								},
							},
						},
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "cancel",
							Description: "Cancel a running workflow",
							Options: []*discordgo.ApplicationCommandOption{
								{
									Type:        discordgo.ApplicationCommandOptionString,
									Name:        "run_id",
									Description: "Workflow run ID to cancel",
									Required:    true,
								},
							},
						},
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "delete",
							Description: "Delete a workflow run",
							Options: []*discordgo.ApplicationCommandOption{
								{
									Type:        discordgo.ApplicationCommandOptionString,
									Name:        "run_id",
									Description: "Workflow run ID to delete",
									Required:    true,
								},
							},
						},
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "retry",
							Description: "Retry a failed or completed workflow run",
							Options: []*discordgo.ApplicationCommandOption{
								{
									Type:        discordgo.ApplicationCommandOptionString,
									Name:        "run_id",
									Description: "Workflow run ID to retry",
									Required:    true,
								},
							},
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "status",
					Description: "Show bot status",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "stop",
					Description: "Stop the currently running agent",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "readme",
					Description: "Show the README documentation",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
					Name:        "template",
					Description: "Manage task templates",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "add",
							Description: "Load a task template into this channel",
							Options: []*discordgo.ApplicationCommandOption{
								{
									Type:        discordgo.ApplicationCommandOptionString,
									Name:        "name",
									Description: "Template name from config",
									Required:    true,
								},
							},
						},
						{
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Name:        "list",
							Description: "List available task templates",
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "allow_user",
					Description: "Grant a user owner or member role in this channel",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionUser,
							Name:        "target_id",
							Description: "The user to grant a role to",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "role",
							Description: "Role to grant (default: member)",
							Required:    false,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "owner", Value: "owner"},
								{Name: "member", Value: "member"},
							},
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "allow_role",
					Description: "Grant a Discord role owner or member access in this channel",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionRole,
							Name:        "target_id",
							Description: "The Discord role to grant access to",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "role",
							Description: "Role to grant (default: member)",
							Required:    false,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "owner", Value: "owner"},
								{Name: "member", Value: "member"},
							},
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "deny_user",
					Description: "Remove a user's DB-granted role from this channel",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionUser,
							Name:        "target_id",
							Description: "The user to remove",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "deny_role",
					Description: "Remove a Discord role's DB-granted access from this channel",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionRole,
							Name:        "target_id",
							Description: "The Discord role to remove",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "iamtheowner",
					Description: "Claim ownership of this channel (only works during initial setup)",
				},
			},
		},
	}
}
