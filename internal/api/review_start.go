package api

import (
	"cmp"
	"net/http"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/review"
	"github.com/radutopala/loop/internal/workflow"
)

// Review workflows the panel's Run button starts, by mode.
const (
	reviewLoopWorkflow    = "review-loop"
	reviewFixLoopWorkflow = "review-fix-loop"
)

// reviewStartRequest is the body of POST /api/channels/{id}/review/start.
// Model and effort default to the channel's own overrides; empty after that
// means the config's default, as on the panel. Fork picks the session the
// run forks: "chat" (the channel's chat session), "fresh", or a session id;
// empty keeps the review session's own choice. Scope picks what the runs
// review: "auto", "full" or "incremental"; empty keeps the session's choice.
type reviewStartRequest struct {
	PRNumber      int    `json:"pr_number"`
	Model         string `json:"model"`
	Effort        string `json:"effort"`
	Mode          string `json:"mode"`
	MaxIterations int    `json:"max_iterations"`
	Fork          string `json:"fork"`
	Scope         string `json:"scope"`
}

// reviewStartScopes maps the request's scope values to the session's.
var reviewStartScopes = map[string]review.Scope{"auto": review.ScopeAuto, "full": review.ScopeFull, "incremental": review.ScopeIncremental}

// reviewStartFork maps the request's fork value to the review session's
// fork mode and session id. ok is false when fork is empty: the session's
// choice stands.
func reviewStartFork(fork string) (mode review.ForkMode, sessionID string, ok bool) {
	switch fork = strings.TrimSpace(fork); fork {
	case "":
		return "", "", false
	case "chat":
		return review.ForkCurrent, "", true
	case "fresh":
		return review.ForkNone, "", true
	default:
		return review.ForkCustom, fork, true
	}
}

type reviewStartResponse struct {
	RunID        string          `json:"run_id"`
	WorkflowName string          `json:"workflow_name"`
	Model        string          `json:"model"`
	Effort       string          `json:"effort"`
	Session      *review.Session `json:"session"`
}

// handleReviewStart does what the Review panel's Run button does, in one
// call: it loads the PR into the channel's review session unless the session
// is already on it, records the model, effort and fork choice, and starts the review-loop
// (mode "review") or review-fix-loop (mode "review-fix") workflow. A panel
// open on the channel picks the run up. Answers 409 while the channel has a
// review loading, running or about to start.
func (s *Server) handleReviewStart(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	rs := s.review
	if rs.sessions == nil || rs.client == nil || rs.worktree == nil {
		http.Error(w, "review service not configured", http.StatusNotImplemented)
		return
	}

	channelID := r.PathValue("id")
	var req reviewStartRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.PRNumber <= 0 {
		http.Error(w, "pr_number is required", http.StatusBadRequest)
		return
	}
	workflowName, ok := map[string]string{"": reviewLoopWorkflow, "review": reviewLoopWorkflow, "review-fix": reviewFixLoopWorkflow}[req.Mode]
	if !ok {
		http.Error(w, "mode must be review or review-fix", http.StatusBadRequest)
		return
	}
	if req.MaxIterations < 0 || req.MaxIterations > config.MaxReviewIterations {
		http.Error(w, "max_iterations must be between 1 and 10", http.StatusBadRequest)
		return
	}
	effort := strings.TrimSpace(req.Effort)
	if _, ok := validEfforts[effort]; !ok {
		http.Error(w, "invalid effort: must be one of low, medium, high, xhigh, max (or empty)", http.StatusBadRequest)
		return
	}
	scope, setScope := reviewStartScopes[req.Scope]
	if !setScope && req.Scope != "" {
		http.Error(w, "scope must be auto, full or incremental", http.StatusBadRequest)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	model := cmp.Or(strings.TrimSpace(req.Model), ch.ModelOverride)
	effort = cmp.Or(effort, ch.EffortOverride)

	// Two starts racing on one channel would both see it idle; the second
	// one loses here.
	if _, busy := s.reviewStarting.LoadOrStore(channelID, struct{}{}); busy {
		http.Error(w, "a review is starting for this channel", http.StatusConflict)
		return
	}
	defer s.reviewStarting.Delete(channelID)

	if msg := s.reviewBusy(r, channelID); msg != "" {
		http.Error(w, msg, http.StatusConflict)
		return
	}

	if sess := rs.sessions.Get(channelID); sess == nil || sess.PR == nil || sess.PR.Number != req.PRNumber || sess.Status != review.StatusReady {
		if _, err := rs.loadPR(r.Context(), channelID, req.PRNumber, nil); err != nil {
			respondReviewError(w, err)
			return
		}
	}
	rs.sessions.UpdateAgent(channelID, model, effort)
	if mode, sessionID, ok := reviewStartFork(req.Fork); ok {
		rs.sessions.UpdateFork(channelID, mode, sessionID)
	}
	if setScope {
		rs.sessions.UpdateScope(channelID, scope)
	}
	// Every run of the loop belongs to this review: its findings are the
	// panel's new ones.
	rs.sessions.StartReview(channelID, rs.newReviewID())

	dirPath, parentDirPath, err := s.resolveWorkflowConfigPaths(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Without max_iterations the run takes the project's review.max_iterations.
	iterations := cmp.Or(req.MaxIterations, s.configs.reviewMaxIterations(dirPath, parentDirPath))
	runID, err := s.workflowEngine.StartRun(r.Context(), workflow.StartRunOptions{
		WorkflowName:  workflowName,
		ChannelID:     channelID,
		DirPath:       dirPath,
		ParentDirPath: parentDirPath,
		Inputs:        map[string]string{"max_iterations": strconv.Itoa(iterations), "pr": strconv.Itoa(req.PRNumber)},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusCreated, reviewStartResponse{
		RunID:        runID,
		WorkflowName: workflowName,
		Model:        model,
		Effort:       effort,
		Session:      rs.sessions.Get(channelID),
	}, s.logger)
}

// reviewBusy names what keeps channelID from starting a review: a run in
// flight, a session mid-load or mid-review, or a review workflow that is
// running or paused. Empty when nothing does.
func (s *Server) reviewBusy(r *http.Request, channelID string) string {
	if s.review.isReviewRunActive(channelID) {
		return "review run in flight for this channel"
	}
	if sess := s.review.sessions.Get(channelID); sess != nil {
		switch sess.Status {
		case review.StatusLoading, review.StatusReviewing, review.StatusVerifying:
			return "review session is " + string(sess.Status)
		}
	}
	runs, err := s.workflowEngine.ListRuns(r.Context(), channelID, 50, 0)
	if err != nil {
		return ""
	}
	for _, run := range runs {
		if run.WorkflowName != reviewLoopWorkflow && run.WorkflowName != reviewFixLoopWorkflow {
			continue
		}
		if run.Status == db.WorkflowRunStatusRunning || run.Status == db.WorkflowRunStatusPaused {
			return "review workflow run " + run.ID + " is " + string(run.Status)
		}
	}
	return ""
}
