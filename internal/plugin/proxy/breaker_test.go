package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/plugin/runtime"
)

type BreakerSuite struct {
	suite.Suite
	now time.Time
	bs  *breakers
	ep  runtime.Endpoint
}

func TestBreakerSuite(t *testing.T) {
	suite.Run(t, new(BreakerSuite))
}

func (s *BreakerSuite) SetupTest() {
	s.now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	s.bs = newBreakers(func() time.Time { return s.now })
	s.ep = runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "sec"}
}

var errRefused = errors.New("refused")

// fail records n calls to ep that weren't answered.
func (s *BreakerSuite) fail(ep runtime.Endpoint, n int) {
	for range n {
		require.NoError(s.T(), s.bs.allow(ep))
		s.bs.done(ep, context.Background(), errRefused)
	}
}

func (s *BreakerSuite) TestOpensAfterFailuresInARow() {
	s.fail(s.ep, breakerFailures-1)
	require.NoError(s.T(), s.bs.allow(s.ep))
	s.bs.done(s.ep, context.Background(), errRefused)

	err := s.bs.allow(s.ep)
	require.ErrorIs(s.T(), err, ErrUnavailable)
	require.EqualError(s.T(), err, "the plugin isn't answering: 3 calls in a row failed, trying it again in 30s")
	s.now = s.now.Add(breakerCooldown - time.Second)
	require.ErrorContains(s.T(), s.bs.allow(s.ep), "trying it again in 1s")
}

func (s *BreakerSuite) TestAnswerResets() {
	s.fail(s.ep, breakerFailures-1)
	require.NoError(s.T(), s.bs.allow(s.ep))
	s.bs.done(s.ep, context.Background(), nil)
	require.Empty(s.T(), s.bs.byKey)
	// The count starts over.
	s.fail(s.ep, breakerFailures-1)
	require.NoError(s.T(), s.bs.allow(s.ep))
}

func (s *BreakerSuite) TestProbeAfterCooldown() {
	tests := []struct {
		name     string
		probeErr error
		wantOpen bool
	}{
		{name: "answered closes it", probeErr: nil},
		{name: "unanswered opens it again", probeErr: errRefused, wantOpen: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.fail(s.ep, breakerFailures)
			s.now = s.now.Add(breakerCooldown)

			require.NoError(s.T(), s.bs.allow(s.ep))
			// Only the one call tries the container.
			require.EqualError(s.T(), s.bs.allow(s.ep), "the plugin isn't answering: 3 calls in a row failed, trying it again now")
			s.bs.done(s.ep, context.Background(), tt.probeErr)

			err := s.bs.allow(s.ep)
			if !tt.wantOpen {
				require.NoError(s.T(), err)
				return
			}
			require.EqualError(s.T(), err, "the plugin isn't answering: 4 calls in a row failed, trying it again in 30s")
		})
	}
}

func (s *BreakerSuite) TestCallerGaveUp() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A call the caller cancelled doesn't count against the container.
	require.NoError(s.T(), s.bs.allow(s.ep))
	s.bs.done(s.ep, ctx, context.Canceled)
	require.Empty(s.T(), s.bs.byKey)

	// Nor does it hold up the next try once open.
	s.fail(s.ep, breakerFailures)
	s.now = s.now.Add(breakerCooldown)
	require.NoError(s.T(), s.bs.allow(s.ep))
	s.bs.done(s.ep, ctx, context.Canceled)
	require.NoError(s.T(), s.bs.allow(s.ep))
}

func (s *BreakerSuite) TestPerContainer() {
	s.fail(s.ep, breakerFailures)
	// The same container restarted has a new secret.
	require.NoError(s.T(), s.bs.allow(runtime.Endpoint{URL: s.ep.URL, Secret: "new"}))
	require.NoError(s.T(), s.bs.allow(runtime.Endpoint{URL: "http://127.0.0.1:5678", Secret: "sec"}))
	require.ErrorIs(s.T(), s.bs.allow(s.ep), ErrUnavailable)
}

func (s *BreakerSuite) TestForgetsOldFailures() {
	old := runtime.Endpoint{URL: s.ep.URL, Secret: "old"}
	s.fail(old, breakerFailures)
	s.now = s.now.Add(breakerForget + time.Second)
	s.fail(s.ep, 1)
	require.Len(s.T(), s.bs.byKey, 1)
	require.Contains(s.T(), s.bs.byKey, breakerKey(s.ep))
}

// countingDoer counts its calls and answers each with answer.
type countingDoer struct {
	calls  int
	answer func() (*http.Response, error)
}

func (d *countingDoer) Do(*http.Request) (*http.Response, error) {
	d.calls++
	return d.answer()
}

func (s *BreakerSuite) TestClientFailsFastOnceOpen() {
	d := &countingDoer{answer: func() (*http.Response, error) { return nil, errRefused }}
	c := NewClient(d)
	c.breakers.now = func() time.Time { return s.now }
	ctx := context.Background()

	for range breakerFailures {
		require.EqualError(s.T(), c.Action(ctx, s.ep, "ping", ActionRequest{}), "calling the plugin: refused")
	}
	calls := []func() error{
		func() error { return c.Action(ctx, s.ep, "ping", ActionRequest{}) },
		func() error { _, err := c.Tool(ctx, s.ep, "t", ToolRequest{}); return err },
		func() error { return c.RunDone(ctx, s.ep, "r1", RunDoneRequest{}) },
		func() error { _, err := c.X(ctx, s.ep, XRequest{Method: http.MethodGet}); return err },
	}
	for _, call := range calls {
		require.ErrorIs(s.T(), call(), ErrUnavailable)
	}
	require.Equal(s.T(), breakerFailures, d.calls)

	// After the cooldown a call tries the container, which answers now.
	s.now = s.now.Add(breakerCooldown)
	d.answer = func() (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Body: http.NoBody}, nil
	}
	require.ErrorContains(s.T(), c.Action(ctx, s.ep, "ping", ActionRequest{}), "the plugin answered 500")
	require.Empty(s.T(), c.breakers.byKey)
}

func (s *BreakerSuite) TestClientCountsUnreadableAnswerAsAnswered() {
	// A body that breaks while being read still came from the container,
	// unless the call ran out of time.
	d := &countingDoer{answer: func() (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: errBody{}}, nil
	}}
	c := NewClient(d)
	for range breakerFailures + 1 {
		_, err := c.X(context.Background(), s.ep, XRequest{Method: http.MethodGet})
		require.True(s.T(), strings.HasPrefix(err.Error(), "reading the plugin's answer"))
	}
	require.Equal(s.T(), breakerFailures+1, d.calls)
}
