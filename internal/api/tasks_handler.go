package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/radutopala/loop/internal/db"
)

type createTaskRequest struct {
	ChannelID     string `json:"channel_id"`
	Schedule      string `json:"schedule"`
	Type          string `json:"type"`
	Prompt        string `json:"prompt"`
	AutoDeleteSec int    `json:"auto_delete_sec"`
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
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	task := &db.ScheduledTask{
		ChannelID:     req.ChannelID,
		Schedule:      req.Schedule,
		Type:          db.TaskType(req.Type),
		Prompt:        req.Prompt,
		Enabled:       true,
		AutoDeleteSec: req.AutoDeleteSec,
	}

	id, err := s.scheduler.AddTask(r.Context(), task)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createTaskResponse{ID: id})
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		http.Error(w, "channel_id query parameter required", http.StatusBadRequest)
		return
	}

	tasks, err := s.scheduler.ListTasks(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := make([]taskResponse, 0, len(tasks))
	for _, t := range tasks {
		resp = append(resp, taskResponse{
			ID:            t.ID,
			ChannelID:     t.ChannelID,
			Schedule:      t.Schedule,
			Type:          string(t.Type),
			Prompt:        t.Prompt,
			Enabled:       t.Enabled,
			NextRunAt:     t.NextRunAt,
			TemplateName:  t.TemplateName,
			AutoDeleteSec: t.AutoDeleteSec,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid task id", http.StatusBadRequest)
		return
	}

	if err := s.scheduler.RemoveTask(r.Context(), taskID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid task id", http.StatusBadRequest)
		return
	}

	var req updateTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Enabled == nil && req.Schedule == nil && req.Type == nil && req.Prompt == nil && req.AutoDeleteSec == nil {
		http.Error(w, "at least one field is required", http.StatusBadRequest)
		return
	}

	if req.Enabled != nil {
		if err := s.scheduler.SetTaskEnabled(r.Context(), taskID, *req.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if req.Schedule != nil || req.Type != nil || req.Prompt != nil || req.AutoDeleteSec != nil {
		if err := s.scheduler.EditTask(r.Context(), taskID, req.Schedule, req.Type, req.Prompt, req.AutoDeleteSec); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}
