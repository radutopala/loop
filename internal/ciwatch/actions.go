package ciwatch

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/forge"
)

// ErrNoFailure is returned by FixNow when the PR/MR's checks aren't failing.
var ErrNoFailure = errors.New("no failing checks to fix")

// AgentStatus follows the thread's agent runs, to tell its commits from
// others' and to know when a fix run ended. It doesn't block.
func (w *Watcher) AgentStatus(channelID string, d events.AgentStatusEventData) {
	go w.agentStatus(context.Background(), channelID, d)
}

func (w *Watcher) agentStatus(ctx context.Context, channelID string, d events.AgentStatusEventData) {
	// A thread's run is told to its parent too, with the thread's id.
	if d.ThreadID != "" && d.ThreadID != channelID {
		return
	}
	switch d.Status {
	case "running":
		head := w.Head(ctx, channelID)
		e := w.entry(channelID)
		w.mu.Lock()
		e.running, e.startHead = true, head
		if e.fix == fixQueued {
			e.fix = fixRunning
		}
		w.mu.Unlock()
	case "completed", "error":
		head := w.Head(ctx, channelID)
		e := w.entry(channelID)
		w.mu.Lock()
		e.running = false
		e.graceUntil = w.now().Add(w.grace)
		moved := head != "" && head != e.startHead
		if e.fix == fixRunning {
			e.fix = fixDone
		}
		w.mu.Unlock()
		if moved {
			w.recordSHA(ctx, channelID, head)
		}
		w.Kick(channelID)
	}
}

// ChannelUpdated follows the thread's checkout: a commit made while its
// agent runs, or just after, is the agent's. It doesn't block.
func (w *Watcher) ChannelUpdated(d events.ChannelUpdatedData) {
	go w.channelUpdated(context.Background(), d)
}

func (w *Watcher) channelUpdated(ctx context.Context, d events.ChannelUpdatedData) {
	if d.Commit == "" {
		return
	}
	e := w.entry(d.ChannelID)
	w.mu.Lock()
	moved := d.Commit != e.commit
	e.commit = d.Commit
	agent := e.running || w.now().Before(e.graceUntil)
	w.mu.Unlock()
	if !moved {
		return
	}
	if agent {
		w.recordSHA(ctx, d.ChannelID, d.Commit)
	}
	w.Kick(d.ChannelID)
}

// recordSHA remembers sha as a commit of the thread's agent.
func (w *Watcher) recordSHA(ctx context.Context, channelID, sha string) {
	e := w.entry(channelID)
	e.lock.Lock()
	defer e.lock.Unlock()
	st, err := w.load(ctx, channelID)
	if err != nil {
		w.Logger.Warn("ci: loading CI state", "channel_id", channelID, "error", err)
		return
	}
	if slices.Contains(st.AgentSHAs, sha) {
		return
	}
	st.AgentSHAs = append(st.AgentSHAs, sha)
	if n := len(st.AgentSHAs); n > maxAgentSHAs {
		st.AgentSHAs = st.AgentSHAs[n-maxAgentSHAs:]
	}
	w.save(ctx, st)
}

// Status returns what the thread's PR/MR CI was last seen as, polling it
// first when it wasn't seen yet. nil when the thread has no PR/MR.
func (w *Watcher) Status(ctx context.Context, channelID string) *events.CIEventData {
	e := w.entry(channelID)
	w.mu.Lock()
	last := e.last
	w.mu.Unlock()
	if last == nil {
		e.lock.Lock()
		w.poll(ctx, channelID, e)
		e.lock.Unlock()
		w.mu.Lock()
		last = e.last
		w.mu.Unlock()
	}
	if last == nil || last.Kind == "" {
		return nil
	}
	d := *last
	return &d
}

// Attention returns each thread's CI attention, for the threads that have
// one.
func (w *Watcher) Attention(ctx context.Context) (map[string]string, error) {
	states, err := w.Store.ListCIStates(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, st := range states {
		if st.Attention != "" {
			out[st.ChannelID] = st.Attention
		}
	}
	return out, nil
}

// SetAutoFix overrides the config's auto_fix for the thread; nil goes back
// to the config's.
func (w *Watcher) SetAutoFix(ctx context.Context, channelID string, on *bool) error {
	e := w.entry(channelID)
	e.lock.Lock()
	defer e.lock.Unlock()
	st, err := w.load(ctx, channelID)
	if err != nil {
		return err
	}
	st.AutoFix = on
	if err := w.Store.SaveCIState(ctx, st); err != nil {
		return err
	}
	w.mu.Lock()
	e.last = nil // the next poll broadcasts the change
	w.mu.Unlock()
	w.Kick(channelID)
	return nil
}

// FixNow sends the PR/MR's current failure to the thread's agent, whoever
// made the commit and whatever the guards say: the owner asked.
func (w *Watcher) FixNow(ctx context.Context, channelID string) error {
	e := w.entry(channelID)
	e.lock.Lock()
	defer e.lock.Unlock()
	st, err := w.load(ctx, channelID)
	if err != nil {
		return err
	}
	t, f, err := w.target(ctx, st)
	if err != nil {
		return err
	}
	ci, err := f.CI(ctx, t.Ref)
	if err != nil {
		return err
	}
	if ci.Checks.Failed == 0 {
		return ErrNoFailure
	}
	fcs, err := f.FailedChecks(ctx, t.Ref)
	if err != nil {
		return err
	}
	prompt, hash := w.prompt(ctx, f, t, ci.HeadSHA, fcs)
	w.sendFix(ctx, e, st, ci.HeadSHA, hash, prompt)
	if err := w.Store.SaveCIState(ctx, st); err != nil {
		return err
	}
	w.publish(e, w.eventData(ctx, t, f, st, ci))
	w.Kick(channelID)
	return nil
}

// Arm has the PR/MR merged with method once its checks pass, through the
// forge's auto-merge when the repo allows it, else by Loop. An empty
// method is the config's merge_method, else the first the repo allows.
func (w *Watcher) Arm(ctx context.Context, channelID, method string) error {
	e := w.entry(channelID)
	e.lock.Lock()
	defer e.lock.Unlock()
	st, err := w.load(ctx, channelID)
	if err != nil {
		return err
	}
	t, f, err := w.target(ctx, st)
	if err != nil {
		return err
	}
	method, settings, err := pickMethod(ctx, f, t, method)
	if err != nil {
		return err
	}
	ci, err := f.CI(ctx, t.Ref)
	if err != nil {
		return err
	}
	if ci.Merged || ci.Closed {
		return fmt.Errorf("%s is no longer open", label(t))
	}
	via := viaLoop
	if settings.AutoMerge {
		if err := f.EnableAutoMerge(ctx, t.Ref, method); err != nil {
			return err
		}
		via = viaForge
	}
	st.MergeArmed, st.MergeVia, st.ArmedSHA = method, via, ci.HeadSHA
	st.Watching = true
	if err := w.Store.SaveCIState(ctx, st); err != nil {
		return err
	}
	w.Kick(channelID)
	return nil
}

// CancelMerge disarms "merge when ready".
func (w *Watcher) CancelMerge(ctx context.Context, channelID string) error {
	e := w.entry(channelID)
	e.lock.Lock()
	defer e.lock.Unlock()
	st, err := w.load(ctx, channelID)
	if err != nil {
		return err
	}
	t, f, err := w.target(ctx, st)
	if err != nil {
		return err
	}
	if st.MergeVia == viaForge {
		if err := f.DisableAutoMerge(ctx, t.Ref); err != nil {
			return err
		}
	}
	st.MergeArmed, st.MergeVia, st.ArmedSHA = "", "", ""
	if err := w.Store.SaveCIState(ctx, st); err != nil {
		return err
	}
	w.Kick(channelID)
	return nil
}

// MergeNow merges the PR/MR now with method, or the one Arm would pick.
func (w *Watcher) MergeNow(ctx context.Context, channelID, method string) error {
	e := w.entry(channelID)
	e.lock.Lock()
	defer e.lock.Unlock()
	st, err := w.load(ctx, channelID)
	if err != nil {
		return err
	}
	t, f, err := w.target(ctx, st)
	if err != nil {
		return err
	}
	method, _, err = pickMethod(ctx, f, t, method)
	if err != nil {
		return err
	}
	if err := f.Merge(ctx, t.Ref, method); err != nil {
		return err
	}
	w.Kick(channelID)
	return nil
}

// pickMethod is the merge method to use: method, else the config's
// merge_method, else the first the repo allows. It returns the repo's merge
// settings too.
func pickMethod(ctx context.Context, f forge.Forge, t *Target, method string) (string, forge.MergeSettings, error) {
	settings, err := f.MergeSettings(ctx, t.Ref)
	if err != nil {
		return "", settings, err
	}
	if method == "" {
		method = t.CI.MergeMethod
	}
	method, err = forge.PickMethod(method, settings)
	return method, settings, err
}
