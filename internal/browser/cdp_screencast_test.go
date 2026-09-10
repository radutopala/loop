package browser

import (
	"context"
	"encoding/base64"
	"errors"
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
// not race the goroutine that clears it.
func (s *CDPSuite) screencastingNow() bool {
	s.client.mu.Lock()
	defer s.client.mu.Unlock()
	return s.client.screencasting
}

// A failed start leaves Chrome not streaming. If the flag stayed set, every
// later StartScreencast would take the "already screencasting" shortcut and
// hand back a channel nothing ever writes to.
func (s *CDPSuite) TestStartScreencastClearsFlagWhenCommandFails() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		return errors.New("target closed")
	})

	s.client.StartScreencast(60, 1920, 1080)

	require.Eventually(s.T(), func() bool { return !s.screencastingNow() },
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

	s.client.StartScreencast(60, 1920, 1080)

	require.Eventually(s.T(), func() bool { return !s.screencastingNow() },
		time.Second, 5*time.Millisecond,
		"a hung start must time out rather than block silently")
}

// A start that succeeds must leave the flag set, or the next call would issue a
// duplicate command.
func (s *CDPSuite) TestStartScreencastKeepsFlagWhenCommandSucceeds() {
	s.client.StartScreencast(60, 1920, 1080)

	require.Never(s.T(), func() bool { return !s.screencastingNow() },
		100*time.Millisecond, 10*time.Millisecond)
}

func (s *CDPSuite) TestScreencastDeadline() {
	require.Equal(s.T(), defaultScreencastTimeout, s.client.screencastDeadline())

	s.client.screencastTimeout = 3 * time.Second
	require.Equal(s.T(), 3*time.Second, s.client.screencastDeadline())
}
