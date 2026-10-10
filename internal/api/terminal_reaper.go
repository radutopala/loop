package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

const (
	// terminalClaimGrace is how long a terminal session no pane is attached
	// to stays once no app window claims it any more. The app claims every
	// minute, so a few missed claims never close a session.
	terminalClaimGrace = 5 * time.Minute
	// terminalReapInterval is how often unclaimed sessions are looked for.
	terminalReapInterval = time.Minute
)

// terminalClaims records when an app window last held each terminal
// session, so a session that no window holds any more, one of a closed or
// crashed window say, is closed instead of running on.
type terminalClaims struct {
	mu        sync.Mutex
	lastHeld  map[string]time.Time
	lastSweep time.Time
	now       func() time.Time // tests pin the clock; nil means time.Now
}

func (c *terminalClaims) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// claim marks the sessions as held now.
func (c *terminalClaims) claim(ids []string) {
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastHeld == nil {
		c.lastHeld = make(map[string]time.Time)
	}
	for _, id := range ids {
		c.lastHeld[id] = now
	}
}

// terminalClaimsRequest lists the terminal sessions an app window holds,
// on tabs it isn't showing too.
type terminalClaimsRequest struct {
	SessionIDs []string `json:"session_ids"`
}

func (s *Server) handleClaimTerminals(w http.ResponseWriter, r *http.Request) {
	var req terminalClaimsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.termClaims.claim(req.SessionIDs)
	w.WriteHeader(http.StatusNoContent)
}

// RunTerminalReaper closes, every minute, the terminal sessions that no pane
// is attached to and no app window has claimed for a few minutes.
func (s *Server) RunTerminalReaper(ctx context.Context) {
	s.runTerminalReaper(ctx, terminalClaimGrace, terminalReapInterval)
}

func (s *Server) runTerminalReaper(ctx context.Context, grace, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reapUnclaimedTerminals(ctx, grace, interval)
		}
	}
}

// sessionLister lists a terminal manager's sessions, mapped to whether a
// client is attached to each.
type sessionLister interface {
	Sessions() map[string]bool
}

// unclaimedTerminal is a session the reaper closes.
type unclaimedTerminal struct {
	id     string
	mgr    TerminalManager
	isHost bool
}

// reapUnclaimedTerminals closes the sessions that no pane is attached to and
// that went unclaimed for longer than grace. An attached session counts as
// held, and so does one seen for the first time.
func (s *Server) reapUnclaimedTerminals(ctx context.Context, grace, interval time.Duration) {
	c := &s.termClaims
	now := c.clock()
	c.mu.Lock()
	if c.lastHeld == nil {
		c.lastHeld = make(map[string]time.Time)
	}
	// A gap far longer than the interval means the machine slept, and the
	// app couldn't claim anything meanwhile, so every session counts as held.
	slept := !c.lastSweep.IsZero() && now.Sub(c.lastSweep) > 2*interval
	c.lastSweep = now
	present := make(map[string]bool)
	var reap []unclaimedTerminal
	for _, m := range []struct {
		mgr    TerminalManager
		isHost bool
	}{{s.termManager, false}, {s.hostTermManager, true}} {
		l, ok := m.mgr.(sessionLister)
		if !ok {
			continue
		}
		for id, attached := range l.Sessions() {
			present[id] = true
			last, seen := c.lastHeld[id]
			if attached || slept || !seen {
				c.lastHeld[id] = now
				continue
			}
			if now.Sub(last) > grace {
				reap = append(reap, unclaimedTerminal{id: id, mgr: m.mgr, isHost: m.isHost})
			}
		}
	}
	for id := range c.lastHeld {
		if !present[id] {
			delete(c.lastHeld, id)
		}
	}
	c.mu.Unlock()

	for _, u := range reap {
		s.closeUnclaimedTerminal(ctx, u)
	}
}

// closeUnclaimedTerminal closes a session the way closing its pane would.
func (s *Server) closeUnclaimedTerminal(ctx context.Context, u unclaimedTerminal) {
	if !u.isHost {
		if err := u.mgr.KillProcessGroup(ctx, u.id); err != nil {
			s.logger.Warn("terminal reaper: kill process group failed", "session_id", u.id, "error", err)
		}
	}
	containerID, err := u.mgr.StopSession(u.id)
	if err != nil {
		s.logger.Warn("terminal reaper: stop session failed", "session_id", u.id, "error", err)
		return
	}
	if !u.isHost {
		s.releaseShell(containerID)
	}
	s.logger.Info("terminal reaper: closed a session no window holds", "session_id", u.id, "host", u.isHost)
}
