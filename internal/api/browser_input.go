package api

import (
	"sync"

	"github.com/radutopala/loop/internal/browser"
)

// inputQueue buffers pointer input for one browser pane, collapsing bursts that
// the sidecar cannot keep up with.
//
// Dispatching a wheel or move event costs about one compositor frame — measured
// at ~22ms against a real page, so roughly 45 events a second — while a trackpad
// emits 60-120. Dispatching straight from the WebSocket read loop therefore built
// a backlog that kept replaying long after the user's fingers stopped, which is
// what the pane looked like: input first, picture much later.
//
// Coalescing bounds that backlog instead of dropping input: consecutive moves
// collapse to the latest position, consecutive scrolls sum their deltas, and a
// summed wheel delta scrolls a page to exactly the same offset as the individual
// events would have (verified against Chrome: 10 x 120 and 1 x 1200 both land on
// scrollY 1200).
type inputQueue struct {
	mu      sync.Mutex
	pending []browser.InputEvent

	// wake carries a single token: a signal that the queue is non-empty, not a
	// count of events. It is buffered and written non-blocking so a producer
	// (the read loop) never waits on the dispatcher.
	wake chan struct{}
}

func newInputQueue() *inputQueue {
	return &inputQueue{wake: make(chan struct{}, 1)}
}

// push adds an event, merging it into the tail when possible, and signals the
// worker. It never blocks.
func (q *inputQueue) push(ev browser.InputEvent) {
	q.mu.Lock()
	q.pending = mergeInput(q.pending, ev)
	q.mu.Unlock()

	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// pop returns the oldest pending event, or false when the queue is empty.
func (q *inputQueue) pop() (browser.InputEvent, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return browser.InputEvent{}, false
	}
	ev := q.pending[0]
	q.pending = q.pending[1:]
	return ev, true
}

// mergeInput appends ev to pending, folding it into the tail when the two are
// the same kind of continuous gesture.
//
// Only the tail is considered, so a click or keystroke between two scrolls acts
// as a barrier: everything the user did still happens, and in the order they did
// it. Clicks and keys are never merged — they are discrete, cheap to dispatch
// (~2ms), and dropping or reordering one would lose real intent.
func mergeInput(pending []browser.InputEvent, ev browser.InputEvent) []browser.InputEvent {
	if len(pending) > 0 {
		last := &pending[len(pending)-1]
		switch {
		case ev.Type == "mousemove" && last.Type == "mousemove":
			// Only the final position matters; the ones in between are frames
			// the pane never got to show anyway.
			*last = ev
			return pending
		case ev.Type == "scroll" && last.Type == "scroll":
			ev.DeltaX += last.DeltaX
			ev.DeltaY += last.DeltaY
			*last = ev
			return pending
		}
	}
	return append(pending, ev)
}

// runInputWorker dispatches queued input until the connection goes away. It runs
// off the read loop so a slow CDP round trip cannot stall reading the socket —
// which is what let the backlog grow unbounded in the first place.
func (bc *browserWSConn) runInputWorker() {
	for {
		select {
		case <-bc.stopCh:
			return
		case <-bc.inputQ.wake:
			for {
				ev, ok := bc.inputQ.pop()
				if !ok {
					break
				}
				bc.dispatchInput(ev)
			}
		}
	}
}
