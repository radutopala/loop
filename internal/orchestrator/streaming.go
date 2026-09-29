package orchestrator

import "github.com/radutopala/loop/internal/agent"

// streamTracker provides streaming callback helpers: it filters empty turns,
// tracks the last streamed text, and detects duplicate final responses.
type streamTracker struct {
	lastText string
	send     func(text string, ref agent.TurnRef)
}

// newStreamTracker creates a streamTracker that delegates non-empty turns to send.
func newStreamTracker(send func(text string, ref agent.TurnRef)) *streamTracker {
	return &streamTracker{send: send}
}

// OnTurn is the callback passed to agent.AgentRequest.OnTurn.
// It skips empty text and tracks the last streamed text.
func (s *streamTracker) OnTurn(text string, ref agent.TurnRef) {
	if text == "" {
		return
	}
	s.lastText = text
	s.send(text, ref)
}

// IsDuplicate returns true if finalResponse matches the last streamed text.
func (s *streamTracker) IsDuplicate(finalResponse string) bool {
	return finalResponse == s.lastText
}
