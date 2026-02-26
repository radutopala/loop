package bot

import (
	"context"
	"time"
)

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

// Interaction represents a slash command interaction.
type Interaction struct {
	ChannelID   string
	GuildID     string
	CommandName string
	Options     map[string]string
	AuthorID    string   // user who invoked the command
	AuthorRoles []string // role IDs (Discord only)
}

// MessageHandler is a callback for incoming messages.
type MessageHandler = func(ctx context.Context, msg *IncomingMessage)

// InteractionHandler is a callback for slash command interactions.
type InteractionHandler = func(ctx context.Context, i *Interaction)

// ChannelDeleteHandler is a callback for channel/thread deletion events.
type ChannelDeleteHandler = func(ctx context.Context, channelID string, isThread bool)

// ChannelJoinHandler is a callback for when the bot joins a channel.
type ChannelJoinHandler = func(ctx context.Context, channelID string)
