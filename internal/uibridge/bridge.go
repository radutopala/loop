// Package uibridge lets the API drive the desktop app's windows. Each window
// connects, reports its UI state as it changes, and runs the commands sent to
// it, answering with one result per step.
package uibridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Errors Run returns before a command reaches a window.
var (
	ErrNoClient      = errors.New("no app window is connected")
	ErrUnknownClient = errors.New("no app window with that client id is connected")
	ErrDisconnected  = errors.New("the app window disconnected before it answered")
)

// Message types on a window's connection.
const (
	MsgHello   = "hello"   // window → daemon: {client_id}
	MsgState   = "state"   // window → daemon: {state}
	MsgResult  = "result"  // window → daemon: {id, results, error}
	MsgCommand = "command" // daemon → window: {id, steps, timeout_ms}
)

// Message is every message on a window's connection; each type uses some of
// its fields.
type Message struct {
	Type     string          `json:"type"`
	ClientID string          `json:"client_id,omitempty"`
	ID       string          `json:"id,omitempty"`
	Steps    json.RawMessage `json:"steps,omitempty"`
	State    json.RawMessage `json:"state,omitempty"`
	Results  json.RawMessage `json:"results,omitempty"`
	Error    string          `json:"error,omitempty"`
	// TimeoutMS is how long the window has to answer a command, so it
	// stops what it's waiting for when its caller does.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

// Client is a connected window as callers see it.
type Client struct {
	ClientID    string          `json:"client_id"`
	Focused     bool            `json:"focused"`
	ConnectedAt time.Time       `json:"connected_at"`
	FocusedAt   time.Time       `json:"focused_at,omitzero"`
	State       json.RawMessage `json:"state,omitempty"`
}

// Reply is a window's answer to a command.
type Reply struct {
	ClientID string          `json:"client_id"`
	Results  json.RawMessage `json:"results"`
	Error    string          `json:"error,omitempty"`
}

// Snapshot is the windows' state at a version; the version moves on every
// change, so a caller can wait for the next one.
type Snapshot struct {
	Version uint64   `json:"version"`
	Clients []Client `json:"clients"`
}

type client struct {
	Client
	send func([]byte) error
	// conn tells this connection apart from a newer one with the same
	// client id (a reloaded window), so the old one's detach is a no-op.
	conn uint64
}

// Bridge holds the connected windows.
type Bridge struct {
	mu      sync.Mutex
	clients map[string]*client
	pending map[string]pendingCommand
	version uint64
	changed chan struct{} // closed and replaced on every change
	conns   uint64
	ids     uint64
	now     func() time.Time
}

type pendingCommand struct {
	clientID string
	reply    chan Reply
}

// New returns an empty Bridge.
func New() *Bridge {
	return newBridge(time.Now)
}

func newBridge(now func() time.Time) *Bridge {
	return &Bridge{
		clients: map[string]*client{},
		pending: map[string]pendingCommand{},
		changed: make(chan struct{}),
		now:     now,
	}
}

// Attach adds the window clientID, reached through send, replacing an
// earlier connection with the same id. The returned detach removes it and
// fails the commands it hasn't answered.
func (b *Bridge) Attach(clientID string, send func([]byte) error) (detach func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.conns++
	conn := b.conns
	c := &client{Client: Client{ClientID: clientID, ConnectedAt: b.now()}, send: send, conn: conn}
	if old, ok := b.clients[clientID]; ok {
		c.State, c.Focused, c.FocusedAt = old.State, old.Focused, old.FocusedAt
	}
	b.clients[clientID] = c
	b.bumpLocked()
	return func() { b.detach(clientID, conn) }
}

func (b *Bridge) detach(clientID string, conn uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.clients[clientID]
	if !ok || c.conn != conn {
		return
	}
	delete(b.clients, clientID)
	for id, p := range b.pending {
		if p.clientID == clientID {
			delete(b.pending, id)
			p.reply <- Reply{ClientID: clientID, Error: ErrDisconnected.Error()}
		}
	}
	b.bumpLocked()
}

// Handle takes a message the window clientID sent: its state, or a result.
func (b *Bridge) Handle(clientID string, msg Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch msg.Type {
	case MsgState:
		c, ok := b.clients[clientID]
		if !ok {
			return
		}
		var s struct {
			Focused bool `json:"focused"`
		}
		_ = json.Unmarshal(msg.State, &s) // a state without focus reads as unfocused
		if s.Focused && !c.Focused {
			c.FocusedAt = b.now()
		}
		c.Focused = s.Focused
		c.State = msg.State
		b.bumpLocked()
	case MsgResult:
		p, ok := b.pending[msg.ID]
		if !ok || p.clientID != clientID {
			return
		}
		delete(b.pending, msg.ID)
		p.reply <- Reply{ClientID: clientID, Results: msg.Results, Error: msg.Error}
	}
}

// Run sends steps to the window clientID, or to the focused window when
// clientID is "", and waits for its answer until ctx is done. check sees the
// window the steps would go to, as it is then, and refuses them with an
// error.
func (b *Bridge) Run(ctx context.Context, clientID string, steps json.RawMessage, check func(Client) error) (Reply, error) {
	b.mu.Lock()
	c, err := b.targetLocked(clientID)
	if err == nil {
		err = check(c.Client)
	}
	if err != nil {
		b.mu.Unlock()
		return Reply{}, err
	}
	b.ids++
	id := fmt.Sprintf("cmd-%d", b.ids)
	reply := make(chan Reply, 1)
	b.pending[id] = pendingCommand{clientID: c.ClientID, reply: reply}
	send := c.send
	b.mu.Unlock()

	cmd := Message{Type: MsgCommand, ID: id, Steps: steps}
	if deadline, ok := ctx.Deadline(); ok {
		cmd.TimeoutMS = max(time.Until(deadline).Milliseconds(), 1)
	}
	data, _ := json.Marshal(cmd) // a struct of strings and raw JSON always marshals
	if err := send(data); err != nil {
		b.drop(id)
		return Reply{}, fmt.Errorf("sending the command: %w", err)
	}
	select {
	case r := <-reply:
		if r.Error == ErrDisconnected.Error() {
			return Reply{}, ErrDisconnected
		}
		return r, nil
	case <-ctx.Done():
		b.drop(id)
		return Reply{}, ctx.Err()
	}
}

func (b *Bridge) drop(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}

// targetLocked picks the window a command goes to: the one clientID names,
// else the focused one, else the one focused last, else the one connected
// last.
func (b *Bridge) targetLocked(clientID string) (*client, error) {
	if clientID != "" {
		c, ok := b.clients[clientID]
		if !ok {
			return nil, ErrUnknownClient
		}
		return c, nil
	}
	clients := b.sortedLocked()
	if len(clients) == 0 {
		return nil, ErrNoClient
	}
	return clients[0], nil
}

// sortedLocked orders the windows the way targetLocked prefers them.
func (b *Bridge) sortedLocked() []*client {
	clients := make([]*client, 0, len(b.clients))
	for _, c := range b.clients {
		clients = append(clients, c)
	}
	sort.Slice(clients, func(i, j int) bool {
		a, c := clients[i], clients[j]
		if a.Focused != c.Focused {
			return a.Focused
		}
		if !a.FocusedAt.Equal(c.FocusedAt) {
			return a.FocusedAt.After(c.FocusedAt)
		}
		if !a.ConnectedAt.Equal(c.ConnectedAt) {
			return a.ConnectedAt.After(c.ConnectedAt)
		}
		return a.ClientID < c.ClientID
	})
	return clients
}

// Snapshot returns the windows, the one a command would go to first.
func (b *Bridge) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

func (b *Bridge) snapshotLocked() Snapshot {
	s := Snapshot{Version: b.version, Clients: []Client{}}
	for _, c := range b.sortedLocked() {
		s.Clients = append(s.Clients, c.Client)
	}
	return s
}

// Wait returns the snapshot once its version is past after, or the current
// one when ctx is done first.
func (b *Bridge) Wait(ctx context.Context, after uint64) Snapshot {
	for {
		b.mu.Lock()
		if b.version > after {
			s := b.snapshotLocked()
			b.mu.Unlock()
			return s
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return b.Snapshot()
		}
	}
}

func (b *Bridge) bumpLocked() {
	b.version++
	close(b.changed)
	b.changed = make(chan struct{})
}
