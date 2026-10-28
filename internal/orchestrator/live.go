package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// liveAuthorID authors the turns a live session starts by itself.
const liveAuthorID = "loop-live"

// EventLiveStatus is published on a channel when its live session starts or
// ends, with a LiveStatus.
const EventLiveStatus = "live.status"

// LiveStatus is the payload of a live.status event, and what LiveSession
// returns. Running is false once the session ended; Reason says why.
type LiveStatus struct {
	Running bool `json:"running"`
	// Enabled is whether the channel's chat turns run live; set by
	// LiveSession only.
	Enabled bool `json:"enabled,omitempty"`
	agent.LiveInfo
	Reason string `json:"reason,omitempty"`
}

// ErrLiveUnavailable is returned when no live runner is configured.
var ErrLiveUnavailable = errors.New("live sessions are not available")

// LiveRunner runs the channels' live sessions. container.DockerRunner
// implements it.
type LiveRunner interface {
	StartLive(ctx context.Context, req *agent.AgentRequest, hooks agent.LiveHooks) error
	StopLive(channelID, reason string) error
	StopLiveTask(channelID, taskID string) error
	InterruptLive(channelID string) error
	LiveInfo(channelID string) (agent.LiveInfo, bool)
	LiveChannels() []string
	SendLiveControl(ctx context.Context, channelID string, request json.RawMessage) (json.RawMessage, error)
	HoldLive(channelID, holder string, on bool)
	LiveHolds() map[string][]string
}

// SetLiveRunner configures live sessions: lr runs them, and publish sends
// their live.status events.
func (o *Orchestrator) SetLiveRunner(lr LiveRunner, publish func(channelID, eventType string, data any)) {
	o.live = lr
	o.livePublish = publish
}

// SetLiveSystem sends fn the live sessions' "system" events but a turn's
// "init", by subtype, as the CLI wrote them.
func (o *Orchestrator) SetLiveSystem(fn func(channelID, subtype string, event json.RawMessage)) {
	o.liveSystem = fn
}

// LiveControl sends request, a stream-json control request's body, to the
// channel's live session and returns the body of its answer.
func (o *Orchestrator) LiveControl(ctx context.Context, channelID string, request json.RawMessage) (json.RawMessage, error) {
	if o.live == nil {
		return nil, ErrLiveUnavailable
	}
	return o.live.SendLiveControl(ctx, channelID, request)
}

// HoldLive keeps the channel's live session from ending idle while holder
// holds it, or with on false lets go of holder's hold.
func (o *Orchestrator) HoldLive(channelID, holder string, on bool) error {
	if o.live == nil {
		return ErrLiveUnavailable
	}
	o.live.HoldLive(channelID, holder, on)
	return nil
}

// LiveHolds returns the holders of each held channel.
func (o *Orchestrator) LiveHolds() map[string][]string {
	if o.live == nil {
		return nil
	}
	return o.live.LiveHolds()
}

// EndLiveSession ends the channel's live session, telling its viewers
// reason. The channel's next chat turn starts a new one, resuming the
// session.
func (o *Orchestrator) EndLiveSession(channelID, reason string) error {
	if o.live == nil {
		return ErrLiveUnavailable
	}
	return o.live.StopLive(channelID, reason)
}

// SweepLiveSessions ends the live sessions of channels that no longer
// exist, after a channel or thread was deleted.
func (o *Orchestrator) SweepLiveSessions(ctx context.Context) {
	if o.live == nil {
		return
	}
	for _, channelID := range o.live.LiveChannels() {
		channel, err := o.store.GetChannel(ctx, channelID)
		if err != nil {
			o.logger.Error("getting channel", "error", err, "channel_id", channelID)
			continue
		}
		if channel == nil {
			_ = o.live.StopLive(channelID, "deleted")
		}
	}
}

// StopLiveTask stops the background task taskID of the channel's live
// session.
func (o *Orchestrator) StopLiveTask(channelID, taskID string) error {
	if o.live == nil {
		return ErrLiveUnavailable
	}
	return o.live.StopLiveTask(channelID, taskID)
}

// LiveSession describes the channel's live session, and whether its chat
// turns run live.
func (o *Orchestrator) LiveSession(ctx context.Context, channelID string) LiveStatus {
	if o.live == nil {
		return LiveStatus{}
	}
	info, ok := o.live.LiveInfo(channelID)
	status := LiveStatus{Running: ok, LiveInfo: info}
	channel, err := o.store.GetChannel(ctx, channelID)
	if err == nil && channel != nil {
		status.Enabled = !db.IsHiddenKind(channel.Kind) && o.currentConfig().LiveFor(channel.Platform)
	}
	return status
}

func (o *Orchestrator) publishLive(channelID string, status LiveStatus) {
	if o.livePublish != nil {
		o.livePublish(channelID, EventLiveStatus, status)
	}
}

// useLive reports whether req, the run of msg in channel, runs as a turn of
// the channel's live session: plain chat turns do, on platforms live
// sessions are on for; hidden threads' runs, read-only, review and plugin
// runs, and runs with their own system prompt don't.
func (o *Orchestrator) useLive(req *agent.AgentRequest, msg *bot.IncomingMessage, channel *db.Channel) bool {
	if o.live == nil || db.IsHiddenKind(channel.Kind) || !o.currentConfig().LiveFor(channel.Platform) {
		return false
	}
	if req.AgentID != agent.ChatAgentID || req.ReadOnly || req.ReviewMode || req.SystemPrompt != "" || len(req.MCPArgs) > 0 {
		return false
	}
	switch msg.AuthorID {
	case learnAuthorID, explainAuthorID, pluginAuthorID:
		return false
	}
	return true
}

// WarmLiveSession starts the channel's live session ahead of its next
// chat turn, as authorID's turn would, so the turn doesn't wait for the
// container. It reports whether it started one: it doesn't when a session
// already runs, a run holds the channel, the channel's turns don't run
// live, or the start would wait out an image build: it holds the channel
// meanwhile, so the turn being typed would sit queued with no word of the
// build, which the turn's own start shows.
func (o *Orchestrator) WarmLiveSession(ctx context.Context, channelID, authorID string) (bool, error) {
	return o.warmLive(ctx, channelID, authorID, false)
}

// KeepLiveSession starts the channel's live session for a holder, as
// WarmLiveSession does, except that an image build doesn't stop it: the
// start goes on in the background, and the session's live status tells
// when it runs.
func (o *Orchestrator) KeepLiveSession(ctx context.Context, channelID, authorID string) error {
	_, err := o.warmLive(ctx, channelID, authorID, true)
	return err
}

// warmLive starts the channel's live session ahead of authorID's turn. A
// start that waits out an image build is given up, or, with throughBuild,
// left to go on in the background.
func (o *Orchestrator) warmLive(ctx context.Context, channelID, authorID string, throughBuild bool) (bool, error) {
	if o.live == nil {
		return false, ErrLiveUnavailable
	}
	if _, running := o.live.LiveInfo(channelID); running {
		return false, nil
	}
	lock := channelLock(&o.channelLocks, channelID)
	if !lock.TryLock() {
		return false, nil
	}
	locked := true
	defer func() {
		if locked {
			lock.Unlock()
		}
	}()
	channel, err := o.store.GetChannel(ctx, channelID)
	if err != nil {
		return false, err
	}
	if channel == nil || db.IsHiddenKind(channel.Kind) {
		return false, nil
	}
	msg := &bot.IncomingMessage{ChannelID: channelID, AuthorID: authorID, Platform: channel.Platform}
	req, _, _, err := o.prepareAgentRequest(ctx, msg, "")
	if err != nil {
		return false, err
	}
	if !req.Live {
		return false, nil
	}
	startCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	building := make(chan struct{})
	var buildOnce sync.Once
	req.OnActivity = func(activity, _ string) {
		if activity != "image_build" {
			return
		}
		if !throughBuild {
			cancel(errWarmImageBuild)
			return
		}
		buildOnce.Do(func() { close(building) })
	}
	done := make(chan error, 1)
	locked = false
	go func() {
		err := o.live.StartLive(startCtx, req, o.liveHooks(channelID))
		if err == nil {
			o.publishLive(channelID, o.LiveSession(startCtx, channelID))
		}
		// Unlocked before the answer, so the channel is free once warming
		// returns.
		lock.Unlock()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			if errors.Is(context.Cause(startCtx), errWarmImageBuild) {
				o.logger.Info("not warming the live session during an image build", "channel_id", channelID)
				return false, nil
			}
			return false, err
		}
		return true, nil
	case <-building:
		o.logger.Info("starting the live session after an image build", "channel_id", channelID)
		go func() {
			if err := <-done; err != nil {
				o.logger.Warn("starting a held live session", "channel_id", channelID, "error", err)
			}
		}()
		return false, nil
	}
}

// errWarmImageBuild is why a warm-up stops starting a live session.
var errWarmImageBuild = errors.New("an image build is in progress")

// runChat runs req, a message's run: a live turn starts the channel's live
// session when none runs, from req, which carries the fork or resume the
// turn would have run one-shot. A run that can't go live, such as one with
// live sessions turned off, ends the session it would resume, so the two
// don't write to it at once. The run itself then goes to the runner, which
// hands a live turn to the session, or runs it one-shot when the session
// couldn't start.
func (o *Orchestrator) runChat(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	if o.live != nil {
		info, running := o.live.LiveInfo(req.ChannelID)
		switch {
		case req.Live && !running:
			if err := o.live.StartLive(ctx, req, o.liveHooks(req.ChannelID)); err != nil {
				o.logger.Warn("starting live session; running one-shot", "error", err, "channel_id", req.ChannelID)
			} else {
				o.publishLive(req.ChannelID, o.LiveSession(ctx, req.ChannelID))
			}
		case !req.Live && running && !req.ForkSession && req.SessionID == info.SessionID:
			_ = o.live.StopLive(req.ChannelID, "one-shot run")
		}
	}
	return o.runner.Run(ctx, req)
}

// liveHooks is how the channel's live session reports to the orchestrator.
func (o *Orchestrator) liveHooks(channelID string) agent.LiveHooks {
	return agent.LiveHooks{
		BeginTurn: func() (*agent.AgentRequest, func(*agent.AgentResponse, error)) {
			return o.beginLiveTurn(channelID)
		},
		Activity: func(activity, detail string) {
			if o.events != nil {
				o.events.BroadcastAgentActivity(channelID, activityData(activity, detail))
			}
		},
		BackgroundTasks: func(tasks []agent.BackgroundTask) {
			if o.events != nil {
				o.events.BroadcastAgentActivity(channelID, backgroundData(tasks))
			}
		},
		Ended: func(reason string) {
			o.logger.Info("live session ended", "channel_id", channelID, "reason", reason)
			o.publishLive(channelID, LiveStatus{LiveInfo: agent.LiveInfo{ChannelID: channelID}, Reason: reason})
		},
		Restarted: func(reason string) {
			status := o.LiveSession(context.Background(), channelID)
			status.Reason = reason
			o.publishLive(channelID, status)
		},
		System: func(subtype string, event json.RawMessage) {
			if o.liveSystem != nil {
				o.liveSystem(channelID, subtype, event)
			}
		},
		Suggestion: func(text string) {
			if o.events != nil {
				o.events.BroadcastPromptSuggestion(channelID, events.PromptSuggestionEventData{Suggestion: text})
			}
		},
		Prompt: func(text string) {
			msgID := storeUserPrompt(context.Background(), o.store, o.events, channelID, liveAuthorID, "remote", text)
			// The turn it starts answers it.
			if _, ok := o.liveTriggers.Load(channelID); ok {
				o.liveTriggers.Store(channelID, msgID)
			}
		},
	}
}

// liveOutcome is the outcome of a turn a live session started by itself.
type liveOutcome struct {
	resp *agent.AgentResponse
	err  error
}

// beginLiveTurn runs a turn the channel's live session started by itself
// (a background task or a monitor of the agent's ended) as a run of its
// own: streamed, stoppable, and holding the channel's queue until it ends.
// It returns the request whose callbacks get the turn's events, and the
// func that hands the run the turn's outcome.
//
// It is called on the session's reader, so it must not wait for a run to
// end: with the queue busy, a message's turn is about to start and waits
// for this one, so this turn goes unstreamed and only its answer is posted.
func (o *Orchestrator) beginLiveTurn(channelID string) (*agent.AgentRequest, func(*agent.AgentResponse, error)) {
	lock := channelLock(&o.channelLocks, channelID)
	if !lock.TryLock() {
		return nil, func(resp *agent.AgentResponse, err error) {
			if err == nil && resp.Response != "" {
				o.postLiveAnswer(channelID, resp.Response)
			}
		}
	}

	ready := make(chan *agent.AgentRequest)
	outcome := make(chan liveOutcome, 1)
	run := func(runCtx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
		ready <- req
		select {
		case out := <-outcome:
			return out.resp, out.err
		case <-runCtx.Done():
		}
		// Stopped: the session interrupts the turn, which still ends with
		// a result.
		if err := o.live.InterruptLive(channelID); err != nil {
			o.logger.Warn("interrupting the live turn", "error", err, "channel_id", channelID)
		}
		out := <-outcome
		if out.err == nil {
			out.err = runCtx.Err()
		}
		return out.resp, out.err
	}
	// Set before the turn can store anything, and dropped before the
	// channel is free for the next one.
	o.liveTriggers.Store(channelID, o.lastTurnTrigger(context.Background(), channelID))
	// Not drainSpawn: tests run that inline, and this has to return first.
	o.drainWG.Add(1)
	go func() {
		defer o.drainWG.Done()
		defer lock.Unlock()
		defer o.liveTriggers.Delete(channelID)
		o.runLiveTurn(channelID, run)
	}()
	return <-ready, func(resp *agent.AgentResponse, err error) {
		outcome <- liveOutcome{resp, err}
	}
}

// runLiveTurn runs a turn a live session started by itself, run being the
// wait for its outcome.
func (o *Orchestrator) runLiveTurn(channelID string, run func(context.Context, *agent.AgentRequest) (*agent.AgentResponse, error)) {
	ctx := context.Background()
	msg := &bot.IncomingMessage{ChannelID: channelID, AuthorID: liveAuthorID}
	channel, err := o.store.GetChannel(ctx, channelID)
	if err != nil {
		o.logger.Error("getting channel", "error", err, "channel_id", channelID)
	}
	if channel != nil {
		msg.Platform = channel.Platform
	}
	req := o.buildAgentRequest(channelID, nil, channel)
	defer o.activeRuns.Delete(channelID)
	resp, lastStreamedText, runID, finish, err := o.executeAgentRun(ctx, msg, req, channel, run)
	if err != nil {
		if o.events != nil {
			o.events.BroadcastAgentStatus(channelID, events.AgentStatusEventData{
				Status:  finish.status,
				RunID:   runID,
				Error:   finish.errMsg,
				Stopped: finish.stopped,
				Trigger: "live",
			})
		}
		return
	}
	o.deliverResponse(ctx, msg, resp, nil, lastStreamedText, runID, "live")
}

// lastTurnSearch is how many of a channel's latest messages lastTurnTrigger
// looks through.
const lastTurnSearch = 50

// lastTurnTrigger is the message that asked for the channel's latest turn:
// the trigger of its latest reply, or its latest prompt that ran without
// one. Queued prompts haven't run, and replies of no turn (notices, answers
// posted unstreamed) say nothing. It is "" when there is none.
func (o *Orchestrator) lastTurnTrigger(ctx context.Context, channelID string) string {
	msgs, err := o.store.GetRecentMessages(ctx, channelID, lastTurnSearch)
	if err != nil {
		o.logger.Warn("finding the latest turn", "error", err, "channel_id", channelID)
		return ""
	}
	for _, m := range msgs {
		switch {
		case m.IsBot && m.TriggerMsgID != "":
			return m.TriggerMsgID
		case !m.IsBot && m.IsProcessed:
			return m.MsgID
		}
	}
	return ""
}

// turnTrigger is the message the rows of msg's turn link to: msg itself,
// or, for a turn a live session started by itself, the remote prompt it
// answers or else the turn before it, which it carries on. Explain and Learn
// find a turn by that link, and the chat ends a turn at its last reply.
func (o *Orchestrator) turnTrigger(msg *bot.IncomingMessage) string {
	if msg.MessageID != "" {
		return msg.MessageID
	}
	v, _ := o.liveTriggers.Load(msg.ChannelID)
	id, _ := v.(string)
	return id
}

// postLiveAnswer posts the answer of a turn a live session ran unstreamed.
func (o *Orchestrator) postLiveAnswer(channelID, text string) {
	ctx := context.Background()
	if err := o.bot.SendMessage(ctx, &bot.OutgoingMessage{ChannelID: channelID, Content: text}); err != nil {
		o.logger.Error("sending live answer", "error", err, "channel_id", channelID)
	}
	storeBotMessage(ctx, o.store, o.events, channelID, text, "")
}

// activityData is the agent.activity payload of an activity the runner
// reports.
func activityData(activity, detail string) events.AgentActivityEventData {
	data := events.AgentActivityEventData{Activity: activity}
	if activity == "model" {
		data.Model = detail
	} else {
		data.Description = detail
	}
	return data
}

// backgroundData is the agent.activity payload of a run's background tasks.
func backgroundData(tasks []agent.BackgroundTask) events.AgentActivityEventData {
	return events.AgentActivityEventData{
		Activity:    "background_tasks",
		Description: agent.BackgroundSummary(tasks),
		Tasks:       tasks,
	}
}
