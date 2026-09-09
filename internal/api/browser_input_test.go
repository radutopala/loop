package api

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/browser"
)

type BrowserInputSuite struct {
	suite.Suite
}

func TestBrowserInputSuite(t *testing.T) {
	suite.Run(t, new(BrowserInputSuite))
}

func (s *BrowserInputSuite) TestMergeInput() {
	tests := []struct {
		name    string
		pending []browser.InputEvent
		ev      browser.InputEvent
		want    []browser.InputEvent
	}{
		{
			name: "first event is appended",
			ev:   browser.InputEvent{Type: "scroll", DeltaY: 100},
			want: []browser.InputEvent{{Type: "scroll", DeltaY: 100}},
		},
		{
			name:    "consecutive scrolls sum their deltas and keep the latest position",
			pending: []browser.InputEvent{{Type: "scroll", X: 1, Y: 2, DeltaX: 10, DeltaY: 100}},
			ev:      browser.InputEvent{Type: "scroll", X: 3, Y: 4, DeltaX: 5, DeltaY: 20},
			want:    []browser.InputEvent{{Type: "scroll", X: 3, Y: 4, DeltaX: 15, DeltaY: 120}},
		},
		{
			name:    "consecutive moves collapse to the latest position",
			pending: []browser.InputEvent{{Type: "mousemove", X: 1, Y: 1}},
			ev:      browser.InputEvent{Type: "mousemove", X: 9, Y: 9},
			want:    []browser.InputEvent{{Type: "mousemove", X: 9, Y: 9}},
		},
		{
			name:    "a click between scrolls is a barrier",
			pending: []browser.InputEvent{{Type: "scroll", DeltaY: 100}, {Type: "click", X: 5, Y: 5}},
			ev:      browser.InputEvent{Type: "scroll", DeltaY: 20},
			want: []browser.InputEvent{
				{Type: "scroll", DeltaY: 100},
				{Type: "click", X: 5, Y: 5},
				{Type: "scroll", DeltaY: 20},
			},
		},
		{
			name:    "a move does not merge into a scroll",
			pending: []browser.InputEvent{{Type: "scroll", DeltaY: 100}},
			ev:      browser.InputEvent{Type: "mousemove", X: 2, Y: 2},
			want:    []browser.InputEvent{{Type: "scroll", DeltaY: 100}, {Type: "mousemove", X: 2, Y: 2}},
		},
		{
			name:    "clicks are never merged",
			pending: []browser.InputEvent{{Type: "click", X: 1, Y: 1}},
			ev:      browser.InputEvent{Type: "click", X: 2, Y: 2},
			want:    []browser.InputEvent{{Type: "click", X: 1, Y: 1}, {Type: "click", X: 2, Y: 2}},
		},
		{
			name:    "keystrokes are never merged",
			pending: []browser.InputEvent{{Type: "typetext", Text: "a"}},
			ev:      browser.InputEvent{Type: "typetext", Text: "b"},
			want:    []browser.InputEvent{{Type: "typetext", Text: "a"}, {Type: "typetext", Text: "b"}},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, mergeInput(tt.pending, tt.ev))
		})
	}
}

func (s *BrowserInputSuite) TestQueuePopEmpty() {
	q := newInputQueue()
	_, ok := q.pop()
	require.False(s.T(), ok)
}

func (s *BrowserInputSuite) TestQueuePreservesOrder() {
	q := newInputQueue()
	q.push(browser.InputEvent{Type: "click", X: 1})
	q.push(browser.InputEvent{Type: "keypress", Key: "Enter"})

	first, ok := q.pop()
	require.True(s.T(), ok)
	require.Equal(s.T(), "click", first.Type)

	second, ok := q.pop()
	require.True(s.T(), ok)
	require.Equal(s.T(), "keypress", second.Type)

	_, ok = q.pop()
	require.False(s.T(), ok)
}

// A burst larger than the wake channel's single slot must not block the
// producer: the read loop pushes while the worker is still busy dispatching.
func (s *BrowserInputSuite) TestQueuePushNeverBlocks() {
	q := newInputQueue()
	for range 100 {
		q.push(browser.InputEvent{Type: "click"})
	}
	require.Len(s.T(), q.pending, 100)
}

func (s *BrowserInputSuite) TestQueueCoalescesBurst() {
	q := newInputQueue()
	for range 50 {
		q.push(browser.InputEvent{Type: "scroll", X: 10, Y: 20, DeltaY: 120})
	}
	require.Len(s.T(), q.pending, 1, "a scroll burst must collapse instead of queueing")

	ev, ok := q.pop()
	require.True(s.T(), ok)
	require.Equal(s.T(), float64(50*120), ev.DeltaY)
}

func (s *BrowserInputSuite) TestWorkerDispatchesQueuedInput() {
	dispatched := make(chan struct{}, 4)
	mockCDP := &mockCDPSession{}
	mockCDP.On("MouseScroll", mock.Anything, float64(1), float64(2), float64(0), float64(240)).
		Run(func(mock.Arguments) { dispatched <- struct{}{} }).
		Return(nil)

	bc := &browserWSConn{
		logger: slog.Default(),
		cdp:    mockCDP,
		stopCh: make(chan struct{}),
		inputQ: newInputQueue(),
	}
	// Queue before starting the worker, so the merge is what is under test
	// rather than the race between producer and dispatcher.
	bc.inputQ.push(browser.InputEvent{Type: "scroll", X: 1, Y: 2, DeltaY: 120})
	bc.inputQ.push(browser.InputEvent{Type: "scroll", X: 1, Y: 2, DeltaY: 120})

	go bc.runInputWorker()
	defer close(bc.stopCh)

	select {
	case <-dispatched:
	case <-time.After(2 * time.Second):
		s.Fail("worker did not dispatch the coalesced scroll")
	}
	mockCDP.AssertExpectations(s.T())
}

func (s *BrowserInputSuite) TestWorkerStopsWithTheConnection() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
		inputQ: newInputQueue(),
	}
	done := make(chan struct{})
	go func() {
		bc.runInputWorker()
		close(done)
	}()

	close(bc.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.Fail("input worker did not stop with the connection")
	}
}
