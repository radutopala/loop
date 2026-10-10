package orchestrator

import (
	"context"
	"fmt"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
)

// hiddenKind shapes the runs of one kind of hidden thread. A hidden thread
// never runs as a plain chat: a run there is whatever its kind makes of it,
// and a kind with no hiddenKind refuses every run.
type hiddenKind struct {
	// name labels the kind in errors and logs.
	name string
	// admit, when set, returns an error for a message that must not run in
	// the thread at all.
	admit func(threadID string, msg *bot.IncomingMessage) error
	// apply shapes req for a run in the thread; parent is the thread's
	// parent channel.
	apply func(ctx context.Context, req *agent.AgentRequest, msg *bot.IncomingMessage, parent *db.Channel, turnID string) error
}

// defaultHiddenKinds returns the hidden kinds Loop itself runs, keyed by
// channel kind.
func (o *Orchestrator) defaultHiddenKinds() map[string]hiddenKind {
	return map[string]hiddenKind{
		// A learn thread only ever runs read-only, as a learn pass or a
		// user's reply to one.
		db.ChannelKindLearn: {
			name: "learn",
			apply: func(ctx context.Context, req *agent.AgentRequest, msg *bot.IncomingMessage, parent *db.Channel, turnID string) error {
				return o.applyLearnRequest(ctx, req, parent, msg.AuthorID == learnAuthorID, turnID)
			},
		},
		// An explain thread only ever runs explanations, each a read-only
		// fork of its parent's session.
		db.ChannelKindExplain: {
			name: "explain",
			admit: func(threadID string, msg *bot.IncomingMessage) error {
				if msg.AuthorID != explainAuthorID {
					return errNotExplainRun(threadID)
				}
				return nil
			},
			apply: func(ctx context.Context, req *agent.AgentRequest, _ *bot.IncomingMessage, parent *db.Channel, turnID string) error {
				return o.applyExplainRequest(ctx, req, parent, turnID)
			},
		},
	}
}

// lookupHiddenKind returns the hiddenKind for a hidden thread's kind. Every
// plugin's kind shares the one keyed by db.ChannelKindPluginPrefix.
func (o *Orchestrator) lookupHiddenKind(kind string) (hiddenKind, bool) {
	if _, ok := db.PluginOfKind(kind); ok {
		kind = db.ChannelKindPluginPrefix
	}
	h, ok := o.hiddenKinds[kind]
	return h, ok
}

// shapeHiddenRun shapes req for a run of msg in hidden thread ch, whose
// parent is parent (nil when it's gone, parentErr then saying why), or
// refuses the run.
func (o *Orchestrator) shapeHiddenRun(ctx context.Context, req *agent.AgentRequest, msg *bot.IncomingMessage, ch, parent *db.Channel, parentErr error, turnID string) error {
	h, ok := o.lookupHiddenKind(ch.Kind)
	if !ok {
		return fmt.Errorf("hidden thread %s: nothing runs %q threads", ch.ChannelID, ch.Kind)
	}
	if h.admit != nil {
		if err := h.admit(ch.ChannelID, msg); err != nil {
			return err
		}
	}
	// Without its parent there's nothing to set up, and the thread must not
	// run as a chat with the full tool set instead.
	if parent == nil {
		o.logger.Error(h.name+": loading the "+h.name+" thread's parent", "error", parentErr, "channel_id", ch.ChannelID, "parent_id", ch.ParentID)
		return fmt.Errorf("%s thread %s: parent %q not found", h.name, ch.ChannelID, ch.ParentID)
	}
	return h.apply(ctx, req, msg, parent, turnID)
}
