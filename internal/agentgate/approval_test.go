package agentgate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

// --- Mocks ---

type fakeBot struct {
	mu         sync.Mutex
	sendErr    error
	sendCount  int
	removeCnt  int
	lastReqID  string
	lastTarget string
	onSend     func(req ApprovalRequest) // optional callback
}

func (f *fakeBot) SendApproval(_ context.Context, _ string, req ApprovalRequest) (string, error) {
	f.mu.Lock()
	f.sendCount++
	f.lastReqID = req.ID
	f.lastTarget = req.Target
	cb := f.onSend
	err := f.sendErr
	f.mu.Unlock()
	if cb != nil {
		cb(req)
	}
	if err != nil {
		return "", err
	}
	return "msg-" + req.ID, nil
}

func (f *fakeBot) RemoveApproval(_ context.Context, _, _ string) error {
	f.mu.Lock()
	f.removeCnt++
	f.mu.Unlock()
	return nil
}

type fakeRouter struct {
	bot Bot
}

func (r *fakeRouter) For(_ string) Bot { return r.bot }

// --- Suite ---

type ApprovalSuite struct {
	suite.Suite
}

func TestApprovalSuite(t *testing.T) {
	suite.Run(t, new(ApprovalSuite))
}

// fixedNow is the deterministic clock every test manager uses, so stamped
// deadlines (ExpiresAt = now + approvalTimeout) are assertable.
var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func (s *ApprovalSuite) newManager(bot Bot, limits types.RateLimits) *Manager {
	m := NewManager(&fakeRouter{bot: bot}, limits)
	var counter int64
	m.idGen = func() string { return fmt.Sprintf("r-%d", atomic.AddInt64(&counter, 1)) }
	m.now = func() time.Time { return fixedNow }
	return m
}

// request spawns Request in a goroutine and returns a channel for the outcome.
func (s *ApprovalSuite) request(m *Manager, req ApprovalRequest) <-chan Outcome {
	ch := make(chan Outcome, 1)
	go func() { ch <- m.Request(context.Background(), "chan1", req) }()
	return ch
}

// waitForPending blocks until Manager has exactly `n` pending requests,
// then returns the one registered reqID (when n==1) or "" (when n==0).
func (s *ApprovalSuite) waitForPending(m *Manager, n int) string {
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		size := len(m.pending)
		var id string
		for k := range m.pending {
			id = k
			break
		}
		m.mu.Unlock()
		if size == n {
			return id
		}
		time.Sleep(2 * time.Millisecond)
	}
	s.Failf("timeout waiting for pending", "expected %d pending requests", n)
	return ""
}

// --- Cache ---

func (s *ApprovalSuite) TestCacheHitSkipsPrompt() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})
	m.cache["k1"] = types.DecisionAllow

	var promptFired bool
	out := m.Request(context.Background(), "chan1", ApprovalRequest{
		CacheKey: "k1",
		OnPrompt: func() { promptFired = true },
	})
	require.Equal(s.T(), types.DecisionAllow, out.Decision)
	require.True(s.T(), out.FromCache)
	require.Equal(s.T(), "cache-hit", out.Reason)
	require.Equal(s.T(), 0, bot.sendCount)
	require.False(s.T(), promptFired, "OnPrompt must not fire when cache short-circuits")
}

func (s *ApprovalSuite) TestOnPromptFiresOnceOnRealPrompt() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	var promptCount int
	outCh := s.request(m, ApprovalRequest{
		Kind:     "exec",
		Target:   "x",
		OnPrompt: func() { promptCount++ },
	})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	<-outCh

	require.Equal(s.T(), 1, promptCount, "OnPrompt fires exactly once when the bot is dispatched")
}

func (s *ApprovalSuite) TestOnPromptSkippedWhenRateLimited() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{Total: 1})
	m.totalPrompts = 1 // force the total-rate-limit short-circuit

	var promptFired bool
	out := m.Request(context.Background(), "chan1", ApprovalRequest{
		OnPrompt: func() { promptFired = true },
	})
	require.Equal(s.T(), types.DecisionDeny, out.Decision)
	require.True(s.T(), out.RateLimited)
	require.Equal(s.T(), 0, bot.sendCount)
	require.False(s.T(), promptFired, "OnPrompt must not fire when rate-limit short-circuits")
}

func (s *ApprovalSuite) TestEmptyCacheKeyBypassesCache() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})
	m.cache[""] = types.DecisionAllow // shouldn't be consulted

	outCh := s.request(m, ApprovalRequest{Kind: "exec", Target: "x", CacheKey: ""})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "user1"))
	out := <-outCh

	require.Equal(s.T(), types.DecisionAllow, out.Decision)
	require.False(s.T(), out.FromCache)
	require.Equal(s.T(), 1, bot.sendCount)
}

// --- Resolve outcomes ---

func (s *ApprovalSuite) TestResolveOnceAllowNotCached() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	out := <-outCh

	require.Equal(s.T(), types.DecisionAllow, out.Decision)
	require.Equal(s.T(), "u", out.Actor)
	m.mu.Lock()
	_, cached := m.cache["k"]
	m.mu.Unlock()
	require.False(s.T(), cached)
	require.Equal(s.T(), 1, bot.removeCnt)
}

func (s *ApprovalSuite) TestResolveSessionAllowCached() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionSession, "u"))
	<-outCh

	m.mu.Lock()
	d, ok := m.cache["k"]
	m.mu.Unlock()
	require.True(s.T(), ok)
	require.Equal(s.T(), types.DecisionAllow, d)
}

func (s *ApprovalSuite) TestResolveDeny() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionDeny, "u"))
	out := <-outCh

	require.Equal(s.T(), types.DecisionDeny, out.Decision)
	m.mu.Lock()
	_, cached := m.cache["k"]
	m.mu.Unlock()
	require.False(s.T(), cached)
}

func (s *ApprovalSuite) TestResolveDenySessionCached() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionDenySession, "u"))
	<-outCh

	m.mu.Lock()
	d, ok := m.cache["k"]
	m.mu.Unlock()
	require.True(s.T(), ok)
	require.Equal(s.T(), types.DecisionDeny, d)
}

func (s *ApprovalSuite) TestResolveEmptyCacheKeySessionSkipsCache() {
	// "session" with empty CacheKey must not crash or persist.
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{CacheKey: ""})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionSession, "u"))
	out := <-outCh

	require.Equal(s.T(), types.DecisionAllow, out.Decision)
	m.mu.Lock()
	size := len(m.cache)
	m.mu.Unlock()
	require.Equal(s.T(), 0, size)
}

// --- Burst memo ---
//
// A single logical command can trap the gate many times with byte-identical
// argv — most often a PATH search, where execvp(3) issues one execve(2) per
// PATH entry. These cover the short-lived memo that collapses such a burst
// into one prompt without granting session scope.

// movableClock returns a clock function plus a knob to advance it, so the
// burst TTL can be crossed without sleeping.
func movableClock() (now func() time.Time, advance func(time.Duration)) {
	var mu sync.Mutex
	t := fixedNow
	return func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return t
		}, func(d time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			t = t.Add(d)
		}
}

func (s *ApprovalSuite) TestOnceDecisionCollapsesRepeatBurst() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{CacheKey: "execve:git:push origin"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	require.Equal(s.T(), types.DecisionAllow, (<-outCh).Decision)

	// The next PATH probe arrives microseconds later with the same key.
	var promptFired bool
	out := m.Request(context.Background(), "chan1", ApprovalRequest{
		CacheKey: "execve:git:push origin",
		OnPrompt: func() { promptFired = true },
	})

	require.Equal(s.T(), types.DecisionAllow, out.Decision)
	require.True(s.T(), out.FromCache)
	require.Equal(s.T(), "burst-hit", out.Reason)
	require.False(s.T(), promptFired)
	require.Equal(s.T(), 1, bot.sendCount, "the burst must cost exactly one card")

	// Once-scope must still stay out of the session cache.
	m.mu.Lock()
	_, cached := m.cache["execve:git:push origin"]
	m.mu.Unlock()
	require.False(s.T(), cached)
}

func (s *ApprovalSuite) TestDenyOnceCollapsesRepeatBurst() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionDeny, "u"))
	<-outCh

	out := m.Request(context.Background(), "chan1", ApprovalRequest{CacheKey: "k"})
	require.Equal(s.T(), types.DecisionDeny, out.Decision)
	require.Equal(s.T(), "burst-hit", out.Reason)
	require.Equal(s.T(), 1, bot.sendCount)
}

func (s *ApprovalSuite) TestBurstMemoExpiresAndPromptsAgain() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})
	now, advance := movableClock()
	m.now = now

	outCh := s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	<-outCh

	advance(m.burstTTL) // exactly at the deadline — expired, not "still valid"

	outCh = s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID = s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	require.Equal(s.T(), types.DecisionAllow, (<-outCh).Decision)
	require.Equal(s.T(), 2, bot.sendCount, "an expired memo must prompt again")
}

func (s *ApprovalSuite) TestSessionDecisionSupersedesBurstMemo() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	// Two identical traps land before either is answered, so both prompt.
	// The first click memoises once-scope; the second must supersede it.
	firstCh := s.request(m, ApprovalRequest{ID: "a", CacheKey: "k"})
	secondCh := s.request(m, ApprovalRequest{ID: "b", CacheKey: "k"})
	s.waitForPending(m, 2)
	require.NoError(s.T(), m.Resolve("a", DecisionOnce, "u"))
	<-firstCh
	require.NoError(s.T(), m.Resolve("b", DecisionSession, "u"))
	<-secondCh

	m.mu.Lock()
	_, stillMemoed := m.burst["k"]
	cached := m.cache["k"]
	m.mu.Unlock()
	require.False(s.T(), stillMemoed, "session scope must drop the stale once-memo")
	require.Equal(s.T(), types.DecisionAllow, cached)

	out := m.Request(context.Background(), "chan1", ApprovalRequest{CacheKey: "k"})
	require.Equal(s.T(), types.DecisionAllow, out.Decision)
	require.Equal(s.T(), "cache-hit", out.Reason)
}

func (s *ApprovalSuite) TestZeroBurstTTLDisablesMemo() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})
	m.burstTTL = 0

	outCh := s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	<-outCh

	m.mu.Lock()
	size := len(m.burst)
	m.mu.Unlock()
	require.Equal(s.T(), 0, size)
}

func (s *ApprovalSuite) TestBurstMemoSweepsExpiredKeys() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})
	m.burst["stale"] = burstEntry{decision: types.DecisionAllow, expires: fixedNow.Add(-time.Second)}
	m.burst["fresh"] = burstEntry{decision: types.DecisionAllow, expires: fixedNow.Add(time.Minute)}

	outCh := s.request(m, ApprovalRequest{CacheKey: "k"})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	<-outCh

	m.mu.Lock()
	_, stale := m.burst["stale"]
	_, fresh := m.burst["fresh"]
	_, added := m.burst["k"]
	m.mu.Unlock()
	require.False(s.T(), stale, "writing a memo sweeps expired ones")
	require.True(s.T(), fresh)
	require.True(s.T(), added)
}

func (s *ApprovalSuite) TestEmptyCacheKeyLeavesNoBurstMemo() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{CacheKey: ""})
	reqID := s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	<-outCh

	m.mu.Lock()
	size := len(m.burst)
	m.mu.Unlock()
	require.Equal(s.T(), 0, size)
}

// --- Resolve errors ---

func (s *ApprovalSuite) TestResolveUnknownReqIDError() {
	m := s.newManager(&fakeBot{}, types.RateLimits{})
	err := m.Resolve("nope", DecisionOnce, "u")
	require.ErrorIs(s.T(), err, ErrNoSuchRequest)
}

func (s *ApprovalSuite) TestResolveInvalidDecisionError() {
	m := s.newManager(&fakeBot{}, types.RateLimits{})
	err := m.Resolve("whatever", "maybe", "u")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unknown decision")
}

// --- Bot failure modes ---

func (s *ApprovalSuite) TestNoBotReturnsDeny() {
	m := NewManager(&fakeRouter{bot: nil}, types.RateLimits{})
	out := m.Request(context.Background(), "chan1", ApprovalRequest{})
	require.Equal(s.T(), types.DecisionDeny, out.Decision)
	require.Equal(s.T(), "no-bot", out.Reason)
}

func (s *ApprovalSuite) TestBotSendErrorReturnsDeny() {
	bot := &fakeBot{sendErr: errors.New("discord down")}
	m := s.newManager(bot, types.RateLimits{})
	out := m.Request(context.Background(), "chan1", ApprovalRequest{})
	require.Equal(s.T(), types.DecisionDeny, out.Decision)
	require.Equal(s.T(), "bot-send-failed", out.Reason)
	// pending must be cleaned up even on send error
	m.mu.Lock()
	require.Empty(s.T(), m.pending)
	m.mu.Unlock()
}

func (s *ApprovalSuite) TestContextCancelReturnsDeny() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan Outcome, 1)
	go func() { done <- m.Request(ctx, "chan1", ApprovalRequest{}) }()
	s.waitForPending(m, 1)
	cancel()

	out := <-done
	require.Equal(s.T(), types.DecisionDeny, out.Decision)
	require.Equal(s.T(), "cancelled", out.Reason)
	require.Equal(s.T(), 1, bot.removeCnt) // still removes the prompt
}

// --- Shutdown ---

func (s *ApprovalSuite) TestShutdownDrainsPendingAndBroadcastsResolve() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh1 := s.request(m, ApprovalRequest{ID: "a"})
	outCh2 := s.request(m, ApprovalRequest{ID: "b"})
	s.waitForPending(m, 2)

	m.Shutdown()

	out1 := <-outCh1
	out2 := <-outCh2
	require.Equal(s.T(), types.DecisionDeny, out1.Decision)
	require.Equal(s.T(), "container-gone", out1.Actor)
	require.Equal(s.T(), types.DecisionDeny, out2.Decision)
	require.Equal(s.T(), "container-gone", out2.Actor)

	// Both prompts had their bot.RemoveApproval called, fanning out the
	// gate.approval_resolved broadcast that clears the FE bouncer.
	require.Equal(s.T(), 2, bot.removeCnt)

	// pending map is reset so a follow-up Shutdown is a no-op.
	m.mu.Lock()
	require.Empty(s.T(), m.pending)
	m.mu.Unlock()
}

func (s *ApprovalSuite) TestShutdownEmptyIsNoop() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})
	m.Shutdown()
	require.Equal(s.T(), 0, bot.removeCnt)
}

// TestShutdownIgnoresAlreadyResolvedChannel covers the select/default branch:
// a pending entry whose buffered slot is already taken by a concurrent Resolve
// must not block Shutdown.
func (s *ApprovalSuite) TestShutdownIgnoresAlreadyResolvedChannel() {
	m := s.newManager(&fakeBot{}, types.RateLimits{})
	ch := make(chan resolution, 1)
	ch <- resolution{decision: DecisionOnce, actor: "preempt"} // saturate buffer
	m.mu.Lock()
	m.pending["x"] = &pendingEntry{ch: ch}
	m.mu.Unlock()

	m.Shutdown() // must not block on the full channel
	m.mu.Lock()
	require.Empty(s.T(), m.pending)
	m.mu.Unlock()
}

// --- ListPending ---

func (s *ApprovalSuite) TestListPendingEmpty() {
	m := s.newManager(&fakeBot{}, types.RateLimits{})
	require.Empty(s.T(), m.ListPending())
}

func (s *ApprovalSuite) TestListPendingReturnsSnapshot() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{
		ID:      "p-1",
		Kind:    "exec",
		Target:  "git push",
		Source:  "terminal:leaf-7",
		Message: "Allow exec?",
		Details: map[string]string{"cwd": "/work"},
	})
	s.waitForPending(m, 1)

	got := m.ListPending()
	require.Len(s.T(), got, 1)
	require.Equal(s.T(), PendingApproval{
		ReqID:     "p-1",
		ChannelID: "chan1",
		Kind:      "exec",
		Target:    "git push",
		Source:    "terminal:leaf-7",
		ExpiresAt: fixedNow.Add(approvalTimeout),
		Message:   "Allow exec?",
		Details:   map[string]string{"cwd": "/work"},
	}, got[0])

	require.NoError(s.T(), m.Resolve("p-1", DecisionOnce, "u"))
	<-outCh

	// After resolve, snapshot is empty again.
	require.Empty(s.T(), m.ListPending())
}

// --- ID generation ---

func (s *ApprovalSuite) TestRequestIDAssignedWhenEmpty() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{ID: "", Target: "t"})
	reqID := s.waitForPending(m, 1)
	require.NotEmpty(s.T(), reqID)
	require.NoError(s.T(), m.Resolve(reqID, DecisionOnce, "u"))
	<-outCh
	require.Equal(s.T(), reqID, bot.lastReqID)
}

func (s *ApprovalSuite) TestRequestIDPreservedWhenSet() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{ID: "preset-id"})
	s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve("preset-id", DecisionOnce, "u"))
	<-outCh
	require.Equal(s.T(), "preset-id", bot.lastReqID)
}

// --- Rate limits ---

func (s *ApprovalSuite) TestRateLimitPending() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{Pending: 2})

	// Two long-held requests saturate the pending cap.
	outCh1 := s.request(m, ApprovalRequest{ID: "a"})
	outCh2 := s.request(m, ApprovalRequest{ID: "b"})
	s.waitForPending(m, 2)

	// Third is denied synchronously.
	out := m.Request(context.Background(), "chan1", ApprovalRequest{ID: "c"})
	require.Equal(s.T(), types.DecisionDeny, out.Decision)
	require.True(s.T(), out.RateLimited)
	require.Equal(s.T(), "rate-limit-pending", out.Reason)

	require.NoError(s.T(), m.Resolve("a", DecisionOnce, "u"))
	require.NoError(s.T(), m.Resolve("b", DecisionOnce, "u"))
	<-outCh1
	<-outCh2
}

func (s *ApprovalSuite) TestRateLimitTotalStaysTripped() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{Total: 1})

	// First request is accepted, resolved once.
	outCh := s.request(m, ApprovalRequest{ID: "a"})
	s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve("a", DecisionOnce, "u"))
	<-outCh

	// Second trips total cap and stays tripped.
	out := m.Request(context.Background(), "chan1", ApprovalRequest{})
	require.Equal(s.T(), "rate-limit-total", out.Reason)
	require.True(s.T(), out.RateLimited)

	// Third also denied on the sticky flag path (covers the totalTripped early return).
	out = m.Request(context.Background(), "chan1", ApprovalRequest{})
	require.Equal(s.T(), "rate-limit-total", out.Reason)
}

func (s *ApprovalSuite) TestRateLimitPerMinute() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{PerMinute: 2})
	base := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return base }

	// Two requests within the minute both allowed (well, pending).
	outCh1 := s.request(m, ApprovalRequest{ID: "a"})
	outCh2 := s.request(m, ApprovalRequest{ID: "b"})
	s.waitForPending(m, 2)

	// Third within the same minute denied.
	out := m.Request(context.Background(), "chan1", ApprovalRequest{ID: "c"})
	require.Equal(s.T(), "rate-limit-per-minute", out.Reason)
	require.True(s.T(), out.RateLimited)

	require.NoError(s.T(), m.Resolve("a", DecisionOnce, "u"))
	require.NoError(s.T(), m.Resolve("b", DecisionOnce, "u"))
	<-outCh1
	<-outCh2

	// Advance beyond the minute → old entries trim, new request accepted.
	m.now = func() time.Time { return base.Add(2 * time.Minute) }
	outCh3 := s.request(m, ApprovalRequest{ID: "d"})
	reqID := s.waitForPending(m, 1)
	require.Equal(s.T(), "d", reqID)
	require.NoError(s.T(), m.Resolve("d", DecisionOnce, "u"))
	<-outCh3
}

// --- Cache hit after session decision ---

func (s *ApprovalSuite) TestCacheHitAfterSessionAllow() {
	bot := &fakeBot{}
	m := s.newManager(bot, types.RateLimits{})

	outCh := s.request(m, ApprovalRequest{ID: "a", CacheKey: "k"})
	s.waitForPending(m, 1)
	require.NoError(s.T(), m.Resolve("a", DecisionSession, "u"))
	<-outCh

	// Second request with same cache key: no bot prompt.
	require.Equal(s.T(), 1, bot.sendCount)
	out := m.Request(context.Background(), "chan1", ApprovalRequest{CacheKey: "k"})
	require.True(s.T(), out.FromCache)
	require.Equal(s.T(), types.DecisionAllow, out.Decision)
	require.Equal(s.T(), 1, bot.sendCount)
}

// --- Random ID generator smoke ---

func (s *ApprovalSuite) TestRandomIDGeneratorDistinct() {
	seen := map[string]struct{}{}
	for range 50 {
		id := randomID()
		require.Len(s.T(), id, 32)
		require.NotContains(s.T(), seen, id)
		seen[id] = struct{}{}
	}
}
