package proxy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/plugin/runtime"
)

const (
	// breakerFailures is how many calls in a row a plugin container must
	// leave unanswered before its breaker opens.
	breakerFailures = 3
	// breakerCooldown is how long an open breaker fails calls at once
	// before it lets one through to try the container again.
	breakerCooldown = 30 * time.Second
	// breakerForget drops a breaker whose container hasn't failed a call
	// for this long, e.g. one that was replaced.
	breakerForget = 10 * time.Minute
)

// ErrUnavailable is returned without calling a plugin container that left
// its last calls unanswered, until its breaker lets a call try it again.
var ErrUnavailable = errors.New("the plugin isn't answering")

// breakers holds a circuit breaker per plugin container. A container is
// its endpoint and secret, and the secret is new on every start, so a
// restarted container starts with a closed breaker. Only containers that
// failed their last call have one.
type breakers struct {
	now func() time.Time

	mu    sync.Mutex
	byKey map[string]*breaker
}

type breaker struct {
	failures   int
	lastFailed time.Time
	openUntil  time.Time
	// probing is set while the one call let through after the cooldown
	// is under way; others fail at once until it ends.
	probing bool
}

func newBreakers(now func() time.Time) *breakers {
	return &breakers{now: now, byKey: map[string]*breaker{}}
}

func breakerKey(ep runtime.Endpoint) string {
	return ep.URL + "\x00" + ep.Secret
}

// allow returns ErrUnavailable when the container's breaker is open, and
// otherwise lets the call through.
func (bs *breakers) allow(ep runtime.Endpoint) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	b := bs.byKey[breakerKey(ep)]
	if b == nil || b.failures < breakerFailures {
		return nil
	}
	if wait := b.openUntil.Sub(bs.now()); wait > 0 {
		return fmt.Errorf("%w: %d calls in a row failed, trying it again in %s", ErrUnavailable, b.failures, wait.Round(time.Second))
	}
	if b.probing {
		return fmt.Errorf("%w: %d calls in a row failed, trying it again now", ErrUnavailable, b.failures)
	}
	b.probing = true
	return nil
}

// done records how a call let through by allow ended. err is the call's
// error when the container didn't answer; parent is the caller's context,
// so a call the caller gave up on counts for nothing.
func (bs *breakers) done(ep runtime.Endpoint, parent context.Context, err error) {
	k := breakerKey(ep)
	bs.mu.Lock()
	defer bs.mu.Unlock()
	switch {
	case err == nil:
		delete(bs.byKey, k)
	case parent.Err() != nil:
		if b := bs.byKey[k]; b != nil {
			b.probing = false
		}
	default:
		now := bs.now()
		for other, b := range bs.byKey {
			if now.Sub(b.lastFailed) > breakerForget {
				delete(bs.byKey, other)
			}
		}
		b := bs.byKey[k]
		if b == nil {
			b = &breaker{}
			bs.byKey[k] = b
		}
		b.failures++
		b.lastFailed = now
		b.probing = false
		if b.failures >= breakerFailures {
			b.openUntil = now.Add(breakerCooldown)
		}
	}
}
