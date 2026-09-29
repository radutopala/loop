package orchestrator

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/explain"
	"github.com/radutopala/loop/internal/types"
)

// The author of the message that starts an explain run. The id tags the
// run's agent.status events with the "explain" trigger (see runTrigger) and
// links the run to its explanation (see explainRunStarted).
const explainAuthorID = "loop-explain"

// Explain explains the turn in ch that ended with bot message messageID: it
// returns the turn's explanation when there is one and force is false, and
// otherwise queues a new one in ch's hidden explain thread, created on first
// use. The run forks the session where the turn ended, so it sees the whole
// turn and not the ones that followed. Runs in the
// explain thread take turns on its queue, so one explanation of ch runs at
// a time. An explanation already queued or running is returned as it is.
func (o *Orchestrator) Explain(ctx context.Context, ch *db.Channel, messageID string, force bool) (*db.Explanation, error) {
	if explain.Unavailable(ch) != "" {
		return nil, explain.ErrUnavailable
	}
	if !force {
		e, err := o.store.GetExplanation(ctx, ch.ChannelID, messageID)
		if err != nil || e != nil {
			return e, err
		}
	}
	reply, err := o.store.GetChatMessage(ctx, ch.ChannelID, messageID)
	if err != nil {
		return nil, err
	}
	if reply == nil || !reply.IsBot || reply.TriggerMsgID == "" {
		return nil, explain.ErrNotATurn
	}
	if !canForkTurn(ch, reply) {
		return nil, explain.ErrNoSession
	}
	var prompt string
	if p, err := o.store.GetChatMessage(ctx, ch.ChannelID, reply.TriggerMsgID); err != nil {
		o.logger.Warn("explain: loading the turn's prompt", "error", err, "channel_id", ch.ChannelID)
	} else if p != nil {
		prompt = p.Content
	}

	x, err := o.ensureHiddenThread(ctx, ch, db.ChannelKindExplain)
	if err != nil {
		return nil, err
	}
	triggerID := generateMessageID()
	e, queued, err := o.store.QueueExplanation(ctx, &db.Explanation{
		ChannelID:        ch.ChannelID,
		MessageID:        messageID,
		ExplainChannelID: x.ChannelID,
		TriggerMsgID:     triggerID,
	})
	if err != nil {
		return nil, err
	}
	e.MessageRowID, e.Prompt, e.Reply = reply.ID, db.ExplainSnippet(prompt), db.ExplainSnippet(reply.Content)
	if !queued {
		return e, nil
	}
	o.logger.Info("explain: queued", "channel_id", ch.ChannelID, "explain_channel_id", x.ChannelID, "message_id", messageID)
	o.broadcastExplanation(e)
	o.HandleMessage(ctx, &bot.IncomingMessage{
		ChannelID:  x.ChannelID,
		GuildID:    x.GuildID,
		MessageID:  triggerID,
		AuthorID:   explainAuthorID,
		AuthorName: learnAuthorName,
		Content:    explain.TriggerMessage(ch.Name, prompt, reply.Content),
		HasPrefix:  true,
		Platform:   types.PlatformLocal,
		Timestamp:  o.timeNow().UTC(),
	})
	return e, nil
}

// explainSkipReason says why a finished run in ch isn't explained on its
// own, or "" when it should be: only a completed chat turn in a channel
// explanations run for (see explain.Unavailable), with its Explain switch
// on, a session to fork and not parked on a plan or question card is.
func explainSkipReason(ch *db.Channel, cfg config.ExplainConfig, parked bool) string {
	switch {
	case explain.Unavailable(ch) != "":
		return explain.Unavailable(ch)
	case parked:
		return "parked on a plan or question"
	case !ch.ExplainEnabled(cfg.Enabled):
		return "explain off"
	case ch.SessionID == "":
		return "no session"
	}
	return ""
}

// maybeExplain explains the turn msg started in ch, which just finished,
// when ch's Explain switch is on (see explainSkipReason).
func (o *Orchestrator) maybeExplain(ctx context.Context, ch *db.Channel, msg *bot.IncomingMessage) {
	if db.IsHiddenKind(ch.Kind) {
		return
	}
	// ch was loaded before the run: it may be gone since, its Explain
	// switch flipped, and its session is the one before the run.
	fresh, err := o.store.GetChannel(ctx, ch.ChannelID)
	if err != nil || fresh == nil {
		o.logger.Debug("explain: skipped", "channel_id", ch.ChannelID, "reason", "channel gone", "error", err)
		return
	}
	ch = fresh
	merged, _ := o.resolvedConfig(ctx, ch)
	parked := o.IsChannelPlanned(ch.ChannelID) || o.IsChannelAsked(ch.ChannelID)
	if reason := explainSkipReason(ch, merged.Explain, parked); reason != "" {
		o.logger.Debug("explain: skipped", "channel_id", ch.ChannelID, "reason", reason)
		return
	}
	reply, err := o.store.LastBotMessage(ctx, ch.ChannelID, msg.MessageID)
	if err != nil || reply == nil {
		o.logger.Debug("explain: skipped", "channel_id", ch.ChannelID, "reason", "no reply", "error", err)
		return
	}
	if _, err := o.Explain(ctx, ch, reply.MsgID, false); err != nil {
		o.logger.Error("explain: queueing", "error", err, "channel_id", ch.ChannelID)
	}
}

// applyExplainRequest turns req, built for an explain thread under parent,
// into an explain run: a read-only fork of the session of the turn that
// ended with bot message turnID (see forkAtTurn), with its own agent id and
// the explain system prompt. The model and effort are explain.model /
// explain.effort, else parent's overrides.
func (o *Orchestrator) applyExplainRequest(ctx context.Context, req *agent.AgentRequest, parent *db.Channel, turnID string) error {
	if !o.forkAtTurn(ctx, req, parent, turnID) {
		return explain.ErrNoSession
	}
	cfg, _ := o.resolvedConfig(ctx, parent)
	req.AgentID = explain.AgentID
	req.ReadOnly = true
	req.Model = cmp.Or(cfg.Explain.Model, parent.ModelOverride)
	req.Effort = cmp.Or(cfg.Explain.Effort, parent.EffortOverride)
	req.SystemPrompt = explain.SystemPrompt(cfg.Explain.Prompt)
	return nil
}

// explainRunStarted marks the explanation msg's run is for running and
// returns it, or nil when msg doesn't start an explain run or its
// explanation is gone.
func (o *Orchestrator) explainRunStarted(ctx context.Context, msg *bot.IncomingMessage) *db.Explanation {
	if msg.AuthorID != explainAuthorID {
		return nil
	}
	e, err := o.store.GetExplanationByTrigger(ctx, msg.ChannelID, msg.MessageID)
	if err != nil || e == nil {
		o.logger.Warn("explain: explanation not found for the run", "error", err, "explain_channel_id", msg.ChannelID)
		return nil
	}
	o.setExplanation(ctx, e, db.ExplainRunning, "", "")
	return e
}

// explainRunDone records how e's run ended: done with content, its final
// reply, or failed with runErr. A nil e isn't an explain run.
func (o *Orchestrator) explainRunDone(ctx context.Context, e *db.Explanation, content string, runErr error) {
	if e == nil {
		return
	}
	if runErr == nil && strings.TrimSpace(content) == "" {
		runErr = errors.New("the run ended without an explanation")
	}
	if runErr != nil {
		o.setExplanation(ctx, e, db.ExplainFailed, "", runErr.Error())
		return
	}
	o.setExplanation(ctx, e, db.ExplainDone, content, "")
}

// setExplanation stores e's new status, content and error and tells the
// chat.
func (o *Orchestrator) setExplanation(ctx context.Context, e *db.Explanation, status, content, errText string) {
	if err := o.store.UpdateExplanation(ctx, e.ID, status, content, errText); err != nil {
		o.logger.Error("explain: updating the explanation", "error", err, "id", e.ID)
		return
	}
	e.Status, e.Content, e.Error, e.UpdatedAt = status, content, errText, o.timeNow().UTC()
	o.broadcastExplanation(e)
}

func (o *Orchestrator) broadcastExplanation(e *db.Explanation) {
	if o.events != nil {
		o.events.BroadcastExplainUpdated(e)
	}
}

// errNotExplainRun is why an explain thread refuses a message that doesn't
// start an explain run: it runs nothing else.
func errNotExplainRun(channelID string) error {
	return fmt.Errorf("explain thread %s only runs explanations", channelID)
}
