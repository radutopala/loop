package slack

import (
	"context"

	"github.com/radutopala/loop/internal/orchestrator"
)

// IncomingMessage is an alias for orchestrator.IncomingMessage.
type IncomingMessage = orchestrator.IncomingMessage

// OutgoingMessage is an alias for orchestrator.OutgoingMessage.
type OutgoingMessage = orchestrator.OutgoingMessage

// MessageHandler is a callback for incoming messages.
type MessageHandler = func(ctx context.Context, msg *IncomingMessage)

// InteractionHandler is a callback for slash command interactions.
type InteractionHandler = func(ctx context.Context, i any)

// ChannelDeleteHandler is a callback for channel deletion events.
type ChannelDeleteHandler = func(ctx context.Context, channelID string, isThread bool)

// ChannelJoinHandler is a callback for when the bot joins a channel.
type ChannelJoinHandler = func(ctx context.Context, channelID string)
