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

// formatMention formats a Discord mention for a user or role.
func formatMention(targetID, targetType string) string {
	if targetType == "role" {
		return fmt.Sprintf("Role <@&%s>", targetID)
	}
	return fmt.Sprintf("<@%s>", targetID)
}
