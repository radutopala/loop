package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"

	"github.com/radutopala/loop/internal/workflow"
	"github.com/tailscale/hujson"
)

// WorkflowEngine is the interface needed by the API server for workflow operations.
type WorkflowEngine interface {
	workflow.Engine
}

// SetWorkflowEngine configures the workflow engine.
func (s *Server) SetWorkflowEngine(e WorkflowEngine) {
	s.workflowEngine = e
}

type startWorkflowRunRequest struct {
	WorkflowName string            `json:"workflow_name"`
	ChannelID    string            `json:"channel_id"`
	DirPath      string            `json:"dir_path"`
	Inputs       map[string]string `json:"inputs"`
}

type startWorkflowRunResponse struct {
	RunID string `json:"run_id"`
}

func (s *Server) handleStartWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}

	var req startWorkflowRunRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.WorkflowName == "" {
		http.Error(w, "workflow_name is required", http.StatusBadRequest)
		return
	}

	// Resolve parentDirPath for worktree channels so the engine can
	// perform three-layer config merging (global → parent → worktree).
	var parentDirPath string
	if req.ChannelID != "" {
		resolved, parent, err := s.resolveWorkflowConfigPaths(r.Context(), req.ChannelID)
		if err != nil {
			s.logger.Error("failed to resolve workflow config paths", "channel_id", req.ChannelID, "error", err)
		} else {
			if req.DirPath == "" {
				req.DirPath = resolved
			}
			parentDirPath = parent
		}
	}

	runID, err := s.workflowEngine.StartRun(r.Context(), workflow.StartRunOptions{
		WorkflowName:  req.WorkflowName,
		ChannelID:     req.ChannelID,
		DirPath:       req.DirPath,
		ParentDirPath: parentDirPath,
		Inputs:        req.Inputs,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusCreated, startWorkflowRunResponse{RunID: runID}, s.logger)
}

func (s *Server) handleGetWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}

	runID := r.PathValue("id")
	run, nodeRuns, err := s.workflowEngine.GetRun(r.Context(), runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if run == nil {
		http.Error(w, "workflow run not found", http.StatusNotFound)
		return
	}

	writeHTTPJSON(w, http.StatusOK, map[string]any{
		"run":       run,
		"node_runs": nodeRuns,
	}, s.logger)
}

func (s *Server) handleListWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}

	channelID := r.URL.Query().Get("channel_id")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	runs, err := s.workflowEngine.ListRuns(r.Context(), channelID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, runs, s.logger)
}

func (s *Server) handleCancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}

	runID := r.PathValue("id")
	if err := s.workflowEngine.CancelRun(r.Context(), runID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type resumeWorkflowRunRequest struct {
	Response string `json:"response"`
}

func (s *Server) handleResumeWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}

	runID := r.PathValue("id")

	var req resumeWorkflowRunRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := s.workflowEngine.ResumeRun(r.Context(), runID, req.Response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}

	runID := r.PathValue("id")
	if err := s.workflowEngine.DeleteRun(r.Context(), runID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type retryWorkflowRunResponse struct {
	RunID string `json:"run_id"`
}

func (s *Server) handleRetryWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}

	runID := r.PathValue("id")
	newRunID, err := s.workflowEngine.RetryRun(r.Context(), runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusCreated, retryWorkflowRunResponse{RunID: newRunID}, s.logger)
}

func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.workflowEngine, "workflow engine not configured") {
		return
	}

	dirPath := r.URL.Query().Get("dir_path")
	channelID := r.URL.Query().Get("channel_id")
	var parentDirPath string

	// When channel_id is provided, resolve dirPath and parentDirPath from DB.
	// This enables threads to inherit parent workflows and worktrees to merge
	// their own on top (global → parent → worktree).
	if channelID != "" {
		resolved, parent, err := s.resolveWorkflowConfigPaths(r.Context(), channelID)
		if err == nil {
			dirPath = resolved
			parentDirPath = parent
		}
	}

	workflows, err := s.workflowEngine.ListWorkflows(r.Context(), dirPath, parentDirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, workflows, s.logger)
}

type workflowModifyRequest struct {
	Action    string          `json:"action"` // "add", "update", "delete"
	Scope     string          `json:"scope"`  // "global" or "project"
	ChannelID string          `json:"channel_id"`
	Workflow  json.RawMessage `json:"workflow"` // full WorkflowDef JSON (for add/update)
	Name      string          `json:"name"`     // workflow name (for delete)
}

// handleModifyWorkflow adds, updates, or deletes a workflow definition in the
// global or project config file. Follows the same pattern as handleModifyShortcut.
func (s *Server) handleModifyWorkflow(w http.ResponseWriter, r *http.Request) {
	var req workflowModifyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Action != "add" && req.Action != "update" && req.Action != "delete" {
		http.Error(w, "action must be add, update, or delete", http.StatusBadRequest)
		return
	}

	// For add/update, parse the workflow definition; for delete, just need the name.
	var wfName string
	var wfMap map[string]any
	if req.Action == "add" || req.Action == "update" {
		if len(req.Workflow) == 0 {
			http.Error(w, "workflow is required for add/update", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(req.Workflow, &wfMap); err != nil {
			http.Error(w, "workflow must be valid JSON", http.StatusBadRequest)
			return
		}
		name, _ := wfMap["name"].(string)
		if name == "" {
			http.Error(w, "workflow.name is required", http.StatusBadRequest)
			return
		}
		wfName = name
	} else {
		// delete
		if req.Name == "" {
			http.Error(w, "name is required for delete", http.StatusBadRequest)
			return
		}
		wfName = req.Name
	}

	// Resolve config file path.
	configPath, ok := s.resolveConfigPath(w, r, req.Scope, req.ChannelID)
	if !ok {
		return
	}

	// Read existing config.
	configData, err := s.sys.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			http.Error(w, "failed to read config file", http.StatusInternalServerError)
			return
		}
		configData = []byte("{}")
	}

	// Standardize HJSON to JSON, parse into generic map.
	standardized, err := hujson.Standardize(configData)
	if err != nil {
		http.Error(w, "config file contains invalid HJSON", http.StatusInternalServerError)
		return
	}
	var configMap map[string]any
	if err := jsonUnmarshalFn(standardized, &configMap); err != nil {
		http.Error(w, "config file contains invalid JSON", http.StatusInternalServerError)
		return
	}

	// Extract existing workflows array.
	var workflows []map[string]any
	if raw, ok := configMap["workflows"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					workflows = append(workflows, m)
				}
			}
		}
	}

	switch req.Action {
	case "add":
		for _, wf := range workflows {
			if wf["name"] == wfName {
				http.Error(w, "workflow with this name already exists; use update to modify it", http.StatusConflict)
				return
			}
		}
		workflows = append(workflows, wfMap)

	case "update":
		found := false
		for i, wf := range workflows {
			if wf["name"] == wfName {
				found = true
				workflows[i] = wfMap
				break
			}
		}
		if !found {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}

	case "delete":
		found := false
		filtered := workflows[:0]
		for _, wf := range workflows {
			if wf["name"] == wfName {
				found = true
				continue
			}
			filtered = append(filtered, wf)
		}
		if !found {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		workflows = filtered
	}

	// Write back.
	configMap["workflows"] = workflows
	out, err := jsonMarshalIndent(configMap, "", "  ")
	if err != nil {
		http.Error(w, "failed to serialize config", http.StatusInternalServerError)
		return
	}
	if err := s.sys.WriteFile(configPath, append(out, '\n'), 0644); err != nil {
		http.Error(w, "failed to write config file", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
