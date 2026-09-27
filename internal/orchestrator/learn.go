package orchestrator

import (
	"context"
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

// learnProjectDir is the root checkout for ch: where its config is merged
// from and where config proposals land. For a worktree chain that's the
// nearest non-worktree ancestor's directory, else ch's own.
func (o *Orchestrator) learnProjectDir(ctx context.Context, ch *db.Channel) string {
	if dir := worktreeRootFor(ctx, o.store, ch); dir != "" {
		return dir
	}
	return ch.DirPath
}

// learnConfigFor returns the merged config for dir, falling back to the
// global config when there's no dir or the project config fails to load.
func (o *Orchestrator) learnConfigFor(dir string) *config.Config {
	cfg := o.currentConfig()
	if dir == "" {
		return cfg
	}
	merged, err := o.loadProjectConfig(dir, cfg)
	if err != nil {
		o.logger.Warn("learn: loading project config", "error", err, "dir", dir)
		return cfg
	}
	return merged
}

// learnSkipReason says why a finished run in ch doesn't start a learn pass,
// or "" when it should. Only a completed chat turn in a desktop channel
// that's on, long enough and not parked on a plan or question card learns;
// learn threads and task threads never do, and Slack or Discord channels
// don't either, since their proposals can only be seen in the desktop app.
func learnSkipReason(ch *db.Channel, resp *agent.AgentResponse, cfg config.LearnConfig, parked bool) string {
	switch {
	case ch.Kind == db.ChannelKindLearn:
		return "learn thread"
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
// pass's trigger is queued (at triggeredAt) until its run ends; next is the
// latest run that finished meanwhile, reviewed once the thread is free. Runs
// in between are folded into it: the fork of the newest session covers them.
type learnSlot struct {
	triggered   bool
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
	cfg := o.learnConfigFor(o.learnProjectDir(ctx, ch)).Learn
	parked := o.IsChannelPlanned(ch.ChannelID) || o.IsChannelAsked(ch.ChannelID)
	if reason := learnSkipReason(ch, resp, cfg, parked); reason != "" {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", reason)
		return
	}
	if resp.SessionID == "" {
		o.logger.Debug("learn: skipped", "channel_id", ch.ChannelID, "reason", "no session")
		return
	}
	l, err := o.ensureLearnChannel(ctx, ch)
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
	if o.learnSlots == nil {
		o.learnSlots = map[string]*learnSlot{}
	}
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
		slot.triggered = false
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
	l, err := o.store.GetChannel(ctx, channelID)
	if err != nil || l == nil {
		o.logger.Error("learn: loading learn thread", "error", err, "learn_channel_id", channelID)
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

// startLearn points learn thread l at a fork of pass's session and queues
// the trigger message there.
func (o *Orchestrator) startLearn(ctx context.Context, l *db.Channel, pass *learnPass) {
	ch := pass.parent
	if err := o.store.MarkSessionForkPending(ctx, l.ChannelID, pass.sessionID); err != nil {
		o.logger.Error("learn: forking session", "error", err, "channel_id", l.ChannelID)
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
// same checkout and can resume ch's sessions.
func (o *Orchestrator) ensureLearnChannel(ctx context.Context, ch *db.Channel) (*db.Channel, error) {
	l, err := o.store.GetLearnChannel(ctx, ch.ChannelID)
	if err != nil || l != nil {
		return l, err
	}
	l = &db.Channel{
		ChannelID: "learn-" + randutil.HexID(6),
		GuildID:   ch.GuildID,
		Name:      "learn: " + ch.Name,
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
	dir := o.learnProjectDir(ctx, parent)
	cfg := o.learnConfigFor(dir)
	tasks, err := o.store.ListScheduledTasks(ctx, parent.ChannelID)
	if err != nil {
		o.logger.Warn("learn: listing tasks", "error", err, "channel_id", parent.ChannelID)
	}
	req.AgentID = learn.AgentID
	req.LearnMode = true
	req.Model = firstNonEmpty(cfg.Learn.Model, parent.ModelOverride)
	req.Effort = firstNonEmpty(cfg.Learn.Effort, parent.EffortOverride)
	req.SystemPrompt = learn.SystemPrompt(learn.State{
		ChannelName: parent.Name,
		Description: parent.Description,
		TicketURL:   parent.TicketURL,
		Worktree:    parent.Worktree,
		ProjectDir:  dir,
		Config:      cfg,
		Tasks:       tasks,
	})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
