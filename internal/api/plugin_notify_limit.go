package api

import (
	"sync"
	"time"
)

const (
	// pluginNotifyBurst is how many notifications a plugin may show within
	// pluginNotifyWindow.
	pluginNotifyBurst  = 5
	pluginNotifyWindow = time.Minute
)

// notifyLimit caps how many desktop notifications each plugin shows, so a
// plugin can't flood the owner's desktop. The zero value is ready to use.
type notifyLimit struct {
	mu   sync.Mutex
	sent map[string][]time.Time // each plugin's notifications within the window
	now  func() time.Time       // tests pin the clock
}

// allow reports whether pluginID may show a notification now, counting it
// when it may; otherwise it returns how long until it may.
func (l *notifyLimit) allow(pluginID string) (bool, time.Duration) {
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sent == nil {
		l.sent = map[string][]time.Time{}
	}
	cutoff := now.Add(-pluginNotifyWindow)
	recent := l.sent[pluginID]
	for len(recent) > 0 && !recent[0].After(cutoff) {
		recent = recent[1:]
	}
	if len(recent) >= pluginNotifyBurst {
		l.sent[pluginID] = recent
		return false, recent[0].Sub(cutoff)
	}
	l.sent[pluginID] = append(recent, now)
	return true, 0
}
