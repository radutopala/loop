// Package terminal manages interactive terminal sessions inside Docker
// containers. It provides a Manager that creates exec-based PTY sessions
// with ring-buffered output, and supports attach/detach, input, resize,
// and stop operations.
package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Default ring buffer size: 64 KB.
const defaultRingBufSize = 64 * 1024

// clientChannelBuffer is the capacity of per-client output channels.
const clientChannelBuffer = 64

// readBufSize is the size of the temporary buffer used in readLoop.
const readBufSize = 4096

// ExecClient abstracts the Docker exec operations needed by the terminal
// manager (docker exec), making it testable without a real Docker daemon.
//
// This is distinct from container.DockerClient, which handles container
// lifecycle (docker create/start/stop/rm). ExecClient runs commands inside
// already-running containers for interactive PTY sessions.
type ExecClient interface {
	ContainerExecCreate(ctx context.Context, containerID string, cmd []string, tty bool) (string, error)
	ContainerExecAttach(ctx context.Context, execID string) (io.ReadWriteCloser, error)
	ContainerExecResize(ctx context.Context, execID string, height, width uint) error
}

var randRead = rand.Read

// generateID returns a short random hex string for session IDs.
func generateID() string {
	b := make([]byte, 4)
	_, _ = randRead(b)
	return hex.EncodeToString(b)
}

// Session represents a single interactive terminal session backed by a
// Docker exec instance with a PTY.
type Session struct {
	id          string
	containerID string
	execID      string
	conn        io.ReadWriteCloser
	buf         *RingBuffer
	logger      *slog.Logger
	mu          sync.Mutex
	clients     map[chan []byte]struct{}
	done        chan struct{}
	closeOnce   sync.Once
	idleTimeout time.Duration
}

// ID returns the session identifier.
func (s *Session) ID() string { return s.id }

// ContainerID returns the container this session runs in.
func (s *Session) ContainerID() string { return s.containerID }

// Attach registers a new client channel that receives a copy of all
// subsequent output. The caller receives the current ring buffer
// contents followed by a live stream. Close the returned channel by
// calling Detach.
func (s *Session) Attach() (<-chan []byte, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	history := s.buf.Bytes()
	ch := make(chan []byte, clientChannelBuffer)
	s.clients[ch] = struct{}{}
	return ch, history
}

// ErrClientNotFound is returned when a detach is attempted with an
// unrecognized client channel.
var ErrClientNotFound = errors.New("client not found")

// Detach removes a previously attached client channel.
func (s *Session) Detach(ch <-chan []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find the matching send channel.
	for c := range s.clients {
		if c == ch {
			delete(s.clients, c)
			close(c)
			return nil
		}
	}
	return ErrClientNotFound
}

// Done returns a channel that is closed when the session ends.
func (s *Session) Done() <-chan struct{} { return s.done }

// readResult holds the result of a single conn.Read call.
type readResult struct {
	data []byte
	err  error
}

// readLoop reads from the exec connection, writes to the ring buffer,
// and fans out to all attached clients. If an idle timeout is set, the
// session closes when no output is received within the timeout period.
func (s *Session) readLoop() {
	defer s.closeOnce.Do(func() { close(s.done) })

	ch := make(chan readResult, 1)
	go func() {
		tmp := make([]byte, readBufSize)
		for {
			n, err := s.conn.Read(tmp)
			if n > 0 {
				data := make([]byte, n)
				copy(data, tmp[:n])
				ch <- readResult{data: data}
			}
			if err != nil {
				ch <- readResult{err: err}
				return
			}
		}
	}()

	var timer *time.Timer
	var timerC <-chan time.Time
	if s.idleTimeout > 0 {
		timer = time.NewTimer(s.idleTimeout)
		timerC = timer.C
		defer timer.Stop()
	}

	for {
		select {
		case res := <-ch:
			if res.data != nil {
				_, _ = s.buf.Write(res.data)

				s.mu.Lock()
				for c := range s.clients {
					select {
					case c <- res.data:
					default:
						s.logger.Warn("slow consumer, dropped output", "session_id", s.id, "bytes", len(res.data))
					}
				}
				s.mu.Unlock()

				if timer != nil {
					timer.Stop()
					timer = time.NewTimer(s.idleTimeout)
					timerC = timer.C
				}
			}
			if res.err != nil {
				return
			}
		case <-timerC:
			s.logger.Info("terminal session idle timeout", "session_id", s.id, "timeout", s.idleTimeout)
			s.conn.Close()
			return
		}
	}
}

// Manager manages terminal sessions.
type Manager struct {
	mu          sync.Mutex
	sessions    map[string]*Session
	client      ExecClient
	logger      *slog.Logger
	ringBufSize int
	idleTimeout time.Duration
}

// NewManager creates a new terminal session manager.
func NewManager(client ExecClient, logger *slog.Logger) *Manager {
	return &Manager{
		sessions:    make(map[string]*Session),
		client:      client,
		logger:      logger,
		ringBufSize: defaultRingBufSize,
	}
}

// SetRingBufSize sets the ring buffer size for new sessions.
func (m *Manager) SetRingBufSize(size int) {
	m.ringBufSize = size
}

// SetIdleTimeout sets the idle timeout for new sessions. Sessions that
// receive no output within this duration are automatically closed.
// A zero value disables the timeout.
func (m *Manager) SetIdleTimeout(d time.Duration) {
	m.idleTimeout = d
}

// CreateSession starts a new interactive terminal session by creating a
// Docker exec with a PTY, attaching to it, and starting the read loop.
func (m *Manager) CreateSession(ctx context.Context, containerID string, cmd []string) (*Session, error) {
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}

	execID, err := m.client.ContainerExecCreate(ctx, containerID, cmd, true)
	if err != nil {
		return nil, fmt.Errorf("creating exec: %w", err)
	}

	conn, err := m.client.ContainerExecAttach(ctx, execID)
	if err != nil {
		return nil, fmt.Errorf("attaching exec: %w", err)
	}

	s := &Session{
		id:          generateID(),
		containerID: containerID,
		execID:      execID,
		conn:        conn,
		buf:         NewRingBuffer(m.ringBufSize),
		logger:      m.logger,
		clients:     make(map[chan []byte]struct{}),
		done:        make(chan struct{}),
		idleTimeout: m.idleTimeout,
	}

	go s.readLoop()

	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()

	return s, nil
}

// ErrSessionNotFound is returned when a session ID is not recognized.
var ErrSessionNotFound = errors.New("session not found")

// GetSession returns the session with the given ID.
func (m *Manager) GetSession(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return s, nil
}

// SendInput writes raw bytes to the session's PTY stdin.
func (m *Manager) SendInput(id string, data []byte) error {
	s, err := m.GetSession(id)
	if err != nil {
		return err
	}
	_, err = s.conn.Write(data)
	if err != nil {
		return fmt.Errorf("writing input: %w", err)
	}
	return nil
}

// Resize changes the PTY dimensions of a session.
func (m *Manager) Resize(ctx context.Context, id string, rows, cols uint) error {
	s, err := m.GetSession(id)
	if err != nil {
		return err
	}
	if err := m.client.ContainerExecResize(ctx, s.execID, rows, cols); err != nil {
		return fmt.Errorf("resizing exec: %w", err)
	}
	return nil
}

// StopSession closes the exec connection and removes the session.
// It returns the container ID the session was running in.
func (m *Manager) StopSession(id string) (string, error) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return "", ErrSessionNotFound
	}
	delete(m.sessions, id)
	m.mu.Unlock()

	err := s.conn.Close()

	// Close all client channels.
	s.mu.Lock()
	for ch := range s.clients {
		delete(s.clients, ch)
		close(ch)
	}
	s.mu.Unlock()

	return s.containerID, err
}

// ListSessions returns all active session IDs.
func (m *Manager) ListSessions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}
