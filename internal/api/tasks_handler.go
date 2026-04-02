package api

import (
	"context"
	"net/http"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// resolveTaskChannelID walks up from deeply nested threads to the nearest
// channel that is either a top-level channel or a direct child of one.
// This ensures tasks are always listed/created at the correct level.
func (s *Server) resolveTaskChannelID(ctx context.Context, channelID string) string {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil || ch == nil || ch.ParentID == "" {
		return channelID
	}
	parent, err := s.store.GetChannel(ctx, ch.ParentID)
	if err != nil || parent == nil || parent.ParentID == "" {
		return channelID
	}
	return ch.ParentID
}

type createTaskRequest struct {
	ChannelID     string `json:"channel_id"`
	Schedule      string `json:"schedule"`
	Type          string `json:"type"`
	Prompt        string `json:"prompt"`
	TemplateName  string `json:"template_name,omitempty"`
	AutoDeleteSec int    `json:"auto_delete_sec"`
	Worktree      bool   `json:"worktree"`
}

type createTaskResponse struct {
	ID int64 `json:"id"`
}

type updateTaskRequest struct {
	Enabled       *bool   `json:"enabled"`
	Schedule      *string `json:"schedule"`
	Type          *string `json:"type"`
	Prompt        *string `json:"prompt"`
	AutoDeleteSec *int    `json:"auto_delete_sec"`
	Worktree      *bool   `json:"worktree"`
}

type taskResponse struct {
	ID            int64     `json:"id"`
	ChannelID     string    `json:"channel_id"`
	Schedule      string    `json:"schedule"`
	Type          string    `json:"type"`
	Prompt        string    `json:"prompt"`
	Enabled       bool      `json:"enabled"`
	NextRunAt     time.Time `json:"next_run_at"`
	TemplateName  string    `json:"template_name,omitempty"`
	AutoDeleteSec int       `json:"auto_delete_sec"`
	Worktree      bool      `json:"worktree"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	task := &db.ScheduledTask{
		ChannelID:     s.resolveTaskChannelID(r.Context(), req.ChannelID),
		Schedule:      req.Schedule,
		Type:          db.TaskType(req.Type),
		Prompt:        req.Prompt,
		Enabled:       true,
		TemplateName:  req.TemplateName,
		AutoDeleteSec: req.AutoDeleteSec,
		Worktree:      req.Worktree,
	}

	id, err := s.scheduler.AddTask(r.Context(), task)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTaskCreated(events.TaskEventData{TaskID: id, ChannelID: task.ChannelID})
	}

	writeHTTPJSON(w, http.StatusCreated, createTaskResponse{ID: id}, s.logger)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		http.Error(w, "channel_id query parameter required", http.StatusBadRequest)
		return
	}

	tasks, err := s.scheduler.ListTasks(r.Context(), s.resolveTaskChannelID(r.Context(), channelID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := make([]taskResponse, 0, len(tasks))
	for _, t := range tasks {
		resp = append(resp, toTaskResponse(t))
	}

	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	task, err := s.scheduler.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	writeHTTPJSON(w, http.StatusOK, toTaskResponse(task), s.logger)
}

func toTaskResponse(t *db.ScheduledTask) taskResponse {
	return taskResponse{
		ID:            t.ID,
		ChannelID:     t.ChannelID,
		Schedule:      t.Schedule,
		Type:          string(t.Type),
		Prompt:        t.Prompt,
		Enabled:       t.Enabled,
		NextRunAt:     t.NextRunAt,
		TemplateName:  t.TemplateName,
		AutoDeleteSec: t.AutoDeleteSec,
		Worktree:      t.Worktree,
	}
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	if err := s.scheduler.RemoveTask(r.Context(), taskID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTaskDeleted(events.TaskEventData{TaskID: taskID})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	var req updateTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Enabled == nil && req.Schedule == nil && req.Type == nil && req.Prompt == nil && req.AutoDeleteSec == nil && req.Worktree == nil {
		http.Error(w, "at least one field is required", http.StatusBadRequest)
		return
	}

	if req.Enabled != nil {
		if err := s.scheduler.SetTaskEnabled(r.Context(), taskID, *req.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if req.Schedule != nil || req.Type != nil || req.Prompt != nil || req.AutoDeleteSec != nil || req.Worktree != nil {
		if err := s.scheduler.EditTask(r.Context(), taskID, req.Schedule, req.Type, req.Prompt, req.AutoDeleteSec, req.Worktree); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastTaskUpdated(events.TaskEventData{TaskID: taskID})
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleListTaskRuns(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parsePathInt64(w, r, "id")
	if !ok {
		return
	}

	runs, err := s.store.ListTaskRunLogs(r.Context(), taskID, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, runs, s.logger)
}
