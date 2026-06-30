package container

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

// setupMockAttempts wires N sequential non-streaming container runs, each
// returning the corresponding jsonOutput. Distinct container IDs per attempt
// keep the ContainerLogs/Wait expectations unambiguous. All attempts share the
// deterministic container name from osRandRead.
func (s *RunnerSuite) setupMockAttempts(ctx context.Context, jsonOutputs ...string) {
	s.client.On("NetworkEnsure", ctx, mock.Anything).Maybe().Return(nil)
	for i, out := range jsonOutputs {
		cid := fmt.Sprintf("container-attempt-%d", i)
		reader := bytes.NewReader([]byte(out))
		waitCh := make(chan WaitResponse, 1)
		waitCh <- WaitResponse{StatusCode: 0}
		errCh := make(chan error, 1)
		s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(*ContainerConfig) bool { return true }), testContainerName).Return(cid, nil).Once()
		s.client.On("ContainerLogs", ctx, cid).Return(reader, nil)
		s.client.On("ContainerStart", ctx, cid).Return(nil)
		s.client.On("ContainerWait", ctx, cid).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	}
}

func (s *RunnerSuite) TestRunRetriesTransientThenSucceeds() {
	ctx := context.Background()
	s.cfg.AgentRetry = config.AgentRetryConfig{MaxAttempts: 3, BackoffBase: time.Second, BackoffMax: time.Minute}

	var slept []time.Duration
	s.runner.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}

	var activities []string
	req := &agent.AgentRequest{
		SessionID: "sess-1",
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
		OnActivity: func(activity, detail string) {
			if activity == "rate_limited" {
				activities = append(activities, detail)
			}
		},
	}

	// Attempt 1 + 2: transient rate-limit error; attempt 3: success.
	rl := `{"type":"result","result":"Server is temporarily limiting requests (not your usage limit)","session_id":"sess-1","is_error":true}`
	ok := `{"type":"result","result":"Done!","session_id":"sess-1","is_error":false}`
	s.setupMockAttempts(ctx, rl, rl, ok)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Done!", resp.Response)
	// Two backoffs before the third attempt: 1s then 2s.
	require.Equal(s.T(), []time.Duration{time.Second, 2 * time.Second}, slept)
	require.Len(s.T(), activities, 2)
	require.Contains(s.T(), activities[0], "retrying in")
	require.Contains(s.T(), activities[0], "(1/3)")
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunRetriesExhausted() {
	ctx := context.Background()
	s.cfg.AgentRetry = config.AgentRetryConfig{MaxAttempts: 2, BackoffBase: time.Second, BackoffMax: time.Minute}
	s.runner.sleep = func(context.Context, time.Duration) error { return nil }

	req := &agent.AgentRequest{
		SessionID: "sess-1",
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	}

	rl := `{"type":"result","result":"overloaded_error","session_id":"sess-1","is_error":true}`
	// Initial + 2 retries = 3 attempts, all failing.
	s.setupMockAttempts(ctx, rl, rl, rl)

	resp, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "overloaded")
	// The last response is surfaced (carries the session for the caller).
	require.NotNil(s.T(), resp)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunDoesNotRetryTerminalError() {
	ctx := context.Background()
	s.cfg.AgentRetry = config.AgentRetryConfig{MaxAttempts: 5, BackoffBase: time.Second, BackoffMax: time.Minute}
	var sleeps int
	s.runner.sleep = func(context.Context, time.Duration) error { sleeps++; return nil }

	req := &agent.AgentRequest{
		SessionID: "sess-1",
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	}

	// A quota/limit error must neither enter the backoff loop NOR trigger the
	// legacy blind resume-retry — it runs exactly once. Wiring a single attempt
	// (and asserting expectations) proves no second container is spawned.
	quota := `{"type":"result","result":"Your usage limit reached. Limit resets at midnight.","session_id":"sess-1","is_error":true}`
	s.setupMockAttempts(ctx, quota)

	_, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Equal(s.T(), 0, sleeps, "terminal error must not sleep/retry with backoff")
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunDoesNotBlindRetryWeeklyLimit() {
	ctx := context.Background()
	// No backoff configured — isolate the blind-retry behavior.
	s.cfg.AgentRetry = config.AgentRetryConfig{}
	req := &agent.AgentRequest{
		SessionID: "sess-1",
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	}
	// "You've hit your weekly limit · resets 8am" must run exactly once — the
	// blind resume-retry previously fired here, showing the error twice.
	weekly := `{"type":"result","result":"You've hit your weekly limit · resets 8am (Europe/Bucharest)","session_id":"sess-1","is_error":true}`
	s.setupMockAttempts(ctx, weekly)

	_, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "weekly limit")
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunRetryCancelledDuringBackoff() {
	ctx := context.Background()
	s.cfg.AgentRetry = config.AgentRetryConfig{MaxAttempts: 5, BackoffBase: time.Second, BackoffMax: time.Minute}

	// Simulate the user hitting Stop during the backoff sleep.
	var sleeps atomic.Int32
	s.runner.sleep = func(context.Context, time.Duration) error {
		sleeps.Add(1)
		return context.Canceled
	}

	req := &agent.AgentRequest{
		SessionID: "sess-1",
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	}

	rl := `{"type":"result","result":"rate_limit_error","session_id":"sess-1","is_error":true}`
	// Only the initial attempt runs; the backoff is cancelled before attempt 2.
	s.setupMockAttempts(ctx, rl)

	_, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Equal(s.T(), int32(1), sleeps.Load())
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunRetryDisabledByDefault() {
	ctx := context.Background()
	// SetupTest leaves AgentRetry zero-valued (MaxAttempts: 0).
	var sleeps int
	s.runner.sleep = func(context.Context, time.Duration) error { sleeps++; return nil }

	req := &agent.AgentRequest{
		SessionID: "sess-1",
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	}

	rl := `{"type":"result","result":"overloaded_error","session_id":"sess-1","is_error":true}`
	s.setupMockAttempts(ctx, rl)

	_, err := s.runner.Run(ctx, req)
	require.Error(s.T(), err)
	require.Equal(s.T(), 0, sleeps)
	s.client.AssertExpectations(s.T())
}
