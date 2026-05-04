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

func (s *CDPSuite) TestStartScreencastFrameDropped() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	_ = s.client.StartScreencast(60, 1920, 1080)

	// Fill the channel.
	encoded := base64.StdEncoding.EncodeToString([]byte("frame1"))
	listenerFn(&cdppage.EventScreencastFrame{Data: encoded, SessionID: 1})
	listenerFn(&cdppage.EventScreencastFrame{Data: encoded, SessionID: 2})

	// Third should be dropped (channel buffer = 2).
	listenerFn(&cdppage.EventScreencastFrame{Data: encoded, SessionID: 3})

	require.Len(s.T(), s.client.frameCh, 2)
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
