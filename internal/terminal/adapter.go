// Package terminal — adapter.go provides ManagerAdapter which bridges
// terminal.Manager to the api.TerminalManager interface.
package terminal

import (
	"context"
)

// ManagerAdapter wraps a terminal.Manager to satisfy the
// api.TerminalManager interface expected by the WebSocket handler.
type ManagerAdapter struct {
	mgr *Manager
}

// NewManagerAdapter creates a new adapter around the given Manager.
func NewManagerAdapter(mgr *Manager) *ManagerAdapter {
	return &ManagerAdapter{mgr: mgr}
}

// CreateSession creates a PTY session and immediately attaches a client
// channel, returning the decomposed session fields.
func (a *ManagerAdapter) CreateSession(ctx context.Context, containerID string, cmd []string) (string, <-chan []byte, []byte, <-chan struct{}, error) {
	s, err := a.mgr.CreateSession(ctx, containerID, cmd)
	if err != nil {
		return "", nil, nil, nil, err
	}
	output, history := s.Attach()
	return s.ID(), output, history, s.Done(), nil
}

// AttachSession re-attaches to an existing session.
func (a *ManagerAdapter) AttachSession(sessionID string) (<-chan []byte, []byte, <-chan struct{}, error) {
	s, err := a.mgr.GetSession(sessionID)
	if err != nil {
		return nil, nil, nil, err
	}
	output, history := s.Attach()
	return output, history, s.Done(), nil
}

// DetachSession detaches a client channel from a session.
func (a *ManagerAdapter) DetachSession(sessionID string, output <-chan []byte) error {
	s, err := a.mgr.GetSession(sessionID)
	if err != nil {
		return err
	}
	return s.Detach(output)
}

// SendInput writes raw bytes to the session's PTY stdin.
func (a *ManagerAdapter) SendInput(sessionID string, data []byte) error {
	return a.mgr.SendInput(sessionID, data)
}

// Resize changes the PTY dimensions of a session.
func (a *ManagerAdapter) Resize(ctx context.Context, sessionID string, rows, cols uint) error {
	return a.mgr.Resize(ctx, sessionID, rows, cols)
}

// StopSession closes the exec connection and removes the session.
// Returns the container ID the session was running in.
func (a *ManagerAdapter) StopSession(sessionID string) (string, error) {
	return a.mgr.StopSession(sessionID)
}
