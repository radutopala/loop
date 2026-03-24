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
	lastUsedAt        time.Time
}

// sessionManager provides shared session lifecycle management for browser providers.
type sessionManager struct {
	mu       sync.Mutex
	sessions map[string]*browserSession
	timeNow  func() time.Time
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
