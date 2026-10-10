package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/terminal"
)

// Terminal WebSocket control message types (client → server).
const (
	wsMsgCreate = "create"
	wsMsgAttach = "attach"
	wsMsgInput  = "input"
	wsMsgResize = "resize"
	wsMsgStop   = "stop"
	wsMsgClose  = "close"
	wsMsgKill   = "kill"
)

// Terminal WebSocket status message types (server → client).
const (
	wsStatusCreated  = "created"
	wsStatusAttached = "attached"
	wsStatusError    = "error"
	wsStatusClosed   = "closed"
	wsStatusStopped  = "stopped"
)

// Terminal WebSocket error codes sent alongside error messages.
const (
	wsErrCodeInvalidJSON    = "invalid_json"
	wsErrCodeNoSession      = "no_session"
	wsErrCodeMissingField   = "missing_field"
	wsErrCodeInvalidInput   = "invalid_input"
	wsErrCodeSessionFailed  = "session_failed"
	wsErrCodeUnknownMessage = "unknown_message"
	// wsErrCodeSessionGone answers an attach to a session the server no
	// longer has, after a daemon restart say. The client starts a new one.
	wsErrCodeSessionGone = "session_gone"
)

// TerminalManager abstracts the terminal session operations needed by the handler.
type TerminalManager interface {
	CreateSession(ctx context.Context, containerID string, cmd []string) (sessionID string, output <-chan []byte, history []byte, done <-chan struct{}, err error)
	CreateSessionWithEnv(ctx context.Context, containerID string, cmd, env []string) (sessionID string, output <-chan []byte, history []byte, done <-chan struct{}, err error)
	AttachSession(sessionID string) (output <-chan []byte, history []byte, done <-chan struct{}, err error)
	DetachSession(sessionID string, output <-chan []byte) error
	SendInput(sessionID string, data []byte) error
	Resize(ctx context.Context, sessionID string, rows, cols uint) error
	StopSession(sessionID string) (containerID string, err error)
	KillProcessGroup(ctx context.Context, sessionID string) error
	// LiveSessions returns how many sessions in a container still run.
	LiveSessions(containerID string) int
}

// AgentTerminals records the terminal session an agent pane runs in.
type AgentTerminals interface {
	SetTerminal(channelID, agentID, sessionID string)
}

// wsControlMessage represents a JSON control message from the client.
type wsControlMessage struct {
	Type        string `json:"type"`
	ContainerID string `json:"container_id,omitempty"`
	ChannelID   string `json:"channel_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"` // e.g. "agent-0" — registers in agent registry
	// LeafID identifies a single terminal pane in the FE's tab tree. When
	// present on a create message it is stamped onto the exec as the
	// LOOP_TERMINAL_LEAF env var so the in-container dockerproxy can
	// attribute approval prompts back to this specific pane.
	LeafID     string   `json:"leaf_id,omitempty"`
	Cmd        []string `json:"cmd,omitempty"`
	SessionID  string   `json:"session_id,omitempty"`
	Data       string   `json:"data,omitempty"` // base64-encoded input
	Rows       uint     `json:"rows,omitempty"`
	Cols       uint     `json:"cols,omitempty"`
	Target     string   `json:"target,omitempty"` // "host" or "agent" (default)
	NewSession bool     `json:"new_session,omitempty"`
	// OpenMode explicitly selects how an agent terminal boots Claude relative
	// to the channel's stored session:
	//   "resume" — continue the channel session in place (no fork).
	//   "fork"   — resume the channel session and immediately fork to a new id.
	//   "fresh"  — start with no session at all.
	// When unset, falls back to the legacy NewSession/auto-fork behavior so
	// older clients (and non-agent terminals) keep working unchanged.
	OpenMode string `json:"open_mode,omitempty"`
	// RootIndex selects which workspace root (0 = primary dir, 1+ = extra_dirs)
	// a shell pane opens in. Resolved server-side against the channel's
	// authoritative root list; out-of-range or 0 falls back to the primary dir.
	// Only meaningful for host-shell / docker-shell panes.
	RootIndex int `json:"root_index,omitempty"`
}

// Valid OpenMode values. Anything else is treated as unset (legacy fallback).
const (
	openModeResume = "resume"
	openModeFork   = "fork"
	openModeFresh  = "fresh"
)

// wsStatusMessage represents a JSON status message sent to the client.
type wsStatusMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// InteractiveCmdBuilder builds the interactive Claude command for a terminal session.
type InteractiveCmdBuilder interface {
	BuildInteractiveCmd(channelID, dirPath, parentDirPath, sessionID, agentID string, forkSession bool) string
	// BuildContinueCmd builds the command that relaunches Claude after its
	// process exits unexpectedly (e.g. OOM-killed). It resumes sessionID when
	// that is set and its transcript exists, and otherwise falls back to
	// `claude --continue`, the most recently modified session for the working
	// directory (used when the pane forked or started a session whose id Loop
	// never learns).
	BuildContinueCmd(channelID, dirPath, parentDirPath, sessionID, agentID string) string
}

// terminalWSConn manages a single WebSocket terminal connection.
type terminalWSConn struct {
	conn              *websocket.Conn
	connWriteMessage  func(messageType int, data []byte) error
	manager           TerminalManager
	hostManager       TerminalManager  // may be nil
	containerRegistry ContainerManager // may be nil
	// releaseShell and claimShell are Server.releaseShell and
	// Server.claimShell. May be nil.
	releaseShell    func(containerID string)
	claimShell      func(containerID string)
	browserProvider BrowserProvider // may be nil
	// closeBrowserCDP drops the channel's cached browser CDPManager, which is
	// tied to the sidecar container this connection may destroy. May be nil.
	closeBrowserCDP func(channelID string)
	cmdBuilder      InteractiveCmdBuilder
	// agentTerminals records which terminal session runs each agent pane's
	// Claude, so messages to the agent can be typed into it. May be nil.
	agentTerminals AgentTerminals
	store          ChannelLister
	loopDir        string // fallback work dir root (e.g. ~/.loop)
	// rootDirs returns the channel's ordered workspace roots (index 0 = primary
	// dir, 1+ = extra_dirs). Used to resolve a shell pane's RootIndex to an
	// absolute path. May be nil (then RootIndex is ignored — primary dir only).
	rootDirs func(ctx context.Context, channelID string) ([]string, error)
	logger   *slog.Logger
	writeMu  sync.Mutex
	stopOnce sync.Once
	stopCh   chan struct{}

	sessionID     string
	outputCh      <-chan []byte
	sessionTarget string // "host" or "agent"
	stopOnClose   bool   // if true, stop (not just detach) the session on WS disconnect

	// sessionIDAtomic mirrors sessionID for the one cross-goroutine reader:
	// the delayed relaunch goroutine spawned by scanClaudeExit. sessionID
	// itself is otherwise only ever read/written on the WS connection's own
	// goroutine, so it doesn't need synchronization elsewhere.
	sessionIDAtomic atomic.Value

	// autoAccept scans terminal output and sends Enter when prompts are detected.
	autoAcceptMu        sync.Mutex
	autoAcceptRemaining int // remaining prompts to auto-accept (0 = disabled)

	// relaunch scans terminal output for Claude's exit marker and, on an
	// abnormal exit, resends the interactive Claude command (see
	// BuildContinueCmd) so a Claude process that died unexpectedly (e.g.
	// OOM-killed) comes back without the user having to notice and retype it.
	relaunchMu        sync.Mutex
	relaunchEnabled   bool // true only for panes that auto-boot Claude (not explicit cmd / sessions panel)
	relaunchRemaining int  // remaining auto-relaunches for the current session (0 = disabled)
	relaunchChannelID string
	relaunchDirPath   string
	relaunchParentDir string
	relaunchSessionID string // the session the pane resumed, "" when it forked or started fresh
	relaunchAgentID   string
}

// maxClaudeRelaunches is the maximum number of times a terminal pane's Claude
// process is auto-relaunched after an abnormal exit, per session creation.
const maxClaudeRelaunches = 2

// activeManager returns the correct manager based on the current session target.
func (t *terminalWSConn) activeManager() TerminalManager {
	if t.sessionTarget == "host" {
		return t.hostManager
	}
	return t.manager
}

func newTerminalWSConn(conn *websocket.Conn, manager TerminalManager, hostManager TerminalManager, registry ContainerManager, cmdBuilder InteractiveCmdBuilder, store ChannelLister, loopDir string, logger *slog.Logger) *terminalWSConn {
	return &terminalWSConn{
		conn:              conn,
		connWriteMessage:  conn.WriteMessage,
		manager:           manager,
		hostManager:       hostManager,
		containerRegistry: registry,
		cmdBuilder:        cmdBuilder,
		store:             store,
		loopDir:           loopDir,
		logger:            logger,
		stopCh:            make(chan struct{}),
	}
}

// writeMessage is the shared, mutex-protected write path for all WebSocket frames.
func (t *terminalWSConn) writeMessage(msgType int, data []byte) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if err := t.connWriteMessage(msgType, data); err != nil {
		t.logger.Error("terminal ws: write failed", "error", err, "msg_type", msgType, "len", len(data))
	}
}

func (t *terminalWSConn) writeJSON(msg wsStatusMessage) {
	// wsStatusMessage contains only string fields; Marshal cannot fail.
	data, _ := json.Marshal(msg)
	t.writeMessage(websocket.TextMessage, data)
}

func (t *terminalWSConn) writeBinary(data []byte) {
	t.writeMessage(websocket.BinaryMessage, data)
}

func (t *terminalWSConn) sendError(message, code string) {
	t.writeJSON(wsStatusMessage{Type: wsStatusError, Message: message, ErrorCode: code})
}

// streamOutput forwards terminal output to the WebSocket client.
// It also scans for auto-accept trigger strings and sends Enter when matched.
func (t *terminalWSConn) streamOutput(output <-chan []byte, done <-chan struct{}) {
	for {
		select {
		case data, ok := <-output:
			if !ok {
				t.writeJSON(wsStatusMessage{Type: wsStatusClosed})
				return
			}
			t.writeBinary(data)
			t.scanAutoAccept(data)
			t.scanClaudeExit(data)
		case <-done:
			t.writeJSON(wsStatusMessage{Type: wsStatusClosed})
			return
		case <-t.stopCh:
			return
		}
	}
}

// detachCurrent detaches from the current session if attached.
func (t *terminalWSConn) detachCurrent() {
	if t.sessionID != "" && t.outputCh != nil {
		if mgr := t.activeManager(); mgr != nil {
			if err := mgr.DetachSession(t.sessionID, t.outputCh); err != nil {
				t.logger.Warn("terminal ws: detach failed", "session_id", t.sessionID, "error", err)
			}
		}
		t.setSessionID("")
		t.outputCh = nil
		t.sessionTarget = ""
	}
	t.disableRelaunch()
}

// disableRelaunch zeroes the relaunch budget so a Claude-exit marker arriving
// after the pane has been detached, stopped, or closed can never fire a
// relaunch into a session the client no longer owns.
func (t *terminalWSConn) disableRelaunch() {
	t.relaunchMu.Lock()
	t.relaunchEnabled = false
	t.relaunchRemaining = 0
	t.relaunchMu.Unlock()
}

// enableRelaunch records the state needed to relaunch Claude after an
// abnormal exit and resets the per-session relaunch budget.
func (t *terminalWSConn) enableRelaunch(channelID, dirPath, parentDirPath, sessionID, agentID string) {
	t.relaunchMu.Lock()
	t.relaunchEnabled = true
	t.relaunchRemaining = maxClaudeRelaunches
	t.relaunchChannelID = channelID
	t.relaunchDirPath = dirPath
	t.relaunchParentDir = parentDirPath
	t.relaunchSessionID = sessionID
	t.relaunchAgentID = agentID
	t.relaunchMu.Unlock()
}

// close stops streaming and detaches (or stops) the current session.
// By default sessions are detached so they survive WebSocket disconnects.
// When stopOnClose is set (e.g. sessions panel), the session is stopped
// to avoid leaving orphaned Claude processes in the container.
func (t *terminalWSConn) close() {
	t.stopOnce.Do(func() { close(t.stopCh) })
	if t.stopOnClose {
		t.stopCurrentSession()
	} else {
		t.detachCurrent()
	}
}

// stopCurrentSession kills the exec's process group via a PID file, then stops the session.
// Used only for sessions panel terminals (stopOnClose) to avoid orphaned Claude processes.
func (t *terminalWSConn) stopCurrentSession() {
	if t.sessionID != "" && t.outputCh != nil {
		if mgr := t.activeManager(); mgr != nil {
			if err := mgr.KillProcessGroup(context.Background(), t.sessionID); err != nil {
				t.logger.Warn("terminal ws: kill process group failed", "session_id", t.sessionID, "error", err)
			}
			containerID, err := mgr.StopSession(t.sessionID)
			if err != nil {
				t.logger.Warn("terminal ws: stop on close failed", "session_id", t.sessionID, "error", err)
			} else if t.sessionTarget != "host" && t.releaseShell != nil {
				t.releaseShell(containerID)
			}
		}
		t.setSessionID("")
		t.outputCh = nil
		t.sessionTarget = ""
	}
	t.disableRelaunch()
}

// autoAcceptTrigger is the prompt text that triggers an automatic Enter.
// Spaces stripped because ANSI escape codes between characters get removed by stripANSI,
// collapsing "Enter to confirm" into "Entertoconfirm".
const autoAcceptTrigger = "Entertoconfirm" // workspace trust prompt

// maxAutoAccepts is the maximum number of prompts to auto-accept per session.
const maxAutoAccepts = 3

// enableAutoAccept sets up output scanning to auto-accept interactive prompts.
func (t *terminalWSConn) enableAutoAccept() {
	t.autoAcceptMu.Lock()
	defer t.autoAcceptMu.Unlock()
	t.autoAcceptRemaining = maxAutoAccepts
}

// disableAutoAccept zeroes the remaining budget. Called on first user input
// so trigger text in later TUI screens (e.g. /model's footer) is ignored —
// auto-accept is only intended for the boot prompts before the user types.
func (t *terminalWSConn) disableAutoAccept() {
	t.autoAcceptMu.Lock()
	t.autoAcceptRemaining = 0
	t.autoAcceptMu.Unlock()
}

// ansiEscapeRe matches ANSI escape sequences (CSI, OSC, etc.).
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x1b]*(?:\x1b\\|\x07)|\x1b[()][0-9A-B]`)

// stripANSI removes ANSI escape codes from terminal output.
func stripANSI(data []byte) []byte {
	return ansiEscapeRe.ReplaceAll(data, nil)
}

// scanAutoAccept checks terminal output for the trigger string and sends Enter.
func (t *terminalWSConn) scanAutoAccept(data []byte) {
	t.autoAcceptMu.Lock()
	if t.autoAcceptRemaining <= 0 {
		t.autoAcceptMu.Unlock()
		return
	}
	clean := stripANSI(data)
	if !bytes.Contains(clean, []byte(autoAcceptTrigger)) {
		t.autoAcceptMu.Unlock()
		return
	}
	t.autoAcceptRemaining--
	sid := t.sessionID
	t.autoAcceptMu.Unlock()

	// Send Enter after a delay, retrying a few times in case the TUI isn't ready.
	go func() {
		for _, delay := range []time.Duration{500 * time.Millisecond, time.Second, time.Second} {
			time.Sleep(delay)
			if err := t.manager.SendInput(sid, []byte("\r")); err != nil {
				t.logger.Warn("terminal ws: auto-accept send failed", "session_id", sid, "error", err)
				return
			}
			t.logger.Info("terminal ws: auto-accept sent Enter", "session_id", sid, "trigger", autoAcceptTrigger)
		}
	}()
}

// claudeExitMarkerRe matches the exit-code line an interactive Claude command
// emits (see container.ClaudeExitMarkerPrefix / claudeExitTrailer) once the
// Claude process exits, e.g. "__LOOP_CLAUDE_EXIT:137".
// setSessionID updates sessionID and its atomic mirror (see sessionIDAtomic).
func (t *terminalWSConn) setSessionID(id string) {
	t.sessionID = id
	t.sessionIDAtomic.Store(id)
}

// getSessionID reads the atomic mirror of sessionID, safe to call from a
// goroutine other than the WS connection's own (e.g. the delayed relaunch
// goroutine in scanClaudeExit).
func (t *terminalWSConn) getSessionID() string {
	v, _ := t.sessionIDAtomic.Load().(string)
	return v
}

var claudeExitMarkerRe = regexp.MustCompile(regexp.QuoteMeta(container.ClaudeExitMarkerPrefix) + `(\d+)`)

// scanClaudeExit checks live terminal output for a Claude exit marker and, on
// an abnormal (non-zero) exit with relaunch budget remaining, resends the
// interactive Claude command (see BuildContinueCmd) so a process that died
// unexpectedly (e.g. OOM-killed) comes back automatically. An exit code of 0
// means the user quit deliberately, so relaunching is disabled for the rest
// of the session's lifetime.
func (t *terminalWSConn) scanClaudeExit(data []byte) {
	clean := stripANSI(data)
	m := claudeExitMarkerRe.FindSubmatch(clean)
	if m == nil {
		return
	}
	code, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return
	}

	t.relaunchMu.Lock()
	if !t.relaunchEnabled {
		t.relaunchMu.Unlock()
		return
	}
	if code == 0 {
		t.relaunchRemaining = 0
		t.relaunchMu.Unlock()
		return
	}
	if t.relaunchRemaining <= 0 {
		t.relaunchMu.Unlock()
		return
	}
	t.relaunchRemaining--
	channelID, dirPath, parentDirPath, sessionID, agentID := t.relaunchChannelID, t.relaunchDirPath, t.relaunchParentDir, t.relaunchSessionID, t.relaunchAgentID
	t.relaunchMu.Unlock()

	sid := t.sessionID
	t.logger.Warn("terminal ws: claude exited abnormally, relaunching", "session_id", sid, "exit_code", code, "channel_id", channelID)

	go func() {
		time.Sleep(time.Second)
		if t.getSessionID() != sid {
			return // pane moved on (detached/stopped/closed) — don't relaunch into it
		}
		cmd := t.cmdBuilder.BuildContinueCmd(channelID, dirPath, parentDirPath, sessionID, agentID)
		if err := t.manager.SendInput(sid, []byte(cmd+"\n")); err != nil {
			t.logger.Warn("terminal ws: claude relaunch send failed", "session_id", sid, "error", err)
		}
	}()
}

// startSession attaches to a session and begins streaming output.
func (t *terminalWSConn) startSession(sessionID string, output <-chan []byte, history []byte, done <-chan struct{}, statusType string) {
	t.setSessionID(sessionID)
	t.outputCh = output
	t.writeJSON(wsStatusMessage{Type: statusType, SessionID: sessionID})
	if len(history) > 0 {
		t.writeBinary(history)
		// Scan history for auto-accept triggers (handles reattach to a stuck prompt).
		t.scanAutoAccept(history)
	}
	go t.streamOutput(output, done)
}

// maxCmdArgs is the maximum number of arguments allowed in a create command.
const maxCmdArgs = 64

// shellSingleQuote wraps s in single quotes, escaping any embedded single
// quotes, so it can be safely interpolated into a POSIX shell command.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// resolveRootDir maps a shell pane's RootIndex to an absolute workspace path.
// Index 0 (or an unset/out-of-range index, or no resolver) keeps the supplied
// default. Resolution goes through rootDirs so the index is validated against
// the channel's authoritative root list rather than trusting client input.
func (t *terminalWSConn) resolveRootDir(ctx context.Context, channelID string, rootIndex int, def string) string {
	if rootIndex <= 0 || t.rootDirs == nil || channelID == "" {
		return def
	}
	paths, err := t.rootDirs(ctx, channelID)
	if err != nil || rootIndex >= len(paths) || paths[rootIndex] == "" {
		return def
	}
	return paths[rootIndex]
}

func (t *terminalWSConn) handleCreateHost(ctx context.Context, msg wsControlMessage) {
	if t.hostManager == nil {
		t.sendError("host terminal not configured", wsErrCodeSessionFailed)
		return
	}

	// Resolve channel_id → dir_path. For threads, fall back to the parent's dir_path.
	// When DirPath is empty, fall back to the loop work dir (same logic as channel listing).
	dirPath := os.Getenv("HOME")
	if msg.ChannelID != "" && t.store != nil {
		if ch, err := t.store.GetChannel(ctx, msg.ChannelID); err == nil && ch != nil {
			switch {
			case ch.DirPath != "":
				dirPath = ch.DirPath
			case ch.ParentID != "":
				if parent, err := t.store.GetChannel(ctx, ch.ParentID); err == nil && parent != nil && parent.DirPath != "" {
					dirPath = parent.DirPath
				} else if t.loopDir != "" {
					dirPath = filepath.Join(t.loopDir, msg.ChannelID, "work")
				}
			case t.loopDir != "":
				dirPath = filepath.Join(t.loopDir, msg.ChannelID, "work")
			}
		}
	}

	// Multi-root workspaces: open the shell in the selected extra root.
	dirPath = t.resolveRootDir(ctx, msg.ChannelID, msg.RootIndex, dirPath)

	// A host session is always the user's login shell. The UI never asks
	// for anything else, and a caller-chosen argv would be host command
	// execution in one message.
	if len(msg.Cmd) > 0 {
		t.sendError("host sessions don't take a cmd", wsErrCodeInvalidInput)
		return
	}

	t.detachCurrent()

	sid, output, history, done, err := t.hostManager.CreateSession(ctx, dirPath, nil)
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	t.sessionTarget = "host"
	t.startSession(sid, output, history, done, wsStatusCreated)

	if msg.Rows > 0 && msg.Cols > 0 {
		if err := t.hostManager.Resize(ctx, sid, msg.Rows, msg.Cols); err != nil {
			t.logger.Warn("terminal ws: initial resize failed", "session_id", sid, "error", err)
		}
	}
}

func (t *terminalWSConn) handleCreate(ctx context.Context, msg wsControlMessage) {
	if msg.Target == "host" {
		t.handleCreateHost(ctx, msg)
		return
	}

	// Resolve channel_id to container_id via ContainerFinder.
	var dirPath, parentDirPath, claudeSessionID string
	var forkSession bool
	if msg.ContainerID == "" && msg.ChannelID != "" && t.containerRegistry != nil {
		// Look up channel's dir_path and session_id for the interactive command.
		if t.store != nil {
			if ch, err := t.store.GetChannel(ctx, msg.ChannelID); err == nil && ch != nil {
				dirPath = ch.DirPath
				switch msg.OpenMode {
				case openModeFresh:
					// Explicit fresh — no session at all.
					claudeSessionID = ""
					forkSession = false
				case openModeResume:
					// Explicit resume — channel session, no fork.
					claudeSessionID = ch.SessionID
					forkSession = false
				case openModeFork:
					// Explicit fork — channel session, branch a new one.
					claudeSessionID = ch.SessionID
					forkSession = claudeSessionID != ""
				default:
					// Legacy path: NewSession + auto-fork heuristic.
					if msg.NewSession {
						claudeSessionID = ""
					} else {
						claudeSessionID = ch.SessionID
						// For threads still using the parent's session, fork so the thread
						// gets its own session while inheriting the parent's context.
						if ch.ParentID != "" {
							if parent, err := t.store.GetChannel(ctx, ch.ParentID); err == nil && parent != nil && parent.SessionID != "" {
								if claudeSessionID == "" || claudeSessionID == parent.SessionID {
									claudeSessionID = parent.SessionID
									forkSession = true
								}
							}
						}
						// Agent terminals fork from the channel session so each agent
						// gets its own session while inheriting the shared context.
						if msg.AgentID != "" && claudeSessionID != "" {
							forkSession = true
						}
					}
				}
				// For worktree channels, resolve the parent project dir so
				// the shell container gets parent config mounts merged in.
				if ch.Worktree && ch.ParentID != "" {
					if parent, err := t.store.GetChannel(ctx, ch.ParentID); err == nil && parent != nil {
						parentDirPath = parent.DirPath
					}
				}
			}
		}
		containerID, err := t.containerRegistry.FindOrCreateShell(ctx, msg.ChannelID, dirPath, parentDirPath)
		if err != nil {
			t.sendError("no running container for channel: "+err.Error(), wsErrCodeSessionFailed)
			return
		}
		msg.ContainerID = containerID
	}
	if msg.ContainerID == "" {
		t.sendError("container_id or channel_id required", wsErrCodeMissingField)
		return
	}
	if len(msg.Cmd) > maxCmdArgs {
		t.sendError("cmd exceeds maximum arguments", wsErrCodeInvalidInput)
		return
	}
	for _, arg := range msg.Cmd {
		if arg == "" {
			t.sendError("cmd contains empty argument", wsErrCodeInvalidInput)
			return
		}
	}

	t.detachCurrent()

	// Stamp LOOP_TERMINAL_LEAF on agent-pane execs so the in-container
	// dockerproxy can attribute approval prompts back to this specific pane.
	// The leaf id is the FE's per-pane handle; the chat agent runs as the
	// container entrypoint and never carries this env var, so its approval
	// requests surface as "chat" source.
	var sid string
	var output <-chan []byte
	var history []byte
	var done <-chan struct{}
	var err error
	if msg.LeafID != "" {
		env := []string{"LOOP_TERMINAL_LEAF=" + msg.LeafID}
		sid, output, history, done, err = t.manager.CreateSessionWithEnv(ctx, msg.ContainerID, msg.Cmd, env)
	} else {
		sid, output, history, done, err = t.manager.CreateSession(ctx, msg.ContainerID, msg.Cmd)
	}
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	if t.claimShell != nil {
		t.claimShell(msg.ContainerID)
	}
	// Enable auto-accept BEFORE startSession begins streaming, so the scanner
	// is ready when the prompt output arrives.
	if msg.AgentID != "" {
		t.enableAutoAccept()
	}
	t.startSession(sid, output, history, done, wsStatusCreated)

	// Resize the PTY to match the client's terminal dimensions if provided.
	if msg.Rows > 0 && msg.Cols > 0 {
		if err := t.manager.Resize(ctx, sid, msg.Rows, msg.Cols); err != nil {
			t.logger.Warn("terminal ws: initial resize failed", "session_id", sid, "error", err)
		}
	}

	// Docker shell panes opened in a non-primary workspace root: cd into the
	// selected root. Extra roots are bind-mounted into the shell container at
	// their real host paths, so the path is valid inside the container. Only
	// shells (explicit cmd) take this path; the agent boots via the interactive
	// cmd below (len(Cmd) == 0). The dir is server-resolved from the channel's
	// root list, not raw client input, but we shell-quote it defensively.
	if len(msg.Cmd) > 0 && msg.RootIndex > 0 {
		if shellDir := t.resolveRootDir(ctx, msg.ChannelID, msg.RootIndex, dirPath); shellDir != "" && shellDir != dirPath {
			if err := t.manager.SendInput(sid, []byte("cd "+shellSingleQuote(shellDir)+"\n")); err != nil {
				t.logger.Warn("terminal ws: failed to cd shell into root", "session_id", sid, "error", err)
			}
		}
	}

	// When no explicit command was provided, send the interactive Claude
	// command as shell input so the terminal starts Claude automatically.
	if len(msg.Cmd) == 0 && t.cmdBuilder != nil && msg.ChannelID != "" {
		// Allow the client to override the session ID (e.g. sessions panel resuming a specific session).
		// Stop the exec process on WS disconnect to avoid orphaned Claude processes.
		if msg.SessionID != "" {
			claudeSessionID = msg.SessionID
			forkSession = false
			t.stopOnClose = true
		}
		cmd := t.cmdBuilder.BuildInteractiveCmd(msg.ChannelID, dirPath, parentDirPath, claudeSessionID, msg.AgentID, forkSession)
		if err := t.manager.SendInput(sid, []byte(cmd+"\n")); err != nil {
			t.logger.Warn("terminal ws: failed to send interactive cmd", "session_id", sid, "error", err)
		}
		if msg.AgentID != "" && t.agentTerminals != nil {
			t.agentTerminals.SetTerminal(msg.ChannelID, msg.AgentID, sid)
		}
		// Sessions-panel panes (stopOnClose) resume an explicit, fixed session
		// the user picked — don't auto-relaunch those into a different one.
		// A resumed session is relaunched by id; a fork's new id is never
		// reported back, so that one falls back to --continue.
		if !t.stopOnClose {
			resumeID := claudeSessionID
			if forkSession {
				resumeID = ""
			}
			t.enableRelaunch(msg.ChannelID, dirPath, parentDirPath, resumeID, msg.AgentID)
		}
	}
}

func (t *terminalWSConn) handleAttach(msg wsControlMessage) {
	if msg.SessionID == "" {
		t.sendError("session_id required", wsErrCodeMissingField)
		return
	}
	t.detachCurrent()

	// Attach only through the manager the client names, agent by default as
	// on create: a session ID never reaches a host shell unless the client
	// asks for one.
	target := msg.Target
	if target == "" {
		target = "agent"
	}
	mgr := t.manager
	switch target {
	case "agent":
	case "host":
		mgr = t.hostManager
	default:
		t.sendError("unknown target "+target, wsErrCodeInvalidInput)
		return
	}
	if mgr == nil {
		t.sendError("no terminal manager for target "+target, wsErrCodeSessionFailed)
		return
	}
	output, history, done, err := mgr.AttachSession(msg.SessionID)
	if err != nil {
		code := wsErrCodeSessionFailed
		if errors.Is(err, terminal.ErrSessionNotFound) {
			code = wsErrCodeSessionGone
		}
		t.sendError(err.Error(), code)
		return
	}
	t.sessionTarget = target
	// Enable auto-accept before startSession so history is scanned for prompts.
	if msg.AgentID != "" {
		t.enableAutoAccept()
	}
	t.startSession(msg.SessionID, output, history, done, wsStatusAttached)
}

func (t *terminalWSConn) handleInput(msg wsControlMessage) {
	if t.sessionID == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		t.sendError("invalid base64 data", wsErrCodeInvalidInput)
		return
	}
	t.disableAutoAccept()
	if err := t.activeManager().SendInput(t.sessionID, data); err != nil {
		t.logger.Error("terminal ws: send input failed", "session_id", t.sessionID, "error", err)
		t.sendError(err.Error(), wsErrCodeSessionFailed)
	}
}

func (t *terminalWSConn) handleResize(ctx context.Context, msg wsControlMessage) {
	if t.sessionID == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	if msg.Rows == 0 || msg.Cols == 0 {
		t.sendError("rows and cols required", wsErrCodeMissingField)
		return
	}
	if err := t.activeManager().Resize(ctx, t.sessionID, msg.Rows, msg.Cols); err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
	}
}

func (t *terminalWSConn) handleStop(ctx context.Context, msg wsControlMessage) {
	// Allow stopping a detached session by passing session_id explicitly.
	sid := t.sessionID
	if sid == "" {
		sid = msg.SessionID
	}
	if sid == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	isHost := t.sessionTarget == "host"
	containerID, err := t.activeManager().StopSession(sid)
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	// Clear session state directly — StopSession already removed the session
	// from the manager, so calling detachCurrent would fail with "session not found".
	t.setSessionID("")
	t.outputCh = nil
	t.sessionTarget = ""
	t.disableRelaunch()

	// Remove the container before notifying the client, so the channel list
	// API reflects the updated running state when the client refreshes.
	// Skip container removal for host sessions (no container involved).
	if !isHost && containerID != "" && t.containerRegistry != nil {
		if err := t.containerRegistry.RemoveContainer(ctx, containerID); err != nil {
			t.logger.Warn("terminal ws: container remove failed", "container_id", containerID, "error", err)
		}
	}
	t.writeJSON(wsStatusMessage{Type: wsStatusStopped})
}

// handleClose stops the exec session but does NOT remove the container.
// Used when closing an individual terminal pane.
func (t *terminalWSConn) handleClose(msg wsControlMessage) {
	sid := t.sessionID
	if sid == "" {
		sid = msg.SessionID
	}
	if sid == "" {
		t.sendError("no active session", wsErrCodeNoSession)
		return
	}
	// A session no pane is attached to, one on a deleted tab say, is named
	// by the message, with its target.
	target := t.sessionTarget
	if t.sessionID == "" {
		target = msg.Target
	}
	isHost := target == "host"
	mgr := t.manager
	if isHost {
		mgr = t.hostManager
	}
	if mgr == nil {
		t.sendError("terminal not configured", wsErrCodeSessionFailed)
		return
	}
	// Stopping only drops the attach stream, and the shell would run on in
	// the container, where a later pane reclaims it.
	if !isHost {
		if err := mgr.KillProcessGroup(context.Background(), sid); err != nil {
			t.logger.Warn("terminal ws: kill process group failed", "session_id", sid, "error", err)
		}
	}
	containerID, err := mgr.StopSession(sid)
	if err != nil {
		t.sendError(err.Error(), wsErrCodeSessionFailed)
		return
	}
	t.setSessionID("")
	t.outputCh = nil
	t.sessionTarget = ""
	t.disableRelaunch()
	if !isHost && t.releaseShell != nil {
		t.releaseShell(containerID)
	}
	t.writeJSON(wsStatusMessage{Type: wsStatusStopped})
}

// releaseShell marks a channel's shell container for removal after the
// keep-alive once no terminal session runs in it any more. Every docker pane
// of the channel, in any tab, shares the container, so closing one pane, or
// the tab it's on, leaves it to the others. A pane opened within the
// keep-alive takes it back. It runs when a pane closes, and when a
// session's process ends, a shell left with `exit` say.
func (s *Server) releaseShell(containerID string) {
	if s.containerRegistry == nil || s.termManager == nil {
		return
	}
	s.shellMu.Lock()
	defer s.shellMu.Unlock()
	info := s.containerRegistry.Get(containerID)
	if info == nil || info.Type != container.ContainerTypeShell || info.Status != container.ContainerStatusRunning {
		return
	}
	if s.termManager.LiveSessions(containerID) > 0 {
		return
	}
	s.containerRegistry.ScheduleRemove(containerID, s.containerKeepAlive)
}

// claimShell takes back a shell container that a session was just created
// in, when the last pane's release marked it for removal between the lookup
// and the session's start. shellMu orders the two: a release either counts
// the new session or comes before this.
func (s *Server) claimShell(containerID string) {
	if s.containerRegistry == nil {
		return
	}
	s.shellMu.Lock()
	defer s.shellMu.Unlock()
	info := s.containerRegistry.Get(containerID)
	if info == nil || info.Type != container.ContainerTypeShell || info.Status != container.ContainerStatusPendingRemoval {
		return
	}
	s.containerRegistry.Reclaim(containerID)
}

// handleKill removes a channel's containers at once, whatever panes still
// use them, without requiring an active session. Used by the explicit
// kill-agents action; closing panes and tabs releases the shell container
// through releaseShell instead.
func (t *terminalWSConn) handleKill(ctx context.Context, msg wsControlMessage) {
	if msg.ChannelID == "" {
		t.sendError("channel_id required", wsErrCodeMissingField)
		return
	}
	if t.containerRegistry == nil {
		t.sendError("container management not configured", wsErrCodeSessionFailed)
		return
	}

	// If there's an active agent session, stop it first.
	if t.sessionID != "" && t.sessionTarget != "host" {
		if _, err := t.activeManager().StopSession(t.sessionID); err != nil {
			t.logger.Warn("terminal ws: kill session stop failed", "session_id", t.sessionID, "error", err)
		}
		t.setSessionID("")
		t.outputCh = nil
		t.sessionTarget = ""
		t.disableRelaunch()
	}

	// Remove all containers for this channel.
	for _, info := range t.containerRegistry.ListByChannel(msg.ChannelID) {
		if info.Type == container.ContainerTypeChrome {
			continue // handled separately via BrowserProvider
		}
		if err := t.containerRegistry.RemoveContainer(ctx, info.ContainerID); err != nil {
			// Suppress benign race: another goroutine (e.g. scheduleRemove) already started removal.
			if !strings.Contains(err.Error(), "already in progress") {
				t.logger.Warn("terminal ws: kill container remove failed", "container_id", info.ContainerID, "error", err)
			}
		}
	}
	// Also stop and remove the Chrome sidecar container for this channel.
	if t.browserProvider != nil {
		// The cached manager outlives the container unless it is dropped here,
		// and every later browser call would reuse its canceled client.
		if t.closeBrowserCDP != nil {
			t.closeBrowserCDP(msg.ChannelID)
		}
		containerID, _ := t.browserProvider.StopBrowser(ctx, msg.ChannelID)
		if containerID != "" && t.containerRegistry != nil {
			_ = t.containerRegistry.RemoveContainer(ctx, containerID)
		}
	}
	t.writeJSON(wsStatusMessage{Type: wsStatusStopped})
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if s.termManager == nil && s.hostTermManager == nil {
		http.Error(w, "terminal not configured", http.StatusNotImplemented)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	tc := newTerminalWSConn(conn, s.termManager, s.hostTermManager, s.containerRegistry, s.cmdBuilder, s.store, s.loopDir, s.logger)
	tc.rootDirs = s.allDirPaths
	tc.releaseShell = s.releaseShell
	tc.claimShell = s.claimShell
	if s.agentRegistry != nil {
		tc.agentTerminals = s.agentRegistry
	}
	tc.browserProvider = s.browser.dockerProvider
	tc.closeBrowserCDP = func(channelID string) {
		s.browser.closeCDPManager(channelID, s.browser.modeFor(channelID))
	}
	defer tc.close()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg wsControlMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			tc.sendError("invalid JSON", wsErrCodeInvalidJSON)
			continue
		}

		switch msg.Type {
		case wsMsgCreate:
			tc.handleCreate(r.Context(), msg)
		case wsMsgAttach:
			tc.handleAttach(msg)
		case wsMsgInput:
			tc.handleInput(msg)
		case wsMsgResize:
			tc.handleResize(r.Context(), msg)
		case wsMsgStop:
			tc.handleStop(r.Context(), msg)
		case wsMsgClose:
			tc.handleClose(msg)
		case wsMsgKill:
			tc.handleKill(r.Context(), msg)
		default:
			tc.sendError("unknown message type: "+msg.Type, wsErrCodeUnknownMessage)
		}
	}
}

// SetTerminalManager configures the terminal manager for WebSocket terminal sessions.
// When a session's process ends, its shell container is released and the
// agent whose pane it ran is unregistered.
func (s *Server) SetTerminalManager(mgr TerminalManager) {
	s.termManager = mgr
	if n, ok := mgr.(exitNotifier); ok {
		n.SetOnExit(s.terminalExited)
	}
}

// exitNotifier reports when a terminal session's exec ends.
type exitNotifier interface {
	SetOnExit(fn func(sessionID, containerID string))
}

// terminalExited runs once a docker terminal session's exec ends.
func (s *Server) terminalExited(sessionID, containerID string) {
	s.releaseShell(containerID)
	s.releaseAgentTerminal(sessionID)
}

// SetInteractiveCmdBuilder configures the command builder for interactive terminal sessions.
func (s *Server) SetInteractiveCmdBuilder(builder InteractiveCmdBuilder) {
	s.cmdBuilder = builder
}

// SetHostTerminalManager configures the host terminal manager for non-Docker shell sessions.
func (s *Server) SetHostTerminalManager(mgr TerminalManager) {
	s.hostTermManager = mgr
}
