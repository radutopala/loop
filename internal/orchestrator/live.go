package orchestrator

import (
	"context"
	"errors"

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
	InterruptLive(channelID string) error
	LiveInfo(channelID string) (agent.LiveInfo, bool)
	LiveChannels() []string
}

// SetLiveRunner configures live sessions: lr runs them, and publish sends
// their live.status events.
func (o *Orchestrator) SetLiveRunner(lr LiveRunner, publish func(channelID, eventType string, data any)) {
	o.live = lr
	o.livePublish = publish
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
// already runs, a run holds the channel, or the channel's turns don't run
// live.
func (o *Orchestrator) WarmLiveSession(ctx context.Context, channelID, authorID string) (bool, error) {
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
	defer lock.Unlock()
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
	if err := o.live.StartLive(context.WithoutCancel(ctx), req, o.liveHooks(channelID)); err != nil {
		return false, err
	}
	o.publishLive(channelID, o.LiveSession(ctx, channelID))
	return true, nil
}

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
		Ended: func(reason string) {
			o.logger.Info("live session ended", "channel_id", channelID, "reason", reason)
			o.publishLive(channelID, LiveStatus{LiveInfo: agent.LiveInfo{ChannelID: channelID}, Reason: reason})
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
	// Not drainSpawn: tests run that inline, and this has to return first.
	o.drainWG.Add(1)
	go func() {
		defer o.drainWG.Done()
		defer lock.Unlock()
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
				Trigger: "live",
			})
		}
		return
	}
	o.deliverResponse(ctx, msg, resp, nil, lastStreamedText, runID, "live")
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
