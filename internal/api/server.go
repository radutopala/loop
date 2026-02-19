package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/scheduler"
)

// MemoryIndexer abstracts memory search and indexing for the memory API endpoints.
type MemoryIndexer interface {
	Search(ctx context.Context, memoryDir, query string, topK int) ([]memory.SearchResult, error)
	Index(ctx context.Context, memoryDir string) (int, error)
}

// Server exposes a lightweight HTTP API for task CRUD operations.
type Server struct {
	scheduler     scheduler.Scheduler
	channels      ChannelEnsurer
	threads       ThreadEnsurer
	store         ChannelLister
	messages      MessageSender
	memoryIndexer MemoryIndexer
	loopDir       string
	logger        *slog.Logger
	server        *http.Server
	listener      net.Listener
}

// SetMemoryIndexer configures the memory indexer for the /api/memory/* endpoints.
func (s *Server) SetMemoryIndexer(idx MemoryIndexer) {
	s.memoryIndexer = idx
}

// SetLoopDir sets the loop directory used for fallback work dir resolution.
func (s *Server) SetLoopDir(dir string) {
	s.loopDir = dir
}

// NewServer creates a new API server. The channels, threads, store, and messages
// parameters may be nil if those features are not configured.
func NewServer(sched scheduler.Scheduler, channels ChannelEnsurer, threads ThreadEnsurer, store ChannelLister, messages MessageSender, logger *slog.Logger) *Server {
	return &Server{
		scheduler: sched,
		channels:  channels,
		threads:   threads,
		store:     store,
		messages:  messages,
		logger:    logger,
	}
}

type ensureChannelRequest struct {
	DirPath string `json:"dir_path"`
}

type ensureChannelResponse struct {
	ChannelID string `json:"channel_id"`
}

type createChannelRequest struct {
	Name     string `json:"name"`
	AuthorID string `json:"author_id"`
}

type createChannelResponse struct {
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
	Message   string `json:"message"`
}

type createThreadResponse struct {
	ThreadID string `json:"thread_id"`
}

type channelResponse struct {
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
	DirPath   string `json:"dir_path"`
	ParentID  string `json:"parent_id"`
	Active    bool   `json:"active"`
}

type sendMessageRequest struct {
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
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
	mux.HandleFunc("GET /api/channels", s.handleSearchChannels)
	mux.HandleFunc("POST /api/channels", s.handleEnsureChannel)
	mux.HandleFunc("POST /api/channels/create", s.handleCreateChannel)
	mux.HandleFunc("POST /api/messages", s.handleSendMessage)
	mux.HandleFunc("POST /api/threads", s.handleCreateThread)
	mux.HandleFunc("DELETE /api/threads/{id}", s.handleDeleteThread)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("POST /api/memory/search", s.handleMemorySearch)
	mux.HandleFunc("POST /api/memory/index", s.handleMemoryIndex)

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
		http.Error(w, "channel creation not configured (discord_guild_id not set or Slack not configured)", http.StatusNotImplemented)
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

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		http.Error(w, "channel creation not configured (discord_guild_id not set or Slack not configured)", http.StatusNotImplemented)
		return
	}

	var req createChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	channelID, err := s.channels.CreateChannel(r.Context(), req.Name, req.AuthorID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createChannelResponse{ChannelID: channelID})
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

	threadID, err := s.threads.CreateThread(r.Context(), req.ChannelID, req.Name, req.AuthorID, req.Message)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createThreadResponse{ThreadID: threadID})
}

func (s *Server) handleSearchChannels(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "channel listing not configured", http.StatusNotImplemented)
		return
	}

	channels, err := s.store.ListChannels(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	query := r.URL.Query().Get("query")

	resp := make([]channelResponse, 0, len(channels))
	for _, ch := range channels {
		if query != "" && !containsFold(ch.Name, query) {
			continue
		}
		resp = append(resp, channelResponse{
			ChannelID: ch.ChannelID,
			Name:      ch.Name,
			DirPath:   ch.DirPath,
			ParentID:  ch.ParentID,
			Active:    ch.Active,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if s.messages == nil {
		http.Error(w, "message sending not configured", http.StatusNotImplemented)
		return
	}

	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.Content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	if err := s.messages.PostMessage(r.Context(), req.ChannelID, req.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	if s.threads == nil {
		http.Error(w, "thread deletion not configured", http.StatusNotImplemented)
		return
	}

	threadID := r.PathValue("id")

	if err := s.threads.DeleteThread(r.Context(), threadID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type memorySearchRequest struct {
	Query     string `json:"query"`
	TopK      int    `json:"top_k"`
	DirPath   string `json:"dir_path"`
	ChannelID string `json:"channel_id"`
}

type memorySearchResponse struct {
	Results []memory.SearchResult `json:"results"`
}

type memoryIndexRequest struct {
	DirPath   string `json:"dir_path"`
	ChannelID string `json:"channel_id"`
}

type memoryIndexResponse struct {
	Count int `json:"count"`
}

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	if s.memoryIndexer == nil {
		http.Error(w, "memory indexer not configured", http.StatusNotImplemented)
		return
	}

	var req memorySearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	dirPath, err := s.resolveDirPath(r.Context(), req.DirPath, req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results, err := s.memoryIndexer.Search(r.Context(), dirPath, req.Query, req.TopK)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(memorySearchResponse{Results: results})
}

func (s *Server) handleMemoryIndex(w http.ResponseWriter, r *http.Request) {
	if s.memoryIndexer == nil {
		http.Error(w, "memory indexer not configured", http.StatusNotImplemented)
		return
	}

	var req memoryIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	dirPath, err := s.resolveDirPath(r.Context(), req.DirPath, req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	count, err := s.memoryIndexer.Index(r.Context(), dirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(memoryIndexResponse{Count: count})
}

// resolveDirPath returns the dir_path from the request, resolving via channel_id lookup if needed.
func (s *Server) resolveDirPath(ctx context.Context, dirPath, channelID string) (string, error) {
	if dirPath != "" {
		return dirPath, nil
	}
	if channelID == "" {
		return "", fmt.Errorf("dir_path or channel_id is required")
	}
	if s.store == nil {
		return "", fmt.Errorf("channel lookup not configured")
	}
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("looking up channel: %w", err)
	}
	if ch == nil {
		return "", fmt.Errorf("channel %s not found", channelID)
	}
	if ch.DirPath == "" {
		// Fall back to the default work dir for channels without a project dir.
		if s.loopDir != "" {
			return filepath.Join(s.loopDir, channelID, "work"), nil
		}
		return "", fmt.Errorf("channel %s has no dir_path", channelID)
	}
	return ch.DirPath, nil
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
