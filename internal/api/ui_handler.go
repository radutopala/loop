package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
// terminals, which run in its container, and the chats, where send_input
// queues a message. A host shell runs on the host, so typing into one would
// run commands there, and reading one could show what the host has.
var uiInputPanels = []string{"docker-agent", "docker-shell", "chat", "chat-side"}

// uiSidePane is the side view open beside the chat, which the terminal steps
// may use too: a shell in the chat's own container, the Learn view's learn
// thread, or the Explain view's write-ups. None of them runs on the host.
const uiSidePane = "side"

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
	Panel     string `json:"panel"`
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
	req, steps, ok := parseUICommand(w, r)
	if !ok {
		return
	}
	asked := slices.Clone(steps)
	s.uiSideChatsToParents(r.Context(), &req, steps)
	if msg := s.uiAgentRefusal(r, asked, steps); msg != "" {
		http.Error(w, msg, http.StatusForbidden)
		return
	}
	s.runUICommand(w, r, req, steps)
}

// parseUICommand reads a command's body and the part of its steps the
// daemon checks. Otherwise it answers the request and returns false.
func parseUICommand(w http.ResponseWriter, r *http.Request) (uiCommandRequest, []uiStep, bool) {
	var req uiCommandRequest
	if !decodeJSON(w, r, &req) {
		return req, nil, false
	}
	var steps []uiStep
	if err := json.Unmarshal(req.Steps, &steps); err != nil || len(steps) == 0 {
		http.Error(w, "steps must be a non-empty array of objects", http.StatusBadRequest)
		return req, nil, false
	}
	for _, step := range steps {
		if step.Op == "" {
			http.Error(w, "every step needs an op", http.StatusBadRequest)
			return req, nil, false
		}
	}
	if msg := uiStepKeysRespelled(req.Steps); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return req, nil, false
	}
	return req, steps, true
}

// uiStepKeys are the keys of a step the daemon reads, uiStep's.
var uiStepKeys = []string{"op", "pane", "panel", "channel_id"}

// uiStepKeysRespelled returns why steps spell a key the daemon reads some
// other way, or "" when they don't. encoding/json matches a key to uiStep
// case-insensitively, keeping the last match, but the window reads the key
// as written: {"channel_id":"other","Channel_ID":"own"} would have the daemon
// check one channel and the window open the other.
func uiStepKeysRespelled(steps json.RawMessage) string {
	var raw []map[string]json.RawMessage
	_ = json.Unmarshal(steps, &raw) // parseUICommand read them as objects
	for _, step := range raw {
		for k := range step {
			for _, key := range uiStepKeys {
				if k != key && strings.EqualFold(k, key) {
					return fmt.Sprintf("step key %q must be written %q", k, key)
				}
			}
		}
	}
	return ""
}

// uiSideChatsToParents has a select_channel to a side chat open the side
// chat's parent instead: the window lists no side chats, and shows a side
// chat's pane in its parent's view. A side chat's agent's ui_run starts
// with one. A channel it can't look up stays as it is.
func (s *Server) uiSideChatsToParents(ctx context.Context, req *uiCommandRequest, steps []uiStep) {
	var raw []map[string]json.RawMessage
	_ = json.Unmarshal(req.Steps, &raw) // parseUICommand read them as objects
	changed := false
	for i, step := range steps {
		if step.Op != "select_channel" || step.ChannelID == "" {
			continue
		}
		group, _, err := s.agentGroup(ctx, step.ChannelID)
		if err != nil || group == step.ChannelID {
			continue
		}
		steps[i].ChannelID = group
		raw[i]["channel_id"], _ = json.Marshal(group)
		changed = true
	}
	if changed {
		req.Steps, _ = json.Marshal(raw)
	}
}

// runUICommand sends a parsed command to its window and answers with the
// window's results.
func (s *Server) runUICommand(w http.ResponseWriter, r *http.Request, req uiCommandRequest, steps []uiStep) {
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
// terminal or a chat. Its pane is a panel type, which the window resolves to
// its first pane of that type, the id of a pane the window c reports, or
// "$n": the pane step n before it adds, an agent terminal or a chat. The
// window checks the pane again when it runs the step.
func checkUIInput(steps []uiStep, c uibridge.Client) error {
	var state struct {
		Panes []uiPane `json:"panes"`
	}
	_ = json.Unmarshal(c.State, &state) // a window with no state has no panes
	for at, step := range steps {
		if !slices.Contains(uiTerminalOps, step.Op) || slices.Contains(uiInputPanels, step.Pane) || step.Pane == uiSidePane {
			continue
		}
		if n, ok := paneRef(step.Pane); ok {
			if n > at || !uiMakesInputPane(steps[n-1]) {
				return fmt.Errorf("%w: %s's pane %s isn't a docker-agent, docker-shell, chat or chat-side pane or the side view a step before it opens", errUIStep, step.Op, step.Pane)
			}
			continue
		}
		i := slices.IndexFunc(state.Panes, func(p uiPane) bool { return p.ID == step.Pane })
		if i < 0 || !slices.Contains(uiInputPanels, state.Panes[i].Panel) {
			return fmt.Errorf("%w: %s only works on docker-agent, docker-shell, chat and chat-side panes and the side view, not %q", errUIStep, step.Op, step.Pane)
		}
	}
	return nil
}

// uiPaneOps are the steps that make a pane, which a later step can name as
// "$n".
var uiPaneOps = []string{"add_pane", "replace_pane"}

// uiMakesInputPane reports whether step makes a pane the terminal steps may
// use: an agent terminal or a chat, or the side view open_side opens.
func uiMakesInputPane(step uiStep) bool {
	return step.Op == "open_side" || (slices.Contains(uiPaneOps, step.Op) && slices.Contains(uiInputPanels, step.Panel))
}

// paneRef returns n of a pane named "$n", the pane step n (from 1) made.
func paneRef(pane string) (int, bool) {
	ref, ok := strings.CutPrefix(pane, "$")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(ref)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// uiAgentRefusal returns why an agent may not run steps, or "" when it may.
// An agent drives a window only in its own project: its command opens one of
// the project's channels first, and opens no other. steps are judged as they
// will run, side chats moved to their parents; a refusal names the channel
// as asked, so it shows nothing of another project's the agent didn't name.
func (s *Server) uiAgentRefusal(r *http.Request, asked, steps []uiStep) string {
	if !isAgentRequest(r) {
		return ""
	}
	p, _ := apiauth.PrincipalFrom(r.Context())
	if steps[0].Op != "select_channel" {
		return "an agent's command starts with select_channel, to a channel of its project"
	}
	for i, step := range steps {
		switch {
		case step.Op != "select_channel":
		case step.ChannelID == "":
			return "an agent's select_channel needs a channel_id"
		case !s.agentOwnsChannel(r.Context(), p, step.ChannelID):
			return "channel " + asked[i].ChannelID + " is outside this agent's project"
		}
	}
	return ""
}
