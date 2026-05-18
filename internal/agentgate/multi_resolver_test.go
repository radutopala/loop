package agentgate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

// managerWithPending returns a Manager that has exactly one pending request
// matching reqID, ready to accept a Resolve call synchronously (since the
// buffered channel absorbs the write).
func managerWithPending(t *testing.T, reqID string) *Manager {
	t.Helper()
	m := NewManager(&fakeRouter{bot: &fakeBot{}}, types.RateLimits{})
	m.mu.Lock()
	m.pending[reqID] = &pendingEntry{
		ch:        make(chan resolution, 1),
		channelID: "ch-" + reqID,
		req:       ApprovalRequest{ID: reqID},
	}
	m.mu.Unlock()
	return m
}

func TestMultiManagerResolveFindsOwner(t *testing.T) {
	r := NewMultiManagerResolver()

	m1 := managerWithPending(t, "other")
	m2 := managerWithPending(t, "wanted")
	r.Add("c1", m1)
	r.Add("c2", m2)

	require.NoError(t, r.Resolve("wanted", DecisionOnce, "user-1"))

	// m2 should have consumed the pending entry.
	m2.mu.Lock()
	_, stillPending := m2.pending["wanted"]
	m2.mu.Unlock()
	require.False(t, stillPending)

	// m1 is untouched.
	m1.mu.Lock()
	_, still := m1.pending["other"]
	m1.mu.Unlock()
	require.True(t, still)
}

func TestMultiManagerResolveUnknownReqReturnsErrNoSuchRequest(t *testing.T) {
	r := NewMultiManagerResolver()
	m := managerWithPending(t, "other")
	r.Add("c1", m)

	err := r.Resolve("ghost", DecisionOnce, "")
	require.ErrorIs(t, err, ErrNoSuchRequest)
}

func TestMultiManagerResolveEmptyReturnsErrNoSuchRequest(t *testing.T) {
	r := NewMultiManagerResolver()
	require.ErrorIs(t, r.Resolve("anything", DecisionOnce, ""), ErrNoSuchRequest)
}

func TestMultiManagerResolveNonNoSuchErrorSurfaces(t *testing.T) {
	// Unknown-decision error from Manager.Resolve is not ErrNoSuchRequest; it
	// should propagate through the multiplexer.
	r := NewMultiManagerResolver()
	m := managerWithPending(t, "wanted")
	r.Add("c1", m)

	err := r.Resolve("wanted", "bogus-decision", "u")
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrNoSuchRequest))
}

func TestMultiManagerRemove(t *testing.T) {
	r := NewMultiManagerResolver()
	m := managerWithPending(t, "wanted")
	r.Add("c1", m)
	r.Remove("c1")

	require.ErrorIs(t, r.Resolve("wanted", DecisionOnce, ""), ErrNoSuchRequest)

	// Shutdown wiped the Manager's pending map too.
	m.mu.Lock()
	require.Empty(t, m.pending)
	m.mu.Unlock()

	// Remove of unknown id is a no-op.
	r.Remove("does-not-exist")
}

// TestMultiManagerRemoveDrainsLivePending exercises the end-to-end shutdown
// path: a live Request goroutine blocked on a click receives a deny resolution
// when Remove tears down its container, and bot.RemoveApproval is invoked so
// the FE gets a gate.approval_resolved broadcast.
func TestMultiManagerRemoveDrainsLivePending(t *testing.T) {
	fb := &fakeBot{}
	m := NewManager(&fakeRouter{bot: fb}, types.RateLimits{})
	m.idGen = func() string { return "live-id" }

	r := NewMultiManagerResolver()
	r.Add("c1", m)

	outcome := make(chan Outcome, 1)
	go func() {
		outcome <- m.Request(context.Background(), "ch", ApprovalRequest{Target: "git push"})
	}()

	require.Eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		_, ok := m.pending["live-id"]
		return ok
	}, time.Second, 5*time.Millisecond)

	r.Remove("c1")

	got := <-outcome
	require.Equal(t, types.DecisionDeny, got.Decision)
	require.Equal(t, "container-gone", got.Actor)

	fb.mu.Lock()
	defer fb.mu.Unlock()
	require.Equal(t, 1, fb.removeCnt)
}

func TestMultiManagerAddWithTokenByTokenRoundTrip(t *testing.T) {
	r := NewMultiManagerResolver()
	m := managerWithPending(t, "wanted")
	r.AddWithToken("c1", "tok-abc", m, "ch-42")

	cid, gotMgr, channelID, ok := r.ByToken("tok-abc")
	require.True(t, ok)
	require.Equal(t, "c1", cid)
	require.Same(t, m, gotMgr)
	require.Equal(t, "ch-42", channelID)

	// Resolve still works via containerID (click routing is unaffected by token).
	require.NoError(t, r.Resolve("wanted", DecisionOnce, "u"))
}

func TestMultiManagerByTokenUnknownReturnsFalse(t *testing.T) {
	r := NewMultiManagerResolver()
	r.AddWithToken("c1", "tok-abc", managerWithPending(t, "w"), "ch")

	cid, mgr, channelID, ok := r.ByToken("tok-wrong")
	require.False(t, ok)
	require.Empty(t, cid)
	require.Nil(t, mgr)
	require.Empty(t, channelID)
}

func TestMultiManagerByTokenEmptyReturnsFalse(t *testing.T) {
	r := NewMultiManagerResolver()
	r.AddWithToken("c1", "tok-abc", managerWithPending(t, "w"), "ch")

	_, _, _, ok := r.ByToken("")
	require.False(t, ok)
}

func TestMultiManagerAddWithTokenEmptyTokenSkipsTokenMap(t *testing.T) {
	// Empty token registers Manager but leaves no HTTP route.
	r := NewMultiManagerResolver()
	r.AddWithToken("c1", "", managerWithPending(t, "w"), "ch")

	_, _, _, ok := r.ByToken("")
	require.False(t, ok)

	// But click-routing works via Resolve.
	require.NoError(t, r.Resolve("w", DecisionOnce, ""))
}

// TestMultiManagerByTokenOrphanTokenReturnsFalse covers the defensive path
// where tokens has a cid but managers has none (transient mid-Remove state
// is the only realistic source). Construct it by hand to exercise the branch.
func TestMultiManagerByTokenOrphanToken(t *testing.T) {
	r := NewMultiManagerResolver()
	r.tokens["orphan"] = "c-gone"
	r.channels["c-gone"] = "ch"

	cid, mgr, channelID, ok := r.ByToken("orphan")
	require.False(t, ok)
	require.Empty(t, cid)
	require.Nil(t, mgr)
	require.Empty(t, channelID)
}

func TestMultiManagerRemoveClearsTokenAndChannel(t *testing.T) {
	r := NewMultiManagerResolver()
	m := managerWithPending(t, "w")
	r.AddWithToken("c1", "tok-xyz", m, "ch-1")

	r.Remove("c1")

	_, _, _, ok := r.ByToken("tok-xyz")
	require.False(t, ok)
}

func TestMultiManagerByTokenScansAllTokens(t *testing.T) {
	// Guards the constant-time-compare property indirectly: multiple tokens
	// registered, ByToken resolves the correct one even when iteration order
	// is non-deterministic.
	r := NewMultiManagerResolver()
	m1 := managerWithPending(t, "r1")
	m2 := managerWithPending(t, "r2")
	m3 := managerWithPending(t, "r3")
	r.AddWithToken("c1", "tok-1", m1, "ch-1")
	r.AddWithToken("c2", "tok-2", m2, "ch-2")
	r.AddWithToken("c3", "tok-3", m3, "ch-3")

	cid, mgr, channelID, ok := r.ByToken("tok-2")
	require.True(t, ok)
	require.Equal(t, "c2", cid)
	require.Same(t, m2, mgr)
	require.Equal(t, "ch-2", channelID)
}

func TestMultiManagerListPendingEmpty(t *testing.T) {
	r := NewMultiManagerResolver()
	require.Empty(t, r.ListPending())
}

func TestMultiManagerListPendingAggregatesAcrossManagers(t *testing.T) {
	r := NewMultiManagerResolver()
	m1 := managerWithPending(t, "r1")
	m2 := managerWithPending(t, "r2")
	r.Add("c1", m1)
	r.Add("c2", m2)

	got := r.ListPending()
	require.Len(t, got, 2)

	byContainer := map[string]ContainerPendingApproval{}
	for _, p := range got {
		byContainer[p.ContainerID] = p
	}
	require.Equal(t, "r1", byContainer["c1"].ReqID)
	require.Equal(t, "ch-r1", byContainer["c1"].ChannelID)
	require.Equal(t, "r2", byContainer["c2"].ReqID)
	require.Equal(t, "ch-r2", byContainer["c2"].ChannelID)
}

// Ensures Manager-through-Multi round-trip works end-to-end with a real
// Request/Resolve cycle (not just a pre-seeded pending map).
func TestMultiManagerEndToEnd(t *testing.T) {
	fb := &fakeBot{}
	m := NewManager(&fakeRouter{bot: fb}, types.RateLimits{})
	m.idGen = func() string { return "fixed-id" }

	r := NewMultiManagerResolver()
	r.Add("container-1", m)

	outcome := make(chan Outcome, 1)
	go func() {
		outcome <- m.Request(context.Background(), "ch", ApprovalRequest{Target: "git push"})
	}()

	require.Eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		_, ok := m.pending["fixed-id"]
		return ok
	}, time.Second, 5*time.Millisecond)

	require.NoError(t, r.Resolve("fixed-id", DecisionOnce, "user-7"))
	got := <-outcome
	require.Equal(t, types.DecisionAllow, got.Decision)
	require.Equal(t, "user-7", got.Actor)
}
