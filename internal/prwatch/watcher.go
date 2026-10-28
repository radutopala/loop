// Package prwatch watches each thread's PR/MR: its checks, merge state and
// review threads. A failure on a commit the thread's agent made goes back
// to that agent to fix, within guards; any other, and any merge conflict,
// flags the thread for its owner. The owner is told of new unresolved
// review threads. It also carries out "merge when ready" where the forge can't.
package prwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/forge"
)

// AuthorID is the author of the messages that send a failure to the agent.
const AuthorID = "loop-pr"

// Merge-when-ready modes: the forge's own auto-merge, or Loop merging once
// the checks pass.
const (
	viaForge = "forge"
	viaLoop  = "loop"
)

// Defaults for the Watcher's timing.
const (
	defaultTick     = time.Second
	defaultLinger   = 2 * time.Minute
	defaultGrace    = 15 * time.Second
	defaultParallel = 4
	// maxAgentSHAs bounds the commits remembered per thread.
	maxAgentSHAs = 50
	// maxLogs bounds the failed checks whose logs go into a prompt.
	maxLogs = 5
)

// ErrNoPR is returned for a thread whose branch has no PR/MR.
var ErrNoPR = errors.New("no PR/MR for this thread's branch")

// Target is a thread's PR/MR and the ci config of its workspace.
type Target struct {
	Kind string // forge.KindPR or forge.KindMR
	Ref  forge.Ref
	URL  string
	PR   config.PRConfig
}

// Store keeps each thread's CI state.
type Store interface {
	GetPRState(ctx context.Context, channelID string) (*db.PRState, error)
	SavePRState(ctx context.Context, st *db.PRState) error
	ListPRStates(ctx context.Context) ([]*db.PRState, error)
}

// Deps are what a Watcher works with.
type Deps struct {
	// Resolve returns the thread's PR/MR, nil when its branch has none.
	Resolve func(ctx context.Context, channelID string) (*Target, error)
	// Head returns the commit the thread's checkout is on, "" if unknown.
	Head func(ctx context.Context, channelID string) string
	// Forges are the forges by Target kind.
	Forges map[string]forge.Forge
	Store  Store
	// Enqueue sends content to the thread's agent.
	Enqueue func(ctx context.Context, channelID, content string)
	// Notice posts a system notice in the thread.
	Notice func(ctx context.Context, channelID, content string)
	// Broadcast tells the UI of a thread's CI.
	Broadcast func(data events.PREventData)
	Logger    *slog.Logger
}

// fix states of a thread's auto-fix run.
const (
	fixNone = iota
	fixQueued
	fixRunning
	fixDone
)

// entry is the Watcher's in-memory view of a thread.
type entry struct {
	lock sync.Mutex // serializes the thread's state changes

	// The fields below are guarded by Watcher.mu.
	due        time.Time // next poll; zero when not scheduled
	until      time.Time // watched at least until
	polling    bool
	running    bool
	startHead  string    // the checkout's commit when the running run began
	graceUntil time.Time // a commit before this is still the last run's
	commit     string    // the checkout's last seen commit
	fix        int
	last       *events.PREventData
}

// Watcher polls the CI of the threads it watches. A thread is watched while
// its checks run, a fix is in flight or a merge is armed, and for a while
// after a commit or a run, when a push is likely. An open PR/MR is polled
// slowly otherwise, so a conflict with its moving base is seen.
type Watcher struct {
	Deps
	now      func() time.Time
	tick     time.Duration
	linger   time.Duration
	grace    time.Duration
	parallel int

	mu      sync.Mutex
	entries map[string]*entry
}

// New returns a Watcher.
func New(d Deps) *Watcher {
	return &Watcher{
		Deps:     d,
		now:      time.Now,
		tick:     defaultTick,
		linger:   defaultLinger,
		grace:    defaultGrace,
		parallel: defaultParallel,
		entries:  map[string]*entry{},
	}
}

// entry returns the thread's entry, creating it.
func (w *Watcher) entry(channelID string) *entry {
	w.mu.Lock()
	defer w.mu.Unlock()
	e, ok := w.entries[channelID]
	if !ok {
		e = &entry{}
		w.entries[channelID] = e
	}
	return e
}

// Run polls the watched threads until ctx ends, starting with the ones the
// stored state says were watched. It returns once the polls in flight end.
func (w *Watcher) Run(ctx context.Context) {
	w.restore(ctx)
	t := time.NewTicker(w.tick)
	defer t.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	sem := make(chan struct{}, w.parallel)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, id := range w.dueNow() {
				select {
				case <-ctx.Done():
					return
				case sem <- struct{}{}:
				}
				wg.Go(func() {
					defer func() { <-sem }()
					w.Poll(ctx, id)
				})
			}
		}
	}
}

// restore schedules the threads that were watched or armed, then the ones
// with an open PR/MR, and resumes tracking a fix that was in flight.
func (w *Watcher) restore(ctx context.Context) {
	states, err := w.Store.ListPRStates(ctx)
	if err != nil {
		w.Logger.Warn("pr: listing PR state", "error", err)
		return
	}
	for _, st := range states {
		if st.Attention == events.PRAttentionFixing {
			e := w.entry(st.ChannelID)
			w.mu.Lock()
			e.fix = fixQueued
			w.mu.Unlock()
		}
		switch {
		case st.Watching || st.MergeArmed != "":
			w.Kick(st.ChannelID)
		case st.Open:
			e := w.entry(st.ChannelID)
			w.mu.Lock()
			e.due = w.now()
			w.mu.Unlock()
		}
	}
}

// dueNow marks the threads due a poll as polling and returns them.
func (w *Watcher) dueNow() []string {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	var ids []string
	for id, e := range w.entries {
		if e.polling || e.due.IsZero() || now.Before(e.due) {
			continue
		}
		e.polling = true
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Kick has the thread polled now and watched for a while: its checkout
// moved or a run ended, so a push may follow.
func (w *Watcher) Kick(channelID string) {
	e := w.entry(channelID)
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	e.until = now.Add(w.linger)
	e.due = now
}

// Watch has the thread polled when it isn't watched already: its PR/MR was
// just looked up.
func (w *Watcher) Watch(channelID string) {
	e := w.entry(channelID)
	w.mu.Lock()
	scheduled := !e.due.IsZero() || e.polling
	w.mu.Unlock()
	if !scheduled {
		w.Kick(channelID)
	}
}

// Poll polls the thread's CI once and schedules its next poll.
func (w *Watcher) Poll(ctx context.Context, channelID string) {
	e := w.entry(channelID)
	e.lock.Lock()
	keep, every, idle := w.poll(ctx, channelID, e)
	e.lock.Unlock()
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	e.polling = false
	switch {
	case keep || now.Before(e.until):
		e.due = now.Add(every)
	case idle > 0:
		e.due = now.Add(idle)
	default:
		e.due = time.Time{}
	}
}

// load returns the thread's stored state, a new one when it has none.
func (w *Watcher) load(ctx context.Context, channelID string) (*db.PRState, error) {
	st, err := w.Store.GetPRState(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		st = &db.PRState{ChannelID: channelID}
	}
	return st, nil
}

// save stores st, logging a failure.
func (w *Watcher) save(ctx context.Context, st *db.PRState) {
	if err := w.Store.SavePRState(ctx, st); err != nil {
		w.Logger.Warn("pr: saving PR state", "channel_id", st.ChannelID, "error", err)
	}
}

// target resolves the thread's PR/MR and its forge, and aligns st with it:
// another PR/MR starts over.
func (w *Watcher) target(ctx context.Context, st *db.PRState) (*Target, forge.Forge, error) {
	t, err := w.Resolve(ctx, st.ChannelID)
	if err != nil {
		return nil, nil, err
	}
	if t == nil {
		return nil, nil, ErrNoPR
	}
	f := w.Forges[t.Kind]
	if f == nil {
		return nil, nil, fmt.Errorf("no forge for %q", t.Kind)
	}
	if st.Forge != t.Kind || st.Number != t.Ref.Number {
		*st = db.PRState{ChannelID: st.ChannelID, AgentSHAs: st.AgentSHAs, AutoFix: st.AutoFix, Forge: t.Kind, Number: t.Ref.Number}
	}
	return t, f, nil
}

// poll polls the thread's CI and acts on it. It reports whether the thread
// stays watched, how often it's polled then and, for an open PR/MR, how
// often otherwise.
func (w *Watcher) poll(ctx context.Context, channelID string, e *entry) (bool, time.Duration, time.Duration) {
	every := time.Duration(config.DefaultPRPollSec) * time.Second
	idle := time.Duration(config.DefaultPRIdlePollSec) * time.Second
	st, err := w.load(ctx, channelID)
	if err != nil {
		w.Logger.Warn("pr: loading PR state", "channel_id", channelID, "error", err)
		return false, every, 0
	}
	t, f, err := w.target(ctx, st)
	if errors.Is(err, ErrNoPR) {
		if st.Watching || st.Attention != "" || st.Open {
			st.Watching, st.Attention, st.Open = false, "", false
			w.save(ctx, st)
			w.publish(e, events.PREventData{ChannelID: channelID})
		}
		return false, every, 0
	}
	if err != nil {
		w.Logger.Warn("pr: finding the PR/MR", "channel_id", channelID, "error", err)
		return st.Watching, every, openIdle(st, idle)
	}
	every = time.Duration(t.PR.PollSec) * time.Second
	idle = time.Duration(t.PR.IdlePollSec) * time.Second
	ci, err := f.Status(ctx, t.Ref)
	if err != nil {
		w.Logger.Warn("pr: reading the status", "channel_id", channelID, "error", err)
		return st.Watching, every, openIdle(st, idle)
	}

	st.Open = !ci.Merged && !ci.Closed
	if st.Attention == events.PRAttentionFixing && !w.fixInFlight(e) {
		st.Attention = ""
	}
	if st.Attention == events.PRAttentionConflict && ci.MergeState != forge.MergeConflict {
		// The conflict is gone: resolved, or the base moved back.
		st.Attention = ""
	}
	agent := agentCommit(st.AgentSHAs, ci.HeadSHA)
	alert := ""
	switch {
	case ci.Merged || ci.Closed:
		st.Watching, st.Attention, st.Reason, st.Failed = false, "", "", nil
		st.MergeArmed, st.MergeVia, st.ArmedSHA = "", "", ""
	case ci.MergeState == forge.MergeConflict:
		w.checkArmedHead(ctx, st, ci, agent)
		if st.Attention == events.PRAttentionFixing {
			// Flagged once the agent's fix of a failure is done.
			st.Watching = true
			break
		}
		// A conflict is the owner's to resolve: it lights the thread's
		// conflict pill and alerts no one.
		st.Attention, st.Reason, st.Failed = events.PRAttentionConflict, "", nil
		st.Watching = st.MergeArmed != ""
	case ci.Checks.Pending > 0:
		w.checkArmedHead(ctx, st, ci, agent)
		st.Watching = true
		if ci.HeadSHA != st.LastFailedSHA {
			st.Failed = nil
			if st.Attention == events.PRAttentionFailed {
				st.Attention, st.Reason = "", ""
			}
		}
	case ci.Checks.Failed > 0:
		w.checkArmedHead(ctx, st, ci, agent)
		alert = w.failure(ctx, e, t, f, ci, st, agent)
	default:
		w.checkArmedHead(ctx, st, ci, agent)
		st.Attention, st.Attempts, st.LastFailedSHA, st.LastLogHash = "", 0, "", ""
		st.Reason, st.Failed = "", nil
		st.Watching = st.MergeArmed != ""
		if st.MergeVia == viaLoop && ci.MergeState == forge.MergeClean {
			// Watched until the forge reports it merged.
			st.Watching = w.mergeArmed(ctx, f, t, st)
		}
	}
	if st.Open {
		alert = w.threads(ctx, t, f, st, alert)
	} else {
		st.Threads = nil
	}
	w.save(ctx, st)
	data := w.eventData(ctx, t, f, st, ci)
	data.Alert = alert
	w.publish(e, data)
	return st.Watching, every, openIdle(st, idle)
}

// threads keeps the PR/MR's unresolved review threads in st and returns
// alert, telling the owner of any new one too.
func (w *Watcher) threads(ctx context.Context, t *Target, f forge.Forge, st *db.PRState, alert string) string {
	ids, err := f.UnresolvedThreads(ctx, t.Ref)
	if err != nil {
		w.Logger.Warn("pr: listing unresolved review threads", "channel_id", st.ChannelID, "error", err)
		return alert
	}
	fresh := slices.ContainsFunc(ids, func(id string) bool { return !slices.Contains(st.Threads, id) })
	st.Threads = ids
	if !fresh {
		return alert
	}
	what := fmt.Sprintf("%d unresolved review thread", len(ids))
	if len(ids) > 1 {
		what += "s"
	}
	if alert == "" {
		return label(t) + ": " + what
	}
	return alert + "; " + what
}

// openIdle is how often the thread's PR/MR is polled while it isn't
// watched: every idle while open, never otherwise.
func openIdle(st *db.PRState, idle time.Duration) time.Duration {
	if !st.Open {
		return 0
	}
	return idle
}

// checkArmedHead disarms Loop's merge when the head moved to a commit that
// isn't the agent's: the owner armed it for what they saw.
func (w *Watcher) checkArmedHead(ctx context.Context, st *db.PRState, ci *forge.Status, agent bool) {
	if st.MergeVia != viaLoop || ci.HeadSHA == st.ArmedSHA {
		return
	}
	if agent {
		st.ArmedSHA = ci.HeadSHA
		return
	}
	st.MergeArmed, st.MergeVia, st.ArmedSHA = "", "", ""
	w.Notice(ctx, st.ChannelID, fmt.Sprintf("Merge when ready was cancelled: %s was pushed by someone other than this thread's agent.", short(ci.HeadSHA)))
}

// mergeArmed merges the PR/MR Loop armed, once: it's disarmed whether the
// merge succeeds or not. It reports whether it merged.
func (w *Watcher) mergeArmed(ctx context.Context, f forge.Forge, t *Target, st *db.PRState) bool {
	method := st.MergeArmed
	st.MergeArmed, st.MergeVia, st.ArmedSHA = "", "", ""
	if err := f.Merge(ctx, t.Ref, method); err != nil {
		w.Notice(ctx, st.ChannelID, fmt.Sprintf("Merge when ready failed: %v", err))
		return false
	}
	return true
}

// failure acts on finished checks with failures: re-runs infra failures
// once, sends a failure on the agent's commit back to it within the guards
// and flags any other. It keeps the failed checks and why it acted in st,
// and returns the alert to tell the owner, if any.
func (w *Watcher) failure(ctx context.Context, e *entry, t *Target, f forge.Forge, ci *forge.Status, st *db.PRState, agent bool) string {
	armed := st.MergeArmed != ""
	if ci.HeadSHA == st.LastFailedSHA {
		switch st.Attention {
		case events.PRAttentionFixing:
			st.Watching = true
			return ""
		case events.PRAttentionFailed:
			st.Watching = armed
			w.backfill(ctx, t, f, st, agent)
			return ""
		}
	}
	fcs, err := f.FailedChecks(ctx, t.Ref)
	if err != nil {
		w.Logger.Warn("pr: listing failed checks", "channel_id", st.ChannelID, "error", err)
		st.Watching = true
		return ""
	}
	st.Failed = fcs
	if ci.HeadSHA == st.LastFailedSHA && st.Attempts > 0 {
		return w.stop(ctx, t, ci, st, events.PRReasonNoNewCommit, "Auto-fix stopped: the last fix pushed no new commit.")
	}
	if allInfra(fcs) {
		if st.InfraRetriedSHA != ci.HeadSHA && w.rerun(ctx, f, t, ci, st, fcs) {
			st.Watching = true
			st.Reason = events.PRReasonInfraRerun
			return ""
		}
		return w.stop(ctx, t, ci, st, events.PRReasonInfraAgain, "CI failed again for infrastructure reasons after a re-run.")
	}
	if reason := flagReason(t, st, agent); reason != "" {
		notice := ""
		if reason == events.PRReasonMaxAttempts {
			notice = fmt.Sprintf("Auto-fix stopped: CI still fails after %d attempts.", st.Attempts)
		}
		return w.stop(ctx, t, ci, st, reason, notice)
	}
	prompt, hash := w.prompt(ctx, f, t, ci.HeadSHA, fcs)
	if st.Attempts > 0 && hash == st.LastLogHash {
		return w.stop(ctx, t, ci, st, events.PRReasonSameFailure, "Auto-fix stopped: the same failure came back after the fix.")
	}
	w.sendFix(ctx, e, st, ci.HeadSHA, hash, prompt, events.PRReasonFixSent)
	return ""
}

// flagReason is why a failure on the head isn't sent to the agent, "" when
// it can be.
func flagReason(t *Target, st *db.PRState, agent bool) string {
	switch {
	case !agent:
		return events.PRReasonNotAgent
	case !autoFix(t, st):
		return events.PRReasonAutoFixOff
	case st.Attempts >= t.PR.MaxAttempts:
		return events.PRReasonMaxAttempts
	}
	return ""
}

// backfill fills in the failed checks and the reason of a failure flagged
// before Loop kept them.
func (w *Watcher) backfill(ctx context.Context, t *Target, f forge.Forge, st *db.PRState, agent bool) {
	if st.Reason == "" {
		st.Reason = flagReason(t, st, agent)
	}
	if st.Failed != nil {
		return
	}
	fcs, err := f.FailedChecks(ctx, t.Ref)
	if err != nil {
		w.Logger.Warn("pr: listing failed checks", "channel_id", st.ChannelID, "error", err)
		return
	}
	st.Failed = fcs
}

// allInfra reports whether every failed check failed for infrastructure
// reasons.
func allInfra(fcs []forge.FailedCheck) bool {
	for _, fc := range fcs {
		if !fc.Infra {
			return false
		}
	}
	return len(fcs) > 0
}

// rerun re-runs the failed checks, once per head, and reports whether it
// did.
func (w *Watcher) rerun(ctx context.Context, f forge.Forge, t *Target, ci *forge.Status, st *db.PRState, fcs []forge.FailedCheck) bool {
	st.InfraRetriedSHA = ci.HeadSHA
	for _, fc := range fcs {
		if err := f.Rerun(ctx, t.Ref, fc); err != nil {
			w.Logger.Warn("pr: re-running a check", "channel_id", st.ChannelID, "check", fc.Label(), "error", err)
			return false
		}
	}
	w.Notice(ctx, st.ChannelID, fmt.Sprintf("CI failed on %s for infrastructure reasons; re-running it once.", short(ci.HeadSHA)))
	return true
}

// stop flags the failure for the owner for reason, with notice in the
// thread when set, and returns the alert.
func (w *Watcher) stop(ctx context.Context, t *Target, ci *forge.Status, st *db.PRState, reason, notice string) string {
	st.Attention, st.Reason = events.PRAttentionFailed, reason
	st.LastFailedSHA = ci.HeadSHA
	st.Watching = st.MergeArmed != ""
	if notice != "" {
		w.Notice(ctx, st.ChannelID, notice)
	}
	return alertText(t, st.Failed)
}

// sendFix sends the failure to the agent, for reason.
func (w *Watcher) sendFix(ctx context.Context, e *entry, st *db.PRState, sha, hash, prompt, reason string) {
	st.Attempts++
	st.LastFailedSHA, st.LastLogHash = sha, hash
	st.Attention, st.Reason = events.PRAttentionFixing, reason
	st.Watching = true
	w.mu.Lock()
	e.fix = fixQueued
	w.mu.Unlock()
	w.Enqueue(ctx, st.ChannelID, prompt)
}

// fixInFlight reports whether the thread's fix is queued or running.
func (w *Watcher) fixInFlight(e *entry) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return e.fix == fixQueued || e.fix == fixRunning
}

// autoFix is the thread's effective auto-fix setting.
func autoFix(t *Target, st *db.PRState) bool {
	if st.AutoFix != nil {
		return *st.AutoFix
	}
	return t.PR.AutoFix
}

// eventData is the channel.pr payload for the thread.
func (w *Watcher) eventData(ctx context.Context, t *Target, f forge.Forge, st *db.PRState, ci *forge.Status) events.PREventData {
	d := events.PREventData{
		ChannelID:         st.ChannelID,
		Kind:              t.Kind,
		Number:            t.Ref.Number,
		URL:               t.URL,
		HeadSHA:           ci.HeadSHA,
		BaseRef:           ci.BaseRef,
		Checks:            ci.Checks,
		MergeState:        ci.MergeState,
		AutoMerge:         ci.AutoMerge,
		Merged:            ci.Merged,
		Closed:            ci.Closed,
		MergeArmed:        st.MergeArmed,
		Attention:         st.Attention,
		AutoFix:           autoFix(t, st),
		AutoFixDefault:    t.PR.AutoFix,
		Attempts:          st.Attempts,
		MaxAttempts:       t.PR.MaxAttempts,
		Failed:            st.Failed,
		Reason:            st.Reason,
		CheckoutSHA:       w.Head(ctx, st.ChannelID),
		UnresolvedThreads: len(st.Threads),
	}
	if !ci.Merged && !ci.Closed {
		if s, err := f.MergeSettings(ctx, t.Ref); err == nil {
			d.MergeMethods = s.Methods
			d.MergeMethod, _ = forge.PickMethod(t.PR.MergeMethod, s)
		}
	}
	return d
}

// publish broadcasts data when it differs from what was last told, or
// carries an alert.
func (w *Watcher) publish(e *entry, data events.PREventData) {
	w.mu.Lock()
	last := e.last
	quiet := data
	quiet.Alert = ""
	e.last = &quiet
	w.mu.Unlock()
	if data.Alert == "" && last != nil && reflect.DeepEqual(*last, quiet) {
		return
	}
	w.Broadcast(data)
}

// prompt is the message that sends the failure to the agent, with the
// hash of the failure's logs.
func (w *Watcher) prompt(ctx context.Context, f forge.Forge, t *Target, sha string, fcs []forge.FailedCheck) (string, string) {
	var b, logs strings.Builder
	fmt.Fprintf(&b, "CI failed on %s for %s.\n", short(sha), label(t))
	for i, fc := range fcs {
		fmt.Fprintf(&b, "\n%s", fc.Label())
		if fc.URL != "" {
			fmt.Fprintf(&b, " (%s)", fc.URL)
		}
		b.WriteString(":\n")
		logs.WriteString(fc.Label() + "\n")
		if i >= maxLogs {
			b.WriteString("(log not included)\n")
			continue
		}
		log, err := f.Log(ctx, t.Ref, fc)
		if err != nil {
			w.Logger.Warn("pr: reading a failed check's log", "check", fc.Label(), "error", err)
		}
		if log == "" {
			b.WriteString("(no log available)\n")
			continue
		}
		logs.WriteString(log + "\n")
		fmt.Fprintf(&b, "```\n%s\n```\n", log)
	}
	// One line: the chat renders each newline as a line break.
	fmt.Fprintf(&b, "\nFix the cause, commit and push. If the failure is unrelated to this branch "+
		"(flaky test, infra, base branch), don't change code — re-run it with %s and say why.\n", rerunHint(t.Kind, fcs))
	return b.String(), forge.LogHash(logs.String())
}

// rerunHint is the command that re-runs the failed checks.
func rerunHint(kind string, fcs []forge.FailedCheck) string {
	var cmds []string
	for _, fc := range fcs {
		switch {
		case kind == forge.KindMR && fc.ID != "":
			cmds = append(cmds, "`glab ci retry "+fc.ID+"`")
		case kind == forge.KindPR && fc.RunID != "":
			cmds = append(cmds, "`gh run rerun "+fc.RunID+" --failed`")
		}
	}
	cmds = slices.Compact(cmds)
	if len(cmds) == 0 {
		if kind == forge.KindMR {
			return "`glab ci retry <job-id>`"
		}
		return "`gh run rerun <run-id> --failed`"
	}
	return strings.Join(cmds, ", ")
}

// alertText is the notification of a failure, e.g. "PR #12: lint failed".
func alertText(t *Target, fcs []forge.FailedCheck) string {
	var names []string
	for _, fc := range fcs {
		names = append(names, fc.Name)
	}
	what := "CI"
	switch {
	case len(names) > 3:
		what = strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
	case len(names) > 0:
		what = strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s: %s failed", label(t), what)
}

// agentCommit reports whether head is one of the agent's commits, which
// may be recorded abbreviated.
func agentCommit(shas []string, head string) bool {
	return head != "" && slices.ContainsFunc(shas, func(sha string) bool {
		return sha != "" && strings.HasPrefix(head, sha)
	})
}

// label names the PR/MR: "PR #12" or "MR !12".
func label(t *Target) string {
	if t.Kind == forge.KindMR {
		return fmt.Sprintf("MR !%d", t.Ref.Number)
	}
	return fmt.Sprintf("PR #%d", t.Ref.Number)
}

// short abbreviates a commit SHA.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
