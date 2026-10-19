package container

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/docker/docker/pkg/stdcopy"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

// A live session is one long-lived claude process in one container that
// takes a channel's chat turns as stream-json lines on its stdin. Unlike a
// one-shot run it outlives its turns, so background tasks and Monitor keep
// running after a result: when one ends, the process starts a turn by
// itself.
//
// Exactly one user line is written per turn, and only while no turn runs:
// the CLI merges the lines it gets during a turn into one turn with one
// result, which would leave a reader waiting for a result per line out of
// step for good.

// ErrNoLiveSession is returned for a channel with no live session.
var ErrNoLiveSession = errors.New("no live session")

// ErrNoLiveTask is returned by StopLiveTask for a task the live session
// doesn't run in the background.
var ErrNoLiveTask = errors.New("no such background task")

// ErrLiveSessionRunning is returned by StartLive when the channel already
// has a live session.
var ErrLiveSessionRunning = errors.New("a live session is already running")

// LiveControlError is what SendLiveControl returns when the process
// answers a control request with an error.
type LiveControlError struct {
	Message string
}

func (e *LiveControlError) Error() string { return e.Message }

// errLiveEnded is what a turn waiting on a live session gets when the
// session ends before it could run.
var errLiveEnded = errors.New("live session ended")

// liveInterruptGrace is how long a turn waits for its result after it was
// interrupted before it gives the process up.
const liveInterruptGrace = 30 * time.Second

// stopper is a timer that can be stopped; *time.Timer is one.
type stopper interface{ Stop() bool }

// liveStopTimeout bounds stopping a live session's container.
const liveStopTimeout = 15 * time.Second

// interruptLine is the stream-json control request that stops the running
// turn and keeps the process.
var interruptLine = []byte(`{"type":"control_request","request_id":"loop-interrupt","request":{"subtype":"interrupt"}}` + "\n")

// userTurnLine is the stream-json line of one user turn. Its content is a
// string, as a prompt's is in a one-shot run's transcript, so forks at the
// message find it there.
func userTurnLine(prompt string) []byte {
	line, _ := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": prompt},
	})
	return append(line, '\n')
}

// liveTurn is the turn a live session is running.
type liveTurn struct {
	cb streamCallbacks
	// result gets the result of a turn a user line started.
	result chan liveOutcome
	// done gets the outcome of a turn the process started by itself.
	done func(*agent.AgentResponse, error)
	// started is set once the turn's "init" arrived.
	started bool
}

type liveOutcome struct {
	resp *claudeResponse
	err  error
}

type liveSession struct {
	channelID     string
	containerID   string
	mcpConfigPath string
	keepMCP       bool
	startedAt     time.Time
	// fingerprint sums up what the process was started with (see
	// liveFingerprint).
	fingerprint string
	hooks       agent.LiveHooks
	// asked is the request the session was started for, before a missing
	// transcript turned it into a new session (see sessionMoved).
	asked *agent.AgentRequest
	// forkOf is the session the process forks, until it reports its own.
	forkOf string

	attach  *AttachStream
	stdout  *io.PipeReader
	writeMu sync.Mutex

	mu         sync.Mutex
	turn       *liveTurn
	idle       chan struct{} // closed when the running turn ends
	sessionID  string
	background []agent.BackgroundTask
	closed     bool
	// used is set once the session ran a turn.
	used      bool
	idleTimer stopper
	idleGen   uint64
	// controls are the control requests SendLiveControl waits on the
	// answers of, by id; controlSeq numbers them.
	controls   map[string]chan []byte
	controlSeq int
}

// write sends one line to the process.
func (s *liveSession) write(line []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.attach.Writer.Write(line)
	return err
}

func (s *liveSession) info() agent.LiveInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return agent.LiveInfo{
		ChannelID:       s.channelID,
		StartedAt:       s.startedAt,
		SessionID:       s.sessionID,
		Busy:            s.turn != nil,
		Background:      agent.BackgroundSummary(s.background),
		BackgroundTasks: s.background,
	}
}

// liveFingerprint sums up what a live process for req is started with that
// can change between turns: its claude command and its container's image,
// env, mounts, MCP servers and gates, as the channel's merged config gives
// them. A turn whose fingerprint differs from the process's restarts it.
// The session the command resumes is left out; see sessionMoved.
func (r *DockerRunner) liveFingerprint(req *agent.AgentRequest) (string, error) {
	cfg, err := r.runConfig(r.resolveWorkDir(req.ChannelID, req.DirPath), req.ParentDirPath)
	if err != nil {
		return "", err
	}
	base := *req
	base.SessionID, base.ForkSession, base.ResumeAt = "", false, ""
	sum := sha256.New()
	_ = json.NewEncoder(sum).Encode(struct {
		Cmd                                []string
		Image                              string
		Mounts, ExtraDirs, CopyFiles       []string
		Envs                               map[string]string
		Proxies                            config.ProxiesConfig
		MCPServers                         map[string]config.MCPServerConfig
		Memory, Browser                    bool
		Gates                              config.GatesConfig
		BaseURL, OAuthToken, APIKey, Model string
	}{
		buildLiveClaudeCmd(cfg, "", &base),
		cfg.ContainerImage,
		cfg.Mounts, cfg.ExtraDirs, cfg.CopyFiles,
		cfg.Envs,
		cfg.Proxies,
		cfg.MCPServers,
		cfg.Memory.Enabled, cfg.Browser.Enabled,
		cfg.Gates,
		cfg.AnthropicBaseURL, cfg.ClaudeCodeOAuthToken, cfg.AnthropicAPIKey, cfg.ClaudeModel,
	})
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// sessionMoved reports whether req asks for another session than the one
// the process is on: the channel was switched to another session, or set
// to fork, since the process started. Before the first turn the process is
// on what it was started from.
func (s *liveSession) sessionMoved(req *agent.AgentRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.used {
		return req.SessionID != s.asked.SessionID || req.ForkSession != s.asked.ForkSession || req.ResumeAt != s.asked.ResumeAt
	}
	return req.ForkSession || req.SessionID != s.sessionID
}

// ownSession is the session the process is on, "" while it is new or a
// fork that hasn't reported its own session yet: resuming the forked
// session instead would write to it. Call it with s.mu held.
func (s *liveSession) ownSession() string {
	if s.forkOf != "" && s.sessionID == s.forkOf {
		return ""
	}
	return s.sessionID
}

// StartLive starts a live session for req.ChannelID from req: a resume of
// req.SessionID, a fork of it with req.ForkSession (cut at req.ResumeAt),
// or a new session without one.
func (r *DockerRunner) StartLive(ctx context.Context, req *agent.AgentRequest, hooks agent.LiveHooks) error {
	r.liveMu.Lock()
	if _, ok := r.live[req.ChannelID]; ok || r.liveStarting[req.ChannelID] {
		r.liveMu.Unlock()
		return ErrLiveSessionRunning
	}
	// The container starts outside liveMu, as it can wait out an image
	// build; meanwhile the channel is marked starting, and other channels'
	// sessions go on.
	r.liveStarting[req.ChannelID] = true
	r.liveMu.Unlock()

	_, err := r.spawnLive(ctx, req, hooks, r.now())
	r.liveMu.Lock()
	delete(r.liveStarting, req.ChannelID)
	r.liveMu.Unlock()
	return err
}

// spawnLive starts a live session's container and its reader, and makes it
// the channel's session.
func (r *DockerRunner) spawnLive(ctx context.Context, req *agent.AgentRequest, hooks agent.LiveHooks, startedAt time.Time) (*liveSession, error) {
	asked := req
	if req.SessionID != "" && r.transcriptMissing(r.resolveWorkDir(req.ChannelID, req.DirPath), req.SessionID) {
		if r.logger != nil {
			r.logger.Warn("session transcript not found; starting a new live session",
				"channel_id", req.ChannelID, "session_id", req.SessionID)
		}
		fresh := *req
		fresh.SessionID = ""
		fresh.ForkSession = false
		fresh.ResumeAt = ""
		req = &fresh
	}
	fingerprint, err := r.liveFingerprint(req)
	if err != nil {
		return nil, fmt.Errorf("loading project config: %w", err)
	}
	containerID, ctrName, mcpConfigPath, keepMCP, err := r.createAndStartContainer(ctx, req.ChannelID, req.DirPath, req.AuthorID, req.ParentDirPath, req.AgentID, req.MCPArgs,
		ContainerTypeAgent, req.OnActivity,
		func(cfg *config.Config, mcpConfigPath string) []string {
			return buildLiveClaudeCmd(cfg, mcpConfigPath, req)
		}, true,
	)
	if containerID != "" && r.registry != nil {
		r.registry.Register(&ContainerInfo{
			ContainerID:   containerID,
			ChannelID:     req.ChannelID,
			Type:          ContainerTypeAgent,
			ContainerName: ctrName,
		})
	}
	dropContainer := func() {
		if mcpConfigPath != "" && !keepMCP {
			_ = r.sys.Remove(mcpConfigPath)
		}
		if containerID != "" && r.registry != nil {
			r.registry.ScheduleRemove(containerID, r.currentConfig().ContainerKeepAlive)
		}
	}
	if err != nil {
		dropContainer()
		return nil, err
	}
	attach, err := r.client.ContainerAttach(ctx, containerID)
	if err != nil {
		r.stopLiveContainer(containerID)
		dropContainer()
		return nil, fmt.Errorf("attaching to the live session: %w", err)
	}

	// The attach stream multiplexes stdout and stderr; the reader wants
	// stdout. Stderr is dropped, as a one-shot run's is.
	pr, pw := io.Pipe()
	go func() {
		_, copyErr := stdcopy.StdCopy(pw, io.Discard, attach.Reader)
		_ = pw.CloseWithError(copyErr)
	}()

	s := &liveSession{
		channelID:     req.ChannelID,
		containerID:   containerID,
		mcpConfigPath: mcpConfigPath,
		keepMCP:       keepMCP,
		startedAt:     startedAt,
		fingerprint:   fingerprint,
		hooks:         hooks,
		asked:         asked,
		attach:        attach,
		stdout:        pr,
		sessionID:     req.SessionID,
	}
	if req.ForkSession {
		s.forkOf = req.SessionID
	}
	// Registered before anything can end it, so its end forgets it.
	r.liveMu.Lock()
	r.live[req.ChannelID] = s
	r.liveMu.Unlock()
	s.mu.Lock()
	r.armLiveIdle(s)
	s.mu.Unlock()
	go r.readLive(s)
	if r.logger != nil {
		r.logger.Info("live session started", "channel_id", req.ChannelID, "container_id", containerID,
			"session_id", req.SessionID, "fork", req.ForkSession)
	}
	return s, nil
}

// liveSession returns the channel's live session, nil without one.
func (r *DockerRunner) liveSession(channelID string) *liveSession {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	return r.live[channelID]
}

// LiveInfo describes the channel's live session; ok is false without one.
func (r *DockerRunner) LiveInfo(channelID string) (agent.LiveInfo, bool) {
	s := r.liveSession(channelID)
	if s == nil {
		return agent.LiveInfo{}, false
	}
	return s.info(), true
}

// InterruptLive stops the turn the channel's live session runs, if any. The
// process lives on, unless a turn it started by itself is still running
// liveInterruptGrace later: nothing else waits for that turn's result, so
// a process that doesn't answer the interrupt would hold the channel.
func (r *DockerRunner) InterruptLive(channelID string) error {
	s := r.liveSession(channelID)
	if s == nil {
		return ErrNoLiveSession
	}
	if err := s.write(interruptLine); err != nil {
		return err
	}
	s.mu.Lock()
	turn := s.turn
	s.mu.Unlock()
	if turn == nil || turn.result != nil {
		return nil
	}
	r.afterFunc(liveInterruptGrace, func() {
		s.mu.Lock()
		stuck := s.turn == turn
		s.mu.Unlock()
		if stuck {
			r.endLive(s, "interrupt timed out")
			r.stopLiveContainer(s.containerID)
		}
	})
	return nil
}

// StopLiveTask stops the background task taskID of the channel's live
// session; the process reports the set without it once it stopped.
func (r *DockerRunner) StopLiveTask(channelID, taskID string) error {
	s := r.liveSession(channelID)
	if s == nil {
		return ErrNoLiveSession
	}
	s.mu.Lock()
	found := slices.ContainsFunc(s.background, func(t agent.BackgroundTask) bool { return t.ID == taskID })
	s.mu.Unlock()
	if !found {
		return ErrNoLiveTask
	}
	return s.write(stopTaskLine(taskID))
}

// stopTaskLine is the stream-json control request that stops the
// background task taskID.
func stopTaskLine(taskID string) []byte {
	line, _ := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": "loop-stop-task-" + taskID,
		"request":    map[string]any{"subtype": "stop_task", "task_id": taskID},
	})
	return append(line, '\n')
}

// LiveChannels lists the channels with a live session.
func (r *DockerRunner) LiveChannels() []string {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	ids := make([]string, 0, len(r.live))
	for id := range r.live {
		ids = append(ids, id)
	}
	return ids
}

// StopLive ends the channel's live session, telling hooks.Ended reason, and
// stops its container.
func (r *DockerRunner) StopLive(channelID, reason string) error {
	s := r.liveSession(channelID)
	if s == nil {
		return ErrNoLiveSession
	}
	r.endLive(s, reason)
	r.stopLiveContainer(s.containerID)
	return nil
}

// CloseLiveSessions ends every live session and stops their containers,
// for shutdown: a container is otherwise only removed by a timer that dies
// with the daemon, and would run on.
func (r *DockerRunner) CloseLiveSessions() {
	r.liveMu.Lock()
	sessions := make([]*liveSession, 0, len(r.live))
	for _, s := range r.live {
		sessions = append(sessions, s)
	}
	r.liveMu.Unlock()
	for _, s := range sessions {
		r.endLive(s, "shutdown")
		r.stopLiveContainer(s.containerID)
	}
}

func (r *DockerRunner) stopLiveContainer(containerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), liveStopTimeout)
	defer cancel()
	_ = r.client.ContainerStop(ctx, containerID)
}

// endLive forgets the session and drops its connection, which ends its
// reader and with it any turn still waiting. hooks.Ended gets reason, once.
func (r *DockerRunner) endLive(s *liveSession, reason string) {
	if !r.closeLive(s, reason) {
		return
	}
	if s.hooks.Ended != nil {
		s.hooks.Ended(reason)
	}
}

// closeLive is endLive without telling hooks.Ended; it reports whether the
// session was still open.
func (r *DockerRunner) closeLive(s *liveSession, reason string) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.closed = true
	s.idleGen++
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	for id, answer := range s.controls {
		close(answer)
		delete(s.controls, id)
	}
	s.mu.Unlock()

	r.liveMu.Lock()
	if r.live[s.channelID] == s {
		delete(r.live, s.channelID)
	}
	r.liveMu.Unlock()

	if r.logger != nil {
		r.logger.Info("live session ended", "channel_id", s.channelID, "container_id", s.containerID, "reason", reason)
	}
	s.attach.Close()
	_ = s.stdout.Close()
	if s.mcpConfigPath != "" && !s.keepMCP {
		_ = r.sys.Remove(s.mcpConfigPath)
	}
	if r.registry != nil {
		r.registry.ScheduleRemove(s.containerID, r.currentConfig().ContainerKeepAlive)
	}
	return true
}

// armLiveIdle (re)starts the countdown that ends an idle session. Call it
// with s.mu held.
func (r *DockerRunner) armLiveIdle(s *liveSession) {
	s.idleGen++
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	idle := r.currentConfig().LiveSessionIdle
	if idle <= 0 {
		return
	}
	gen := s.idleGen
	s.idleTimer = r.afterFunc(idle, func() { r.liveIdleFired(s, gen) })
}

// liveIdleFired ends the session unless it was used since, or still runs a
// turn or a background task, or is held (see HoldLive): those count as
// use, so the countdown restarts.
func (r *DockerRunner) liveIdleFired(s *liveSession, gen uint64) {
	s.mu.Lock()
	if s.closed || s.idleGen != gen {
		s.mu.Unlock()
		return
	}
	if s.turn != nil || len(s.background) > 0 || r.liveHeld(s.channelID) {
		r.armLiveIdle(s)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	r.endLive(s, "idle")
	r.stopLiveContainer(s.containerID)
}

// runLive runs req as a turn of the channel's live session. ran is false
// when the channel has none, or when the session couldn't take the turn
// before it was given; the caller then runs req as usual.
func (r *DockerRunner) runLive(ctx context.Context, req *agent.AgentRequest) (resp *agent.AgentResponse, ran bool, err error) {
	s := r.liveSession(req.ChannelID)
	if s == nil {
		return nil, false, nil
	}
	if s.sessionMoved(req) {
		s, err = r.restartLive(ctx, s, req, "session changed")
	} else if fingerprint, fpErr := r.liveFingerprint(req); fpErr == nil && fingerprint != s.fingerprint {
		// The new process goes on with the session this one is on, or is
		// started as this one was while it has none of its own.
		s.mu.Lock()
		sessionID := s.ownSession()
		s.mu.Unlock()
		next := *req
		if sessionID != "" {
			next.SessionID, next.ForkSession, next.ResumeAt = sessionID, false, ""
		}
		s, err = r.restartLive(ctx, s, &next, "config changed")
	}
	if errors.Is(err, errLiveEnded) {
		// The session ended meanwhile.
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}

	turn := &liveTurn{
		cb:     requestCallbacks(req),
		result: make(chan liveOutcome, 1),
	}
	if err := r.claimLiveTurn(ctx, s, turn); err != nil {
		if errors.Is(err, errLiveEnded) {
			return nil, false, nil
		}
		return nil, true, err
	}

	prompt := req.Prompt
	if prompt == "" && len(req.Messages) > 0 {
		prompt = req.Messages[len(req.Messages)-1].Content
	}
	if req.PlanMode {
		prompt = planModePromptPrefix + prompt
	}
	if err := s.write(userTurnLine(prompt)); err != nil {
		// The process is gone. The turn runs as usual, resuming the session
		// the process is on, or as asked while it has none of its own.
		r.endLive(s, "stdin closed")
		s.mu.Lock()
		sessionID := s.ownSession()
		s.mu.Unlock()
		if sessionID != "" {
			req.SessionID = sessionID
			req.ForkSession = false
			req.ResumeAt = ""
		}
		return nil, false, nil
	}

	resp, err = liveResponse(ctx, r.awaitLiveTurn(ctx, s, turn))
	return resp, true, err
}

// claimLiveTurn makes turn the session's running turn, after the turn the
// process may have started by itself ends. Cancelling ctx while it waits
// interrupts that turn.
func (r *DockerRunner) claimLiveTurn(ctx context.Context, s *liveSession, turn *liveTurn) error {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return errLiveEnded
		}
		if s.turn == nil {
			s.turn = turn
			s.used = true
			s.idle = make(chan struct{})
			s.idleGen++ // a turn is use
			s.mu.Unlock()
			return nil
		}
		idle := s.idle
		s.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			_ = s.write(interruptLine)
			return ctx.Err()
		}
	}
}

// awaitLiveTurn waits for the turn's result. Cancelling ctx interrupts the
// turn; the result of an interrupted turn still comes, unless the process
// is stuck, which ends the session.
func (r *DockerRunner) awaitLiveTurn(ctx context.Context, s *liveSession, turn *liveTurn) liveOutcome {
	select {
	case out := <-turn.result:
		return out
	case <-ctx.Done():
	}
	_ = s.write(interruptLine)
	grace := r.afterFunc(liveInterruptGrace, func() {
		r.endLive(s, "interrupt timed out")
		r.stopLiveContainer(s.containerID)
	})
	defer grace.Stop()
	return <-turn.result
}

// liveResponse is runOnce's answer for a live turn's outcome.
func liveResponse(ctx context.Context, out liveOutcome) (*agent.AgentResponse, error) {
	if out.err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("live session: %w", out.err)
	}
	res := out.resp
	if ctx.Err() != nil {
		return &agent.AgentResponse{SessionID: res.SessionID}, ctx.Err()
	}
	if res.IsError {
		errText := res.errorText()
		return &agent.AgentResponse{SessionID: res.SessionID, Error: errText}, fmt.Errorf("claude returned error: %s", errText)
	}
	return &agent.AgentResponse{
		Response:   res.Result,
		SessionID:  res.SessionID,
		DurationMs: res.DurationMs,
		NumTurns:   res.NumTurns,
		StopReason: res.StopReason,
		Model:      res.Model,
	}, nil
}

// restartLive replaces the session's process with one started from req,
// for reason.
func (r *DockerRunner) restartLive(ctx context.Context, s *liveSession, req *agent.AgentRequest, reason string) (*liveSession, error) {
	if !r.closeLive(s, reason) {
		return nil, errLiveEnded
	}
	r.stopLiveContainer(s.containerID)

	ns, err := r.spawnLive(ctx, req, s.hooks, s.startedAt)
	if err != nil {
		if s.hooks.Ended != nil {
			s.hooks.Ended("restart failed: " + err.Error())
		}
		return nil, err
	}
	if s.hooks.Restarted != nil {
		s.hooks.Restarted(reason)
	}
	return ns, nil
}

// readLive reads the session's stream until it ends, handing each event to
// the running turn, and each result to whoever waits for it.
func (r *DockerRunner) readLive(s *liveSession) {
	br := bufio.NewReaderSize(s.stdout, scannerBufInit)
	var st streamState
	cb := r.liveCallbacks(s)
	for {
		line, err := readLineOrSkip(br)
		if err != nil {
			r.liveStreamEnded(s, err)
			return
		}
		res := st.dispatch(line, cb)
		if res == nil || (res.flush() && !r.ownTurnRunning(s)) {
			continue
		}
		r.liveResult(s, res, &st)
	}
}

// ownTurnRunning reports whether the running turn is one the process
// started by itself. A flush result ends such a turn: the process opened it
// with an "init" while it drained a background-task notification, and
// nothing else will end it.
func (r *DockerRunner) ownTurnRunning(s *liveSession) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turn != nil && s.turn.result == nil
}

// liveCallbacks routes the stream's events to the running turn's callbacks;
// with no turn running, activity goes to hooks.Activity.
func (r *DockerRunner) liveCallbacks(s *liveSession) streamCallbacks {
	current := func() streamCallbacks {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.turn == nil {
			return streamCallbacks{}
		}
		return s.turn.cb
	}
	return streamCallbacks{
		onTurnStart: func() { r.liveTurnStarted(s) },
		onSystem: func(subtype string, line []byte) {
			if s.hooks.System != nil {
				s.hooks.System(subtype, line)
			}
		},
		onControlResponse: s.controlAnswered,
		onPrompt: func(text string) {
			if s.hooks.Prompt != nil {
				s.hooks.Prompt(text)
			}
		},
		onSession: func(id string) {
			s.mu.Lock()
			s.sessionID = id
			s.mu.Unlock()
			if c := current(); c.onSession != nil {
				c.onSession(id)
			}
		},
		onTurn: func(text string, ref agent.TurnRef) {
			if c := current(); c.onTurn != nil {
				c.onTurn(text, ref)
			}
		},
		onThinking: func(text string) {
			if c := current(); c.onThinking != nil {
				c.onThinking(text)
			}
		},
		onToolUse: func(id, name, input string) {
			if c := current(); c.onToolUse != nil {
				c.onToolUse(id, name, input)
			}
		},
		onToolUseRaw: func(id, name, raw string) {
			if c := current(); c.onToolUseRaw != nil {
				c.onToolUseRaw(id, name, raw)
			}
		},
		onToolResult: func(id, out string, isErr bool) {
			if c := current(); c.onToolResult != nil {
				c.onToolResult(id, out, isErr)
			}
		},
		onBackgroundTasks: func(tasks []agent.BackgroundTask) {
			s.mu.Lock()
			s.background = tasks
			turn := s.turn
			s.mu.Unlock()
			if turn != nil {
				if turn.cb.onBackgroundTasks != nil {
					turn.cb.onBackgroundTasks(tasks)
				}
				return
			}
			if s.hooks.BackgroundTasks != nil {
				s.hooks.BackgroundTasks(tasks)
			}
		},
		onActivity: func(activity, detail string) {
			s.mu.Lock()
			turn := s.turn
			s.mu.Unlock()
			if turn != nil {
				if turn.cb.onActivity != nil {
					turn.cb.onActivity(activity, detail)
				}
				return
			}
			if s.hooks.Activity != nil {
				s.hooks.Activity(activity, detail)
			}
		},
	}
}

// liveTurnStarted handles a turn's "init": the start of the turn a user
// line asked for, or of one the process starts by itself.
func (r *DockerRunner) liveTurnStarted(s *liveSession) {
	s.mu.Lock()
	if s.turn != nil {
		s.turn.started = true
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	r.beginOwnTurn(s, true)
}

// beginOwnTurn makes a turn the process started by itself the running one.
func (r *DockerRunner) beginOwnTurn(s *liveSession, started bool) *liveTurn {
	turn := &liveTurn{started: started}
	s.mu.Lock()
	s.used = true
	s.mu.Unlock()
	if s.hooks.BeginTurn != nil {
		// Without a request (the channel is busy) the turn goes unstreamed,
		// and done still gets its answer.
		req, done := s.hooks.BeginTurn()
		if req != nil {
			turn.cb = requestCallbacks(req)
		}
		turn.done = done
	}
	s.mu.Lock()
	s.turn = turn
	s.idle = make(chan struct{})
	s.idleGen++
	s.mu.Unlock()
	return turn
}

// liveResult ends the running turn with res. A result with no turn running
// ends a turn the process started without an "init".
func (r *DockerRunner) liveResult(s *liveSession, res *claudeResponse, st *streamState) {
	s.mu.Lock()
	turn := s.turn
	s.mu.Unlock()
	if turn == nil {
		turn = r.beginOwnTurn(s, true)
	}
	if res.SessionID != "" {
		s.mu.Lock()
		s.sessionID = res.SessionID
		s.mu.Unlock()
	}
	res.Output = st.tail.String()
	r.finishLiveTurn(s, turn, liveOutcome{resp: res})
}

// finishLiveTurn hands the turn its outcome and makes the session idle.
func (r *DockerRunner) finishLiveTurn(s *liveSession, turn *liveTurn, out liveOutcome) {
	s.mu.Lock()
	if s.turn == turn {
		s.turn = nil
		close(s.idle)
		if !s.closed {
			r.armLiveIdle(s)
		}
	}
	s.mu.Unlock()
	if turn.result != nil {
		turn.result <- out
		return
	}
	if turn.done != nil {
		if out.err != nil {
			turn.done(nil, out.err)
			return
		}
		turn.done(liveResponse(context.Background(), out))
	}
}

// liveStreamEnded ends the session when its stream does, and the running
// turn with it.
func (r *DockerRunner) liveStreamEnded(s *liveSession, err error) {
	s.mu.Lock()
	turn := s.turn
	s.mu.Unlock()
	reason := "exited"
	if err != io.EOF {
		reason = "exited: " + err.Error()
	}
	r.endLive(s, reason)
	if turn != nil {
		r.finishLiveTurn(s, turn, liveOutcome{err: errLiveEnded})
	}
}

// SendLiveControl writes request, a stream-json control request's body, to
// the channel's live session and returns the body of its answer. An answer
// that is an error is a *LiveControlError; the session ending first is
// ErrNoLiveSession.
func (r *DockerRunner) SendLiveControl(ctx context.Context, channelID string, request json.RawMessage) (json.RawMessage, error) {
	s := r.liveSession(channelID)
	if s == nil {
		return nil, ErrNoLiveSession
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrNoLiveSession
	}
	s.controlSeq++
	id := fmt.Sprintf("loop-control-%d", s.controlSeq)
	answer := make(chan []byte, 1)
	if s.controls == nil {
		s.controls = map[string]chan []byte{}
	}
	s.controls[id] = answer
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.controls, id)
		s.mu.Unlock()
	}()

	line, err := json.Marshal(struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}{"control_request", id, request})
	if err != nil {
		return nil, fmt.Errorf("encoding the control request: %w", err)
	}
	if err := s.write(append(line, '\n')); err != nil {
		return nil, fmt.Errorf("writing the control request: %w", err)
	}
	select {
	case got, ok := <-answer:
		if !ok {
			return nil, ErrNoLiveSession
		}
		return controlAnswer(got)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// controlAnswer is the body of a control_response line, or its error.
func controlAnswer(line []byte) (json.RawMessage, error) {
	var evt struct {
		Response struct {
			Subtype  string          `json:"subtype"`
			Response json.RawMessage `json:"response"`
			Error    string          `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(line, &evt); err != nil {
		return nil, fmt.Errorf("decoding the control response: %w", err)
	}
	if evt.Response.Subtype != "success" {
		msg := evt.Response.Error
		if msg == "" {
			msg = "control request failed"
		}
		return nil, &LiveControlError{Message: msg}
	}
	if len(evt.Response.Response) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return evt.Response.Response, nil
}

// controlAnswered hands a control_response line to the SendLiveControl
// waiting on it; answers no one waits on, such as an interrupt's, are
// dropped.
func (s *liveSession) controlAnswered(requestID string, line []byte) {
	s.mu.Lock()
	answer := s.controls[requestID]
	delete(s.controls, requestID)
	s.mu.Unlock()
	if answer != nil {
		answer <- line
	}
}

// HoldLive keeps the channel's live session from ending idle while holder
// holds it, or with on false lets go of holder's hold. A hold outlasts the
// session's process: one started while the channel is held is held too.
func (r *DockerRunner) HoldLive(channelID, holder string, on bool) {
	r.liveHoldMu.Lock()
	defer r.liveHoldMu.Unlock()
	if !on {
		delete(r.liveHolds[channelID], holder)
		if len(r.liveHolds[channelID]) == 0 {
			delete(r.liveHolds, channelID)
		}
		return
	}
	if r.liveHolds == nil {
		r.liveHolds = map[string]map[string]bool{}
	}
	if r.liveHolds[channelID] == nil {
		r.liveHolds[channelID] = map[string]bool{}
	}
	r.liveHolds[channelID][holder] = true
}

// LiveHolds returns the holders of each held channel, sorted.
func (r *DockerRunner) LiveHolds() map[string][]string {
	r.liveHoldMu.Lock()
	defer r.liveHoldMu.Unlock()
	out := make(map[string][]string, len(r.liveHolds))
	for channelID, holders := range r.liveHolds {
		for holder := range holders {
			out[channelID] = append(out[channelID], holder)
		}
		slices.Sort(out[channelID])
	}
	return out
}

func (r *DockerRunner) liveHeld(channelID string) bool {
	r.liveHoldMu.Lock()
	defer r.liveHoldMu.Unlock()
	return len(r.liveHolds[channelID]) > 0
}
