package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/scheduler"
)

// Server exposes a lightweight HTTP API for task CRUD operations.
type Server struct {
	scheduler scheduler.Scheduler
	channels  ChannelEnsurer
	threads   ThreadEnsurer
	logger    *slog.Logger
	server    *http.Server
	listener  net.Listener
}

// NewServer creates a new API server. The channels and threads parameters
// may be nil if channel/thread creation is not configured.
func NewServer(sched scheduler.Scheduler, channels ChannelEnsurer, threads ThreadEnsurer, logger *slog.Logger) *Server {
	return &Server{
		scheduler: sched,
		channels:  channels,
		threads:   threads,
		logger:    logger,
	}
}

type ensureChannelRequest struct {
	DirPath string `json:"dir_path"`
}

type ensureChannelResponse struct {
	ChannelID string `json:"channel_id"`
}

type createTaskRequest struct {
	ChannelID string `json:"channel_id"`
	Schedule  string `json:"schedule"`
	Type      string `json:"type"`
	Prompt    string `json:"prompt"`
}

type createTaskResponse struct {
	ID int64 `json:"id"`
}

type updateTaskRequest struct {
	Enabled  *bool   `json:"enabled"`
	Schedule *string `json:"schedule"`
	Type     *string `json:"type"`
	Prompt   *string `json:"prompt"`
}

type createThreadRequest struct {
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
	AuthorID  string `json:"author_id"`
}

type createThreadResponse struct {
	ThreadID string `json:"thread_id"`
}

type taskResponse struct {
	ID        int64     `json:"id"`
	ChannelID string    `json:"channel_id"`
	Schedule  string    `json:"schedule"`
	Type      string    `json:"type"`
	Prompt    string    `json:"prompt"`
	Enabled   bool      `json:"enabled"`
	NextRunAt time.Time `json:"next_run_at"`
}

// Start starts the HTTP server on the given address.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels", s.handleEnsureChannel)
	mux.HandleFunc("POST /api/threads", s.handleCreateThread)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.handleUpdateTask)

	s.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	s.listener = ln

	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("api server error", "error", err)
		}
	}()

	s.logger.Info("api server started", "addr", addr)
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	task := &db.ScheduledTask{
		ChannelID: req.ChannelID,
		Schedule:  req.Schedule,
		Type:      db.TaskType(req.Type),
		Prompt:    req.Prompt,
		Enabled:   true,
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
			ID:        t.ID,
			ChannelID: t.ChannelID,
			Schedule:  t.Schedule,
			Type:      string(t.Type),
			Prompt:    t.Prompt,
			Enabled:   t.Enabled,
			NextRunAt: t.NextRunAt,
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Enabled == nil && req.Schedule == nil && req.Type == nil && req.Prompt == nil {
		http.Error(w, "at least one field is required", http.StatusBadRequest)
		return
	}

	if req.Enabled != nil {
		if err := s.scheduler.SetTaskEnabled(r.Context(), taskID, *req.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if req.Schedule != nil || req.Type != nil || req.Prompt != nil {
		if err := s.scheduler.EditTask(r.Context(), taskID, req.Schedule, req.Type, req.Prompt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleEnsureChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		http.Error(w, "channel creation not configured (discord_guild_id not set)", http.StatusNotImplemented)
		return
	}

	var req ensureChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.DirPath == "" {
		http.Error(w, "dir_path is required", http.StatusBadRequest)
		return
	}

	channelID, err := s.channels.EnsureChannel(r.Context(), req.DirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ensureChannelResponse{ChannelID: channelID})
}

func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	if s.threads == nil {
		http.Error(w, "thread creation not configured", http.StatusNotImplemented)
		return
	}

	var req createThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	threadID, err := s.threads.CreateThread(r.Context(), req.ChannelID, req.Name, req.AuthorID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createThreadResponse{ThreadID: threadID})
}
