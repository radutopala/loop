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

	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/worktree"
)

// ContainerManager provides container registry operations for the API server:
// listing, lifecycle management (find-or-create, remove).
type ContainerManager interface {
	List() []*container.ContainerInfo
	ListByChannel(channelID string) []*container.ContainerInfo
	RunningChannelIDs(ctx context.Context) map[string]struct{}
	RemoveContainer(ctx context.Context, containerID string) error
	ScheduleRemove(containerID string, delay time.Duration)
	FindOrCreateShell(ctx context.Context, channelID, dirPath string) (string, error)
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
	RemoveAll(path string) error
	MkdirAll(path string, perm os.FileMode) error
	UserHomeDir() (string, error)
	Open(name string) (*os.File, error)
}

// Server exposes a lightweight HTTP API for task CRUD operations.
type Server struct {
	scheduler             scheduler.Scheduler
	channels              ChannelEnsurer
	threads               ThreadEnsurer
	store                 ChannelLister
	messages              MessageSender
	memoryIndexer         MemoryIndexer
	termManager           TerminalManager
	hostTermManager       TerminalManager
	dockerBrowserProvider BrowserProvider
	hostBrowserProvider   BrowserProvider                // for host Chrome mode
	activeBrowserMode     map[string]string              // channelID -> "docker"|"host"; nil defaults to docker
	browserModeMu         sync.Mutex                     // protects activeBrowserMode
	cdpManagers           map[string]*browser.CDPManager // "channelID|mode" -> CDPManager
	cdpManagersMu         sync.Mutex
	browserCaptures       map[string]*browser.CaptureState // channelID -> state
	browserCapturesMu     sync.Mutex
	cmdBuilder            InteractiveCmdBuilder
	containerRegistry     ContainerManager
	activeChatLister      ActiveChatLister
	msgHandler            IncomingMessageHandler
	interactionHandler    InteractionHandler
	agentRegistry         *agentregistry.Registry
	eventsHub             *EventsHub
	imageManager          ImageManager
	browserKeepAlive      time.Duration // delay before removing idle browser containers
	loopDir               string
	screenshotDir         string // if set, write screenshots to this dir instead of base64
	logger                *slog.Logger
	server                *http.Server
	listener              net.Listener
	stopErr               error             // if set, Stop returns this error (for testing)
	agentWSWriteJSON      func(v any) error // injectable for testing agent-channel WS write errors
	worktreeCreator       *worktree.Creator
	sys                   serverSystem
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

// BrowserCleaner stops all browser sessions. Implemented by DockerProvider.
type BrowserCleaner interface {
	Cleanup(ctx context.Context)
}

// CleanupBrowsers stops all Docker browser containers during shutdown.
func (s *Server) CleanupBrowsers(ctx context.Context) {
	if c, ok := s.dockerBrowserProvider.(BrowserCleaner); ok {
		c.Cleanup(ctx)
	}
}

// SetContainerRegistry configures the container registry for the /api/containers endpoint.
func (s *Server) SetContainerRegistry(reg ContainerManager) {
	s.containerRegistry = reg
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

// SetBrowserKeepAlive sets the delay before idle browser containers are removed.
func (s *Server) SetBrowserKeepAlive(d time.Duration) {
	s.browserKeepAlive = d
}

// SetImageManager configures the image lifecycle manager for the /api/image/* endpoints.
func (s *Server) SetImageManager(im ImageManager) {
	s.imageManager = im
}

// NewServer creates a new API server. The channels, threads, store, and messages
// parameters may be nil if those features are not configured.
func NewServer(sched scheduler.Scheduler, channels ChannelEnsurer, threads ThreadEnsurer, store ChannelLister, messages MessageSender, logger *slog.Logger) *Server {
	sys := osutil.RealSystem{}
	return &Server{
		scheduler: sched,
		channels:  channels,
		threads:   threads,
		store:     store,
		messages:  messages,
		logger:    logger,
		worktreeCreator: &worktree.Creator{
			Sys: sys,
			Run: worktree.ExecCommandRunner,
		},
		sys: sys,
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
	mux.HandleFunc("GET /api/tasks/{id}/runs", s.handleListTaskRuns)
	mux.HandleFunc("GET /api/channels/{id}/sessions", s.handleListSessions)
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
	mux.HandleFunc("GET /api/channels/{id}/roots", s.handleListRoots)
	mux.HandleFunc("GET /api/channels/{id}/files", s.handleListFiles)
	mux.HandleFunc("GET /api/channels/{id}/file", s.handleReadFile)
	mux.HandleFunc("PUT /api/channels/{id}/file", s.handleWriteFile)
	mux.HandleFunc("DELETE /api/channels/{id}/file", s.handleDeleteFile)
	mux.HandleFunc("POST /api/channels/{id}/dir", s.handleCreateDir)
	mux.HandleFunc("GET /api/channels/{id}/diff", s.handleGitDiff)
	mux.HandleFunc("GET /api/channels/{id}/branches", s.handleListBranches)
	mux.HandleFunc("GET /api/channels/{id}/commits", s.handleListCommits)
	mux.HandleFunc("POST /api/channels/{id}/branches/switch", s.handleSwitchBranch)
	mux.HandleFunc("POST /api/channels/{id}/branches/create", s.handleCreateBranch)
	mux.HandleFunc("POST /api/worktrees", s.handleCreateWorktree)
	mux.HandleFunc("POST /api/worktrees/import", s.handleImportWorktree)
	mux.HandleFunc("POST /api/browser/action", s.handleBrowserAction)
	mux.HandleFunc("POST /api/browser/mode", s.handleBrowserMode)
	mux.HandleFunc("PUT /api/playground", s.handlePlaygroundUpdate)
	mux.HandleFunc("GET /api/playground", s.handlePlaygroundGet)
	mux.HandleFunc("DELETE /api/playground", s.handlePlaygroundDelete)
	mux.HandleFunc("GET /api/playground/items", s.handlePlaygroundList)
	mux.HandleFunc("PUT /api/playground/file", s.handlePlaygroundFileWrite)
	mux.HandleFunc("GET /api/playground/file", s.handlePlaygroundFileRead)
	mux.HandleFunc("DELETE /api/playground/file", s.handlePlaygroundFileDelete)
	mux.HandleFunc("GET /api/playground/files", s.handlePlaygroundFileList)
	mux.HandleFunc("GET /api/playground/serve/{name}", s.handlePlaygroundServe)
	mux.HandleFunc("GET /api/playground/serve/{name}/{path...}", s.handlePlaygroundServeFile)
	mux.HandleFunc("GET /api/playground/serve-project/{channel_id}/{name}", s.handlePlaygroundServeProject)
	mux.HandleFunc("GET /api/playground/serve-project/{channel_id}/{name}/{path...}", s.handlePlaygroundServeProjectFile)
	mux.HandleFunc("POST /api/agents", s.handleRegisterAgent)
	mux.HandleFunc("GET /api/agents", s.handleListAgents)
	mux.HandleFunc("PATCH /api/agents/{id}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", s.handleDeleteAgent)
	mux.HandleFunc("POST /api/agents/{id}/message", s.handleSendAgentMessage)
	mux.HandleFunc("GET /api/ws/agent-channel", s.handleAgentChannelWS)
	mux.HandleFunc("GET /api/image/status", s.handleImageStatus)
	mux.HandleFunc("POST /api/image/rebuild", s.handleImageRebuild)
	mux.HandleFunc("DELETE /api/image", s.handleImageRemove)
	mux.HandleFunc("GET /api/config/schema", s.handleConfigSchema)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handleSaveConfig)
	mux.HandleFunc("GET /api/config/project", s.handleGetProjectConfig)
	mux.HandleFunc("PUT /api/config/project", s.handleSaveProjectConfig)
	mux.HandleFunc("GET /api/containers", s.handleListContainers)
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
