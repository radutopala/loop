package browser

import (
	"context"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// wedge makes every CDP call block until the test ends, standing in for a
// target that accepts a command and never answers it.
func (s *CDPSuite) wedge() {
	release := make(chan struct{})
	s.T().Cleanup(func() { close(release) })

	s.client.commandTimeout = 20 * time.Millisecond
	s.client.runFn = func(_ context.Context, _ ...chromedp.Action) error {
		<-release
		return nil
	}
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) {
		<-release
		return nil, nil
	}
	s.client.activateFunc = func(_ context.Context, _ target.ID) error {
		<-release
		return nil
	}
}

// returnsWithin fails the test unless fn returns well inside the time a wedged
// call would have parked the caller for.
func (s *CDPSuite) returnsWithin(d time.Duration, fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		s.T().Fatal("call did not return: a wedged target must not take the caller with it")
	}
}

func (s *CDPSuite) TestCommandDeadline() {
	require.Equal(s.T(), defaultCommandTimeout, s.client.commandDeadline())

	s.client.commandTimeout = 2 * time.Second
	require.Equal(s.T(), 2*time.Second, s.client.commandDeadline())
}

func (s *CDPSuite) TestBoundedReturnsWhatTheCallProduced() {
	val, err := bounded(s.client, func() (string, error) { return "answered", nil })
	require.NoError(s.T(), err)
	require.Equal(s.T(), "answered", val)
}

// Chrome never acknowledges input dispatched to a backgrounded target, and one
// worker drains the pane's input queue in order, so a scroll that parks takes
// every click, keystroke and paste behind it down with it — silently, while
// frames keep arriving and the pane still looks alive.
func (s *CDPSuite) TestInputGivesUpOnAWedgedTarget() {
	s.wedge()

	cases := []struct {
		name string
		call func() error
	}{
		{"click", func() error { return s.client.MouseClick(context.Background(), 1, 2, "left", 1) }},
		{"mousemove", func() error { return s.client.MouseMove(context.Background(), 1, 2, 0) }},
		{"scroll", func() error { return s.client.MouseScroll(context.Background(), 1, 2, 0, 5) }},
		{"keypress", func() error { return s.client.KeyPress(context.Background(), "a", 0) }},
		{"named keypress", func() error { return s.client.KeyPress(context.Background(), "Enter", 0) }},
		{"typetext", func() error { return s.client.TypeText(context.Background(), "hi") }},
		{"paste", func() error { return s.client.InsertText(context.Background(), "hi") }},
		{"mousedown", func() error { return s.client.MouseDown(context.Background(), 1, 2, "left") }},
		{"mouseup", func() error { return s.client.MouseUp(context.Background(), 1, 2, "left") }},
		{"copy", func() error {
			_, err := s.client.ReadSelection(context.Background())
			return err
		}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			var err error
			s.returnsWithin(time.Second, func() { err = tc.call() })
			require.ErrorContains(s.T(), err, "timed out")
		})
	}
}

// SwitchTarget stops the screencast before it activates, so an unbounded stop
// let one wedged tab freeze every later tab switch.
func (s *CDPSuite) TestStopScreencastGivesUpOnAWedgedTarget() {
	s.wedge()
	s.client.screencasting = true

	s.returnsWithin(time.Second, s.client.StopScreencast)
	require.False(s.T(), s.screencastingNow())
}

// The listing gates every tab switch and the pane's liveness check.
func (s *CDPSuite) TestListTabsGivesUpOnAWedgedTarget() {
	s.wedge()

	var err error
	s.returnsWithin(time.Second, func() { _, err = s.client.ListTabs(context.Background()) })
	require.ErrorContains(s.T(), err, "timed out")
}

func (s *CDPSuite) TestSwitchTargetGivesUpOnAWedgedTarget() {
	s.wedge()

	var err error
	s.returnsWithin(time.Second, func() { err = s.client.SwitchTarget("some-target") })
	require.ErrorContains(s.T(), err, "timed out")
}
