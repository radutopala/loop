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

// learnConfig resolves ch's merged config the way its runs do: global →
// root checkout → worktree for a worktree chain, global → ch's dir
// otherwise. It returns the config and the root checkout, where config
// proposals land: for a worktree chain the nearest non-worktree ancestor's
// directory, else ch's own. It falls back to the global config when ch has
// no dir or the project config fails to load.
func (o *Orchestrator) learnConfig(ctx context.Context, ch *db.Channel) (*config.Config, string) {
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
		o.logger.Warn("learn: loading project config", "error", err, "dir", ch.DirPath)
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
// the prompt that started it and the session to fork.
type learnPass struct {
	parent    *db.Channel
	prompt    string
	sessionID string
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
	merged, _ := o.learnConfig(ctx, ch)
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
	// ch was loaded before the run. The channel may have been deleted
	// since, and a learn thread made for it now would be an orphan; or its
	// Learn switch turned off.
	fresh, err := o.store.GetChannel(ctx, ch.ChannelID)
	if err != nil || fresh == nil {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", "channel gone", "error", err)
		return
	}
	if !fresh.LearnEnabled(cfg.Enabled) {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", "learn off")
		return
	}
	ch = fresh
	l, err := o.ensureLearnChannel(ctx, ch)
	if errors.Is(err, db.ErrLearnParentGone) {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", "channel gone")
		return
	}
	if err != nil {
		o.logger.Error("learn: creating learn thread", "error", err, "channel_id", ch.ChannelID)
		return
	}
	pass := &learnPass{parent: ch, prompt: msg.Content, sessionID: resp.SessionID}
	if o.queueLearn(l.ChannelID, pass) {
		o.logger.Info("learn: queued behind the running pass", "channel_id", ch.ChannelID, "learn_channel_id", l.ChannelID)
		return
	}
	o.startLearn(ctx, l, pass)
}

// queueLearn claims learn thread id for pass and reports false, or, when the
// thread is busy (a pass is queued or running there, or the user is talking
// to it), keeps pass as the one to run next and reports true.
func (o *Orchestrator) queueLearn(id string, pass *learnPass) bool {
	o.learnMu.Lock()
	defer o.learnMu.Unlock()
	slot := o.learnSlots[id]
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
		return true
	}
	o.learnSlots[id] = &learnSlot{triggered: true, triggeredAt: o.timeNow()}
	return false
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
		AuthorID:   learnAuthorID,
		AuthorName: learnAuthorName,
		Content:    learn.TriggerMessage(ch.Name, pass.prompt),
		HasPrefix:  true,
		Platform:   types.PlatformLocal,
		Timestamp:  o.timeNow().UTC(),
	})
}

// ensureLearnChannel returns ch's hidden learn thread, creating it on first
// use. It's a local thread under ch in ch's directory, so its runs see the
// same checkout and can resume ch's sessions. An existing learn thread is
// renamed to follow ch's name. It fails with db.ErrLearnParentGone when ch
// was deleted before its learn thread could be made.
func (o *Orchestrator) ensureLearnChannel(ctx context.Context, ch *db.Channel) (*db.Channel, error) {
	name := "learn: " + ch.Name
	l, err := o.store.GetLearnChannel(ctx, ch.ChannelID)
	if err != nil {
		return nil, err
	}
	if l != nil {
		if l.Name != name {
			if err := o.store.UpdateChannelName(ctx, l.ChannelID, name); err != nil {
				o.logger.Warn("learn: renaming learn thread", "error", err, "learn_channel_id", l.ChannelID)
			} else {
				l.Name = name
			}
		}
		return l, nil
	}
	l = &db.Channel{
		ChannelID: "learn-" + randutil.HexID(6),
		GuildID:   ch.GuildID,
		Name:      name,
		DirPath:   ch.DirPath,
		ParentID:  ch.ChannelID,
		Platform:  types.PlatformLocal,
		Kind:      db.ChannelKindLearn,
	}
	if err := o.store.InsertLearnChannel(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// applyLearnRequest turns req, built for learn thread l under parent, into a
// learn pass: its own agent id and tool denials, and a system prompt that
// teaches it Loop's proposal kinds and what parent's project already has. The
// model and effort are learn.model / learn.effort, else parent's overrides.
func (o *Orchestrator) applyLearnRequest(ctx context.Context, req *agent.AgentRequest, parent *db.Channel) {
	cfg, dir := o.learnConfig(ctx, parent)
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
	req.LearnMode = true
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
