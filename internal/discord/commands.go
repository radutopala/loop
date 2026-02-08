package discord

import "github.com/bwmarrin/discordgo"

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
					Name:        "status",
					Description: "Show bot status",
				},
			},
		},
	}
}
