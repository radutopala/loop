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

// Default ring buffer size: 1 MB.
const defaultRingBufSize = 1024 * 1024

// clientChannelBuffer is the capacity of per-client public output channels.
// The pump keeps a separate unbounded slice queue behind this; the buffered
// channel only smooths handoff between the drain goroutine and the consumer.
const clientChannelBuffer = 64

// defaultClientMaxBytes caps the per-client queued backlog. Above this size
// the pump evicts oldest entries to keep memory bounded. 16 MB easily
// absorbs the burst from a fast test runner or build command without
// dropping output.
const defaultClientMaxBytes = 16 * 1024 * 1024

// readBufSize is the size of the temporary buffer used in readLoop.
const readBufSize = 4096

// PidFileShellCmd is an optional interface that ExecClient implementations
// can implement to provide a PID-file-wrapped default shell command.
// Container-based clients need this for process group cleanup; host-based
// clients manage processes directly and should not implement it.
type PidFileShellCmd interface {
	DefaultShellCmd(pidFile string) []string
}

// ExecClient abstracts the Docker exec operations needed by the terminal
// manager (docker exec), making it testable without a real Docker daemon.
//
// This is distinct from container.DockerClient, which handles container
// lifecycle (docker create/start/stop/rm). ExecClient runs commands inside
// already-running containers for interactive PTY sessions.
type ExecClient interface {
	ExecCreate(ctx context.Context, targetID string, cmd []string, tty bool) (string, error)
	ExecAttach(ctx context.Context, execID string) (io.ReadWriteCloser, error)
	ExecResize(ctx context.Context, execID string, height, width uint) error
	// ExecInspectPid returns the PID of a running exec process (inside the container).
	ExecInspectPid(ctx context.Context, execID string) (int, error)
}

// EnvExecClient is an optional interface ExecClient implementations may
// satisfy to attach environment variables to a newly created exec. The
// terminal Manager type-asserts on this when callers route through
// CreateSessionWithEnv. Containers use it to stamp LOOP_TERMINAL_LEAF on
// terminal-originated execs so the in-container dockerproxy can attribute
// approval prompts back to the specific terminal pane (vs. the chat agent).
type EnvExecClient interface {
	ExecCreateWithEnv(ctx context.Context, targetID string, cmd, env []string, tty bool) (string, error)
}

// generateID returns a short random hex string for session IDs.
// The randRead parameter is the source of randomness (typically crypto/rand.Read).
func generateID(randRead func([]byte) (int, error)) string {
	b := make([]byte, 4)
	_, _ = randRead(b)
	return hex.EncodeToString(b)
}

// Session represents a single interactive terminal session backed by a
// Docker exec instance with a PTY.
type Session struct {
	id             string
	containerID    string
	execID         string
	pidFile        string // path inside the container where the shell's PID is stored
	conn           io.ReadWriteCloser
	buf            *RingBuffer
	logger         *slog.Logger
	mu             sync.Mutex
	clients        map[*outputPump]struct{}
	done           chan struct{}
	closeOnce      sync.Once
	idleTimeout    time.Duration
	clientMaxBytes int
}

// ID returns the session identifier.
func (s *Session) ID() string { return s.id }

// ContainerID returns the container this session runs in.
func (s *Session) ContainerID() string { return s.containerID }

// Attach registers a new client and returns the channel it should read from
// along with a snapshot of the ring buffer (history replay). Each client
// gets its own [outputPump] so a slow consumer cannot stall others or drop
// bytes inside the read loop. Release the channel with Detach.
func (s *Session) Attach() (<-chan []byte, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	history := s.buf.Bytes()
	p := newOutputPump(s.logger, s.id, s.clientMaxBytes)
	s.clients[p] = struct{}{}
	return p.out, history
}

// ErrClientNotFound is returned when a detach is attempted with an
// unrecognized client channel.
var ErrClientNotFound = errors.New("client not found")

// Detach removes a previously attached client and shuts down its pump. The
// pump's drain goroutine then closes the public channel so the reader
// observes the detach as a normal channel close.
func (s *Session) Detach(ch <-chan []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for p := range s.clients {
		if (<-chan []byte)(p.out) == ch {
			delete(s.clients, p)
			p.close()
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
				for p := range s.clients {
					p.push(res.data)
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
	mu             sync.Mutex
	sessions       map[string]*Session
	client         ExecClient
	logger         *slog.Logger
	ringBufSize    int
	idleTimeout    time.Duration
	clientMaxBytes int
	randRead       func([]byte) (int, error)
}

// NewManager creates a new terminal session manager.
func NewManager(client ExecClient, logger *slog.Logger) *Manager {
	return &Manager{
		sessions:       make(map[string]*Session),
		client:         client,
		logger:         logger,
		ringBufSize:    defaultRingBufSize,
		clientMaxBytes: defaultClientMaxBytes,
		randRead:       rand.Read,
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

// SetClientMaxBytes sets the per-client backlog cap for new sessions. When
// a client's queued backlog exceeds this size, the pump evicts oldest
// entries and logs a "slow consumer" warning. Exposed for tests that need
// a small cap to exercise eviction; production code uses the default.
func (m *Manager) SetClientMaxBytes(n int) {
	m.clientMaxBytes = n
}

// pidFileDir is where session PID files are written inside containers.
const pidFileDir = "/tmp"

// CreateSession starts a new interactive terminal session by creating a
// Docker exec with a PTY, attaching to it, and starting the read loop.
// If cmd is empty, starts a shell that writes its PID to a temp file
// for reliable process group cleanup.
func (m *Manager) CreateSession(ctx context.Context, containerID string, cmd []string) (*Session, error) {
	return m.CreateSessionWithEnv(ctx, containerID, cmd, nil)
}

// CreateSessionWithEnv is CreateSession with an env-var list attached to
// the underlying exec. env is propagated only when the ExecClient
// implements [EnvExecClient]; otherwise it is ignored and the call
// degrades to a plain ExecCreate. Callers requiring env propagation
// (e.g. terminal panes stamping LOOP_TERMINAL_LEAF for approval-source
// attribution) should rely on this contract.
func (m *Manager) CreateSessionWithEnv(ctx context.Context, containerID string, cmd, env []string) (*Session, error) {
	sessionID := generateID(m.randRead)
	pidFile := fmt.Sprintf("%s/.loop-exec-%s.pid", pidFileDir, sessionID)

	// When no explicit command and the client supports PID-file tracking
	// (container-based), wrap the shell to write its PID for later kill.
	// Host-based clients handle shell selection and process cleanup directly.
	if len(cmd) == 0 {
		if p, ok := m.client.(PidFileShellCmd); ok {
			cmd = p.DefaultShellCmd(pidFile)
		} else {
			pidFile = "" // host client — no PID-file tracking needed
		}
	}

	var (
		execID string
		err    error
	)
	if envClient, ok := m.client.(EnvExecClient); ok && len(env) > 0 {
		execID, err = envClient.ExecCreateWithEnv(ctx, containerID, cmd, env, true)
	} else {
		execID, err = m.client.ExecCreate(ctx, containerID, cmd, true)
	}
	if err != nil {
		return nil, fmt.Errorf("creating exec: %w", err)
	}

	conn, err := m.client.ExecAttach(ctx, execID)
	if err != nil {
		return nil, fmt.Errorf("attaching exec: %w", err)
	}

	s := &Session{
		id:             sessionID,
		containerID:    containerID,
		execID:         execID,
		pidFile:        pidFile,
		conn:           conn,
		buf:            NewRingBuffer(m.ringBufSize),
		logger:         m.logger,
		clients:        make(map[*outputPump]struct{}),
		done:           make(chan struct{}),
		idleTimeout:    m.idleTimeout,
		clientMaxBytes: m.clientMaxBytes,
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
	if err := m.client.ExecResize(ctx, s.execID, rows, cols); err != nil {
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

	// Shut down all client pumps. Each pump's drain goroutine closes its
	// public channel on exit, so consumers observe the stop as a normal
	// channel close.
	s.mu.Lock()
	for p := range s.clients {
		delete(s.clients, p)
		p.close()
	}
	s.mu.Unlock()

	return s.containerID, err
}

// KillProcessGroup reads the shell's PID from its PID file (written at exec
// startup) and runs `kill -9 -<pid>` (negative = process group) inside the
// container. This reliably kills the shell and all its children (e.g. Claude).
func (m *Manager) KillProcessGroup(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	if s.pidFile == "" {
		return nil // no PID file — session was created with explicit cmd
	}

	// Read PID file and kill the process group in one command.
	killCmd := fmt.Sprintf("kill -9 -$(cat %s) 2>/dev/null; rm -f %s", s.pidFile, s.pidFile)
	killExecID, err := m.client.ExecCreate(ctx, s.containerID, []string{"/bin/sh", "-c", killCmd}, false)
	if err != nil {
		return fmt.Errorf("creating kill exec: %w", err)
	}
	conn, err := m.client.ExecAttach(ctx, killExecID)
	if err != nil {
		return fmt.Errorf("attaching kill exec: %w", err)
	}
	conn.Close()
	return nil
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
