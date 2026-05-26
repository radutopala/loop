package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stretchr/testify/require"
)

// --- resolveReviewAPIURL ---

func (s *MainSuite) TestResolveReviewAPIURLFlagWins() {
	require.Equal(s.T(), "http://flag", resolveReviewAPIURL("http://flag", "http://env"))
}

func (s *MainSuite) TestResolveReviewAPIURLEnvFallback() {
	require.Equal(s.T(), "http://env", resolveReviewAPIURL("", "http://env"))
}

func (s *MainSuite) TestResolveReviewAPIURLDefault() {
	require.Equal(s.T(), "http://localhost:8222", resolveReviewAPIURL("", ""))
}

// --- reviewHTTPClient ---

func (s *MainSuite) TestReviewHTTPClientUsesInjected() {
	stub := &stubHTTPClient{}
	s.app.reviewClient = stub
	require.Same(s.T(), stub, s.app.reviewHTTPClient())
}

func (s *MainSuite) TestReviewHTTPClientFallsBackToDefault() {
	s.app.reviewClient = nil
	require.Same(s.T(), http.DefaultClient, s.app.reviewHTTPClient())
}

// --- runReview (no --wait) ---

func (s *MainSuite) TestRunReviewWithoutWaitPostsAndExits() {
	var posted atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), http.MethodPost, r.Method)
		require.Equal(s.T(), "/api/channels/ch1/review/run", r.URL.Path)
		require.Equal(s.T(), "application/json", r.Header.Get("Content-Type"))
		posted.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	var buf bytes.Buffer
	err := s.app.runReview(context.Background(), &buf, ts.URL, "ch1", false, time.Second)
	require.NoError(s.T(), err)
	require.True(s.T(), posted.Load())
	require.Empty(s.T(), buf.String(), "no --wait means no stdout output")
}

func (s *MainSuite) TestRunReviewPostNon202IsError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	err := s.app.runReview(context.Background(), &bytes.Buffer{}, ts.URL, "ch1", false, time.Second)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unexpected status 500")
}

func (s *MainSuite) TestRunReviewPostTransportError() {
	s.app.reviewClient = &stubHTTPClient{err: errors.New("network down")}
	err := s.app.runReview(context.Background(), &bytes.Buffer{}, "http://example.invalid", "ch1", false, time.Second)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "network down")
}

// --- runReview (--wait) ---

func (s *MainSuite) TestRunReviewWaitsForReady() {
	var pollCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/review/run"):
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/review"):
			c := pollCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if c < 2 {
				// First poll: still reviewing.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"present": true,
					"session": map[string]any{"status": "reviewing"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"present": true,
				"session": map[string]any{
					"status": "ready",
					"comments": []map[string]any{
						{"id": "c1", "path": "f.go", "line": 1, "body": "fix"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	var buf bytes.Buffer
	err := s.app.runReview(context.Background(), &buf, ts.URL, "ch1", true, 10*time.Second)
	require.NoError(s.T(), err)

	var out reviewCLIOutput
	require.NoError(s.T(), json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out))
	require.Equal(s.T(), "ready", out.Status)
	require.False(s.T(), out.NoComments)
	require.Len(s.T(), out.Comments, 1)
	require.Equal(s.T(), "c1", out.Comments[0].ID)
}

func (s *MainSuite) TestRunReviewWaitReadyNoComments() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"present": true,
			"session": map[string]any{"status": "ready", "comments": []map[string]any{}},
		})
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	var buf bytes.Buffer
	err := s.app.runReview(context.Background(), &buf, ts.URL, "ch1", true, 5*time.Second)
	require.NoError(s.T(), err)

	var out reviewCLIOutput
	require.NoError(s.T(), json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out))
	require.True(s.T(), out.NoComments)
}

func (s *MainSuite) TestRunReviewWaitErrorStatusReturnsError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"present": true,
			"session": map[string]any{"status": "error", "error": "engine boom"},
		})
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	var buf bytes.Buffer
	err := s.app.runReview(context.Background(), &buf, ts.URL, "ch1", true, 5*time.Second)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "engine boom")
	// Still printed the error JSON to stdout.
	require.Contains(s.T(), buf.String(), `"status":"error"`)
}

func (s *MainSuite) TestRunReviewWaitTimeout() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"present": true,
			"session": map[string]any{"status": "reviewing"},
		})
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	err := s.app.runReview(context.Background(), &bytes.Buffer{}, ts.URL, "ch1", true, 50*time.Millisecond)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "timed out")
}

// TestRunReviewWaitTimeoutDuringHungResponse verifies the fix for the
// flag-not-bounding-Do bug: when the daemon accepts the GET poll but stalls
// indefinitely on the response, the --timeout must still trip via ctx
// cancellation inside client.Do (not just between polls). Without
// context.WithTimeout on the run ctx, http.DefaultClient has no Timeout and
// the Do call would hang past --timeout.
func (s *MainSuite) TestRunReviewWaitTimeoutDuringHungResponse() {
	hang := make(chan struct{})
	defer close(hang)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		// Block until the request ctx is cancelled (the daemon never
		// flushes a response body). The fix's context.WithTimeout on the
		// run ctx is what cancels the in-flight request.
		select {
		case <-r.Context().Done():
		case <-hang:
		}
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	start := time.Now()
	err := s.app.runReview(context.Background(), &bytes.Buffer{}, ts.URL, "ch1", true, 50*time.Millisecond)
	elapsed := time.Since(start)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "timed out")
	require.Less(s.T(), elapsed, 5*time.Second, "timeout must bound the in-flight client.Do call, not block on it")
}

func (s *MainSuite) TestRunReviewWaitContextCancel() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"present": true,
			"session": map[string]any{"status": "reviewing"},
		})
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel almost immediately so the poll loop's select on ctx.Done fires.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	err := s.app.runReview(ctx, &bytes.Buffer{}, ts.URL, "ch1", true, 5*time.Second)
	require.Error(s.T(), err)
	require.ErrorIs(s.T(), err, context.Canceled)
}

func (s *MainSuite) TestRunReviewWaitPollTransientNotPresent() {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		c := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if c < 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{"present": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"present": true,
			"session": map[string]any{"status": "ready", "comments": []map[string]any{}},
		})
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	err := s.app.runReview(context.Background(), &bytes.Buffer{}, ts.URL, "ch1", true, 5*time.Second)
	require.NoError(s.T(), err)
}

// TestRunReviewPollTransientErrorRetries verifies that a transient
// transport error on the GET poll does not kill the run — the loop backs
// off and keeps polling until the daemon recovers. This is the fix for the
// "one packet drop kills a 30-minute review-fix-loop iteration" bug.
func (s *MainSuite) TestRunReviewPollTransientErrorRetries() {
	var getCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		n := getCalls.Add(1)
		if n <= 2 {
			// First two polls: simulate transport failure by hijacking
			// and dropping the connection. pollReviewOnce returns a
			// transport error; runReview must retry rather than bail.
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				return
			}
			_ = conn.Close()
			return
		}
		// Third poll: daemon recovered, return a terminal status.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"present": true,
			"session": map[string]any{
				"status":   "ready",
				"comments": []map[string]any{},
			},
		})
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	var buf bytes.Buffer
	err := s.app.runReview(context.Background(), &buf, ts.URL, "ch1", true, 5*time.Second)
	require.NoError(s.T(), err, "transient transport errors should be retried, not fatal")
	require.GreaterOrEqual(s.T(), getCalls.Load(), int32(3), "loop should retry past the broken polls and succeed on recovery")

	var out reviewCLIOutput
	require.NoError(s.T(), json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out))
	require.Equal(s.T(), "ready", out.Status)
	require.True(s.T(), out.NoComments)
}

// TestRunReviewPollSustainedTransportErrorTimesOut verifies that a sustained
// transport failure (daemon never recovers) is eventually bounded by the
// --timeout deadline, surfacing as the documented "timed out" error rather
// than spinning indefinitely.
func (s *MainSuite) TestRunReviewPollSustainedTransportErrorTimesOut() {
	var postDone atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postDone.Store(true)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	err := s.app.runReview(context.Background(), &bytes.Buffer{}, ts.URL, "ch1", true, 50*time.Millisecond)
	require.Error(s.T(), err)
	require.True(s.T(), postDone.Load(), "POST should have succeeded before the GETs broke")
	require.Contains(s.T(), err.Error(), "timed out", "sustained transport failure must surface as the documented timeout")
}

// TestRunReviewCtxAlreadyCancelledBeforePoll verifies the ctx.Err() guard at
// the top of the polling for-loop. A custom HTTP client returns a successful
// POST response synchronously and cancels the run context after the POST so
// the for-loop's first iteration observes the cancellation before issuing a
// GET.
func (s *MainSuite) TestRunReviewCtxAlreadyCancelledBeforePoll() {
	ctx, cancel := context.WithCancel(context.Background())

	s.app.reviewClient = &cancelAfterPostClient{cancel: cancel}
	err := s.app.runReview(ctx, &bytes.Buffer{}, "http://stub", "ch1", true, 5*time.Second)
	require.Error(s.T(), err)
	require.ErrorIs(s.T(), err, context.Canceled)
}

// TestRunReviewCancelDuringPollInterval covers the rare case where ctx is
// cancelled between a non-terminal poll returning and the next iteration's
// top-of-loop ctx.Err() guard firing — the poll-interval timer's select picks
// up ctx.Done first, returning the wrapped cancellation error. We stretch the
// poll interval to one minute so the timer can't win the select race.
func (s *MainSuite) TestRunReviewCancelDuringPollInterval() {
	ctx, cancel := context.WithCancel(context.Background())
	s.app.reviewClient = &cancelAfterReviewingGetClient{cancel: cancel}
	s.app.reviewPollInterval = time.Minute

	err := s.app.runReview(ctx, &bytes.Buffer{}, "http://stub", "ch1", true, 5*time.Second)
	require.Error(s.T(), err)
	require.ErrorIs(s.T(), err, context.Canceled)
}

// TestRunReviewFprintlnError verifies the rare write-error branch by using
// a stdout writer that returns an error on Write. The ready-status path
// reaches the Fprintln call and surfaces the wrapped error.
func (s *MainSuite) TestRunReviewFprintlnError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"present": true,
			"session": map[string]any{"status": "ready", "comments": []map[string]any{}},
		})
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	bw := &brokenWriter{}
	err := s.app.runReview(context.Background(), bw, ts.URL, "ch1", true, 5*time.Second)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing output")
}

// --- pollReviewOnce direct branches ---

func (s *MainSuite) TestPollReviewOnceBadStatus() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer ts.Close()

	_, terminal, err := pollReviewOnce(context.Background(), http.DefaultClient, ts.URL)
	require.Error(s.T(), err)
	require.False(s.T(), terminal)
	require.Contains(s.T(), err.Error(), "status 502")
}

func (s *MainSuite) TestPollReviewOnceInvalidJSON() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	_, _, err := pollReviewOnce(context.Background(), http.DefaultClient, ts.URL)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding")
}

func (s *MainSuite) TestPollReviewOnceTransportError() {
	_, _, err := pollReviewOnce(context.Background(), &stubHTTPClient{err: errors.New("conn refused")}, "http://x")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "conn refused")
}

func (s *MainSuite) TestPollReviewOnceBuildRequestError() {
	// An invalid URL with control bytes fails http.NewRequestWithContext.
	_, _, err := pollReviewOnce(context.Background(), http.DefaultClient, "http://\x7f")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "building GET request")
}

func (s *MainSuite) TestRunReviewBuildPostRequestError() {
	s.app.reviewClient = http.DefaultClient
	err := s.app.runReview(context.Background(), &bytes.Buffer{}, "http://\x7f", "ch1", false, time.Second)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "building POST request")
}

// --- newReviewRunCmd flag parsing ---

func (s *MainSuite) TestNewReviewRunCmdInvalidTimeout() {
	cmd := s.app.newReviewCmd()
	cmd.SetArgs([]string{"run", "--channel-id", "ch1", "--timeout", "not-a-duration"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid --timeout")
}

func (s *MainSuite) TestNewReviewRunCmdRequiresChannelID() {
	cmd := s.app.newReviewCmd()
	cmd.SetArgs([]string{"run"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "channel-id")
}

func (s *MainSuite) TestNewReviewRunCmdHappyPathNoWait() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	s.app.reviewClient = http.DefaultClient
	cmd := s.app.newReviewCmd()
	cmd.SetArgs([]string{"run", "--channel-id", "ch1", "--api-url", ts.URL})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	require.NoError(s.T(), cmd.Execute())
}

// --- stubHTTPClient ---

type stubHTTPClient struct {
	resp *http.Response
	err  error
}

// brokenWriter satisfies io.Writer but always errors, exercising rare write-
// error branches in code that emits to stdout.
type brokenWriter struct{}

func (b *brokenWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("broken pipe")
}

// cancelAfterPostClient returns a 202 response on the first call (POST) and
// cancels its embedded context.CancelFunc afterwards. Subsequent calls return
// a generic error — they should never be reached because the ctx.Err() guard
// at the top of runReview's polling loop fires first.
type cancelAfterPostClient struct {
	cancel context.CancelFunc
	calls  atomic.Int32
}

func (c *cancelAfterPostClient) Do(_ *http.Request) (*http.Response, error) {
	n := c.calls.Add(1)
	if n == 1 {
		resp := &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       http.NoBody,
		}
		c.cancel()
		return resp, nil
	}
	return nil, errors.New("unexpected second call")
}

// cancelAfterReviewingGetClient returns 202 on POST, then "reviewing" status
// on the first GET while cancelling its embedded ctx mid-call. The point is to
// force runReview into the poll-interval timer select with ctx already done,
// covering the `case <-ctx.Done()` branch that follows a non-terminal poll.
type cancelAfterReviewingGetClient struct {
	cancel context.CancelFunc
	calls  atomic.Int32
}

func (c *cancelAfterReviewingGetClient) Do(req *http.Request) (*http.Response, error) {
	n := c.calls.Add(1)
	if n == 1 {
		return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody}, nil
	}
	body, _ := json.Marshal(map[string]any{
		"present": true,
		"session": map[string]any{"status": "reviewing"},
	})
	c.cancel()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func (s *stubHTTPClient) Do(_ *http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

// --- compile-time sanity: stubHTTPClient satisfies the interface ---

var _ reviewHTTPClient = (*stubHTTPClient)(nil)
