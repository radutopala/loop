package orchestrator

import (
	"context"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/bot"
)

// GateBotAdapter implements agentgate.Bot on top of an orchestrator.Bot,
// translating agentgate.ApprovalRequest into the transport-agnostic
// bot.ApprovalPrompt payload. The orchestrator.Bot is responsible for the
// actual rendering (Discord buttons, Slack blocks, Local event, etc.).
type GateBotAdapter struct {
	Bot Bot
}

// SendApproval forwards the prompt to the underlying bot.
func (a *GateBotAdapter) SendApproval(ctx context.Context, channelID string, req agentgate.ApprovalRequest) (string, error) {
	return a.Bot.SendApproval(ctx, channelID, bot.ApprovalPrompt{
		ID:        req.ID,
		Kind:      req.Kind,
		Target:    req.Target,
		Source:    req.Source,
		Message:   req.Message,
		Details:   req.Details,
		ExpiresAt: req.ExpiresAt,
	})
}

// RemoveApproval forwards the removal request.
func (a *GateBotAdapter) RemoveApproval(ctx context.Context, channelID, messageID string) error {
	return a.Bot.RemoveApproval(ctx, channelID, messageID)
}

// GateBotRouter wraps an orchestrator.Bot (typically a *BotRouter that already
// routes per-channel) as an agentgate.BotRouter. Channel-to-platform dispatch
// happens inside the wrapped bot; For always returns the same adapter.
type GateBotRouter struct {
	Bot Bot
}

// For satisfies agentgate.BotRouter.
func (r *GateBotRouter) For(string) agentgate.Bot {
	if r.Bot == nil {
		return nil
	}
	return &GateBotAdapter{Bot: r.Bot}
}
