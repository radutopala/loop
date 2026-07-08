package container

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types/events"
)

// oomDebounceWindow limits how often OOMWatcher posts a notice for the same
// container, so a container that repeatedly hits its memory limit doesn't
// spam the channel with a message per kill.
const oomDebounceWindow = 5 * time.Minute

// oomResubscribeDelay is how long OOMWatcher waits before reopening the
// Docker events stream after it ends (closed channel or error).
const oomResubscribeDelay = 5 * time.Second

// OOMWatcher watches Docker "container oom" events for loop-managed agent
// containers and posts a channel notice when one fires. The kernel emits
// this event even when only a process inside the container (not the
// container itself) is killed for exceeding the memory limit, so this is
// the only signal available for that case — there's no corresponding "die"
// event to react to.
type OOMWatcher struct {
	eventsFunc func(ctx context.Context) (<-chan events.Message, <-chan error)
	notify     func(ctx context.Context, channelID, content string)
	logger     *slog.Logger

	now   func() time.Time
	sleep func(time.Duration)

	lastNotified map[string]time.Time
}

// NewOOMWatcher creates an OOMWatcher. eventsFunc streams Docker OOM events
// (e.g. Client.OOMEvents), and notify delivers the resulting notice to a
// channel (e.g. via orchestrator.StoreSystemNotice).
func NewOOMWatcher(eventsFunc func(ctx context.Context) (<-chan events.Message, <-chan error), notify func(ctx context.Context, channelID, content string), logger *slog.Logger) *OOMWatcher {
	return &OOMWatcher{
		eventsFunc:   eventsFunc,
		notify:       notify,
		logger:       logger,
		now:          time.Now,
		sleep:        time.Sleep,
		lastNotified: make(map[string]time.Time),
	}
}

// Run streams OOM events until ctx is canceled, resubscribing after errors
// or a closed stream. It never returns until ctx is done.
func (w *OOMWatcher) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		w.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		w.sleep(oomResubscribeDelay)
	}
}

// runOnce subscribes to the event stream once and processes events until it
// closes, an error arrives, or ctx is canceled.
func (w *OOMWatcher) runOnce(ctx context.Context) {
	msgCh, errCh := w.eventsFunc(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil {
				w.logger.Warn("oom watcher: event stream error, resubscribing", "error", err)
			}
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			w.handleEvent(ctx, msg)
		}
	}
}

// handleEvent posts a channel notice for a single OOM event, subject to
// per-container debouncing.
func (w *OOMWatcher) handleEvent(ctx context.Context, msg events.Message) {
	channelID := msg.Actor.Attributes[ChannelLabelKey]
	if channelID == "" {
		return
	}

	containerID := msg.Actor.ID
	now := w.now()
	if last, ok := w.lastNotified[containerID]; ok && now.Sub(last) < oomDebounceWindow {
		return
	}
	w.lastNotified[containerID] = now

	name := msg.Actor.Attributes["name"]
	if name == "" {
		name = containerID
	}
	content := fmt.Sprintf(
		"⚠️ Container `%s` hit its memory limit — the kernel killed a process inside it (likely a Claude session). "+
			"If a terminal agent died, run `claude --continue` in that terminal to resume, or raise `container_memory_mb` in config.",
		name,
	)
	w.notify(ctx, channelID, content)
}
