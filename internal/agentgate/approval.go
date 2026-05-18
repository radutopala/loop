package agentgate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// Bot renders approval prompts for one transport (Discord/Slack/Local).
type Bot interface {
	SendApproval(ctx context.Context, channelID string, req ApprovalRequest) (messageID string, err error)
	RemoveApproval(ctx context.Context, channelID, messageID string) error
}

// BotRouter resolves the Bot for a given channel. Returns nil if the channel
// has no bound bot (which Manager treats as deny).
type BotRouter interface {
	For(channelID string) Bot
}

// ApprovalRequest is handed to Manager.Request. ID is assigned by the manager
// when empty; the bot echoes it so the click-handler can correlate.
// CacheKey of "" disables caching for this request.
//
// Details carries optional structured key/value fields the renderer can show
// alongside Target — e.g. {"image": "alpine", "binds": "/etc:/host-etc:ro"}
// for a "POST /containers/create" approval. Order is not preserved across
// JSON transit, so renderers should sort keys for stable display.
//
// Source identifies which process originated the request inside the agent
// container, so the renderer can target the right UI surface:
//   - "chat" — the chat agent (container entrypoint, PID 1 ancestor).
//   - "terminal:<leafId>" — the terminal pane whose exec carried
//     LOOP_TERMINAL_LEAF=<leafId> (set by the WS handler when the FE creates
//     an agent-pane terminal). Multiple panes in the same container produce
//     distinct leafIds, so each pane only shows its own approval cards.
type ApprovalRequest struct {
	ID       string
	Kind     string
	Target   string
	Source   string
	Message  string
	CacheKey string
	Details  map[string]string
}

// Outcome is what Request returns. Decision is always Allow or Deny.
// Reason is an operator-facing tag when the decision didn't come from a user
// click (cache-hit, bot-error, rate-limit, context cancel).
type Outcome struct {
	Decision    types.Decision
	Actor       string
	FromCache   bool
	RateLimited bool
	Reason      string
}

// User decisions that may be reported via Resolve.
const (
	DecisionOnce        = "once"
	DecisionSession     = "session"
	DecisionDeny        = "deny"
	DecisionDenySession = "deny-session"
)

// ErrNoSuchRequest is returned by Resolve for an unknown reqID (late click or
// already resolved).
var ErrNoSuchRequest = errors.New("agentgate: no pending approval for request ID")

// PendingApproval is the read-only view of an in-flight approval, returned by
// Manager.ListPending. The FE uses these to rehydrate gateApprovals after a
// WS reconnect or renderer reload, and the electron-main bouncer uses the
// req_id set to reconcile its dock-bounce list.
type PendingApproval struct {
	ReqID     string
	ChannelID string
	Kind      string
	Target    string
	Source    string
	Message   string
	Details   map[string]string
}

// Manager coordinates approval prompts, per-container decision cache, and
// rate limits. One Manager per container; lifecycle = container lifecycle.
type Manager struct {
	bots   BotRouter
	limits types.RateLimits
	now    func() time.Time
	idGen  func() string

	mu           sync.Mutex
	cache        map[string]types.Decision
	pending      map[string]*pendingEntry
	totalPrompts int
	recent       []time.Time
	totalTripped bool
}

// pendingEntry is the internal record of an in-flight approval — the
// resolution channel plus the metadata needed to surface the request to a
// reconnecting FE via ListPending.
type pendingEntry struct {
	ch        chan resolution
	channelID string
	req       ApprovalRequest
}

type resolution struct {
	decision string
	actor    string
}

// NewManager constructs a Manager. Zero-valued RateLimits fields disable that cap.
func NewManager(bots BotRouter, limits types.RateLimits) *Manager {
	return &Manager{
		bots:    bots,
		limits:  limits,
		now:     time.Now,
		idGen:   randomID,
		cache:   map[string]types.Decision{},
		pending: map[string]*pendingEntry{},
	}
}

// Request prompts the user (or returns a cached / rate-limited result).
// Outcome.Decision is always Allow or Deny — never Approve.
func (m *Manager) Request(ctx context.Context, channelID string, req ApprovalRequest) Outcome {
	if req.CacheKey != "" {
		m.mu.Lock()
		d, ok := m.cache[req.CacheKey]
		m.mu.Unlock()
		if ok {
			return Outcome{Decision: d, FromCache: true, Reason: "cache-hit"}
		}
	}

	if out, ok := m.checkLimits(); !ok {
		return out
	}

	id := req.ID
	if id == "" {
		id = m.idGen()
	}
	req.ID = id
	ch := make(chan resolution, 1)
	entry := &pendingEntry{ch: ch, channelID: channelID, req: req}

	m.mu.Lock()
	m.pending[id] = entry
	m.totalPrompts++
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pending, id)
		m.mu.Unlock()
	}()

	bot := m.bots.For(channelID)
	if bot == nil {
		return Outcome{Decision: types.DecisionDeny, Reason: "no-bot"}
	}
	msgID, err := bot.SendApproval(ctx, channelID, req)
	if err != nil {
		return Outcome{Decision: types.DecisionDeny, Reason: "bot-send-failed"}
	}

	select {
	case r := <-entry.ch:
		_ = bot.RemoveApproval(ctx, channelID, msgID)
		return m.applyResolution(req.CacheKey, r)
	case <-ctx.Done():
		_ = bot.RemoveApproval(ctx, channelID, msgID)
		return Outcome{Decision: types.DecisionDeny, Reason: "cancelled"}
	}
}

// Shutdown drains every pending approval by pushing a deny resolution onto
// its channel. Each blocked Request goroutine then runs its own deferred
// cleanup — including bot.RemoveApproval, which broadcasts
// gate.approval_resolved so the desktop UI can dismiss the card and the
// electron-main dock-bounce loop can drop the request id.
//
// Called by MultiManagerResolver.Remove on container teardown. Without this
// path, a container that exits while a prompt is open leaves the FE/electron
// state stuck on a phantom approval until the user quits the app.
//
// Sends are non-blocking via select/default because each pending channel is
// buffered (cap 1) and may already hold a pending resolution from a
// concurrent click.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	pending := m.pending
	m.pending = map[string]*pendingEntry{}
	m.mu.Unlock()
	for _, entry := range pending {
		select {
		case entry.ch <- resolution{decision: DecisionDeny, actor: "container-gone"}:
		default:
		}
	}
}

// Resolve records a user decision for reqID. decision must be one of
// DecisionOnce, DecisionSession, DecisionDeny, DecisionDenySession.
func (m *Manager) Resolve(reqID, decision, actorID string) error {
	switch decision {
	case DecisionOnce, DecisionSession, DecisionDeny, DecisionDenySession:
	default:
		return fmt.Errorf("agentgate: unknown decision %q", decision)
	}
	m.mu.Lock()
	entry, ok := m.pending[reqID]
	if ok {
		delete(m.pending, reqID)
	}
	m.mu.Unlock()
	if !ok {
		return ErrNoSuchRequest
	}
	entry.ch <- resolution{decision: decision, actor: actorID}
	return nil
}

// ListPending returns a snapshot of all in-flight approvals on this Manager.
// Result is a copy, safe to use without holding any lock. Order is not
// stable across calls.
func (m *Manager) ListPending() []PendingApproval {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PendingApproval, 0, len(m.pending))
	for id, e := range m.pending {
		out = append(out, PendingApproval{
			ReqID:     id,
			ChannelID: e.channelID,
			Kind:      e.req.Kind,
			Target:    e.req.Target,
			Source:    e.req.Source,
			Message:   e.req.Message,
			Details:   e.req.Details,
		})
	}
	return out
}

// checkLimits enforces rate caps. On block, returns (denial-outcome, false).
// On pass, increments the per-minute counter and returns (zero, true).
func (m *Manager) checkLimits() (Outcome, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.totalTripped {
		return Outcome{Decision: types.DecisionDeny, RateLimited: true, Reason: "rate-limit-total"}, false
	}
	if m.limits.Total > 0 && m.totalPrompts >= m.limits.Total {
		m.totalTripped = true
		return Outcome{Decision: types.DecisionDeny, RateLimited: true, Reason: "rate-limit-total"}, false
	}
	if m.limits.Pending > 0 && len(m.pending) >= m.limits.Pending {
		return Outcome{Decision: types.DecisionDeny, RateLimited: true, Reason: "rate-limit-pending"}, false
	}
	if m.limits.PerMinute > 0 {
		cutoff := m.now().Add(-time.Minute)
		i := 0
		for i < len(m.recent) && m.recent[i].Before(cutoff) {
			i++
		}
		m.recent = m.recent[i:]
		if len(m.recent) >= m.limits.PerMinute {
			return Outcome{Decision: types.DecisionDeny, RateLimited: true, Reason: "rate-limit-per-minute"}, false
		}
		m.recent = append(m.recent, m.now())
	}
	return Outcome{}, true
}

func (m *Manager) applyResolution(cacheKey string, r resolution) Outcome {
	var d types.Decision
	persist := false
	switch r.decision {
	case DecisionOnce:
		d = types.DecisionAllow
	case DecisionSession:
		d = types.DecisionAllow
		persist = true
	case DecisionDeny:
		d = types.DecisionDeny
	case DecisionDenySession:
		d = types.DecisionDeny
		persist = true
	}
	if persist && cacheKey != "" {
		m.mu.Lock()
		m.cache[cacheKey] = d
		m.mu.Unlock()
	}
	return Outcome{Decision: d, Actor: r.actor}
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
