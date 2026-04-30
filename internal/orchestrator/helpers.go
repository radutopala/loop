package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// storeBotMessage generates an ID, persists a bot message in the database,
// and broadcasts a message.created event. Either store or broadcaster may be nil.
func storeBotMessage(ctx context.Context, store db.Store, broadcaster events.Broadcaster, channelID, content string) {
	msgID := generateMessageID()
	if store != nil {
		ch, err := store.GetChannel(ctx, channelID)
		if err == nil && ch != nil {
			_ = store.InsertMessage(ctx, &db.Message{
				ChatID:      ch.ID,
				ChannelID:   channelID,
				MsgID:       msgID,
				AuthorName:  "agent",
				Content:     content,
				IsBot:       true,
				IsProcessed: true,
				CreatedAt:   time.Now().UTC(),
			})
		}
	}
	if broadcaster != nil {
		broadcaster.BroadcastMessageCreated(channelID, events.MessageEventData{
			MsgID:       msgID,
			AuthorName:  "agent",
			Content:     content,
			IsBot:       true,
			IsProcessed: true,
		})
	}
}

// storeAgentEvent inserts an agent-event row (thinking, tool_use, tool_result)
// for a channel. The row is assigned a fresh chain_position atomically so
// reload-time renders place it after every prior row in the channel. The caller
// must supply the numeric chatID (Channel.ID) so this hot path doesn't issue a
// GetChannel per event. Errors are logged but never propagated — SSE broadcast
// happens regardless so the live UI is never blocked on DB writes.
func storeAgentEvent(ctx context.Context, store db.Store, chatID int64, channelID string, evt *db.Message, logFn func(msg string, args ...any)) {
	if store == nil || chatID == 0 {
		return
	}
	evt.ChatID = chatID
	evt.ChannelID = channelID
	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = time.Now().UTC()
	}
	if err := store.InsertAgentEvent(ctx, evt); err != nil && logFn != nil {
		logFn("inserting agent event", "error", err, "kind", evt.Kind, "channel_id", channelID)
	}
}

// formatMention formats a Discord mention for a user or role.
func formatMention(targetID, targetType string) string {
	if targetType == "role" {
		return fmt.Sprintf("Role <@&%s>", targetID)
	}
	return fmt.Sprintf("<@%s>", targetID)
}
