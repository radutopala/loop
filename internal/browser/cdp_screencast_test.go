package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// --- StartScreencast ---

func (s *CDPSuite) TestStartScreencastAlreadyScreencasting() {
	s.client.screencasting = true
	ch := s.client.StartScreencast(60, 1920, 1080)
	require.NotNil(s.T(), ch)
	require.Equal(s.T(), (<-chan []byte)(s.client.frameCh), ch)
}

func (s *CDPSuite) TestStartScreencastNew() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	ch := s.client.StartScreencast(60, 1920, 1080)
	require.NotNil(s.T(), ch)
	require.True(s.T(), s.client.screencasting)
	require.NotNil(s.T(), listenerFn)

	// Wait a bit for the screencast goroutine to run
	time.Sleep(10 * time.Millisecond)
}

func (s *CDPSuite) TestStartScreencastFrameDecodeSuccess() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	ch := s.client.StartScreencast(60, 1920, 1080)
	require.NotNil(s.T(), listenerFn)

	// Simulate a screencast frame event with valid base64 data.
	frameData := []byte("jpeg-frame-data")
	encoded := base64.StdEncoding.EncodeToString(frameData)
	listenerFn(&cdppage.EventScreencastFrame{
		Data:      encoded,
		SessionID: 1,
	})

	// Read the frame from the channel.
	select {
	case data := <-ch:
		require.Equal(s.T(), frameData, data)
	case <-time.After(time.Second):
		s.T().Fatal("timeout waiting for frame")
	}
}

func (s *CDPSuite) TestStartScreencastFrameDecodeError() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	_ = s.client.StartScreencast(60, 1920, 1080)

	// Simulate an event with invalid base64.
	listenerFn(&cdppage.EventScreencastFrame{
		Data:      "not-valid-base64!!!",
		SessionID: 1,
	})

	// No frame should be sent (decode error logged).
	select {
	case <-s.client.frameCh:
		s.T().Fatal("should not receive frame on decode error")
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func (s *CDPSuite) TestStartScreencastDropsOldestFrameWhenBehind() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	_ = s.client.StartScreencast(60, 1920, 1080)

	send := func(body string, session int64) {
		listenerFn(&cdppage.EventScreencastFrame{
			Data:      base64.StdEncoding.EncodeToString([]byte(body)),
			SessionID: session,
		})
	}

	// Fill the buffer (cap 2), then overflow it twice.
	send("frame1", 1)
	send("frame2", 2)
	send("frame3", 3)
	send("frame4", 4)

	// A pane that is behind must get the newest frames, not the oldest ones:
	// handing it stale frames is what keeps the lag open.
	require.Len(s.T(), s.client.frameCh, 2)
	require.Equal(s.T(), "frame3", string(<-s.client.frameCh))
	require.Equal(s.T(), "frame4", string(<-s.client.frameCh))
}

func (s *CDPSuite) TestStartScreencastNonFrameEvent() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	_ = s.client.StartScreencast(60, 1920, 1080)

	// Send a non-frame event — should be ignored.
	listenerFn("not a frame event")

	select {
	case <-s.client.frameCh:
		s.T().Fatal("should not receive anything for non-frame events")
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func (s *CDPSuite) TestStartScreencastRunError() {
	s.client.listenFunc = func(_ context.Context, _ func(any)) {}
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		return errors.New("screencast start error")
	})

	_ = s.client.StartScreencast(60, 1920, 1080)
	// Error is logged, not returned. Wait for goroutine.
	time.Sleep(50 * time.Millisecond)
}

func (s *CDPSuite) TestStartScreencastAckError() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	var mu sync.Mutex
	callCount := 0
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		mu.Lock()
		callCount++
		c := callCount
		mu.Unlock()
		if c > 1 { // First call is StartScreencast, second is Ack
			return errors.New("ack error")
		}
		return nil
	})

	_ = s.client.StartScreencast(60, 1920, 1080)
	time.Sleep(10 * time.Millisecond)
	require.NotNil(s.T(), listenerFn)

	encoded := base64.StdEncoding.EncodeToString([]byte("frame"))
	listenerFn(&cdppage.EventScreencastFrame{Data: encoded, SessionID: 1})
	time.Sleep(50 * time.Millisecond) // Wait for ack goroutine
}

// --- StopScreencast ---

func (s *CDPSuite) TestStopScreencastWasScreencasting() {
	s.client.screencasting = true
	stopCh := make(chan struct{})
	s.client.stopCh = stopCh

	s.client.StopScreencast()

	require.False(s.T(), s.client.screencasting)
	select {
	case <-stopCh:
		// OK
	default:
		s.T().Fatal("stopCh should be closed")
	}
}

func (s *CDPSuite) TestStopScreencastNotScreencasting() {
	s.client.screencasting = false
	s.client.StopScreencast()
	require.False(s.T(), s.client.screencasting)
}

// screencastingNow reads the flag under the client's own lock, so the test does
// not race the goroutine that clears it. It takes the client rather than
// reaching for s.client: require.Never returns when its timer fires without
// waiting for the condition goroutine it spawned on the last tick, so that
// goroutine can still be running once the next test's SetupTest is assigning
// s.client — a race on the suite field that no lock on the client covers.
func screencastingNow(c *CDPClient) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.screencasting
}

// A failed start leaves Chrome not streaming. If the flag stayed set, every
// later StartScreencast would take the "already screencasting" shortcut and
// hand back a channel nothing ever writes to.
func (s *CDPSuite) TestStartScreencastClearsFlagWhenCommandFails() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		return errors.New("target closed")
	})

	client := s.client
	client.StartScreencast(60, 1920, 1080)

	require.Eventually(s.T(), func() bool { return !screencastingNow(client) },
		time.Second, 5*time.Millisecond,
		"a failed start must leave the client free to try again")
}

// A backgrounded or wedged renderer accepts Page.startScreencast and never
// answers. Without a deadline the goroutine parks forever and the pane sits on
// its last frame with nothing logged.
func (s *CDPSuite) TestStartScreencastClearsFlagWhenCommandHangs() {
	release := make(chan struct{})
	defer close(release)

	s.client.screencastTimeout = 20 * time.Millisecond
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		<-release
		return nil
	})

	client := s.client
	client.StartScreencast(60, 1920, 1080)

	require.Eventually(s.T(), func() bool { return !screencastingNow(client) },
		time.Second, 5*time.Millisecond,
		"a hung start must time out rather than block silently")
}

// A start that succeeds must leave the flag set, or the next call would issue a
// duplicate command.
func (s *CDPSuite) TestStartScreencastKeepsFlagWhenCommandSucceeds() {
	client := s.client
	client.StartScreencast(60, 1920, 1080)

	require.Never(s.T(), func() bool { return !screencastingNow(client) },
		100*time.Millisecond, 10*time.Millisecond)
}

func (s *CDPSuite) TestScreencastDeadline() {
	require.Equal(s.T(), defaultScreencastTimeout, s.client.screencastDeadline())

	s.client.screencastTimeout = 3 * time.Second
	require.Equal(s.T(), 3*time.Second, s.client.screencastDeadline())
}

// --- crashed target recovery ---

// screencastCalls records which CDP commands the screencast path issued, so a
// test can tell a reload apart from a start without reaching into Chrome.
func (s *CDPSuite) screencastCalls(fail func(call string, n int) error) func() []string {
	var mu sync.Mutex
	var calls []string

	s.setRunFn(func(_ context.Context, actions ...chromedp.Action) error {
		call := "other"
		switch actions[0].(type) {
		case *cdppage.StartScreencastParams:
			call = "start"
		case *cdppage.ReloadParams:
			call = "reload"
		}

		mu.Lock()
		calls = append(calls, call)
		n := 0
		for _, c := range calls {
			if c == call {
				n++
			}
		}
		mu.Unlock()

		return fail(call, n)
	})

	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

// Chrome keeps a target whose renderer died and answers every command on it the
// same way, so waiting alone never brings the pane back: reconnect after
// reconnect logged "Target crashed" against a dead frame until the agent
// happened to navigate. Reloading is what the crashed tab's own button does.
func (s *CDPSuite) TestStartScreencastReloadsACrashedTarget() {
	calls := s.screencastCalls(func(call string, n int) error {
		if call == "start" && n == 1 {
			return errors.New("Target crashed (-32000)")
		}
		return nil
	})

	client := s.client
	client.StartScreencast(60, 1920, 1080)

	require.Eventually(s.T(), func() bool {
		return slices.Equal(calls(), []string{"start", "reload", "start"})
	}, time.Second, 5*time.Millisecond,
		"a crashed target must be reloaded and the screencast asked for again")
	require.True(s.T(), screencastingNow(client))
}

// A reload that fails, and a reloaded tab that crashes again, both leave Chrome
// not streaming — so the flag has to come off, or every later StartScreencast
// takes the "already screencasting" shortcut and hands back a dead channel.
func (s *CDPSuite) TestStartScreencastClearsFlagWhenRecoveryFails() {
	cases := []struct {
		name string
		fail func(call string, n int) error
	}{
		{"reload refused", func(call string, _ int) error {
			if call == "reload" {
				return errors.New("reload failed")
			}
			return errors.New("Target crashed (-32000)")
		}},
		{"reloaded tab crashes again", func(call string, _ int) error {
			if call == "reload" {
				return nil
			}
			return errors.New("Target crashed (-32000)")
		}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.screencastCalls(tc.fail)

			client := s.client
			client.StartScreencast(60, 1920, 1080)

			require.Eventually(s.T(), func() bool { return !screencastingNow(client) },
				time.Second, 5*time.Millisecond,
				"a start that could not be recovered must leave the client free to try again")
		})
	}
}

func (s *CDPSuite) TestTargetCrashed() {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"crashed target", errors.New("Target crashed (-32000)"), true},
		{"crashed target, wrapped", fmt.Errorf("starting screencast: %w", errors.New("Target crashed (-32000)")), true},
		{"some other failure", errors.New("Target closed"), false},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, targetCrashed(tc.err))
		})
	}
}
