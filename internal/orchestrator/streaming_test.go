package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
)

type StreamTrackerSuite struct {
	suite.Suite
}

func TestStreamTrackerSuite(t *testing.T) {
	suite.Run(t, new(StreamTrackerSuite))
}

func (s *StreamTrackerSuite) TestOnTurnSkipsEmpty() {
	var calls []string
	tracker := newStreamTracker(func(text string, _ agent.TurnRef) {
		calls = append(calls, text)
	})

	tracker.OnTurn("", agent.TurnRef{})
	require.Empty(s.T(), calls)
	require.Empty(s.T(), tracker.lastText)
}

func (s *StreamTrackerSuite) TestOnTurnDelegatesToSend() {
	var calls []string
	tracker := newStreamTracker(func(text string, _ agent.TurnRef) {
		calls = append(calls, text)
	})

	tracker.OnTurn("hello", agent.TurnRef{})
	tracker.OnTurn("world", agent.TurnRef{})

	require.Equal(s.T(), []string{"hello", "world"}, calls)
	require.Equal(s.T(), "world", tracker.lastText)
}

func (s *StreamTrackerSuite) TestOnTurnSkipsEmptyBetweenNonEmpty() {
	var calls []string
	tracker := newStreamTracker(func(text string, _ agent.TurnRef) {
		calls = append(calls, text)
	})

	tracker.OnTurn("first", agent.TurnRef{})
	tracker.OnTurn("", agent.TurnRef{})
	tracker.OnTurn("second", agent.TurnRef{})

	require.Equal(s.T(), []string{"first", "second"}, calls)
	require.Equal(s.T(), "second", tracker.lastText)
}

func (s *StreamTrackerSuite) TestIsDuplicateMatchesLastText() {
	tracker := newStreamTracker(func(text string, _ agent.TurnRef) {})

	tracker.OnTurn("final answer", agent.TurnRef{})

	require.True(s.T(), tracker.IsDuplicate("final answer"))
	require.False(s.T(), tracker.IsDuplicate("different answer"))
}

func (s *StreamTrackerSuite) TestIsDuplicateReturnsFalseWhenNoTurns() {
	tracker := newStreamTracker(func(text string, _ agent.TurnRef) {})

	require.False(s.T(), tracker.IsDuplicate("anything"))
	// Empty string matches zero-value lastText, but callers guard with nil tracker check
	require.True(s.T(), tracker.IsDuplicate(""))
}
