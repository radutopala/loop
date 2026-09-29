package orchestrator

import (
	"context"
	"sync"
)

// sessionSwitches defers a channel's session switch asked for mid-run to the
// run's end: the run saves its own session id when it ends, which would
// overwrite a switch made meanwhile. running and pending share one lock, so
// a switch is either applied now or picked up by the run's end, never lost
// between the two.
type sessionSwitches struct {
	mu      sync.Mutex
	running map[string]bool
	pending map[string]string // channel id → session id to switch to
}

// SwitchSession makes sessionID the channel's session, so its next run
// resumes that conversation. While a chat run is in progress the switch is
// deferred to its end, and deferred reports true. A session another channel
// or thread also has is marked fork pending, so the next run forks it
// rather than writing into the other's conversation.
func (o *Orchestrator) SwitchSession(ctx context.Context, channelID, sessionID string) (bool, error) {
	o.sessions.mu.Lock()
	defer o.sessions.mu.Unlock()
	if o.sessions.running[channelID] {
		if o.sessions.pending == nil {
			o.sessions.pending = map[string]string{}
		}
		o.sessions.pending[channelID] = sessionID
		return true, nil
	}
	return false, o.applySessionSwitch(ctx, channelID, sessionID)
}

// sessionRunStarted marks a chat run in progress in the channel, before it
// reads the channel's session to resume.
func (o *Orchestrator) sessionRunStarted(channelID string) {
	o.sessions.mu.Lock()
	defer o.sessions.mu.Unlock()
	if o.sessions.running == nil {
		o.sessions.running = map[string]bool{}
	}
	o.sessions.running[channelID] = true
}

// sessionRunDone ends the channel's run, after it saved its session id, and
// applies a switch deferred during it.
func (o *Orchestrator) sessionRunDone(ctx context.Context, channelID string) {
	o.sessions.mu.Lock()
	defer o.sessions.mu.Unlock()
	delete(o.sessions.running, channelID)
	sessionID, ok := o.sessions.pending[channelID]
	if !ok {
		return
	}
	delete(o.sessions.pending, channelID)
	if err := o.applySessionSwitch(ctx, channelID, sessionID); err != nil {
		o.logger.Error("switching session after run", "error", err, "channel_id", channelID, "session_id", sessionID)
	}
}

func (o *Orchestrator) applySessionSwitch(ctx context.Context, channelID, sessionID string) error {
	inUse, err := o.store.SessionInUse(ctx, sessionID, channelID)
	if err != nil {
		return err
	}
	if inUse {
		_, err = o.store.MarkSessionForkPending(ctx, channelID, sessionID)
		return err
	}
	return o.store.UpdateSessionID(ctx, channelID, sessionID)
}
