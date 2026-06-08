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
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/review"
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
	FindOrCreateShell(ctx context.Context, channelID, dirPath, parentDirPath string) (string, error)
}

// ActiveChatLister returns channel IDs with active chat agent runs.
type ActiveChatLister interface {
	ActiveChatChannelIDs() map[string]struct{}
}

// IncomingMessageHandler processes a user message from the API, routing it
// through the orchestrator so Claude can respond.
type IncomingMessageHandler interface {
	HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string)
	HandleIncomingMessageWithPriority(ctx context.Context, channelID, authorID, content, mode string, priority int)
	HandleThreadCreated(ctx context.Context, threadID, authorID, message string)
}

// RunCanceller cancels the active agent run for a channel.
type RunCanceller interface {
	CancelActiveRun(channelID string) bool
}

// PlanResolver clears and resumes a channel parked on an ExitPlanMode card.
// ClearPlannedChannel removes the pause flag set by the orchestrator when the
// agent emitted ExitPlanMode; ResumeChannel kicks the drain so any queued
// rows can now be claimed.
type PlanResolver interface {
	ClearPlannedChannel(channelID string)
	ResumeChannel(ctx context.Context, channelID string)
}

// AskResolver clears and resumes a channel parked on an AskUserQuestion card.
// ClearAskedChannel removes the pause flag set by the orchestrator when the
// agent emitted AskUserQuestion; ResumeChannel kicks the drain so any queued
// rows can now be claimed.
type AskResolver interface {
	ClearAskedChannel(channelID string)
	ResumeChannel(ctx context.Context, channelID string)
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
	EvalSymlinks(path string) (string, error)
	WalkDir(root string, fn fs.WalkDirFunc) error
	// Rename is forwarded to fsmigrate's atomicWriteConfig so the
	// /api/builtins/restore handler can write config.json crash-safely.
	Rename(oldpath, newpath string) error
}

// Server exposes a lightweight HTTP API for task CRUD operations.
type Server struct {
	scheduler                 scheduler.Scheduler
	channels                  ChannelEnsurer
	threads                   ThreadEnsurer
	store                     ChannelLister
	messages                  MessageSender
	memoryIndexer             MemoryIndexer
	termManager               TerminalManager
	hostTermManager           TerminalManager
	dockerBrowserProvider     BrowserProvider
	hostBrowserProvider       BrowserProvider                // for host Chrome mode
	activeBrowserMode         map[string]string              // channelID -> "docker"|"host"; nil defaults to docker
	browserModeMu             sync.Mutex                     // protects activeBrowserMode
	cdpManagers               map[string]*browser.CDPManager // "channelID|mode" -> CDPManager
	cdpManagersMu             sync.Mutex
	browserCaptures           map[string]*browser.CaptureState // channelID -> state
	browserCapturesMu         sync.Mutex
	cmdBuilder                InteractiveCmdBuilder
	containerRegistry         ContainerManager
	activeChatLister          ActiveChatLister
	msgHandler                IncomingMessageHandler
	runCanceller              RunCanceller
	planResolver              PlanResolver
	askResolver               AskResolver
	interactionHandler        InteractionHandler
	agentRegistry             *agentregistry.Registry
	eventsHub                 *EventsHub
	imageManager              ImageManager
	browserKeepAlive          time.Duration // delay before removing idle browser containers
	loopDir                   string
	screenshotDir             string // if set, write screenshots to this dir instead of base64
	logger                    *slog.Logger
	server                    *http.Server
	listener                  net.Listener
	stopErr                   error             // if set, Stop returns this error (for testing)
	agentWSWriteJSON          func(v any) error // injectable for testing agent-channel WS write errors
	workflowEngine            WorkflowEngine
	worktreeCreator           *worktree.Creator
	sys                       serverSystem
	loadConfig                func() (*config.Config, error)                               // injectable for testing
	loadProjectConfig         func(string, *config.Config) (*config.Config, error)         // injectable for testing
	loadWorktreeProjectConfig func(string, string, *config.Config) (*config.Config, error) // injectable for testing
	readFile                  func(string) ([]byte, error)                                 // injectable for testing
	ticketStoreOpener         func(dir string) TicketStore                                 // injectable for testing
	approvalResolver          bot.ApprovalResolver                                         // gate approval dispatcher
	containerApprovalRouter   ContainerApprovalRouter                                      // per-container bearer-token → Manager lookup
	pendingApprovals          PendingApprovalLister                                        // snapshot of in-flight approvals for FE rehydration
	pendingAsks               PendingAsksLister                                            // snapshot of parked AskUserQuestion cards for FE rehydration
	pendingPlans              PendingPlansLister                                           // snapshot of parked ExitPlanMode cards for FE rehydration
	auditDirResolver          AuditDirResolver                                             // per-channel host path to the gate audit jsonl dir
	githubLookup              GitHubLookup                                                 // resolves PR for a channel's branch via `gh`
	reviewClient              GitHubReview                                                 // gh ops for review panel (fetch diff, post comment)
	reviewStore               *review.Store                                                // per-channel review session state
	reviewWorktree            review.PR                                                    // creates/removes PR worktrees
	reviewRunner              ReviewRunner                                                 // drives the agent for /review/run
	reviewSystemPrompt        string                                                       // resolved system prompt for review runs
	reviewPrompt              string                                                       // resolved user prompt for review runs ("" -> built-in default)
	reviewRunTimeout          time.Duration                                                // hard ceiling on runReviewAsync; 0 = unbounded (legacy)
	reviewMu                  sync.Mutex                                                   // guards reviewActive
	reviewActive              map[string]context.CancelFunc                                // per-channel in-flight review runs; cancel detaches the long-running agent ctx on session delete / shutdown

	// Quality-scan wiring. All fields are nil by default — handlers
	// return 501 until the daemon wires concrete implementations. Tests
	// can opt-in via the Set*Quality* setters without spinning up a real
	// engine.
	qualityScanner    QualityScanner
	qualityGraph      QualityGraphProvider
	qualitySnapshots  QualitySnapshotReader
	qualityRulesLoad  QualityRulesLoader
	qualityMetricsCfg QualityMetricsLoader
	qualityHistory    QualityHistoryReader
	qualityMu         sync.Mutex
	qualityCancellers map[string]context.CancelFunc
	qualityProgress   map[string]time.Time // per-channel throttle for quality.scan_progress
}

// AuditDirResolver maps a channel ID to the host directory that backs the
// in-container /var/log/loop-gate bind — where the agentgate FileAuditor
// writes its rotating jsonl files. Returns "" when the gate is disabled
// (no audit dir yet). Typically satisfied by *container.DockerRunner.
type AuditDirResolver interface {
	AuditDir(channelID string) string
}

// SetEventsHub configures the events hub for the /api/ws endpoint.
func (s *Server) SetEventsHub(hub *EventsHub) {
	s.eventsHub = hub
}

// EventsHub returns the configured events hub, or nil if not set.
func (s *Server) EventsHub() *EventsHub {
	return s.eventsHub
}

// SetRunCanceller configures the run canceller for interrupt-mode sends.
func (s *Server) SetRunCanceller(rc RunCanceller) {
	s.runCanceller = rc
}

// SetPlanResolver configures the plan-pause resolver used by
// /api/channels/{id}/plan/resolve.
func (s *Server) SetPlanResolver(pr PlanResolver) {
	s.planResolver = pr
}

// SetAskResolver configures the ask-pause resolver used by
// /api/channels/{id}/ask/resolve.
func (s *Server) SetAskResolver(ar AskResolver) {
	s.askResolver = ar
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

// SetApprovalResolver wires the gate approval resolver used by the local
// /api/gate/approvals/{id} endpoint. Typically backed by
// agentgate.MultiManagerResolver so a single resolver multiplexes clicks
// across all per-container Managers.
func (s *Server) SetApprovalResolver(r bot.ApprovalResolver) {
	s.approvalResolver = r
}

// SetContainerApprovalRouter wires the bearer-token → Manager router used by
// the /api/gate/container-approval endpoint. Typically backed by
// agentgate.MultiManagerResolver.
func (s *Server) SetContainerApprovalRouter(r ContainerApprovalRouter) {
	s.containerApprovalRouter = r
}

// SetPendingApprovalLister wires the snapshot source used by
// GET /api/gate/approvals. Typically backed by agentgate.MultiManagerResolver.
func (s *Server) SetPendingApprovalLister(l PendingApprovalLister) {
	s.pendingApprovals = l
}

// SetPendingAsksLister wires the snapshot source used by
// GET /api/asks/pending. Typically backed by *orchestrator.Orchestrator.
func (s *Server) SetPendingAsksLister(l PendingAsksLister) {
	s.pendingAsks = l
}

// SetPendingPlansLister wires the snapshot source used by
// GET /api/plans/pending. Typically backed by *orchestrator.Orchestrator.
func (s *Server) SetPendingPlansLister(l PendingPlansLister) {
	s.pendingPlans = l
}

// SetAuditDirResolver wires the per-channel gate-audit-dir resolver used by
// /api/channels/{id}/audit endpoints. Typically backed by *container.DockerRunner.
func (s *Server) SetAuditDirResolver(r AuditDirResolver) {
	s.auditDirResolver = r
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
	mux.HandleFunc("DELETE /api/messages/{id}", s.handleDeleteQueuedMessage)
	mux.HandleFunc("POST /api/threads", s.handleCreateThread)
	mux.HandleFunc("DELETE /api/threads/{id}", s.handleDeleteThread)
	mux.HandleFunc("DELETE /api/channels/{id}", s.handleDeleteChannel)
	mux.HandleFunc("PATCH /api/channels/{id}/lock", s.handleSetChannelLocked)
	mux.HandleFunc("POST /api/channels/{id}/plan/resolve", s.handlePlanResolve)
	mux.HandleFunc("POST /api/channels/{id}/ask/resolve", s.handleAskResolve)
	mux.HandleFunc("GET /api/asks/pending", s.handleListPendingAsks)
	mux.HandleFunc("GET /api/plans/pending", s.handleListPendingPlans)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("GET /api/tasks/{id}/runs", s.handleListTaskRuns)
	mux.HandleFunc("POST /api/tasks/{id}/run", s.handleRunTask)
	mux.HandleFunc("GET /api/shortcuts", s.handleListShortcuts)
	mux.HandleFunc("POST /api/shortcuts", s.handleModifyShortcut)
	mux.HandleFunc("GET /api/bash-shortcuts", s.handleListBashShortcuts)
	mux.HandleFunc("POST /api/bash-shortcuts", s.handleModifyBashShortcut)
	mux.HandleFunc("GET /api/channels/{id}/sessions", s.handleListSessions)
	mux.HandleFunc("GET /api/channels/{id}/audit", s.handleListAuditFiles)
	mux.HandleFunc("DELETE /api/channels/{id}/audit/{date}", s.handleDeleteAuditFile)
	mux.HandleFunc("GET /api/channels/{id}/messages", s.handleListMessages)
	mux.HandleFunc("GET /api/channels/{id}/queued", s.handleListQueuedMessages)
	mux.HandleFunc("POST /api/channels/{id}/queued/reorder", s.handleReorderQueuedMessages)
	mux.HandleFunc("GET /api/channels/{id}/timeline", s.handleTimeline)
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
	mux.HandleFunc("GET /api/channels/{id}/files/search", s.handleSearchFiles)
	mux.HandleFunc("GET /api/channels/{id}/file", s.handleReadFile)
	mux.HandleFunc("PUT /api/channels/{id}/file", s.handleWriteFile)
	mux.HandleFunc("DELETE /api/channels/{id}/file", s.handleDeleteFile)
	mux.HandleFunc("POST /api/channels/{id}/files/exists", s.handleFilesExists)
	mux.HandleFunc("POST /api/channels/{id}/dir", s.handleCreateDir)
	mux.HandleFunc("POST /api/channels/{id}/paste-image", s.handlePasteImage)
	mux.HandleFunc("GET /api/channels/{id}/diff", s.handleGitDiff)
	mux.HandleFunc("GET /api/channels/{id}/pr", s.handleChannelPR)
	mux.HandleFunc("GET /api/review/sessions", s.handleReviewSessions)
	mux.HandleFunc("POST /api/channels/{id}/review/load", s.handleReviewLoad)
	mux.HandleFunc("POST /api/channels/{id}/review/sync", s.handleReviewSync)
	mux.HandleFunc("GET /api/channels/{id}/review/prs", s.handleReviewListPRs)
	mux.HandleFunc("GET /api/channels/{id}/review", s.handleReviewGet)
	mux.HandleFunc("DELETE /api/channels/{id}/review", s.handleReviewDelete)
	mux.HandleFunc("POST /api/channels/{id}/review/run", s.handleReviewRun)
	mux.HandleFunc("POST /api/channels/{id}/review/comments/{cid}/push", s.handleReviewPushComment)
	mux.HandleFunc("DELETE /api/channels/{id}/review/comments/{cid}", s.handleReviewDeleteComment)
	mux.HandleFunc("POST /api/channels/{id}/review/push-all", s.handleReviewPushAll)
	mux.HandleFunc("GET /api/channels/{id}/branches", s.handleListBranches)
	mux.HandleFunc("GET /api/channels/{id}/commits", s.handleListCommits)
	mux.HandleFunc("POST /api/channels/{id}/branches/switch", s.handleSwitchBranch)
	mux.HandleFunc("POST /api/channels/{id}/branches/create", s.handleCreateBranch)
	mux.HandleFunc("DELETE /api/channels/{id}/branches", s.handleDeleteBranch)
	mux.HandleFunc("GET /api/tickets", s.handleListTickets)
	mux.HandleFunc("GET /api/tickets/{id}", s.handleGetTicket)
	mux.HandleFunc("POST /api/tickets", s.handleCreateTicket)
	mux.HandleFunc("PATCH /api/tickets/{id}", s.handleUpdateTicket)
	mux.HandleFunc("DELETE /api/tickets/{id}", s.handleDeleteTicket)
	mux.HandleFunc("POST /api/tickets/{id}/assign", s.handleAssignTicket)
	mux.HandleFunc("POST /api/worktrees", s.handleCreateWorktree)
	mux.HandleFunc("POST /api/worktrees/import", s.handleImportWorktree)
	mux.HandleFunc("DELETE /api/worktrees", s.handleRemoveWorktree)
	mux.HandleFunc("POST /api/worktrees/lock", s.handleSetWorktreeLocked)
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
	mux.HandleFunc("POST /api/workflows/runs", s.handleStartWorkflowRun)
	mux.HandleFunc("GET /api/workflows/runs", s.handleListWorkflowRuns)
	mux.HandleFunc("GET /api/workflows/runs/{id}", s.handleGetWorkflowRun)
	mux.HandleFunc("POST /api/workflows/runs/{id}/cancel", s.handleCancelWorkflowRun)
	mux.HandleFunc("DELETE /api/workflows/runs/{id}", s.handleDeleteWorkflowRun)
	mux.HandleFunc("POST /api/workflows/runs/{id}/retry", s.handleRetryWorkflowRun)
	mux.HandleFunc("POST /api/workflows/runs/{id}/resume", s.handleResumeWorkflowRun)
	mux.HandleFunc("GET /api/gate/approvals", s.handleListGateApprovals)
	mux.HandleFunc("POST /api/gate/approvals/{id}", s.handleResolveGateApproval)
	mux.HandleFunc("POST /api/gate/container-approval", s.handleContainerApproval)
	mux.HandleFunc("GET /api/workflows", s.handleListWorkflows)
	mux.HandleFunc("POST /api/workflows", s.handleModifyWorkflow)
	mux.HandleFunc("POST /api/builtins/restore", s.handleRestoreBuiltins)
	mux.HandleFunc("POST /api/channels/{id}/quality/scan", s.handleQualityScan)
	mux.HandleFunc("DELETE /api/channels/{id}/quality/scan", s.handleQualityScanCancel)
	mux.HandleFunc("GET /api/channels/{id}/quality/snapshot", s.handleQualitySnapshot)
	mux.HandleFunc("GET /api/channels/{id}/quality/cycles", s.handleQualityCycles)
	mux.HandleFunc("GET /api/channels/{id}/quality/metrics", s.handleQualityMetrics)
	mux.HandleFunc("GET /api/channels/{id}/quality/diagnostics", s.handleQualityDiagnostics)
	mux.HandleFunc("GET /api/channels/{id}/quality/rules", s.handleQualityRules)
	mux.HandleFunc("POST /api/channels/{id}/quality/whatif", s.handleQualityWhatif)
	mux.HandleFunc("GET /api/channels/{id}/quality/evolution", s.handleQualityEvolution)
	mux.HandleFunc("GET /api/channels/{id}/quality/c4", s.handleQualityC4)
	mux.HandleFunc("GET /api/channels/{id}/quality/bugfactor", s.handleQualityBugFactor)
	mux.HandleFunc("GET /api/channels/{id}/quality/complexity", s.handleQualityComplexity)
	mux.HandleFunc("GET /api/channels/{id}/quality/clones", s.handleQualityClones)
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
	// Cancel every in-flight review run before draining the HTTP server.
	// Otherwise their agent containers (typically 5–20 min runs) outlive
	// the daemon process and keep consuming the Docker socket + LLM quota
	// with nothing left to consume their output.
	s.cancelAllReviewRuns()
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
