package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/promptedit"
	"github.com/radutopala/loop/internal/types"
)

// The author of the message that starts an edit by voice. The id tags the
// run's agent.status events with the "prompt-edit" trigger (see runTrigger)
// and is the only author a prompt-edit thread runs.
const (
	promptEditAuthorID   = "loop-prompt-edit"
	promptEditAuthorName = "voice edit"
)

// promptEditRun is an edit PromptEdit queued, waiting for its run's reply.
type promptEditRun struct {
	edit     *promptedit.Edit
	threadID string
	// done gets the run's reply, or why it failed; it holds one, so the run
	// never waits for an edit given up on.
	done chan promptEditOutcome
}

type promptEditOutcome struct {
	reply string
	err   error
}

// errPromptEditDropped fails an edit whose message was dropped before it
// ran, such as after a restart.
var errPromptEditDropped = errors.New("the edit was dropped before it ran")

// PromptEdit rewrites a composer draft as req's instruction asks, with a
// read-only run in ch's hidden prompt-edit thread (created on first use) on
// ch's model and effort, and returns the new draft. The run's chat stays in
// that thread to look back at. When ctx ends first, the run is stopped (or
// leaves the queue) and PromptEdit returns ctx's error. It fails with
// promptedit.ErrNoInstruction when there is no instruction.
func (o *Orchestrator) PromptEdit(ctx context.Context, ch *db.Channel, req promptedit.Request) (*promptedit.Result, error) {
	edit, err := promptedit.Prepare(req)
	if err != nil {
		return nil, err
	}
	h, err := o.ensureHiddenThread(ctx, ch, db.ChannelKindPromptEdit)
	if err != nil {
		return nil, err
	}
	runID := generateMessageID()
	run := &promptEditRun{edit: edit, threadID: h.ChannelID, done: make(chan promptEditOutcome, 1)}
	o.promptEdits.Store(runID, run)
	o.HandleMessage(ctx, &bot.IncomingMessage{
		ChannelID:  h.ChannelID,
		GuildID:    h.GuildID,
		MessageID:  runID,
		AuthorID:   promptEditAuthorID,
		AuthorName: promptEditAuthorName,
		Content:    edit.Prompt,
		HasPrefix:  true,
		Platform:   types.PlatformLocal,
		Timestamp:  o.timeNow().UTC(),
	})
	select {
	case out := <-run.done:
		if out.err != nil {
			return nil, out.err
		}
		res := edit.Apply(out.reply)
		return &res, nil
	case <-ctx.Done():
		o.cancelPromptEdit(context.WithoutCancel(ctx), runID, run)
		return nil, ctx.Err()
	}
}

// cancelPromptEdit gives up on the edit runID: a queued one leaves the
// queue, a running one is stopped. Forgetting it first keeps a run about to
// start from being admitted.
func (o *Orchestrator) cancelPromptEdit(ctx context.Context, runID string, run *promptEditRun) {
	o.promptEdits.Delete(runID)
	deleted, err := o.store.DeleteQueuedMessage(ctx, run.threadID, runID)
	if err != nil {
		o.logger.Warn("prompt edit: dropping the queued edit", "error", err, "thread_id", run.threadID)
	}
	if deleted {
		if o.events != nil {
			o.events.BroadcastMessageDeleted(run.threadID, events.MessageDeletedData{MsgID: runID})
		}
		return
	}
	if v, ok := o.activeRunMsgIDs.Load(run.threadID); ok && v == runID {
		o.CancelActiveRun(run.threadID)
	}
}

// admitPromptEdit refuses a message in prompt-edit thread threadID that
// isn't an edit PromptEdit queued there and still waits for.
func (o *Orchestrator) admitPromptEdit(threadID string, msg *bot.IncomingMessage) error {
	if msg.AuthorID == promptEditAuthorID {
		if v, ok := o.promptEdits.Load(msg.MessageID); ok && v.(*promptEditRun).threadID == threadID {
			return nil
		}
		return fmt.Errorf("prompt-edit thread %s: %w", threadID, errPromptEditDropped)
	}
	return fmt.Errorf("prompt-edit thread %s: only edits by voice run here", threadID)
}

// applyPromptEditRequest shapes req for the edit msg starts, admitted by
// admitPromptEdit: read-only, in a session of its own, on parent's model
// and effort, with the edit's own message.
func (o *Orchestrator) applyPromptEditRequest(req *agent.AgentRequest, msg *bot.IncomingMessage, parent *db.Channel) error {
	v, ok := o.promptEdits.Load(msg.MessageID)
	if !ok {
		return errPromptEditDropped
	}
	req.AgentID = promptedit.AgentID
	req.ReadOnly = true
	req.SessionID, req.ForkSession, req.ResumeAt = "", false, ""
	req.Model = parent.ModelOverride
	req.Effort = parent.EffortOverride
	req.SystemPrompt = promptedit.SystemPrompt()
	req.Prompt = v.(*promptEditRun).edit.Prompt
	return nil
}

// promptEditDone hands the edit msg started its run's reply, or runErr,
// once the run has ended or was dropped.
func (o *Orchestrator) promptEditDone(msg *bot.IncomingMessage, reply string, runErr error) {
	if msg.AuthorID != promptEditAuthorID {
		return
	}
	if v, ok := o.promptEdits.LoadAndDelete(msg.MessageID); ok {
		v.(*promptEditRun).done <- promptEditOutcome{reply: reply, err: runErr}
	}
}
