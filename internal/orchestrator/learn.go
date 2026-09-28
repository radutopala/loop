package orchestrator

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

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

// learnPass is a finished run a learn pass reviews: the channel it ran in,
// the prompt that started it, the session to fork, the turn's last bot
// message and the id of the trigger message that starts the pass. content
// is the trigger message's text when it isn't learn.TriggerMessage's over
// prompt, as for a pass the user asked for (see LearnTurn). row is the pass
// as recorded, nil when it isn't.
type learnPass struct {
	parent    *db.Channel
	prompt    string
	sessionID string
	messageID string
	triggerID string
	content   string
	row       *db.LearnPass
}

// learnSlot tracks one learn thread's passes. triggered is set from when a
// pass's trigger is queued (at triggeredAt) until its run ends, running
// from when that run starts; next is the latest run that finished
// meanwhile, reviewed once the thread is free. Runs in between are folded
// into it: the fork of the newest session covers them.
type learnSlot struct {
	triggered   bool
	running     bool
	triggeredAt time.Time
	next        *learnPass
}

// learnTriggerLost is how long a queued trigger may go without its run
// ending before queueLearn takes it as dropped (HandleMessage gave up on it)
// and lets the next pass start, so one lost trigger can't stop the channel
// learning until a restart.
const learnTriggerLost = time.Hour

// maybeLearn starts a learn pass over the run that just finished in ch, when
// it should (see learnSkipReason): it creates ch's learn thread on first use
// and starts the pass there, or queues it while the thread is busy. The
// pass runs on the learn thread's own drain, so ch's queue is never held up
// by it.
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
	pass := &learnPass{parent: ch, prompt: msg.Content, sessionID: resp.SessionID, triggerID: generateMessageID()}
	o.recordLearnPass(ctx, l, msg, pass)
	o.runLearnPass(ctx, l, pass)
}

// LearnTurn starts a learn pass the user asked for over the turn in ch that
// ended with bot message messageID, in ch's learn thread, created on first
// use. It runs whether or not ch's Learn switch is on. The pass forks ch's
// current session, so it sees the turn even when later ones followed. A
// pass over the turn already queued or running is returned as it is.
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
	if ch.SessionID == "" {
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
		sessionID: ch.SessionID,
		messageID: messageID,
		triggerID: generateMessageID(),
		content:   learn.TurnTriggerMessage(ch.Name, prompt, reply.Content),
	}
	if err := o.insertLearnPass(ctx, l, pass); err != nil {
		return nil, err
	}
	o.logger.Info("learn: pass asked for", "channel_id", ch.ChannelID, "learn_channel_id", l.ChannelID, "message_id", messageID)
	o.runLearnPass(ctx, l, pass)
	return pass.row, nil
}

// runLearnPass starts pass in learn thread l, or keeps it as the one to run
// next while the thread is busy; the waiting pass it replaces is marked
// superseded.
func (o *Orchestrator) runLearnPass(ctx context.Context, l *db.Channel, pass *learnPass) {
	queued, replaced := o.queueLearn(l.ChannelID, pass)
	if replaced != nil {
		o.setLearnPass(ctx, replaced.row, db.LearnPassSuperseded, "")
	}
	if queued {
		o.logger.Info("learn: queued behind the running pass", "channel_id", pass.parent.ChannelID, "learn_channel_id", l.ChannelID)
		return
	}
	o.startLearn(ctx, l, pass)
}

// recordLearnPass records pass as queued in learn thread l, against the
// turn msg started in pass's channel, and tells the chat. A turn with no
// reply isn't recorded; its pass still runs.
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

// queueLearn claims learn thread id for pass and reports false, or, when the
// thread is busy (a pass is queued or running there, or the user is talking
// to it), keeps pass as the one to run next and reports true. It returns the
// waiting pass that pass replaces, if any: pass forks a newer session, which
// covers that one's turn too.
func (o *Orchestrator) queueLearn(id string, pass *learnPass) (queued bool, replaced *learnPass) {
	o.learnMu.Lock()
	defer o.learnMu.Unlock()
	slot := o.learnSlots[id]
	if slot != nil {
		replaced = slot.next
	}
	_, running := o.activeRuns.Load(id)
	if slot != nil && slot.triggered && !running && o.timeNow().Sub(slot.triggeredAt) > learnTriggerLost {
		o.logger.Warn("learn: trigger never ran, starting the next pass", "learn_channel_id", id)
		slot = nil
	}
	if slot != nil || running {
		if slot == nil {
			slot = &learnSlot{}
			o.learnSlots[id] = slot
		}
		slot.next = pass
		return true, replaced
	}
	o.learnSlots[id] = &learnSlot{triggered: true, triggeredAt: o.timeNow()}
	return false, replaced
}

// learnRunStarted is called when any run in channelID starts. A learn
// pass's run marks its slot running. A pass resumed after a restart has no
// slot yet; it gets one, so the thread counts as busy until the run ends.
func (o *Orchestrator) learnRunStarted(channelID, authorID string) {
	if authorID != learnAuthorID {
		return
	}
	o.learnMu.Lock()
	defer o.learnMu.Unlock()
	slot := o.learnSlots[channelID]
	if slot == nil {
		slot = &learnSlot{triggered: true, triggeredAt: o.timeNow()}
		o.learnSlots[channelID] = slot
	}
	slot.running = true
}

// IsLearnPassRunning reports whether a learn pass is running in learn
// thread id. A user's reply running there isn't a pass, nor is a pass still
// waiting for its turn.
func (o *Orchestrator) IsLearnPassRunning(id string) bool {
	o.learnMu.Lock()
	defer o.learnMu.Unlock()
	slot := o.learnSlots[id]
	return slot != nil && slot.running
}

// learnRunDone is called when any run in channelID ends. A learn thread that
// has no pass of its own still queued is free again; the pass waiting for
// it, if any, starts now.
func (o *Orchestrator) learnRunDone(ctx context.Context, channelID, authorID string) {
	o.learnMu.Lock()
	slot := o.learnSlots[channelID]
	if slot == nil {
		o.learnMu.Unlock()
		return
	}
	if authorID == learnAuthorID {
		slot.triggered, slot.running = false, false
	}
	next := slot.next
	switch {
	case slot.triggered:
		// A user's run ended; the queued pass still has to run.
		next = nil
	case next == nil:
		delete(o.learnSlots, channelID)
	default:
		slot.next, slot.triggered, slot.triggeredAt = nil, true, o.timeNow()
	}
	o.learnMu.Unlock()
	if next == nil {
		return
	}
	// The learn thread goes with its channel, so a missing one means the
	// channel was deleted while the pass waited.
	l, err := o.store.GetChannel(ctx, channelID)
	if err != nil || l == nil {
		o.logger.Debug("learn: dropping the waiting pass", "reason", "learn thread gone", "error", err, "learn_channel_id", channelID)
		o.releaseLearn(channelID)
		return
	}
	o.startLearn(ctx, l, next)
}

// releaseLearn frees learn thread id when its pass never got queued.
func (o *Orchestrator) releaseLearn(id string) {
	o.learnMu.Lock()
	defer o.learnMu.Unlock()
	delete(o.learnSlots, id)
}

// StopLearn forgets learn thread id's queued pass and cancels its running
// one, for when the learn thread is deleted with its channel. The slot goes
// first, so the cancelled run's learnRunDone finds nothing left to start.
func (o *Orchestrator) StopLearn(id string) {
	o.releaseLearn(id)
	o.CancelActiveRun(id)
}

// startLearn points learn thread l at a fork of pass's session and queues
// the trigger message there.
func (o *Orchestrator) startLearn(ctx context.Context, l *db.Channel, pass *learnPass) {
	ch := pass.parent
	ok, err := o.store.MarkSessionForkPending(ctx, l.ChannelID, pass.sessionID)
	if err != nil {
		o.logger.Error("learn: forking session", "error", err, "channel_id", l.ChannelID)
		o.releaseLearn(l.ChannelID)
		o.setLearnPass(ctx, pass.row, db.LearnPassFailed, err.Error())
		return
	}
	if !ok {
		// The learn thread was deleted with its channel since it was looked up.
		o.logger.Debug("learn: dropping the pass", "reason", "learn thread gone", "learn_channel_id", l.ChannelID)
		o.releaseLearn(l.ChannelID)
		return
	}
	o.logger.Info("learn: starting", "channel_id", ch.ChannelID, "learn_channel_id", l.ChannelID)
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
// learn pass: its own agent id and tool denials, and a system prompt that
// teaches it Loop's proposal kinds and what parent's project already has. The
// model and effort are learn.model / learn.effort, else parent's overrides.
func (o *Orchestrator) applyLearnRequest(ctx context.Context, req *agent.AgentRequest, parent *db.Channel) {
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
}
