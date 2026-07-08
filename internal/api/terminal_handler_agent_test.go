package api

import (
	"errors"
	"log/slog"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// --- Agent Auto-Accept and Fork Tests ---

func (s *TerminalHandlerSuite) TestCreateSessionWithAgentIDTriggersAutoAccept() {
	finder := new(mockContainerManager)
	s.srv.containerRegistry = finder
	finder.On("FindOrCreateShell", mock.Anything, "ch-1", "", "").Return("ctr-1", nil)

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-1", "", "", "", "agent-0", false).Return("claude --dangerously-skip-permissions")
	s.srv.SetInteractiveCmdBuilder(builder)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", []string(nil)).
		Return("sid-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sid-1", []byte("claude --dangerously-skip-permissions\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()
	// Auto-accept scans output for prompt triggers and sends Enter.
	s.terminal.On("SendInput", "sid-1", []byte("\r")).Return(nil).Maybe()

	ws, ts := s.dialWS()
	defer ts.Close()
	defer ws.Close()

	sendControl(s.T(), ws, wsControlMessage{
		Type:      "create",
		ChannelID: "ch-1",
		AgentID:   "agent-0",
	})
	msg := readStatusMsg(s.T(), ws)
	require.Equal(s.T(), wsStatusCreated, msg.Type)

	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for interactive cmd SendInput")
	}

	builder.AssertExpectations(s.T())

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestAgentTerminalForksChannelSession() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", SessionID: "sess-main"}, nil)
	s.srv.store = store

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-1", mock.Anything, mock.Anything).Return("ctr-1", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	// forkSession=true because agentID is set and channel has a session.
	builder.On("BuildInteractiveCmd", "ch-1", "", "", "sess-main", "agent-0", true, mock.Anything).
		Return("claude --resume sess-main --fork-session")
	s.srv.SetInteractiveCmdBuilder(builder)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", []string(nil)).
		Return("sid-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sid-1", []byte("claude --resume sess-main --fork-session\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.terminal.On("SendInput", "sid-1", []byte("\r")).Return(nil).Maybe()

	ws, ts := s.dialWS()
	defer ts.Close()
	defer ws.Close()

	sendControl(s.T(), ws, wsControlMessage{
		Type:      "create",
		ChannelID: "ch-1",
		AgentID:   "agent-0",
	})
	msg := readStatusMsg(s.T(), ws)
	require.Equal(s.T(), wsStatusCreated, msg.Type)

	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for fork-session SendInput")
	}
	builder.AssertExpectations(s.T())

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestNewSessionSkipsChannelSession() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", SessionID: "sess-main"}, nil)
	s.srv.store = store

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-1", mock.Anything, mock.Anything).Return("ctr-1", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	// new_session=true → empty sessionID, forkSession=false even though channel has a session.
	builder.On("BuildInteractiveCmd", "ch-1", "", "", "", "agent-0", false, mock.Anything).
		Return("claude --dangerously-skip-permissions")
	s.srv.SetInteractiveCmdBuilder(builder)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", []string(nil)).
		Return("sid-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sid-1", []byte("claude --dangerously-skip-permissions\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.terminal.On("SendInput", "sid-1", []byte("\r")).Return(nil).Maybe()

	ws, ts := s.dialWS()
	defer ts.Close()
	defer ws.Close()

	sendControl(s.T(), ws, wsControlMessage{
		Type:       "create",
		ChannelID:  "ch-1",
		AgentID:    "agent-0",
		NewSession: true,
	})
	msg := readStatusMsg(s.T(), ws)
	require.Equal(s.T(), wsStatusCreated, msg.Type)

	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for new-session SendInput")
	}
	builder.AssertExpectations(s.T())

	close(doneCh)
}

// --- Explicit OpenMode tests (resume / fork / fresh) ---
//
// These cover the new client-driven open-mode protocol where the FE picks the
// fork/resume/fresh choice up-front. The legacy auto-fork heuristic stays as
// the OpenMode="" fallback and is covered by TestAgentTerminalForksChannelSession
// + TestNewSessionSkipsChannelSession above.

func (s *TerminalHandlerSuite) sendCreateWithOpenMode(channelSession, openMode, wantSession string, wantFork bool, wantCmd string) {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-mode").
		Return(&db.Channel{ChannelID: "ch-mode", SessionID: channelSession}, nil)
	s.srv.store = store

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-mode", mock.Anything, mock.Anything).Return("ctr-mode", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-mode", "", "", wantSession, "agent-0", wantFork, mock.Anything).
		Return(wantCmd)
	s.srv.SetInteractiveCmdBuilder(builder)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-mode", []string(nil)).
		Return("sid-mode", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sid-mode", []byte(wantCmd+"\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.terminal.On("SendInput", "sid-mode", []byte("\r")).Return(nil).Maybe()

	ws, ts := s.dialWS()
	defer ts.Close()
	defer ws.Close()

	sendControl(s.T(), ws, wsControlMessage{
		Type:      "create",
		ChannelID: "ch-mode",
		AgentID:   "agent-0",
		OpenMode:  openMode,
	})
	msg := readStatusMsg(s.T(), ws)
	require.Equal(s.T(), wsStatusCreated, msg.Type)

	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatalf("timed out waiting for open_mode=%q SendInput", openMode)
	}
	builder.AssertExpectations(s.T())
	close(doneCh)
}

// open_mode="resume" → reuse channel session, no fork.
func (s *TerminalHandlerSuite) TestOpenModeResumeReusesChannelSession() {
	s.sendCreateWithOpenMode("sess-main", "resume", "sess-main", false, "claude --resume sess-main")
}

// open_mode="fork" → reuse channel session, fork off it.
func (s *TerminalHandlerSuite) TestOpenModeForkBranchesChannelSession() {
	s.sendCreateWithOpenMode("sess-main", "fork", "sess-main", true, "claude --resume sess-main --fork-session")
}

// open_mode="fresh" → no session at all, ignore channel's stored session.
func (s *TerminalHandlerSuite) TestOpenModeFreshIgnoresChannelSession() {
	s.sendCreateWithOpenMode("sess-main", "fresh", "", false, "claude --dangerously-skip-permissions")
}

// open_mode="fork" on a channel with no session degrades gracefully — no fork
// because there's nothing to fork from.
func (s *TerminalHandlerSuite) TestOpenModeForkOnEmptyChannelDoesNotFork() {
	s.sendCreateWithOpenMode("", "fork", "", false, "claude --dangerously-skip-permissions")
}

func (s *TerminalHandlerSuite) TestAutoAcceptScansOutput() {
	s.terminal.On("SendInput", "sid-1", []byte("\r")).Return(nil)

	tc := &terminalWSConn{
		manager:   s.terminal,
		sessionID: "sid-1",
		logger:    slog.Default(),
	}
	tc.enableAutoAccept()

	// Output without trigger — should not send Enter.
	tc.scanAutoAccept([]byte("Loading Claude..."))
	time.Sleep(50 * time.Millisecond)
	s.terminal.AssertNotCalled(s.T(), "SendInput", "sid-1", []byte("\r"))

	// Output with trigger — should send Enter (first retry at 500ms).
	tc.scanAutoAccept([]byte("Entertoconfirm · Esc to cancel"))
	time.Sleep(700 * time.Millisecond)
	s.terminal.AssertCalled(s.T(), "SendInput", "sid-1", []byte("\r"))
}

func (s *TerminalHandlerSuite) TestAutoAcceptFiresForMultiplePrompts() {
	s.terminal.On("SendInput", "sid-1", []byte("\r")).Return(nil)

	tc := &terminalWSConn{
		manager:   s.terminal,
		sessionID: "sid-1",
		logger:    slog.Default(),
	}
	tc.enableAutoAccept()

	// First prompt fires (with retries).
	tc.scanAutoAccept([]byte("Entertoconfirm · Esc to cancel"))
	time.Sleep(3 * time.Second)

	// Second prompt also fires.
	tc.scanAutoAccept([]byte("Entertoconfirm · Esc to cancel"))
	time.Sleep(3 * time.Second)

	// Each trigger sends Enter 3 times (retries), so 6 total.
	s.terminal.AssertNumberOfCalls(s.T(), "SendInput", 6)
}

func (s *TerminalHandlerSuite) TestAutoAcceptSendError() {
	s.terminal.On("SendInput", "sid-1", []byte("\r")).Return(errors.New("session gone"))

	tc := &terminalWSConn{
		manager:   s.terminal,
		sessionID: "sid-1",
		logger:    slog.Default(),
	}
	tc.enableAutoAccept()

	tc.scanAutoAccept([]byte("Entertoconfirm"))
	time.Sleep(700 * time.Millisecond)
	// Error on first retry, stops immediately.
	s.terminal.AssertNumberOfCalls(s.T(), "SendInput", 1)
}

func (s *TerminalHandlerSuite) TestAutoAcceptDisabledByDefault() {
	tc := &terminalWSConn{
		manager:   s.terminal,
		sessionID: "sid-1",
		logger:    slog.Default(),
	}
	// No enableAutoAccept() — should not scan.
	tc.scanAutoAccept([]byte("Entertoconfirm"))
	time.Sleep(50 * time.Millisecond)
	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
}

func (s *TerminalHandlerSuite) TestDisableAutoAcceptStopsTrigger() {
	tc := &terminalWSConn{
		manager:   s.terminal,
		sessionID: "sid-1",
		logger:    slog.Default(),
	}
	tc.enableAutoAccept()
	tc.disableAutoAccept()

	// After disable, the trigger string must be ignored even with budget previously armed.
	tc.scanAutoAccept([]byte("Entertoconfirm · Esc to cancel"))
	time.Sleep(700 * time.Millisecond)
	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
}

// --- Claude exit-marker relaunch tests ---

func (s *TerminalHandlerSuite) newRelaunchConn(builder *MockInteractiveCmdBuilder) *terminalWSConn {
	tc := &terminalWSConn{
		manager:    s.terminal,
		cmdBuilder: builder,
		logger:     slog.Default(),
	}
	tc.setSessionID("sid-1")
	return tc
}

// TestScanClaudeExitRelaunchesOnAbnormalExit proves an abnormal exit code
// triggers a --continue relaunch of the interactive Claude command.
func (s *TerminalHandlerSuite) TestScanClaudeExitRelaunchesOnAbnormalExit() {
	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildContinueCmd", "ch-1", "/work", "", "agent-0").Return("claude --continue")
	relaunched := onSendInputCalled(s.terminal, "sid-1", []byte("claude --continue\n"))

	tc := s.newRelaunchConn(builder)
	tc.enableRelaunch("ch-1", "/work", "", "agent-0")

	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:137\n"))

	select {
	case <-relaunched:
	case <-time.After(3 * time.Second):
		s.T().Fatal("timed out waiting for relaunch SendInput")
	}
	builder.AssertExpectations(s.T())
}

// TestScanClaudeExitCleanExitDoesNotRelaunch proves exit code 0 (deliberate
// quit) disables relaunching instead of firing it.
func (s *TerminalHandlerSuite) TestScanClaudeExitCleanExitDoesNotRelaunch() {
	builder := new(MockInteractiveCmdBuilder)
	tc := s.newRelaunchConn(builder)
	tc.enableRelaunch("ch-1", "/work", "", "agent-0")

	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:0\n"))
	time.Sleep(1200 * time.Millisecond)

	s.terminal.AssertNotCalled(s.T(), "SendInput", "sid-1", []byte("claude --continue\n"))
	builder.AssertNotCalled(s.T(), "BuildContinueCmd", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	// A subsequent abnormal marker must not relaunch either — budget is zeroed.
	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:1\n"))
	time.Sleep(1200 * time.Millisecond)
	s.terminal.AssertNotCalled(s.T(), "SendInput", "sid-1", []byte("claude --continue\n"))
}

// TestScanClaudeExitDisabledByDefault proves relaunching is a no-op when
// enableRelaunch was never called (e.g. explicit cmd / sessions-panel panes).
func (s *TerminalHandlerSuite) TestScanClaudeExitDisabledByDefault() {
	builder := new(MockInteractiveCmdBuilder)
	tc := s.newRelaunchConn(builder)

	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:137\n"))
	time.Sleep(1200 * time.Millisecond)

	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
	builder.AssertNotCalled(s.T(), "BuildContinueCmd", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestScanClaudeExitBudgetExhausted proves relaunching stops after
// maxClaudeRelaunches abnormal exits in a row.
func (s *TerminalHandlerSuite) TestScanClaudeExitBudgetExhausted() {
	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildContinueCmd", "ch-1", "/work", "", "agent-0").Return("claude --continue")
	s.terminal.On("SendInput", "sid-1", []byte("claude --continue\n")).Return(nil)

	tc := s.newRelaunchConn(builder)
	tc.enableRelaunch("ch-1", "/work", "", "agent-0")
	require.Equal(s.T(), maxClaudeRelaunches, tc.relaunchRemaining)

	for i := 0; i < maxClaudeRelaunches; i++ {
		tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:137\n"))
	}
	time.Sleep(1200 * time.Millisecond)
	s.terminal.AssertNumberOfCalls(s.T(), "SendInput", maxClaudeRelaunches)

	// One more abnormal exit beyond the budget must not relaunch again.
	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:137\n"))
	time.Sleep(1200 * time.Millisecond)
	s.terminal.AssertNumberOfCalls(s.T(), "SendInput", maxClaudeRelaunches)
}

// TestScanClaudeExitNoMarkerIsNoop proves output without the exit marker is
// ignored.
func (s *TerminalHandlerSuite) TestScanClaudeExitNoMarkerIsNoop() {
	builder := new(MockInteractiveCmdBuilder)
	tc := s.newRelaunchConn(builder)
	tc.enableRelaunch("ch-1", "/work", "", "agent-0")

	tc.scanClaudeExit([]byte("some ordinary output\n"))
	time.Sleep(200 * time.Millisecond)

	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
}

// TestScanClaudeExitSkipsIfSessionMovedOn proves a relaunch scheduled before
// the pane detached/stopped/closed does not fire into a session the client
// no longer owns.
func (s *TerminalHandlerSuite) TestScanClaudeExitSkipsIfSessionMovedOn() {
	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildContinueCmd", "ch-1", "/work", "", "agent-0").Return("claude --continue").Maybe()

	tc := s.newRelaunchConn(builder)
	tc.enableRelaunch("ch-1", "/work", "", "agent-0")

	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:137\n"))
	// Simulate the pane moving to a new session before the 1s relaunch delay fires.
	tc.setSessionID("sid-2")
	time.Sleep(1200 * time.Millisecond)

	s.terminal.AssertNotCalled(s.T(), "SendInput", "sid-1", mock.Anything)
}

// TestScanClaudeExitRelaunchSendError proves a SendInput failure during
// relaunch is logged rather than panicking.
func (s *TerminalHandlerSuite) TestScanClaudeExitRelaunchSendError() {
	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildContinueCmd", "ch-1", "/work", "", "agent-0").Return("claude --continue")
	sendCh := make(chan struct{}, 1)
	s.terminal.On("SendInput", "sid-1", []byte("claude --continue\n")).
		Return(errors.New("session gone")).
		Run(func(_ mock.Arguments) { sendCh <- struct{}{} })

	tc := s.newRelaunchConn(builder)
	tc.enableRelaunch("ch-1", "/work", "", "agent-0")

	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:137\n"))

	select {
	case <-sendCh:
	case <-time.After(3 * time.Second):
		s.T().Fatal("timed out waiting for relaunch SendInput")
	}
	builder.AssertExpectations(s.T())
}

// TestDisableRelaunchClearsState proves detach/stop/close paths (via
// disableRelaunch) prevent a later marker from relaunching.
func (s *TerminalHandlerSuite) TestDisableRelaunchClearsState() {
	builder := new(MockInteractiveCmdBuilder)
	tc := s.newRelaunchConn(builder)
	tc.enableRelaunch("ch-1", "/work", "", "agent-0")
	tc.disableRelaunch()

	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:137\n"))
	time.Sleep(1200 * time.Millisecond)

	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
	builder.AssertNotCalled(s.T(), "BuildContinueCmd", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestScanClaudeExitNoMarkerIsNoopForNonNumeric proves the marker regex
// requires digits, so trailing non-numeric junk after the prefix is not
// treated as an exit code.
func (s *TerminalHandlerSuite) TestScanClaudeExitNoMarkerIsNoopForNonNumeric() {
	require.Nil(s.T(), claudeExitMarkerRe.FindSubmatch([]byte("__LOOP_CLAUDE_EXIT:abc")))
}

// TestScanClaudeExitOverflowingCodeIsNoop guards the strconv.Atoi error path:
// a numeric-looking but int-overflowing exit code must be ignored rather than
// panicking or relaunching.
func (s *TerminalHandlerSuite) TestScanClaudeExitOverflowingCodeIsNoop() {
	builder := new(MockInteractiveCmdBuilder)
	tc := s.newRelaunchConn(builder)
	tc.enableRelaunch("ch-1", "/work", "", "agent-0")

	tc.scanClaudeExit([]byte("__LOOP_CLAUDE_EXIT:99999999999999999999999\n"))
	time.Sleep(200 * time.Millisecond)

	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
	builder.AssertNotCalled(s.T(), "BuildContinueCmd", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
