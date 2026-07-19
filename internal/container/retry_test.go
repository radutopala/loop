package container

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsAPILimitError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"weekly limit", errors.New("You've hit your weekly limit · resets 8am (Europe/Bucharest)"), true},
		{"session limit", errors.New("You've hit your session limit · resets 11:30pm (Europe/Bucharest)"), true},
		{"plain hit your limit", errors.New("You've hit your limit · resets 8am (UTC)"), true},
		{"usage limit reached", errors.New("Your usage limit reached"), true},
		{"rate limited", errors.New("Server is temporarily limiting requests (not your usage limit) · Rate limited"), true},
		{"overloaded", errors.New("claude returned error: overloaded_error"), true},
		{"quota", errors.New("quota exceeded"), true},
		{"credit balance", errors.New("your credit balance is too low"), true},
		// Non-limit errors: the blind resume-retry should still apply to these.
		{"generic", errors.New("claude returned error: something went wrong"), false},
		{"prompt too long", errors.New("Prompt is too long"), false},
		{"network", errors.New("connection reset by peer"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isAPILimitError(tc.err))
		})
	}
}

func TestIsRetryableAgentError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			name: "temporarily limiting requests (the user's exact case)",
			err:  errors.New("API Error: Server is temporarily limiting requests (not your usage limit) · Rate limited"),
			want: true,
		},
		{"overloaded_error", errors.New("claude returned error: overloaded_error"), true},
		{"http 529", errors.New("request failed with status 529"), true},
		{"http 503", errors.New("upstream returned 503 service unavailable"), true},
		{"bad gateway", errors.New("502 bad gateway"), true},
		{"rate_limit_error", errors.New("rate_limit_error: too many requests"), true},
		// Terminal — must NOT retry even though some share keywords.
		{"usage limit reached", errors.New("Your usage limit reached. Limit resets at 5pm."), false},
		{"reached your usage limit", errors.New("you have reached your usage limit"), false},
		{"session limit (real Anthropic string)", errors.New("You've hit your session limit · resets 8:30pm (UTC)"), false},
		{"credit balance", errors.New("your credit balance is too low"), false},
		{"auth", errors.New("authentication_error: invalid x-api-key"), false},
		{"invalid request", errors.New("invalid_request_error: messages required"), false},
		{"plain failure", errors.New("something went wrong"), false},
		{"prompt too long is not retryable here", errors.New("Prompt is too long"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isRetryableAgentError(tc.err))
		})
	}
}

func TestBackoffDelay(t *testing.T) {
	base := 5 * time.Second
	maxDelay := 120 * time.Second
	require.Equal(t, 5*time.Second, backoffDelay(0, base, maxDelay))
	require.Equal(t, 10*time.Second, backoffDelay(1, base, maxDelay))
	require.Equal(t, 20*time.Second, backoffDelay(2, base, maxDelay))
	require.Equal(t, 40*time.Second, backoffDelay(3, base, maxDelay))
	require.Equal(t, 80*time.Second, backoffDelay(4, base, maxDelay))
	// Capped at max.
	require.Equal(t, maxDelay, backoffDelay(5, base, maxDelay))
	require.Equal(t, maxDelay, backoffDelay(50, base, maxDelay))
	// Zero base disables.
	require.Equal(t, time.Duration(0), backoffDelay(3, 0, maxDelay))
}

func TestSleepCtx(t *testing.T) {
	// Non-positive duration returns immediately.
	require.NoError(t, sleepCtx(context.Background(), 0))

	// Already-cancelled context returns its error even for d<=0.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleepCtx(cancelled, 0), context.Canceled)
	require.ErrorIs(t, sleepCtx(cancelled, time.Hour), context.Canceled)

	// Cancellation mid-wait returns promptly with the ctx error.
	ctx, cancel2 := context.WithCancel(context.Background())
	go cancel2()
	require.ErrorIs(t, sleepCtx(ctx, time.Hour), context.Canceled)

	// Normal completion.
	require.NoError(t, sleepCtx(context.Background(), time.Millisecond))
}

// TestBackoffDelayOverflowReturnsMax pins the overflow guard: when the
// doubling wraps negative, the delay clamps to maxDelay instead of zero.
func TestBackoffDelayOverflowReturnsMax(t *testing.T) {
	got := backoffDelay(1, time.Duration(math.MaxInt64), time.Second)
	require.Equal(t, time.Second, got)
}
