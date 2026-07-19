package browser

import (
	"net"
	"sync"
	"time"
)

// isChromeReachable checks if Chrome's CDP endpoint is reachable at the given host:port
// by attempting a TCP connection.
func isChromeReachable(hostPort string) bool {
	if hostPort == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", hostPort, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// browserSession tracks the lifecycle of a browser instance for a single channel.
// CDP/tab state is managed by CDPManager, not here.
type browserSession struct {
	chromeContainerID string // only used by DockerProvider
	hostPort          string // only used by DockerProvider
	cdpAddr           string // host:port the daemon uses to reach CDP (DockerProvider)
	lastUsedAt        time.Time
}

// sessionManager provides shared session lifecycle management for browser providers.
type sessionManager struct {
	mu       sync.Mutex
	sessions map[string]*browserSession
	timeNow  func() time.Time

	// chanLocks holds one mutex per channel, serializing slow same-channel
	// lifecycle work (container inspect/create, CDP reachability dials)
	// without stalling other channels. The sessions map itself stays guarded
	// by mu, whose critical sections must remain short and I/O-free.
	chanLocks sync.Map // channelID → *sync.Mutex
}

// channelLock returns the per-channel mutex, creating it on first use.
func (s *sessionManager) channelLock(channelID string) *sync.Mutex {
	lock, _ := s.chanLocks.LoadOrStore(channelID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// storeSession publishes a fully-built session for the channel.
func (s *sessionManager) storeSession(channelID string, sess *browserSession) {
	s.mu.Lock()
	s.sessions[channelID] = sess
	s.mu.Unlock()
}

func newSessionManager() sessionManager {
	return sessionManager{
		sessions: make(map[string]*browserSession),
		timeNow:  time.Now,
	}
}

func newBrowserSession(now time.Time) *browserSession {
	return &browserSession{
		lastUsedAt: now,
	}
}
