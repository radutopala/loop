package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/osutil"
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
	HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string)
	HandleThreadCreated(ctx context.Context, threadID, authorID, message string)
}

// serverSystem abstracts OS operations needed by Server.
type serverSystem interface {
	Stat(name string) (os.FileInfo, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadDir(name string) ([]fs.DirEntry, error)
	Remove(name string) error
	MkdirAll(path string, perm os.FileMode) error
	UserHomeDir() (string, error)
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
	hostTermManager    TerminalManager
	containerFinder    ContainerFinder
	containerStopper   ContainerStopper
	browserManager     BrowserManager
	browserCDPFactory  func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...browser.CDPOption) (browserCDPClient, error)
	browserCDPRetries  int
	browserCDPDelay    time.Duration
	browserCaptures    map[string]*browserCaptureState // channelID -> state
	browserCapturesMu  sync.Mutex
	cmdBuilder         InteractiveCmdBuilder
	runningChLister    RunningChannelLister
	activeChatLister   ActiveChatLister
	msgHandler         IncomingMessageHandler
	interactionHandler InteractionHandler
	eventsHub          *EventsHub
	loopDir            string
	screenshotDir      string // if set, write screenshots to this dir instead of base64
	logger             *slog.Logger
	server             *http.Server
	listener           net.Listener
	stopErr            error // if set, Stop returns this error (for testing)
	sys                serverSystem
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

// SetScreenshotDir sets the directory for file-based screenshots.
// When set, screenshots are written as files instead of base64-encoded in JSON.
func (s *Server) SetScreenshotDir(dir string) {
	s.screenshotDir = dir
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
		sys:       osutil.RealSystem{},
	}
}

// buildMux creates the HTTP mux with all API route registrations.
func (s *Server) buildMux() *http.ServeMux {
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
	mux.HandleFunc("GET /api/messages/search", s.handleSearchMessages)
	mux.HandleFunc("POST /api/commands", s.handleCommand)
	mux.HandleFunc("POST /api/memory/search", s.handleMemorySearch)
	mux.HandleFunc("POST /api/memory/index", s.handleMemoryIndex)
	mux.HandleFunc("GET /api/memory/files", s.handleListMemoryFiles)
	mux.HandleFunc("GET /api/memory/files/search", s.handleSearchMemoryFiles)
	mux.HandleFunc("GET /api/memory/file", s.handleReadMemoryFile)
	mux.HandleFunc("PUT /api/memory/file", s.handleWriteMemoryFile)
	mux.HandleFunc("GET /api/readme", s.handleGetReadme)
	mux.HandleFunc("GET /api/channels/{id}/files", s.handleListFiles)
	mux.HandleFunc("GET /api/channels/{id}/file", s.handleReadFile)
	mux.HandleFunc("PUT /api/channels/{id}/file", s.handleWriteFile)
	mux.HandleFunc("DELETE /api/channels/{id}/file", s.handleDeleteFile)
	mux.HandleFunc("GET /api/channels/{id}/diff", s.handleGitDiff)
	mux.HandleFunc("GET /api/channels/{id}/branches", s.handleListBranches)
	mux.HandleFunc("POST /api/channels/{id}/branches/switch", s.handleSwitchBranch)
	mux.HandleFunc("POST /api/channels/{id}/branches/create", s.handleCreateBranch)
	mux.HandleFunc("POST /api/worktrees", s.handleCreateWorktree)
	mux.HandleFunc("POST /api/worktrees/import", s.handleImportWorktree)
	mux.HandleFunc("POST /api/browser/action", s.handleBrowserAction)
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("GET /api/ws/terminal", s.handleTerminalWS)
	mux.HandleFunc("GET /api/ws/browser", s.handleBrowserWS)
	mux.HandleFunc("GET /api/ws", s.handleEventsWS)
	return mux
}

// Start starts the HTTP server on the given address.
func (s *Server) Start(addr string) error {
	mux := s.buildMux()

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

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
}

// corsMiddleware adds CORS headers to all responses, allowing the
// Electron desktop app (or any local client) to call the API.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SetStopError sets a fixed error that Stop will return instead of calling Shutdown.
// This is intended for testing shutdown error handling in callers.
func (s *Server) SetStopError(err error) {
	s.stopErr = err
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.stopErr != nil {
		// Still perform the real shutdown, but return the injected error.
		if s.server != nil {
			_ = s.server.Shutdown(ctx)
		}
		return s.stopErr
	}
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
