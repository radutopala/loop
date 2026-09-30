package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/apiauth"
)

// agentRoutes is everything an agent container's clients call: the MCP
// server's tools, mcp-browser, `loop review` and the agent-channel WS. An
// agent token gets a 403 on every other route, so config, terminals, the
// gate, images and the other owner-only surfaces stay out of reach. Adding
// an MCP tool that calls a new route means adding the route here.
var agentRoutes = []string{
	"GET /api/ws/agent-channel",
	"POST /api/agents",
	"GET /api/agents",
	"PATCH /api/agents/{id}",
	"DELETE /api/agents/{id}",
	"POST /api/agents/{id}/message",

	"GET /api/channels",
	"POST /api/channels/create",
	"POST /api/channels/{id}/rename",
	"POST /api/channels/{id}/description",
	"POST /api/channels/{id}/ticket",
	"GET /api/channels/{id}/sessions",
	"PUT /api/channels/{id}/session",
	"POST /api/messages",
	"DELETE /api/messages/{id}",
	"GET /api/channels/{id}/queued",
	"POST /api/threads",
	"DELETE /api/threads/{id}",
	"POST /api/threads/{id}/fork",
	"POST /api/worktrees",

	"POST /api/tasks",
	"GET /api/tasks",
	"GET /api/tasks/{id}",
	"PATCH /api/tasks/{id}",
	"DELETE /api/tasks/{id}",
	"GET /api/shortcuts",
	"POST /api/shortcuts",
	"GET /api/bash-shortcuts",
	"POST /api/bash-shortcuts",
	"GET /api/components",
	"POST /api/components",

	"POST /api/memory/search",
	"POST /api/memory/index",

	"GET /api/playground",
	"PUT /api/playground",
	"DELETE /api/playground",
	"GET /api/playground/file",
	"PUT /api/playground/file",
	"DELETE /api/playground/file",
	"GET /api/playground/files",
	"PUT /api/playground/share",
	"DELETE /api/playground/share",

	"GET /api/workflows",
	"POST /api/workflows",
	"POST /api/workflows/runs",
	"GET /api/workflows/runs",
	"GET /api/workflows/runs/{id}",
	"DELETE /api/workflows/runs/{id}",
	"POST /api/workflows/runs/{id}/cancel",
	"POST /api/workflows/runs/{id}/retry",
	"POST /api/workflows/runs/{id}/resume",

	"POST /api/channels/{id}/learn/proposals",
	"POST /api/channels/{id}/review/comments",
	"POST /api/channels/{id}/review/load",
	"GET /api/channels/{id}/review",
	"POST /api/channels/{id}/review/run",

	"POST /api/channels/{id}/quality/scan",
	"GET /api/channels/{id}/quality/snapshot",
	"GET /api/channels/{id}/quality/cycles",
	"GET /api/channels/{id}/quality/metrics",
	"GET /api/channels/{id}/quality/diagnostics",
	"GET /api/channels/{id}/quality/rules",
	"POST /api/channels/{id}/quality/whatif",
	"GET /api/channels/{id}/quality/evolution",
	"GET /api/channels/{id}/quality/c4",
	"GET /api/channels/{id}/quality/bugfactor",
	"GET /api/channels/{id}/quality/complexity",
	"GET /api/channels/{id}/quality/clones",

	"POST /api/browser/action",
}

// publicRoutes need no API token. The container-approval route checks its
// own per-container gate token, and content links carry a signed capability.
var publicRoutes = []string{
	"GET /api/health",
	"POST /api/gate/container-approval",
	"GET /c/{cap}/{path...}",
}

// maxGuardedBody caps how much of an agent request body the guard reads.
const maxGuardedBody = 32 << 20

// requestRefs is what an agent request body can name. It's decoded the way
// the handlers decode their bodies (encoding/json, first value, fields
// matched case-insensitively), so the guard sees what the handler will.
type requestRefs struct {
	ChannelID      string `json:"channel_id"`
	LearnChannelID string `json:"learn_channel_id"`
	ThreadID       string `json:"thread_id"`
	DirPath        string `json:"dir_path"`
	Scope          string `json:"scope"`
	WorkflowName   string `json:"workflow_name"`
}

// agentGuard holds an agent to its own project. Every channel, thread,
// task, workflow run and dir an agent request names, in the path, the query
// or the JSON body, must belong to the project of the agent's own channel.
// Owner requests pass straight through.
func (s *Server) agentGuard(next http.Handler) http.Handler {
	guarded := http.NewServeMux()
	for _, pattern := range agentRoutes {
		guarded.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if msg := s.agentRefusal(r, pattern); msg != "" {
				http.Error(w, msg, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAgentRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

// agentRefusal returns why an agent may not make request r (matched by
// pattern), or "" when it may.
func (s *Server) agentRefusal(r *http.Request, pattern string) string {
	ctx := r.Context()
	p, _ := apiauth.PrincipalFrom(ctx)

	var channels, dirs []string
	if id := r.PathValue("id"); id != "" {
		switch {
		case strings.Contains(pattern, "/api/channels/{id}"), strings.Contains(pattern, "/api/threads/{id}"):
			channels = append(channels, id)
		case strings.Contains(pattern, "/api/tasks/{id}"):
			ch, ok := s.taskChannel(ctx, id)
			if !ok {
				return "unknown task"
			}
			channels = append(channels, ch)
		case strings.Contains(pattern, "/api/workflows/runs/{id}"):
			ch, ok := s.workflowRunChannel(ctx, id)
			if !ok {
				return "unknown workflow run"
			}
			channels = append(channels, ch)
		}
	}
	q := r.URL.Query()
	channels = append(channels, q["channel_id"]...)
	dirs = append(dirs, q["dir_path"]...)

	var refs requestRefs
	// The playground file body is the file itself, not a JSON request.
	if r.Body != nil && pattern != "PUT /api/playground/file" {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxGuardedBody+1))
		if err != nil || len(body) > maxGuardedBody {
			return "request body too large"
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		// A type error still leaves the other fields decoded, and the
		// handler would use them, so they're checked either way.
		_ = json.NewDecoder(bytes.NewReader(body)).Decode(&refs)
	}
	channels = append(channels, refs.LearnChannelID, refs.ThreadID)
	// Agents post to any channel, the way they did before API tokens: the
	// user asks one agent to hand work or news to another project's.
	if pattern != "POST /api/messages" {
		channels = append(channels, refs.ChannelID)
	}
	dirs = append(dirs, refs.DirPath)

	for _, ch := range channels {
		if ch != "" && !s.agentOwnsChannel(ctx, p, ch) {
			return "channel " + ch + " is outside this agent's project"
		}
	}
	for _, d := range dirs {
		if d != "" && !s.agentOwnsDir(ctx, p, d) {
			return "dir " + d + " is outside this agent's project"
		}
	}

	// Bash shortcuts run in the user's terminals, host shells included, so
	// an agent may only touch the project ones, which take effect once the
	// user trusts the project config.
	if pattern == "POST /api/bash-shortcuts" && refs.Scope != "project" {
		return "agents can only change project bash shortcuts"
	}
	// With workflow_bash_local, workflow bash nodes run on the host.
	if s.workflowBashLocal && startsWorkflow(pattern, refs) {
		return "agents can't start workflows while workflow bash runs on the host"
	}
	return ""
}

// startsWorkflow reports whether a request starts, or schedules, a workflow run.
func startsWorkflow(pattern string, refs requestRefs) bool {
	switch pattern {
	case "POST /api/workflows/runs", "POST /api/workflows/runs/{id}/retry", "POST /api/workflows/runs/{id}/resume":
		return true
	case "POST /api/tasks", "PATCH /api/tasks/{id}":
		return refs.WorkflowName != ""
	}
	return false
}

// taskChannel returns the channel a scheduled task belongs to.
func (s *Server) taskChannel(ctx context.Context, id string) (string, bool) {
	taskID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || s.scheduler == nil {
		return "", false
	}
	task, err := s.scheduler.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return "", false
	}
	return task.ChannelID, true
}

// workflowRunChannel returns the channel a workflow run belongs to.
func (s *Server) workflowRunChannel(ctx context.Context, id string) (string, bool) {
	if s.workflowEngine == nil {
		return "", false
	}
	run, _, err := s.workflowEngine.GetRun(ctx, id)
	if err != nil || run == nil {
		return "", false
	}
	return run.ChannelID, true
}

// agentOwnsChannel reports whether channelID is in the agent's project: its
// own channel, or any channel, thread or worktree with the same project root.
func (s *Server) agentOwnsChannel(ctx context.Context, p apiauth.Principal, channelID string) bool {
	if channelID == p.ChannelID {
		return true
	}
	own, err := s.playground.projectPlaygroundDir(ctx, p.ChannelID)
	if err != nil {
		return false
	}
	target, err := s.playground.projectPlaygroundDir(ctx, channelID)
	return err == nil && target == own
}

// agentOwnsDir reports whether dir is inside the agent's own dir or its
// project root, after symlinks: a link inside the project can point
// anywhere on the host.
func (s *Server) agentOwnsDir(ctx context.Context, p apiauth.Principal, dir string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	dir = s.realPath(dir)
	if p.DirPath != "" && pathWithin(dir, s.realPath(p.DirPath)) {
		return true
	}
	root, err := s.playground.projectPlaygroundDir(ctx, p.ChannelID)
	return err == nil && pathWithin(dir, s.realPath(root))
}

// realPath resolves symlinks in path, or returns it cleaned when it doesn't
// exist.
func (s *Server) realPath(path string) string {
	if real, err := s.sys.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}

// isAgentRequest reports whether r was made with an agent token.
func isAgentRequest(r *http.Request) bool {
	return apiauth.IsAgent(r.Context())
}
