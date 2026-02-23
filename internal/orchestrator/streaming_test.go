package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type StreamTrackerSuite struct {
	suite.Suite
}

func TestStreamTrackerSuite(t *testing.T) {
	suite.Run(t, new(StreamTrackerSuite))
}

func (s *StreamTrackerSuite) TestOnTurnSkipsEmpty() {
	var calls []string
	tracker := newStreamTracker(func(text string) {
		calls = append(calls, text)
	})

	tracker.OnTurn("")
	require.Empty(s.T(), calls)
	require.Empty(s.T(), tracker.lastText)
}

func (s *StreamTrackerSuite) TestOnTurnDelegatesToSend() {
	var calls []string
	tracker := newStreamTracker(func(text string) {
		calls = append(calls, text)
	})

	tracker.OnTurn("hello")
	tracker.OnTurn("world")

	require.Equal(s.T(), []string{"hello", "world"}, calls)
	require.Equal(s.T(), "world", tracker.lastText)
}

func (s *StreamTrackerSuite) TestOnTurnSkipsEmptyBetweenNonEmpty() {
	var calls []string
	tracker := newStreamTracker(func(text string) {
		calls = append(calls, text)
	})

	tracker.OnTurn("first")
	tracker.OnTurn("")
	tracker.OnTurn("second")

	require.Equal(s.T(), []string{"first", "second"}, calls)
	require.Equal(s.T(), "second", tracker.lastText)
}

func (s *StreamTrackerSuite) TestIsDuplicateMatchesLastText() {
	tracker := newStreamTracker(func(text string) {})

	tracker.OnTurn("final answer")

	require.True(s.T(), tracker.IsDuplicate("final answer"))
	require.False(s.T(), tracker.IsDuplicate("different answer"))
}

func (s *StreamTrackerSuite) TestIsDuplicateReturnsFalseWhenNoTurns() {
	tracker := newStreamTracker(func(text string) {})

	require.False(s.T(), tracker.IsDuplicate("anything"))
	// Empty string matches zero-value lastText, but callers guard with nil tracker check
	require.True(s.T(), tracker.IsDuplicate(""))
}
