package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/uibridge"
)

const (
	// uiCommandTimeout is how long a command waits for the window's answer
	// by default; uiCommandMaxTimeout caps what a caller may ask for.
	uiCommandTimeout    = time.Minute
	uiCommandMaxTimeout = 10 * time.Minute
	// uiStateWait is how long GET /api/ui/state waits for a change past
	// ?after= before it answers with the state as it is.
	uiStateWait = 30 * time.Second
	// uiMaxMessageBytes caps a message from a window; results with
	// terminal output, up to 2000 lines a step, are the largest.
	uiMaxMessageBytes = 8 << 20
)

// uiInputPanels are the panes the terminal steps may use: the agent's
// terminals, which run in its container. A host shell runs on the host, so
// typing into one would run commands there, and reading one could show what
// the host has.
var uiInputPanels = []string{"docker-agent", "docker-shell"}

// uiTerminalOps are the steps that use a terminal pane: they type into it,
// read it or wait on its output.
var uiTerminalOps = []string{"send_input", "read_output", "wait_for"}

// handleUIWS is the connection an app window keeps open: it says hello with
// its client id, then sends its state as it changes and the results of the
// commands it's sent.
func (s *Server) handleUIWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // gorilla/websocket writes the error response
	}
	defer conn.Close()
	conn.SetReadLimit(uiMaxMessageBytes)

	var hello uibridge.Message
	if err := conn.ReadJSON(&hello); err != nil || hello.Type != uibridge.MsgHello || hello.ClientID == "" {
		return
	}
	var writeMu sync.Mutex
	send := func(data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.TextMessage, data)
	}
	defer s.ui.Attach(hello.ClientID, send)()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg uibridge.Message
		if json.Unmarshal(data, &msg) == nil {
			s.ui.Handle(hello.ClientID, msg)
		}
	}
}

// handleGetUIState returns the app windows and their state, the one a
// command goes to by default first. With ?after=<version> it waits for a
// newer version first, for a while.
func (s *Server) handleGetUIState(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query().Get("after")
	if v == "" {
		writeHTTPJSON(w, http.StatusOK, s.uiSnapshotFor(r, s.ui.Snapshot()), s.logger)
		return
	}
	after, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		http.Error(w, "invalid after version", http.StatusBadRequest)
		return
	}
	wait := uiStateWait
	if s.uiStateWaitOverride > 0 {
		wait = s.uiStateWaitOverride
	}
	ctx, cancel := context.WithTimeout(r.Context(), wait)
	defer cancel()
	writeHTTPJSON(w, http.StatusOK, s.uiSnapshotFor(r, s.ui.Wait(ctx, after)), s.logger)
}

// uiSnapshotFor leaves out, for an agent, the state of the windows showing
// a channel outside its project.
func (s *Server) uiSnapshotFor(r *http.Request, snap uibridge.Snapshot) uibridge.Snapshot {
	if !isAgentRequest(r) {
		return snap
	}
	p, _ := apiauth.PrincipalFrom(r.Context())
	for i, c := range snap.Clients {
		var state struct {
			ChannelID string `json:"channel_id"`
		}
		_ = json.Unmarshal(c.State, &state) // a window with no state shows nothing
		if state.ChannelID != "" && !s.agentOwnsChannel(r.Context(), p, state.ChannelID) {
			snap.Clients[i].State = nil
		}
	}
	return snap
}

type uiCommandRequest struct {
	// ClientID picks the window; "" is the focused one.
	ClientID string          `json:"client_id,omitempty"`
	Steps    json.RawMessage `json:"steps"`
	// Timeout is how long to wait for the window's answer, as a Go
	// duration; "" is a minute.
	Timeout string `json:"timeout,omitempty"`
}

// uiStep is the part of a step the daemon reads; the window reads the rest.
type uiStep struct {
	Op        string `json:"op"`
	Pane      string `json:"pane"`
	ChannelID string `json:"channel_id"`
}

// uiPane is a pane in a window's reported state.
type uiPane struct {
	ID    string `json:"id"`
	Panel string `json:"panel"`
}

// errUIStep is a step the daemon refuses.
var errUIStep = errors.New("invalid step")

// handleRunUICommand sends steps to an app window and returns its results.
func (s *Server) handleRunUICommand(w http.ResponseWriter, r *http.Request) {
	var req uiCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var steps []uiStep
	if err := json.Unmarshal(req.Steps, &steps); err != nil || len(steps) == 0 {
		http.Error(w, "steps must be a non-empty array of objects", http.StatusBadRequest)
		return
	}
	for _, step := range steps {
		if step.Op == "" {
			http.Error(w, "every step needs an op", http.StatusBadRequest)
			return
		}
	}
	if msg := s.uiAgentRefusal(r, steps); msg != "" {
		http.Error(w, msg, http.StatusForbidden)
		return
	}
	timeout := uiCommandTimeout
	if req.Timeout != "" {
		d, err := time.ParseDuration(req.Timeout)
		if err != nil || d <= 0 || d > uiCommandMaxTimeout {
			http.Error(w, "timeout must be a positive Go duration of at most 10m", http.StatusBadRequest)
			return
		}
		timeout = d
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	reply, err := s.ui.Run(ctx, req.ClientID, req.Steps, func(c uibridge.Client) error {
		return checkUIInput(steps, c)
	})
	switch {
	case errors.Is(err, errUIStep):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, uibridge.ErrNoClient), errors.Is(err, uibridge.ErrUnknownClient):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "the app window didn't answer in time", http.StatusGatewayTimeout)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
	default:
		writeHTTPJSON(w, http.StatusOK, reply, s.logger)
	}
}

// checkUIInput refuses a terminal step that could use anything but an agent
// terminal. Its pane is a panel type, which the window resolves to
// its first pane of that type, or the id of a pane the window c reports.
// The window checks the pane again when it runs the step, since a pane a
// step before it adds isn't in the reported state yet.
func checkUIInput(steps []uiStep, c uibridge.Client) error {
	var state struct {
		Panes []uiPane `json:"panes"`
	}
	_ = json.Unmarshal(c.State, &state) // a window with no state has no panes
	for _, step := range steps {
		if !slices.Contains(uiTerminalOps, step.Op) || slices.Contains(uiInputPanels, step.Pane) {
			continue
		}
		i := slices.IndexFunc(state.Panes, func(p uiPane) bool { return p.ID == step.Pane })
		if i < 0 || !slices.Contains(uiInputPanels, state.Panes[i].Panel) {
			return fmt.Errorf("%w: %s only works on docker-agent and docker-shell panes, not %q", errUIStep, step.Op, step.Pane)
		}
	}
	return nil
}

// uiAgentRefusal returns why an agent may not run steps, or "" when it may.
// An agent drives a window only in its own project: its command opens one of
// the project's channels first, and opens no other.
func (s *Server) uiAgentRefusal(r *http.Request, steps []uiStep) string {
	if !isAgentRequest(r) {
		return ""
	}
	p, _ := apiauth.PrincipalFrom(r.Context())
	if steps[0].Op != "select_channel" {
		return "an agent's command starts with select_channel, to a channel of its project"
	}
	for _, step := range steps {
		if step.Op == "select_channel" && step.ChannelID != "" && !s.agentOwnsChannel(r.Context(), p, step.ChannelID) {
			return "channel " + step.ChannelID + " is outside this agent's project"
		}
	}
	return ""
}
