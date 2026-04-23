package bot

import (
	"context"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// ApprovalPrompt is the bot-facing payload for a gate approval prompt.
// Kind is a short label ("connect", "execve", "docker-http", ...), Target is
// the human-readable action being approved, Message is the matching rule's
// message (may be empty). Details holds optional structured key/value pairs
// (e.g. image, binds, privileged for a docker create) the bot can render
// alongside Target. The bot implementation renders three buttons whose
// identifiers follow "gate:<ID>:<decision>" where decision is one of
// "once" | "session" | "deny".
type ApprovalPrompt struct {
	ID      string
	Kind    string
	Target  string
	Message string
	Details map[string]string
}

// ApprovalResolver receives a user's decision on an agentgate approval prompt
// and routes it to the Manager holding the pending request. Typically backed
// by agentgate.MultiManagerResolver so a single bot can dispatch clicks to
// any of the running per-container Managers.
type ApprovalResolver interface {
	Resolve(reqID, decision, actorID string) error
}

// IncomingMessage from the chat platform.
type IncomingMessage struct {
	ChannelID    string
	GuildID      string
	AuthorID     string
	AuthorName   string
	Content      string
	MessageID    string
	Platform     types.Platform
	IsBotMention bool
	IsReplyToBot bool
	HasPrefix    bool
	IsDM         bool
	Timestamp    time.Time
	AuthorRoles  []string // role IDs for permission checking (Discord only)
	Mode         string   // "plan" or "" (default = agent)
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
	AuthorID    string         // user who invoked the command
	AuthorRoles []string       // role IDs (Discord only)
	Platform    types.Platform // set by the bot that received the interaction
}

// MessageHandler is a callback for incoming messages.
type MessageHandler = func(ctx context.Context, msg *IncomingMessage)

// InteractionHandler is a callback for slash command interactions.
type InteractionHandler = func(ctx context.Context, i *Interaction)

// ChannelDeleteHandler is a callback for channel/thread deletion events.
type ChannelDeleteHandler = func(ctx context.Context, channelID string, isThread bool)

// ChannelJoinHandler is a callback for when the bot joins a channel.
type ChannelJoinHandler = func(ctx context.Context, channelID string, platform types.Platform)
