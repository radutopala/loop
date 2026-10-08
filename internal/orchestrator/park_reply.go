package orchestrator

import (
	"context"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
)

// isChatPlatform reports whether a platform is a chat platform (Slack,
// Discord) rather than the desktop app, which resolves cards through its own
// endpoints.
func isChatPlatform(p types.Platform) bool {
	return p == types.PlatformSlack || p == types.PlatformDiscord
}

// cardClosedReply answers a click on a card that is no longer open.
const cardClosedReply = "That card is no longer open."

// openCardID returns the ID of the ask or plan card a channel is parked on,
// or "" when it is not parked.
func (o *Orchestrator) openCardID(channelID string) string {
	if v, ok := o.askedChannels.Load(channelID); ok {
		data, _ := v.(events.AskUserQuestionEventData)
		return data.ToolUseID
	}
	if v, ok := o.plannedChannels.Load(channelID); ok {
		data, _ := v.(events.ExitPlanModeEventData)
		return data.ToolUseID
	}
	return ""
}

// normalizeReply lowercases a reply and drops surrounding spaces and trailing
// punctuation so "Approve!" matches the approve keyword.
func normalizeReply(s string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(s)), ".! ")
}

// askOptionLabels maps a reply naming option numbers of a single question
// ("2", or "1, 3" when it allows several) to the options' labels. It returns
// nil for any other reply.
func askOptionLabels(data events.AskUserQuestionEventData, reply string) []string {
	if len(data.Questions) != 1 {
		return nil
	}
	q := data.Questions[0]
	fields := strings.FieldsFunc(reply, func(r rune) bool { return r == ',' || r == ' ' })
	if len(fields) == 0 || (len(fields) > 1 && !q.MultiSelect) {
		return nil
	}
	labels := make([]string, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > len(q.Options) {
			return nil
		}
		labels = append(labels, q.Options[n-1].Label)
	}
	return labels
}

// askAnswerFromReply maps a reply to the answer text sent to the agent. A
// reply naming option numbers becomes the same "Q:/A:" text the desktop card
// sends; anything else is passed through as written.
func askAnswerFromReply(data events.AskUserQuestionEventData, reply string) string {
	labels := askOptionLabels(data, reply)
	if labels == nil {
		return reply
	}
	return "Here are my answers:\n\nQ: " + data.Questions[0].Question + "\nA: " + strings.Join(labels, ", ")
}

// resolveParkFromReply resolves a chat channel parked on an ask or plan card
// with the user's reply, typed or clicked, the way answering on the desktop
// does, and closes the card's buttons. It rewrites msg into the continuation
// to run (answer, approval, or requested changes) and reports run=true, or
// reports run=false when the reply only dropped the card (skip, reject) and
// should be kept as plain history. handled is false when the channel is not
// parked.
func (o *Orchestrator) resolveParkFromReply(ctx context.Context, msg *bot.IncomingMessage) (handled, run bool) {
	asked, isAsked := o.askedChannels.Load(msg.ChannelID)
	planned, isPlanned := o.plannedChannels.Load(msg.ChannelID)
	if !isAsked && !isPlanned {
		return false, false
	}
	reply := normalizeReply(msg.Content)

	var cardID, outcome string
	defer func() {
		if err := o.bot.CloseCard(ctx, msg.ChannelID, cardID, outcome, msg.AuthorID); err != nil {
			o.logger.Error("closing card", "error", err, "channel_id", msg.ChannelID)
		}
	}()

	if isAsked {
		data, _ := asked.(events.AskUserQuestionEventData)
		cardID = data.ToolUseID
		mode := o.AskedChannelMode(msg.ChannelID)
		o.ClearAskedChannel(msg.ChannelID)
		if reply == bot.AskReplySkip {
			outcome = "Skipped"
			return true, false
		}
		outcome = "Answered"
		if labels := askOptionLabels(data, msg.Content); labels != nil {
			outcome = strings.Join(labels, ", ")
		}
		msg.Content = askAnswerFromReply(data, msg.Content)
		msg.Mode = mode
	} else {
		data, _ := planned.(events.ExitPlanModeEventData)
		cardID = data.ToolUseID
		o.ClearPlannedChannel(msg.ChannelID)
		switch reply {
		case bot.PlanReplyReject:
			outcome = "Rejected"
			return true, false
		case bot.PlanReplyApprove:
			outcome = "Approved"
			msg.Content = events.PlanApprovePrompt(data.PlanFilePath)
			msg.Mode = ""
		default:
			outcome = "Changes requested"
			msg.Mode = "plan"
		}
	}

	// Run ahead of anything queued while the card was up, like the desktop
	// resolve endpoints do.
	if p, err := o.store.MaxQueuedPriority(ctx, msg.ChannelID); err == nil {
		msg.Priority = p + 1
	}
	return true, true
}
