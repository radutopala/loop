// retry.go holds the transient-error classifier and backoff helper used by
// DockerRunner.Run to retry agent runs that fail with a retryable API error
// (rate limiting, overload, transient 5xx) rather than a terminal one
// (usage/quota exhaustion, auth, billing, malformed request).
package container

import (
	"context"
	"strings"
	"time"
)

// nonRetryableMarkers are substrings that, when present, mean a retry will not
// help — quota/usage exhaustion, billing, auth, or a malformed request. These
// are checked FIRST so a transient error that merely mentions "usage limit" in
// passing (e.g. "temporarily limiting requests (not your usage limit)") is not
// misclassified: the genuine quota error says "usage limit reached" / "limit
// will reset", which none of the transient strings contain.
var nonRetryableMarkers = []string{
	"usage limit reached",
	"usage limit exceeded",
	"reached your usage limit",
	"session limit",
	"limit will reset",
	"limit resets",
	"credit balance",
	"insufficient",
	"quota exceeded",
	"quota has been",
	"billing",
	"authentication_error",
	"invalid x-api-key",
	"invalid api key",
	"unauthorized",
	"permission_error",
	"permission denied",
	"invalid_request_error",
}

// retryableMarkers are substrings that indicate a transient, retryable
// condition. Anthropic surfaces rate limiting as "Server is temporarily
// limiting requests (not your usage limit)" and overload as overloaded_error /
// HTTP 529; transient gateway failures show up as 5xx.
var retryableMarkers = []string{
	"temporarily limiting requests",
	"overloaded",
	"rate limit",
	"rate_limit",
	"rate-limited",
	"rate limited",
	" 429",
	" 529",
	" 503",
	" 502",
	" 500",
	"service unavailable",
	"bad gateway",
	"internal server error",
	"service overloaded",
}

// isRetryableAgentError reports whether an agent run error is a transient API
// condition worth retrying with backoff. Terminal errors (quota, auth, billing,
// malformed request) return false even if they share keywords with retryable
// ones, because the non-retryable markers are matched first.
func isRetryableAgentError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range nonRetryableMarkers {
		if strings.Contains(msg, m) {
			return false
		}
	}
	for _, m := range retryableMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// apiLimitMarkers indicate any API limit or overload — transient (rate limit,
// overloaded) OR terminal (weekly/session/usage limit, quota, billing). An
// immediate resume-retry of any of these just hits the same wall, so the blind
// resume-retry in runWithRecovery is skipped for all of them: transient ones go
// to the backoff loop, terminal ones are surfaced to the orchestrator (which
// may schedule a session-limit auto-continue).
var apiLimitMarkers = []string{
	"usage limit",
	"rate limit",
	"rate_limit",
	"rate-limited",
	"rate limited",
	"overloaded",
	"temporarily limiting requests",
	"quota",
	"credit balance",
	"insufficient",
	"billing",
}

// isAPILimitError reports whether the error is any API limit/overload condition
// — including the "You've hit your <session|weekly|daily|…> limit" family — for
// which retrying immediately is futile.
func isAPILimitError(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	// "You've hit your <session|weekly|daily|5-hour|…> limit · resets …"
	if strings.Contains(m, "hit your") && strings.Contains(m, "limit") {
		return true
	}
	for _, x := range apiLimitMarkers {
		if strings.Contains(m, x) {
			return true
		}
	}
	return false
}

// backoffDelay returns the wait before the given zero-based retry attempt:
// min(base * 2^attempt, max). attempt 0 is the first retry (after the initial
// failure).
func backoffDelay(attempt int, base, maxDelay time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	shift := attempt
	if shift > 30 {
		shift = 30 // guard against overflow on absurd attempt counts
	}
	delay := base * time.Duration(uint64(1)<<uint(shift))
	if delay <= 0 {
		return maxDelay
	}
	return min(delay, maxDelay)
}

// sleepCtx waits for d or until ctx is cancelled. It returns ctx.Err() if the
// context is cancelled first, nil otherwise. A non-positive d returns nil
// immediately (still honoring an already-cancelled context).
func sleepCtx(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
