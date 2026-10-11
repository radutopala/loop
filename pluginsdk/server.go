package pluginsdk

import (
	"cmp"
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// APIVersion is the Plugin API major the SDK speaks.
const APIVersion = "1"

// Trigger is what started an action.
type Trigger string

const (
	TriggerManual   Trigger = "manual"
	TriggerSchedule Trigger = "schedule"
	TriggerWorkflow Trigger = "workflow"
)

// ActionRequest is the body of POST /actions/{action}.
type ActionRequest struct {
	Trigger Trigger `json:"trigger"`
	// TaskID is the scheduled task that ran the action.
	TaskID int64 `json:"task_id,omitempty"`
}

// ToolRequest is the body of POST /tools/{tool}: a call from Agent, in the
// run RunID in the hidden thread ThreadID. A workflow's call has no Agent
// or ThreadID, and a RunID of the form workflow:<run id>:<node id>. Loop
// has checked Input against the tool's input_schema.
type ToolRequest struct {
	RunID    string          `json:"run_id"`
	Agent    string          `json:"agent"`
	ThreadID string          `json:"thread_id"`
	Input    json.RawMessage `json:"input"`
}

// ToolResult is the answer to a tool call, which the agent sees as the
// tool's result. IsError lets it retry.
type ToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Run statuses Loop sends to POST /runs/{run_id}/done.
const (
	RunStatusDone   = "done"
	RunStatusFailed = "failed"
)

// RunDoneRequest is the body of POST /runs/{run_id}/done: how an agent run
// the plugin started ended.
type RunDoneRequest struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Event is the body of POST /events: a Loop event the plugin subscribes
// to, in ChannelID or, when ThreadID is set, one of its threads. Data is
// the event's data as the app's events WebSocket carries it.
type Event struct {
	Type      string          `json:"type"`
	ChannelID string          `json:"channel_id"`
	ThreadID  string          `json:"thread_id,omitempty"`
	Data      json.RawMessage `json:"data"`
	// Timestamp is when Loop sent it, in Unix milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// Health is the answer to GET /healthz.
type Health struct {
	API     string `json:"api"`
	Version string `json:"version"`
}

// ActionFunc does an action's work, after Loop has had its 202.
type ActionFunc func(ctx context.Context, req ActionRequest) error

// ToolFunc answers a tool call. An error becomes a result with IsError set.
type ToolFunc func(ctx context.Context, req ToolRequest) (ToolResult, error)

// RunDoneFunc learns that an agent run the plugin started ended.
type RunDoneFunc func(ctx context.Context, runID string, req RunDoneRequest) error

// EventFunc takes a Loop event the plugin subscribes to. Loop waits for it
// for up to 10 s; hand long work to Server.Go.
type EventFunc func(ctx context.Context, evt Event) error

const (
	// MaxBody caps the body of an action, tool, run or event call.
	MaxBody = 1 << 20
	// MaxXBody caps the body of a call to /x, as Loop does.
	MaxXBody = 8 << 20
	// DefaultWorkTimeout caps background work when WorkTimeout is zero.
	DefaultWorkTimeout = 2 * time.Minute
)

// Server serves the Loop → plugin contract: /healthz, /actions, /tools,
// /runs/{run_id}/done, /events and the plugin's own API under /x, behind the secret
// Loop hands the container.
type Server struct {
	// Secret is the contents of LOOP_PLUGIN_SECRET_FILE. A Server without
	// one refuses every request.
	Secret string
	// Version is the plugin's version, as /healthz reports it.
	Version string
	// Actions are the manifest's actions by id.
	Actions map[string]ActionFunc
	// Tools are the manifest's tools by name.
	Tools map[string]ToolFunc
	// RunDone, when set, learns when the plugin's agent runs end.
	RunDone RunDoneFunc
	// Events, when set, takes the Loop events the manifest subscribes to.
	Events EventFunc
	// X serves the plugin's API for its UI, with the /x prefix stripped.
	X http.Handler
	// Logger gets failures; nil discards them.
	Logger *slog.Logger
	// WorkTimeout caps each action's and Go's work; zero is
	// DefaultWorkTimeout.
	WorkTimeout time.Duration

	init   sync.Once
	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (s *Server) setup() {
	s.init.Do(func() {
		s.base, s.cancel = context.WithCancel(context.Background())
		if s.Logger == nil {
			s.Logger = slog.New(slog.DiscardHandler)
		}
	})
}

// Go runs fn in the background, for work that outlives a request, such as
// starting an agent run after answering. Its error is logged under name.
func (s *Server) Go(name string, fn func(ctx context.Context) error) {
	s.setup()
	s.wg.Go(func() {
		ctx, cancel := context.WithTimeout(s.base, cmp.Or(s.WorkTimeout, DefaultWorkTimeout))
		defer cancel()
		if err := fn(ctx); err != nil {
			s.Logger.Error(name, "err", err)
		}
	})
}

// Wait waits for background work to end.
func (s *Server) Wait() { s.wg.Wait() }

// Close cancels background work and waits for it.
func (s *Server) Close() {
	s.setup()
	s.cancel()
	s.wg.Wait()
}

// Handler returns the routes behind the secret check.
func (s *Server) Handler() http.Handler {
	s.setup()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /actions/{action}", s.action)
	mux.HandleFunc("POST /tools/{tool}", s.tool)
	mux.HandleFunc("POST /runs/{run_id}/done", s.runDone)
	mux.HandleFunc("POST /events", s.event)
	if s.X != nil {
		mux.Handle("/x/", http.StripPrefix("/x", http.MaxBytesHandler(s.X, MaxXBody)))
	}
	want := []byte("Bearer " + s.Secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Secret == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// WriteJSON answers with v as JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError answers {"error": msg}.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// DecodeJSON reads r's JSON body into v, answering 400 when it can't.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, Health{API: APIVersion, Version: s.Version})
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("action")
	fn, ok := s.Actions[id]
	if !ok {
		WriteError(w, http.StatusNotFound, "unknown action")
		return
	}
	var req ActionRequest
	// The body is optional; a missing or odd one counts as a manual trigger.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBody)).Decode(&req)
	if req.Trigger == "" {
		req.Trigger = TriggerManual
	}
	w.WriteHeader(http.StatusAccepted)
	s.Go("action "+id, func(ctx context.Context) error { return fn(ctx, req) })
}

func (s *Server) tool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("tool")
	fn, ok := s.Tools[name]
	if !ok {
		WriteJSON(w, http.StatusNotFound, ToolResult{Content: "unknown tool", IsError: true})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	var req ToolRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	res, err := fn(r.Context(), req)
	if err != nil {
		s.Logger.Error("tool "+name, "err", err)
		res = ToolResult{Content: err.Error(), IsError: true}
	}
	WriteJSON(w, http.StatusOK, res)
}

func (s *Server) runDone(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	var req RunDoneRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if s.RunDone != nil {
		if err := s.RunDone(r.Context(), r.PathValue("run_id"), req); err != nil {
			s.Logger.Error("run done", "run_id", r.PathValue("run_id"), "err", err)
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) event(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	var evt Event
	if !DecodeJSON(w, r, &evt) {
		return
	}
	if s.Events != nil {
		if err := s.Events(r.Context(), evt); err != nil {
			s.Logger.Error("event", "type", evt.Type, "err", err)
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
