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
	require.False(s.T(), screencastingNow(s.client))
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

func (s *CDPSuite) TestPageReadDeadline() {
	require.Equal(s.T(), defaultPageReadTimeout, s.client.pageReadDeadline())

	s.client.pageReadTimeout = 2 * time.Second
	require.Equal(s.T(), 2*time.Second, s.client.pageReadDeadline())
}

// A capture on a crashed renderer is answered as little as a tree walk is.
func (s *CDPSuite) TestScreenshotGivesUpOnAWedgedTarget() {
	s.wedge()
	s.client.pageReadTimeout = 20 * time.Millisecond

	var err error
	s.returnsWithin(time.Second, func() { _, err = s.client.Screenshot(context.Background()) })
	require.ErrorContains(s.T(), err, "timed out")
}

func (s *CDPSuite) TestScreenshotAbandonedWhenCallerGivesUp() {
	s.wedge()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var err error
	s.returnsWithin(time.Second, func() { _, err = s.client.Screenshot(ctx) })
	require.ErrorIs(s.T(), err, context.Canceled)
}

func (s *CDPSuite) TestNavigationDeadline() {
	require.Equal(s.T(), defaultNavigationTimeout, s.client.navigationDeadline())

	s.client.navigationTimeout = 2 * time.Second
	require.Equal(s.T(), 2*time.Second, s.client.navigationDeadline())
}

// Everything the agent does to a page went through an unbounded call until
// now. A navigation waits for a load event the dead tab never reports, an
// evaluate for a result it never returns, and the tool call behind them sat
// for as long as its client allowed — 73 seconds for one evaluate, ended by
// the CDP connection dropping rather than by anything noticing here.
func (s *CDPSuite) TestPageCallsGiveUpOnAWedgedTarget() {
	cases := []struct {
		name string
		call func() error
	}{
		{"navigate", func() error { return s.client.Navigate(context.Background(), "https://example.test") }},
		{"reload", func() error { return s.client.Reload(context.Background()) }},
		{"back", func() error { return s.client.GoBack(context.Background()) }},
		{"forward", func() error { return s.client.GoForward(context.Background()) }},
		{"page info", func() error {
			_, err := s.client.GetPageInfo(context.Background())
			return err
		}},
		{"evaluate", func() error {
			_, err := s.client.EvaluateJS(context.Background(), "1+1")
			return err
		}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			// A fresh client per case: a wedged call is abandoned rather than
			// cancelled, and it holds the one it ran on until the test ends.
			s.SetupTest()
			s.wedge()
			s.client.navigationTimeout = 20 * time.Millisecond
			s.client.pageReadTimeout = 20 * time.Millisecond

			var err error
			s.returnsWithin(time.Second, func() { err = tc.call() })
			require.ErrorContains(s.T(), err, "timed out")
		})
	}
}

func (s *CDPSuite) TestPageCallsAbandonedWhenCallerGivesUp() {
	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"navigate", func(ctx context.Context) error { return s.client.Navigate(ctx, "https://example.test") }},
		{"reload", func(ctx context.Context) error { return s.client.Reload(ctx) }},
		{"back", func(ctx context.Context) error { return s.client.GoBack(ctx) }},
		{"forward", func(ctx context.Context) error { return s.client.GoForward(ctx) }},
		{"page info", func(ctx context.Context) error {
			_, err := s.client.GetPageInfo(ctx)
			return err
		}},
		{"evaluate", func(ctx context.Context) error {
			_, err := s.client.EvaluateJS(ctx, "1+1")
			return err
		}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.wedge()

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			var err error
			s.returnsWithin(time.Second, func() { err = tc.call(ctx) })
			require.ErrorIs(s.T(), err, context.Canceled)
		})
	}
}
