package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/radutopala/loop/internal/scheduler"
)

// RunningChannelLister returns the set of channel IDs that have running containers.
type RunningChannelLister interface {
	RunningChannelIDs(ctx context.Context) (map[string]struct{}, error)
}

// ActiveChatLister returns channel IDs with active chat agent runs.
type ActiveChatLister interface {
	ActiveChatChannelIDs() map[string]struct{}
}

// IncomingMessageHandler processes a user message from the API, routing it
// through the orchestrator so Claude can respond.
type IncomingMessageHandler interface {
	HandleIncomingMessage(ctx context.Context, channelID, authorID, content string)
	HandleThreadCreated(ctx context.Context, threadID, authorID, message string)
}

// Server exposes a lightweight HTTP API for task CRUD operations.
type Server struct {
	scheduler          scheduler.Scheduler
	channels           ChannelEnsurer
	threads            ThreadEnsurer
	store              ChannelLister
	messages           MessageSender
	memoryIndexer      MemoryIndexer
	termManager        TerminalManager
	containerFinder    ContainerFinder
	containerStopper   ContainerStopper
	cmdBuilder         InteractiveCmdBuilder
	runningChLister    RunningChannelLister
	activeChatLister   ActiveChatLister
	msgHandler         IncomingMessageHandler
	interactionHandler InteractionHandler
	eventsHub          *EventsHub
	loopDir            string
	logger             *slog.Logger
	server             *http.Server
	listener           net.Listener
}

// SetEventsHub configures the events hub for the /api/ws endpoint.
func (s *Server) SetEventsHub(hub *EventsHub) {
	s.eventsHub = hub
}

// EventsHub returns the configured events hub, or nil if not set.
func (s *Server) EventsHub() *EventsHub {
	return s.eventsHub
}

// SetMemoryIndexer configures the memory indexer for the /api/memory/* endpoints.
func (s *Server) SetMemoryIndexer(idx MemoryIndexer) {
	s.memoryIndexer = idx
}

// SetLoopDir sets the loop directory used for fallback work dir resolution.
func (s *Server) SetLoopDir(dir string) {
	s.loopDir = dir
}

// SetRunningChannelLister configures the running channel lister for the channel list endpoint.
func (s *Server) SetRunningChannelLister(lister RunningChannelLister) {
	s.runningChLister = lister
}

// SetActiveChatLister configures the active chat lister for the channel list endpoint.
func (s *Server) SetActiveChatLister(lister ActiveChatLister) {
	s.activeChatLister = lister
}

// SetIncomingMessageHandler configures the handler for user messages from the API.
func (s *Server) SetIncomingMessageHandler(h IncomingMessageHandler) {
	s.msgHandler = h
}

// SetInteractionHandler configures the handler for slash command interactions.
func (s *Server) SetInteractionHandler(h InteractionHandler) {
	s.interactionHandler = h
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

// Start starts the HTTP server on the given address.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels", s.handleSearchChannels)
	mux.HandleFunc("POST /api/channels", s.handleEnsureChannel)
	mux.HandleFunc("POST /api/channels/create", s.handleCreateChannel)
	mux.HandleFunc("POST /api/channels/ensure-all", s.handleEnsureAllChannels)
	mux.HandleFunc("POST /api/messages", s.handleSendMessage)
	mux.HandleFunc("POST /api/threads", s.handleCreateThread)
	mux.HandleFunc("DELETE /api/threads/{id}", s.handleDeleteThread)
	mux.HandleFunc("DELETE /api/channels/{id}", s.handleDeleteChannel)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("GET /api/channels/{id}/messages", s.handleListMessages)
	mux.HandleFunc("POST /api/commands", s.handleCommand)
	mux.HandleFunc("POST /api/memory/search", s.handleMemorySearch)
	mux.HandleFunc("POST /api/memory/index", s.handleMemoryIndex)
	mux.HandleFunc("GET /api/readme", s.handleGetReadme)
	mux.HandleFunc("GET /api/channels/{id}/diff", s.handleGitDiff)
	mux.HandleFunc("GET /api/ws/terminal", s.handleTerminalWS)
	mux.HandleFunc("GET /api/ws", s.handleEventsWS)

	s.server = &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(mux),
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

// corsMiddleware adds CORS headers to all responses, allowing the
// Electron desktop app (or any local client) to call the API.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
