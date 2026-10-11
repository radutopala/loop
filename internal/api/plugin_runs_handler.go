package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

// PluginRunner runs the agents plugins ask for, in each plugin's hidden
// thread.
type PluginRunner interface {
	StartPluginRun(ctx context.Context, parent *db.Channel, run orchestrator.PluginRun) (runID, threadID string, err error)
	ActivePluginRun(threadID string) (orchestrator.ActivePluginRun, bool)
}

// SetPluginRunner configures what runs the agents plugins ask for; left
// unset, plugins can't start agent runs.
func (s *Server) SetPluginRunner(pr PluginRunner) {
	s.pluginRunner = pr
}

type pluginRunRequest struct {
	Agent  string `json:"agent"`
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// handleStartPluginRun queues a run of one of the calling plugin's agents
// in the channel the plugin's container serves.
func (s *Server) handleStartPluginRun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) || !requireConfigured(w, s.pluginRunner, pluginsDisabled) {
		return
	}
	p, _ := apiauth.PrincipalFrom(r.Context())
	if p.Kind != apiauth.KindPlugin {
		http.Error(w, "only a plugin starts its agent runs", http.StatusForbidden)
		return
	}
	var req pluginRunRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Agent == "" || req.Prompt == "" {
		http.Error(w, "agent and prompt are required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	run, err := s.plugins.AgentRun(ctx, p.PluginID, req.Agent)
	switch {
	case errors.Is(err, install.ErrUnknownAgent), errors.Is(err, install.ErrNotInstalled):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, install.ErrNotApproved):
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	case errors.Is(err, install.ErrNotRunnable):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ch, err := s.store.GetChannel(ctx, p.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	runID, threadID, err := s.pluginRunner.StartPluginRun(ctx, ch, orchestrator.PluginRun{
		PluginID: p.PluginID, Agent: run.Agent.ID, ReadOnly: run.Agent.ReadOnly,
		Prompt: req.Prompt, Model: req.Model, Effort: req.Effort, Tools: run.Tools,
	})
	switch {
	case errors.Is(err, db.ErrParentGone):
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{Type: EventPluginRunStarted, ChannelID: ch.ChannelID, Data: map[string]string{
			"plugin_id": p.PluginID, "run_id": runID, "thread_id": threadID,
		}})
	}
	writeHTTPJSON(w, http.StatusAccepted, map[string]string{"run_id": runID, "thread_id": threadID}, s.logger)
}

// pluginAgentResponse is a plugin's hidden thread in a channel, where its
// agent runs run, and whether one is running there.
type pluginAgentResponse struct {
	ThreadID string `json:"thread_id,omitempty"`
	Running  bool   `json:"running"`
}

// handleGetPluginAgent returns the hidden thread a plugin's agent runs run
// in, for the channel_id query parameter's channel (a thread answers for
// its channel), so its panel can show their chat.
func (s *Server) handleGetPluginAgent(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	ch, ok := s.pluginChannel(w, r, channelID)
	if !ok {
		return
	}
	h, err := s.store.GetHiddenThread(r.Context(), ch.ChannelID, db.PluginKind(r.PathValue("plugin")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var resp pluginAgentResponse
	if h != nil {
		resp.ThreadID = h.ChannelID
		if s.pluginRunner != nil {
			_, resp.Running = s.pluginRunner.ActivePluginRun(h.ChannelID)
		}
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

type pluginToolCallRequest struct {
	Input json.RawMessage `json:"input"`
}

// handleCallPluginTool forwards a plugin agent's tool call to the plugin's
// container in the run's channel. Only the agent of a run of that plugin
// going on in the plugin's thread calls its tools. A call the tool can't
// take comes back as the tool's error, for the agent to fix.
func (s *Server) handleCallPluginTool(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) || !requireConfigured(w, s.pluginRunner, pluginsDisabled) {
		return
	}
	pluginID, tool := r.PathValue("plugin"), r.PathValue("tool")
	p, _ := apiauth.PrincipalFrom(r.Context())
	active, ok := s.pluginRunner.ActivePluginRun(p.ChannelID)
	if p.Kind != apiauth.KindAgent || !ok || active.Run.PluginID != pluginID {
		http.Error(w, "only the plugin's own agent runs call its tools", http.StatusForbidden)
		return
	}
	var req pluginToolCallRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	thread, err := s.store.GetChannel(ctx, p.ChannelID)
	var parent *db.Channel
	if err == nil && thread != nil {
		parent, err = s.store.GetChannel(ctx, thread.ParentID)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	// Starting the container outlives an agent that hangs up, so a slow
	// start isn't counted as a failed one.
	res, err := s.plugins.CallTool(context.WithoutCancel(ctx), pluginID, tool,
		runtime.Channel{ID: parent.ChannelID, DirPath: parent.DirPath},
		proxy.ToolRequest{RunID: active.ID, Agent: active.Run.Agent, ThreadID: thread.ChannelID, Input: req.Input})
	switch {
	case errors.Is(err, install.ErrInvalidInput), errors.Is(err, install.ErrUnknownTool):
		res = &proxy.ToolResult{Content: err.Error(), IsError: true}
	case errors.Is(err, install.ErrNotEnabled):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeHTTPJSON(w, http.StatusOK, res, s.logger)
}
