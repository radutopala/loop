package orchestrator

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/explain"
	"github.com/radutopala/loop/internal/learn"
	"github.com/radutopala/loop/internal/randutil"
	"github.com/radutopala/loop/internal/types"
)

// The author of the message that starts a learn pass. The id tags the
// pass's agent.status events with the "learn" trigger (see runTrigger).
const (
	learnAuthorID   = "loop-learn"
	learnAuthorName = "loop"
)

// resolvedConfig resolves ch's merged config the way its runs do: global →
// root checkout → worktree for a worktree chain, global → ch's dir
// otherwise. It returns the config and the root checkout, where config
// proposals land: for a worktree chain the nearest non-worktree ancestor's
// directory, else ch's own. It falls back to the global config when ch has
// no dir or the project config fails to load.
func (o *Orchestrator) resolvedConfig(ctx context.Context, ch *db.Channel) (*config.Config, string) {
	cfg := o.currentConfig()
	if ch.DirPath == "" {
		return cfg, ""
	}
	root := worktreeRootFor(ctx, o.store, ch)
	var (
		merged *config.Config
		err    error
	)
	if root != "" {
		merged, err = o.loadWorktreeProjectConfig(ch.DirPath, root, cfg)
	} else {
		root = ch.DirPath
		merged, err = o.loadProjectConfig(root, cfg)
	}
	if err != nil {
		o.logger.Warn("loading project config", "error", err, "dir", ch.DirPath)
		return cfg, root
	}
	return merged, root
}

// learnSkipReason says why a finished run in ch doesn't start a learn pass,
// or "" when it should. Only a completed chat turn in a desktop channel
// that's on, long enough and not parked on a plan or question card learns;
// task threads never do (nor learn threads, which maybeLearn turns away
// first), and Slack or Discord channels don't either, since their proposals
// can only be seen in the desktop app.
func learnSkipReason(ch *db.Channel, resp *agent.AgentResponse, cfg config.LearnConfig, parked bool) string {
	switch {
	case ch.Platform != types.PlatformLocal:
		return "not a desktop channel"
	case ch.TaskID != 0:
		return "task thread"
	case parked:
		return "parked on a plan or question"
	case !ch.LearnEnabled(cfg.Enabled):
		return "learn off"
	case resp.NumTurns < cfg.MinTurns:
		return fmt.Sprintf("%d turns, below min_turns %d", resp.NumTurns, cfg.MinTurns)
	}
	return ""
}

// learnPass is a turn a learn pass reviews: the channel it ran in, the
// prompt that started it, the turn's last bot message and the id of the
// trigger message that starts the pass. content is the trigger message's
// text when it isn't learn.TriggerMessage's over prompt, as for a pass the
// user asked for (see LearnTurn). row is the pass as recorded, nil when it
// isn't.
type learnPass struct {
	parent    *db.Channel
	prompt    string
	messageID string
	triggerID string
	content   string
	row       *db.LearnPass
}

// maybeLearn queues a learn pass over the run that just finished in ch, when
// it should (see learnSkipReason): it creates ch's learn thread on first use
// and queues the pass there. The pass runs on the learn thread's own drain,
// in turn with its other passes, so ch's queue is never held up by it.
func (o *Orchestrator) maybeLearn(ctx context.Context, ch *db.Channel, msg *bot.IncomingMessage, resp *agent.AgentResponse) {
	if ch.Kind == db.ChannelKindLearn {
		return
	}
	// ch was loaded before the run. The channel may have been deleted
	// since, and a learn thread made for it now would be an orphan; or its
	// Learn switch flipped either way.
	fresh, err := o.store.GetChannel(ctx, ch.ChannelID)
	if err != nil || fresh == nil {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", "channel gone", "error", err)
		return
	}
	ch = fresh
	merged, _ := o.resolvedConfig(ctx, ch)
	cfg := merged.Learn
	parked := o.IsChannelPlanned(ch.ChannelID) || o.IsChannelAsked(ch.ChannelID)
	if reason := learnSkipReason(ch, resp, cfg, parked); reason != "" {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", reason)
		return
	}
	if resp.SessionID == "" {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", "no session")
		return
	}
	l, err := o.ensureHiddenThread(ctx, ch, db.ChannelKindLearn)
	if errors.Is(err, db.ErrParentGone) {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", "channel gone")
		return
	}
	if err != nil {
		o.logger.Error("learn: creating learn thread", "error", err, "channel_id", ch.ChannelID)
		return
	}
	pass := &learnPass{parent: ch, prompt: msg.Content, triggerID: generateMessageID()}
	o.recordLearnPass(ctx, l, msg, pass)
	o.startLearn(ctx, l, pass)
}

// LearnTurn queues a learn pass the user asked for over the turn in ch that
// ended with bot message messageID, in ch's learn thread, created on first
// use. It runs whether or not ch's Learn switch is on. The pass forks the
// session where the turn ended, so it sees the turn as it was, not the ones
// that followed. A pass over the turn already queued or running is returned
// as it is.
func (o *Orchestrator) LearnTurn(ctx context.Context, ch *db.Channel, messageID string) (*db.LearnPass, error) {
	if explain.Unavailable(ch) != "" {
		return nil, learn.ErrUnavailable
	}
	reply, err := o.store.GetChatMessage(ctx, ch.ChannelID, messageID)
	if err != nil {
		return nil, err
	}
	if reply == nil || !reply.IsBot || reply.TriggerMsgID == "" {
		return nil, learn.ErrNotATurn
	}
	if !canForkTurn(ch, reply) {
		return nil, learn.ErrNoSession
	}
	p, err := o.store.ActiveLearnPass(ctx, ch.ChannelID, messageID)
	if err != nil || p != nil {
		return p, err
	}
	var prompt string
	if m, err := o.store.GetChatMessage(ctx, ch.ChannelID, reply.TriggerMsgID); err != nil {
		o.logger.Warn("learn: loading the turn's prompt", "error", err, "channel_id", ch.ChannelID)
	} else if m != nil {
		prompt = m.Content
	}

	l, err := o.ensureHiddenThread(ctx, ch, db.ChannelKindLearn)
	if err != nil {
		return nil, err
	}
	pass := &learnPass{
		parent:    ch,
		prompt:    prompt,
		messageID: messageID,
		triggerID: generateMessageID(),
		content:   learn.TurnTriggerMessage(ch.Name, prompt, reply.Content),
	}
	if err := o.insertLearnPass(ctx, l, pass); err != nil {
		return nil, err
	}
	o.logger.Info("learn: pass asked for", "channel_id", ch.ChannelID, "learn_channel_id", l.ChannelID, "message_id", messageID)
	o.startLearn(ctx, l, pass)
	return pass.row, nil
}

// recordLearnPass records pass as queued in learn thread l, against the
// turn msg started in pass's channel, and tells the chat. A turn with no
// reply isn't recorded; its pass still runs, over a fork of the channel's
// whole session at the time it starts.
func (o *Orchestrator) recordLearnPass(ctx context.Context, l *db.Channel, msg *bot.IncomingMessage, pass *learnPass) {
	ch := pass.parent
	reply, err := o.store.LastBotMessage(ctx, ch.ChannelID, msg.MessageID)
	if err != nil || reply == nil {
		o.logger.Debug("learn: pass not recorded", "channel_id", ch.ChannelID, "reason", "no reply", "error", err)
		return
	}
	pass.messageID = reply.MsgID
	if err := o.insertLearnPass(ctx, l, pass); err != nil {
		o.logger.Error("learn: recording the pass", "error", err, "channel_id", ch.ChannelID)
	}
}

// insertLearnPass records pass as queued in learn thread l, against the
// turn that ended with pass's messageID, and tells the chat.
func (o *Orchestrator) insertLearnPass(ctx context.Context, l *db.Channel, pass *learnPass) error {
	row, err := o.store.InsertLearnPass(ctx, &db.LearnPass{
		ChannelID:      pass.parent.ChannelID,
		MessageID:      pass.messageID,
		LearnChannelID: l.ChannelID,
		TriggerMsgID:   pass.triggerID,
	})
	if err != nil {
		return err
	}
	pass.row = row
	o.broadcastLearnPass(row)
	return nil
}

// startLearn queues pass's trigger message in learn thread l. It waits
// there behind the thread's earlier passes and the user's replies; its run
// forks the reviewed turn's session (see applyLearnRequest).
func (o *Orchestrator) startLearn(ctx context.Context, l *db.Channel, pass *learnPass) {
	ch := pass.parent
	o.logger.Info("learn: queued", "channel_id", ch.ChannelID, "learn_channel_id", l.ChannelID)
	// Tell ch's viewers first: the learn thread is hidden, so nobody is
	// subscribed to it until they hear it exists.
	if o.events != nil {
		o.events.BroadcastLearnStarted(ch.ChannelID, l.ChannelID)
	}
	o.HandleMessage(ctx, &bot.IncomingMessage{
		ChannelID:  l.ChannelID,
		GuildID:    l.GuildID,
		MessageID:  pass.triggerID,
		AuthorID:   learnAuthorID,
		AuthorName: learnAuthorName,
		Content:    cmp.Or(pass.content, learn.TriggerMessage(ch.Name, pass.prompt)),
		HasPrefix:  true,
		Platform:   types.PlatformLocal,
		Timestamp:  o.timeNow().UTC(),
	})
}

// learnPassStarted marks the learn pass msg's run is for running and
// returns it, or nil when msg doesn't start a learn pass or its pass isn't
// recorded.
func (o *Orchestrator) learnPassStarted(ctx context.Context, msg *bot.IncomingMessage) *db.LearnPass {
	if msg.AuthorID != learnAuthorID {
		return nil
	}
	p, err := o.store.GetLearnPassByTrigger(ctx, msg.ChannelID, msg.MessageID)
	if err != nil || p == nil {
		o.logger.Debug("learn: pass not recorded for the run", "error", err, "learn_channel_id", msg.ChannelID)
		return nil
	}
	o.setLearnPass(ctx, p, db.LearnPassRunning, "")
	return p
}

// learnPassDone records how p's run ended: done, or failed with runErr. A
// nil p isn't a recorded learn pass.
func (o *Orchestrator) learnPassDone(ctx context.Context, p *db.LearnPass, runErr error) {
	if runErr != nil {
		o.setLearnPass(ctx, p, db.LearnPassFailed, runErr.Error())
		return
	}
	o.setLearnPass(ctx, p, db.LearnPassDone, "")
}

// setLearnPass stores p's new status and error and tells the chat. A nil p
// isn't recorded, so there's nothing to update.
func (o *Orchestrator) setLearnPass(ctx context.Context, p *db.LearnPass, status, errText string) {
	if p == nil {
		return
	}
	if err := o.store.UpdateLearnPass(ctx, p.ID, status, errText); err != nil {
		o.logger.Error("learn: updating the pass", "error", err, "id", p.ID)
		return
	}
	p.Status, p.Error, p.UpdatedAt = status, errText, o.timeNow().UTC()
	o.broadcastLearnPass(p)
}

func (o *Orchestrator) broadcastLearnPass(p *db.LearnPass) {
	if o.events != nil {
		o.events.BroadcastLearnPass(p)
	}
}

// ensureHiddenThread returns ch's hidden thread of kind (learn or explain),
// creating it on first use. It's a local thread under ch in ch's directory,
// so its runs see the same checkout and can fork ch's sessions. An existing
// one is renamed to follow ch's name. It fails with db.ErrParentGone when ch
// was deleted before the thread could be made.
func (o *Orchestrator) ensureHiddenThread(ctx context.Context, ch *db.Channel, kind string) (*db.Channel, error) {
	name := kind + ": " + ch.Name
	l, err := o.store.GetHiddenThread(ctx, ch.ChannelID, kind)
	if err != nil {
		return nil, err
	}
	if l != nil {
		if l.Name != name {
			if err := o.store.UpdateChannelName(ctx, l.ChannelID, name); err != nil {
				o.logger.Warn("renaming hidden thread", "error", err, "kind", kind, "channel_id", l.ChannelID)
			} else {
				l.Name = name
			}
		}
		return l, nil
	}
	l = &db.Channel{
		ChannelID: kind + "-" + randutil.HexID(6),
		GuildID:   ch.GuildID,
		Name:      name,
		DirPath:   ch.DirPath,
		ParentID:  ch.ChannelID,
		Platform:  types.PlatformLocal,
		Kind:      kind,
	}
	if err := o.store.InsertHiddenThread(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// applyLearnRequest turns req, built for learn thread l under parent, into a
// learn pass or a user's reply to one: its own agent id and tool denials,
// and a system prompt that teaches it Loop's proposal kinds and what
// parent's project already has. The model and effort are learn.model /
// learn.effort, else parent's overrides. A pass (passTrigger) forks the
// session of the turn that ended with bot message turnID (see forkAtTurn);
// a reply resumes the learn thread's own session, the latest pass's fork.
func (o *Orchestrator) applyLearnRequest(ctx context.Context, req *agent.AgentRequest, parent *db.Channel, passTrigger bool, turnID string) error {
	if passTrigger && !o.forkAtTurn(ctx, req, parent, turnID) {
		return learn.ErrNoSession
	}
	cfg, dir := o.resolvedConfig(ctx, parent)
	// Tasks are listed where an applied scheduled_task proposal creates
	// them (see resolveTaskChannelID), so the pass sees the ones it could
	// duplicate.
	taskChannelID := o.resolveTaskChannelID(ctx, parent.ChannelID)
	tasks, err := o.store.ListScheduledTasks(ctx, taskChannelID)
	if err != nil {
		o.logger.Warn("learn: listing tasks", "error", err, "channel_id", taskChannelID)
	}
	proposals, err := o.store.ListLearnProposals(ctx, parent.ChannelID)
	if err != nil {
		o.logger.Warn("learn: listing proposals", "error", err, "channel_id", parent.ChannelID)
	}
	req.AgentID = learn.AgentID
	req.ReadOnly = true
	req.Model = cmp.Or(cfg.Learn.Model, parent.ModelOverride)
	req.Effort = cmp.Or(cfg.Learn.Effort, parent.EffortOverride)
	req.SystemPrompt = learn.SystemPrompt(learn.State{
		ChannelName: parent.Name,
		Description: parent.Description,
		TicketURL:   parent.TicketURL,
		Worktree:    parent.Worktree,
		ProjectDir:  dir,
		Config:      cfg,
		Tasks:       tasks,
		Proposals:   proposals,
	})
	return nil
}

// canForkTurn reports whether the turn in ch that ended with bot message
// reply has a session to fork: its own, recorded on reply, else ch's.
func canForkTurn(ch *db.Channel, reply *db.Message) bool {
	return (reply.SessionID != "" && reply.TranscriptUUID != "") || ch.SessionID != ""
}

// forkAtTurn points req at a fork of the turn in parent that ended with bot
// message turnID, cut where the turn ended, so a review sees the turn as it
// was and not the turns after it. A turn whose reply doesn't record where it
// ended (stored before Loop recorded it, or with no turn at all, turnID "")
// gets a fork of parent's whole current session instead. It reports false
// when there's no session to fork.
func (o *Orchestrator) forkAtTurn(ctx context.Context, req *agent.AgentRequest, parent *db.Channel, turnID string) bool {
	req.ForkSession = true
	req.ResumeAt = ""
	if turnID != "" {
		reply, err := o.store.GetChatMessage(ctx, parent.ChannelID, turnID)
		if err != nil {
			o.logger.Warn("loading the reviewed turn", "error", err, "channel_id", parent.ChannelID, "message_id", turnID)
		}
		if reply != nil && reply.SessionID != "" && reply.TranscriptUUID != "" {
			req.SessionID, req.ResumeAt = reply.SessionID, reply.TranscriptUUID
			return true
		}
	}
	req.SessionID = parent.SessionID
	return parent.SessionID != ""
}
