package container

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

// fakeLiveProc is a live session's claude process: the test reads the lines
// written to its stdin and writes its stdout, framed as the attach stream
// frames it.
type fakeLiveProc struct {
	stdin  chan string
	inR    *io.PipeReader
	inW    *io.PipeWriter
	outR   *io.PipeReader
	outW   *io.PipeWriter
	out    io.Writer
	closed chan struct{}
	once   sync.Once
}

func newFakeLiveProc() *fakeLiveProc {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &fakeLiveProc{
		stdin:  make(chan string, 16),
		inR:    inR,
		inW:    inW,
		outR:   outR,
		outW:   outW,
		out:    stdcopy.NewStdWriter(outW, stdcopy.Stdout),
		closed: make(chan struct{}),
	}
	go func() {
		br := bufio.NewReader(inR)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			p.stdin <- line
		}
	}()
	return p
}

func (p *fakeLiveProc) stream() *AttachStream {
	return &AttachStream{Writer: p.inW, Reader: p.outR, Close: p.close}
}

func (p *fakeLiveProc) close() {
	p.once.Do(func() {
		close(p.closed)
		_ = p.outW.Close()
		_ = p.inR.Close()
	})
}

// emit writes lines to the process's stdout.
func (p *fakeLiveProc) emit(t *testing.T, lines ...string) {
	t.Helper()
	for _, l := range lines {
		_, err := p.out.Write([]byte(l + "\n"))
		require.NoError(t, err)
	}
}

// fakeTimer is a timer the test fires by hand.
type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped atomic.Bool
}

func (t *fakeTimer) Stop() bool { return !t.stopped.Swap(true) }

// fakeClock hands out fakeTimers, in the order they are armed.
type fakeClock struct{ armed chan *fakeTimer }

func (c *fakeClock) afterFunc(d time.Duration, f func()) stopper {
	t := &fakeTimer{d: d, f: f}
	c.armed <- t
	return t
}

// waitFor returns the next value from ch, failing the test if none comes.
func waitFor[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting")
	}
	var zero T
	return zero
}

// requireNone fails if ch holds a value.
func requireNone[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %v", v)
	default:
	}
}

type liveRun struct {
	resp *agent.AgentResponse
	err  error
}

// liveHookLog records what a live session reports through its hooks.
type liveHookLog struct {
	ended     chan string
	restarted chan string
	system    chan string
	prompts   chan string
	activity  chan string
	begun     chan struct{}
	done      chan liveRun
}

func newLiveHookLog() *liveHookLog {
	return &liveHookLog{
		ended:     make(chan string, 4),
		restarted: make(chan string, 4),
		system:    make(chan string, 16),
		prompts:   make(chan string, 16),
		activity:  make(chan string, 16),
		begun:     make(chan struct{}, 16),
		done:      make(chan liveRun, 16),
	}
}

// hooks reports turns the process starts by itself to a request whose
// OnTurn goes to turns, or to none when turns is nil.
func (l *liveHookLog) hooks(turns chan string) agent.LiveHooks {
	return agent.LiveHooks{
		BeginTurn: func() (*agent.AgentRequest, func(*agent.AgentResponse, error)) {
			l.begun <- struct{}{}
			if turns == nil {
				return nil, nil
			}
			req := &agent.AgentRequest{OnTurn: func(text string, _ agent.TurnRef) { turns <- text }}
			return req, func(resp *agent.AgentResponse, err error) { l.done <- liveRun{resp, err} }
		},
		Activity: func(activity, detail string) { l.activity <- activity + "=" + detail },
		BackgroundTasks: func(tasks []agent.BackgroundTask) {
			l.activity <- "background_tasks=" + agent.BackgroundSummary(tasks)
		},
		Ended:     func(reason string) { l.ended <- reason },
		Restarted: func(reason string) { l.restarted <- reason },
		System: func(subtype string, event json.RawMessage) {
			l.system <- subtype + "=" + string(event)
		},
		Prompt: func(text string) { l.prompts <- text },
	}
}

// doneSignalCtx tells waiting when Done is asked for, which a live turn
// does only once it waits for the running turn to end.
type doneSignalCtx struct {
	context.Context
	waiting chan struct{}
}

func (c doneSignalCtx) Done() <-chan struct{} {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

const liveStartedAt = "2026-10-15T10:00:00Z"

// setupLive gets the runner ready for live sessions that end after idle,
// with timers the test fires.
func (s *RunnerSuite) setupLive(idle time.Duration) *fakeClock {
	s.cfg.LiveSessionIdle = idle
	clock := &fakeClock{armed: make(chan *fakeTimer, 16)}
	s.runner.afterFunc = clock.afterFunc
	s.runner.logger = slog.New(slog.DiscardHandler)
	started, _ := time.Parse(time.RFC3339, liveStartedAt)
	s.runner.now = func() time.Time { return started }
	reg := new(MockContainerRegistry)
	reg.On("Register", mock.Anything).Maybe()
	reg.On("ScheduleRemove", mock.Anything, mock.Anything).Maybe()
	s.runner.SetContainerRegistry(reg)
	return clock
}

// expectLiveSpawn expects one live container to be started for proc, and
// returns where its config goes.
func (s *RunnerSuite) expectLiveSpawn(id string, proc *fakeLiveProc) <-chan *ContainerConfig {
	created := make(chan *ContainerConfig, 1)
	s.client.On("ContainerCreate", mock.Anything, mock.MatchedBy(func(c *ContainerConfig) bool { return c.OpenStdin }), mock.Anything).
		Run(func(args mock.Arguments) { created <- args.Get(1).(*ContainerConfig) }).
		Return(id, nil).Once()
	s.client.On("ContainerStart", mock.Anything, id).Return(nil).Once()
	s.client.On("ContainerAttach", mock.Anything, id).Return(proc.stream(), nil).Once()
	s.client.On("ContainerStop", mock.Anything, id).Return(nil).Maybe()
	return created
}

// startLive starts ch-1's live session from req on proc.
func (s *RunnerSuite) startLive(req *agent.AgentRequest, hooks agent.LiveHooks, id string, proc *fakeLiveProc) *ContainerConfig {
	created := s.expectLiveSpawn(id, proc)
	require.NoError(s.T(), s.runner.StartLive(context.Background(), req, hooks))
	return waitFor(s.T(), created)
}

func (s *RunnerSuite) goRun(ctx context.Context, req *agent.AgentRequest) <-chan liveRun {
	out := make(chan liveRun, 1)
	go func() {
		resp, err := s.runner.Run(ctx, req)
		out <- liveRun{resp, err}
	}()
	return out
}

// liveTurn runs a turn of prompt on proc, which answers it with result.
func (s *RunnerSuite) liveTurn(req *agent.AgentRequest, proc *fakeLiveProc, result string) liveRun {
	run := s.goRun(context.Background(), req)
	require.Equal(s.T(), string(userTurnLine(req.Prompt)), waitFor(s.T(), proc.stdin))
	proc.emit(s.T(), `{"type":"system","subtype":"init"}`, result)
	return waitFor(s.T(), run)
}

func liveReq(mod func(*agent.AgentRequest)) *agent.AgentRequest {
	req := &agent.AgentRequest{ChannelID: "ch-1", Live: true}
	if mod != nil {
		mod(req)
	}
	return req
}

func withPrompt(req *agent.AgentRequest, prompt string) *agent.AgentRequest {
	next := *req
	next.Prompt = prompt
	return &next
}

func cmdHas(cmd []string, args ...string) bool {
	for i := range cmd {
		if slices.Equal(cmd[i:min(i+len(args), len(cmd))], args) {
			return true
		}
	}
	return false
}

func (s *RunnerSuite) TestLiveSessionTurns() {
	s.setupLive(0)
	log := newLiveHookLog()
	proc := newFakeLiveProc()
	start := liveReq(func(r *agent.AgentRequest) { r.PlanMode = true })
	cfg := s.startLive(start, log.hooks(nil), "live-1", proc)
	require.True(s.T(), cmdHas(cfg.Cmd, "--input-format", "stream-json"))

	require.ErrorIs(s.T(), s.runner.StartLive(context.Background(), start, log.hooks(nil)), ErrLiveSessionRunning)
	require.Equal(s.T(), []string{"ch-1"}, s.runner.LiveChannels())
	info, ok := s.runner.LiveInfo("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "ch-1", info.ChannelID)
	require.Equal(s.T(), liveStartedAt, info.StartedAt.Format(time.RFC3339))
	require.False(s.T(), info.Busy)

	var mu sync.Mutex
	var events []string
	record := func(format string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, fmt.Sprintf(format, a...))
	}
	req := *start
	req.Messages = []agent.AgentMessage{{Role: "user", Content: "hello"}}
	req.OnTurn = func(text string, ref agent.TurnRef) { record("turn %s %s", text, ref.UUID) }
	req.OnThinking = func(text string) { record("thinking %s", text) }
	req.OnToolUse = func(id, name, _ string) { record("tool %s %s", id, name) }
	req.OnToolUseRaw = func(id, _, raw string) { record("raw %s %s", id, raw) }
	req.OnToolResult = func(id, out string, isErr bool) { record("result %s %s %v", id, out, isErr) }
	req.OnActivity = func(activity, detail string) { record("activity %s=%s", activity, detail) }
	req.OnBackgroundTasks = func(tasks []agent.BackgroundTask) {
		record("activity background_tasks=%s", agent.BackgroundSummary(tasks))
	}
	req.OnSession = func(id string) { record("session %s", id) }

	run := s.goRun(context.Background(), &req)
	require.Equal(s.T(), string(userTurnLine(planModePromptPrefix+"hello")), waitFor(s.T(), proc.stdin))
	proc.emit(s.T(),
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","session_id":"sess-1","uuid":"u1","message":{"model":"claude-x","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Hello"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"system","subtype":"background_tasks_changed","tasks":[{"description":"watch CI"}]}`,
		`{"type":"result","subtype":"success","result":"done","session_id":"sess-1","num_turns":2,"duration_ms":7,"stop_reason":"end_turn"}`,
	)
	got := waitFor(s.T(), run)
	require.NoError(s.T(), got.err)
	require.Equal(s.T(), &agent.AgentResponse{
		Response: "done", SessionID: "sess-1", DurationMs: 7, NumTurns: 2, StopReason: "end_turn", Model: "claude-x",
	}, got.resp)
	mu.Lock()
	require.Equal(s.T(), []string{
		"session sess-1",
		"activity model=claude-x",
		"turn Hello u1",
		"thinking hmm",
		"tool t1 Bash",
		`raw t1 {"command":"ls"}`,
		"result t1 ok false",
		"activity background_tasks=1 background task: watch CI",
	}, events)
	mu.Unlock()

	info, _ = s.runner.LiveInfo("ch-1")
	require.Equal(s.T(), "sess-1", info.SessionID)
	require.Equal(s.T(), "1 background task: watch CI", info.Background)

	// An error result, with the output that isn't JSON as its error.
	req2 := withPrompt(&agent.AgentRequest{ChannelID: "ch-1", Live: true, PlanMode: true, SessionID: "sess-1"}, "again")
	run = s.goRun(context.Background(), req2)
	require.Equal(s.T(), string(userTurnLine(planModePromptPrefix+"again")), waitFor(s.T(), proc.stdin))
	proc.emit(s.T(), "not json", `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"sess-1"}`)
	got = waitFor(s.T(), run)
	require.ErrorContains(s.T(), got.err, "claude returned error: error_during_execution")
	require.Equal(s.T(), "sess-1", got.resp.SessionID)
	require.Contains(s.T(), got.resp.Error, "not json")

	require.NoError(s.T(), s.runner.InterruptLive("ch-1"))
	require.Equal(s.T(), string(interruptLine), waitFor(s.T(), proc.stdin))

	// A background task stops by its id; an unknown one isn't asked to.
	proc.emit(s.T(), `{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"b1","task_type":"monitor","description":"watch CI"}]}`)
	require.Eventually(s.T(), func() bool {
		info, _ := s.runner.LiveInfo("ch-1")
		return len(info.BackgroundTasks) == 1 && info.BackgroundTasks[0].ID == "b1"
	}, time.Second, time.Millisecond)
	require.ErrorIs(s.T(), s.runner.StopLiveTask("ch-1", "nope"), ErrNoLiveTask)
	require.NoError(s.T(), s.runner.StopLiveTask("ch-1", "b1"))
	require.JSONEq(s.T(),
		`{"type":"control_request","request_id":"loop-stop-task-b1","request":{"subtype":"stop_task","task_id":"b1"}}`,
		waitFor(s.T(), proc.stdin))

	require.NoError(s.T(), s.runner.StopLive("ch-1", "stopped by user"))
	require.Equal(s.T(), "stopped by user", waitFor(s.T(), log.ended))
	waitFor(s.T(), proc.closed)
	s.client.AssertCalled(s.T(), "ContainerStop", mock.Anything, "live-1")
	s.sys.AssertCalled(s.T(), "Remove", mock.MatchedBy(func(p string) bool { return strings.HasSuffix(p, ".json") }))

	require.Empty(s.T(), s.runner.LiveChannels())
	_, ok = s.runner.LiveInfo("ch-1")
	require.False(s.T(), ok)
	require.ErrorIs(s.T(), s.runner.StopLive("ch-1", "again"), ErrNoLiveSession)
	require.ErrorIs(s.T(), s.runner.InterruptLive("ch-1"), ErrNoLiveSession)
	require.ErrorIs(s.T(), s.runner.StopLiveTask("ch-1", "b1"), ErrNoLiveSession)
	requireNone(s.T(), log.ended)
}

func (s *RunnerSuite) TestRunLiveWithoutSessionRunsOneShot() {
	ctx := context.Background()
	s.setupMockRun(ctx, mock.MatchedBy(func(c *ContainerConfig) bool { return !c.OpenStdin }), testContainerName, testJSONOK)
	resp, err := s.runner.Run(ctx, withPrompt(liveReq(nil), "hi"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
}

func (s *RunnerSuite) TestStartLiveMissingTranscriptStartsFresh() {
	s.setupLive(0)
	s.runner.transcriptMissing = func(_, sessionID string) bool { return sessionID == "gone" }
	log := newLiveHookLog()
	proc := newFakeLiveProc()
	start := liveReq(func(r *agent.AgentRequest) { r.SessionID = "gone" })
	cfg := s.startLive(start, log.hooks(nil), "live-1", proc)
	require.NotContains(s.T(), cfg.Cmd, "--resume")

	// The channel still asks for the session it was started for: that's no
	// move, so the same process takes the turn.
	got := s.liveTurn(withPrompt(start, "hi"), proc, `{"type":"result","result":"fresh","session_id":"sess-new","num_turns":1}`)
	require.NoError(s.T(), got.err)
	require.Equal(s.T(), "sess-new", got.resp.SessionID)
	require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
}

func (s *RunnerSuite) TestStartLiveErrors() {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		setup   func(proc *fakeLiveProc)
		wantErr string
	}{
		{
			name: "project config",
			setup: func(*fakeLiveProc) {
				s.runner.loadProjectConfig = func(string, *config.Config) (*config.Config, error) { return nil, boom }
			},
			wantErr: "loading project config: boom",
		},
		{
			name: "create",
			setup: func(*fakeLiveProc) {
				s.client.On("ContainerCreate", mock.Anything, mock.Anything, mock.Anything).Return("", boom)
			},
			wantErr: "creating container: boom",
		},
		{
			name: "start",
			setup: func(*fakeLiveProc) {
				s.client.On("ContainerCreate", mock.Anything, mock.Anything, mock.Anything).Return("live-1", nil)
				s.client.On("ContainerStart", mock.Anything, "live-1").Return(boom)
			},
			wantErr: "starting container: boom",
		},
		{
			name: "attach",
			setup: func(*fakeLiveProc) {
				s.client.On("ContainerCreate", mock.Anything, mock.Anything, mock.Anything).Return("live-1", nil)
				s.client.On("ContainerStart", mock.Anything, "live-1").Return(nil)
				s.client.On("ContainerAttach", mock.Anything, "live-1").Return(nil, boom)
				s.client.On("ContainerStop", mock.Anything, "live-1").Return(nil).Once()
			},
			wantErr: "attaching to the live session: boom",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLive(0)
			tc.setup(newFakeLiveProc())
			err := s.runner.StartLive(context.Background(), liveReq(nil), agent.LiveHooks{})
			require.EqualError(s.T(), err, tc.wantErr)
			require.Empty(s.T(), s.runner.LiveChannels())
			s.client.AssertExpectations(s.T())
		})
	}
}

func (s *RunnerSuite) TestRunLiveSessionChanged() {
	tests := []struct {
		name string
		// start is what the session is started from.
		start func(*agent.AgentRequest)
		// use runs a turn before the move.
		use  bool
		next func(*agent.AgentRequest)
		want []string
	}{
		{
			name:  "fork asked before the first turn",
			start: func(r *agent.AgentRequest) { r.SessionID = "parent" },
			next:  func(r *agent.AgentRequest) { r.SessionID, r.ForkSession, r.ResumeAt = "parent", true, "u9" },
			want:  []string{"--fork-session"},
		},
		{
			name: "another session after a turn",
			use:  true,
			next: func(r *agent.AgentRequest) { r.SessionID = "sess-2" },
			want: []string{"--resume", "sess-2"},
		},
		{
			name: "fork after a turn",
			use:  true,
			next: func(r *agent.AgentRequest) { r.SessionID, r.ForkSession = "sess-1", true },
			want: []string{"--fork-session"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLive(0)
			log := newLiveHookLog()
			proc := newFakeLiveProc()
			start := liveReq(tc.start)
			s.startLive(start, log.hooks(nil), "live-1", proc)
			if tc.use {
				got := s.liveTurn(withPrompt(start, "one"), proc, `{"type":"result","result":"a","session_id":"sess-1","num_turns":1}`)
				require.NoError(s.T(), got.err)
			}

			proc2 := newFakeLiveProc()
			created := s.expectLiveSpawn("live-2", proc2)
			got := s.liveTurn(withPrompt(liveReq(tc.next), "two"), proc2, `{"type":"result","result":"b","session_id":"sess-3","num_turns":1}`)
			require.NoError(s.T(), got.err)
			require.Equal(s.T(), "b", got.resp.Response)
			require.True(s.T(), cmdHas(waitFor(s.T(), created).Cmd, tc.want...))

			// The old process is dropped without telling hooks.Ended: the
			// session goes on.
			waitFor(s.T(), proc.closed)
			s.client.AssertCalled(s.T(), "ContainerStop", mock.Anything, "live-1")
			requireNone(s.T(), log.ended)
			require.Equal(s.T(), "session changed", waitFor(s.T(), log.restarted))
			require.Equal(s.T(), []string{"ch-1"}, s.runner.LiveChannels())
			info, _ := s.runner.LiveInfo("ch-1")
			require.Equal(s.T(), liveStartedAt, info.StartedAt.Format(time.RFC3339))

			require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
			require.Equal(s.T(), "done", waitFor(s.T(), log.ended))
		})
	}
}

func (s *RunnerSuite) TestRunLiveConfigChanged() {
	changed := func(string, *config.Config) (*config.Config, error) {
		cfg := *s.cfg
		cfg.ClaudeModel = "other-model"
		return &cfg, nil
	}
	tests := []struct {
		name  string
		start func(*agent.AgentRequest)
		use   bool
		want  []string
		// wantNot must not be in the new command.
		wantNot string
	}{
		{
			name:    "goes on with the session the process is on",
			use:     true,
			want:    []string{"--resume", "sess-1"},
			wantNot: "--fork-session",
		},
		{
			name:  "a fork that has no session of its own yet is started again",
			start: func(r *agent.AgentRequest) { r.SessionID, r.ForkSession, r.ResumeAt = "parent", true, "u1" },
			want:  []string{"--resume", "parent", "--fork-session", "--resume-session-at=u1"},
		},
		{
			name:    "a new session is started new again",
			wantNot: "--resume",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLive(0)
			log := newLiveHookLog()
			proc := newFakeLiveProc()
			start := liveReq(tc.start)
			s.startLive(start, log.hooks(nil), "live-1", proc)
			next := start
			if tc.use {
				got := s.liveTurn(withPrompt(start, "one"), proc, `{"type":"result","result":"a","session_id":"sess-1","num_turns":1}`)
				require.NoError(s.T(), got.err)
				next = liveReq(func(r *agent.AgentRequest) { r.SessionID = "sess-1" })
			}

			s.runner.loadProjectConfig = changed
			proc2 := newFakeLiveProc()
			created := s.expectLiveSpawn("live-2", proc2)
			got := s.liveTurn(withPrompt(next, "two"), proc2, `{"type":"result","result":"b","session_id":"sess-2","num_turns":1}`)
			require.NoError(s.T(), got.err)
			cmd := waitFor(s.T(), created).Cmd
			for _, arg := range tc.want {
				require.Contains(s.T(), cmd, arg)
			}
			if tc.wantNot != "" {
				require.NotContains(s.T(), cmd, tc.wantNot)
			}
			requireNone(s.T(), log.ended)
			require.Equal(s.T(), "config changed", waitFor(s.T(), log.restarted))
			require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
		})
	}
}

func (s *RunnerSuite) TestRunLiveConfigUnreadableKeepsProcess() {
	s.setupLive(0)
	proc := newFakeLiveProc()
	start := liveReq(nil)
	s.startLive(start, agent.LiveHooks{}, "live-1", proc)
	s.runner.loadProjectConfig = func(string, *config.Config) (*config.Config, error) { return nil, errors.New("bad") }
	got := s.liveTurn(withPrompt(start, "hi"), proc, `{"type":"result","result":"same","num_turns":1}`)
	require.NoError(s.T(), got.err)
	require.Equal(s.T(), "same", got.resp.Response)
	require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
}

func (s *RunnerSuite) TestRunLiveRestartFails() {
	s.setupLive(0)
	log := newLiveHookLog()
	proc := newFakeLiveProc()
	s.startLive(liveReq(nil), log.hooks(nil), "live-1", proc)
	s.client.On("ContainerCreate", mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("boom")).Once()

	_, err := s.runner.Run(context.Background(), withPrompt(liveReq(func(r *agent.AgentRequest) { r.SessionID = "other" }), "hi"))
	require.EqualError(s.T(), err, "creating container: boom")
	require.Equal(s.T(), "restart failed: creating container: boom", waitFor(s.T(), log.ended))
	require.Empty(s.T(), s.runner.LiveChannels())
	waitFor(s.T(), proc.closed)
}

func (s *RunnerSuite) TestRunLiveSessionEndedMeanwhile() {
	tests := []struct {
		name string
		req  func(*agent.AgentRequest)
	}{
		{name: "while restarting", req: func(r *agent.AgentRequest) { r.SessionID = "other" }},
		{name: "while claiming the turn"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLive(0)
			proc := newFakeLiveProc()
			s.startLive(liveReq(nil), agent.LiveHooks{}, "live-1", proc)
			// Ended by another goroutine after this turn found it.
			ls := s.runner.liveSession("ch-1")
			ls.mu.Lock()
			ls.closed = true
			ls.mu.Unlock()

			ctx := context.Background()
			s.setupMockRun(ctx, mock.MatchedBy(func(c *ContainerConfig) bool { return !c.OpenStdin }), testContainerName, testJSONOK)
			resp, err := s.runner.Run(ctx, withPrompt(liveReq(tc.req), "hi"))
			require.NoError(s.T(), err)
			require.Equal(s.T(), "ok", resp.Response)
			proc.close()
		})
	}
}

func (s *RunnerSuite) TestRunLiveStdinClosed() {
	tests := []struct {
		name    string
		start   func(*agent.AgentRequest)
		want    []string
		wantNot string
	}{
		{
			name:  "a pending fork runs as asked",
			start: func(r *agent.AgentRequest) { r.SessionID, r.ForkSession, r.ResumeAt = "parent", true, "u1" },
			want:  []string{"--resume", "parent", "--fork-session", "--resume-session-at=u1"},
		},
		{
			name:    "resumes the session the process is on",
			start:   func(r *agent.AgentRequest) { r.SessionID = "sess-1" },
			want:    []string{"--resume", "sess-1"},
			wantNot: "--fork-session",
		},
		{
			name:    "a new session runs new",
			wantNot: "--resume",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLive(0)
			log := newLiveHookLog()
			proc := newFakeLiveProc()
			start := liveReq(tc.start)
			s.startLive(start, log.hooks(nil), "live-1", proc)
			_ = proc.inR.CloseWithError(errors.New("gone"))

			ctx := context.Background()
			created := make(chan []string, 1)
			s.setupMockRun(ctx, mock.MatchedBy(func(c *ContainerConfig) bool {
				if c.OpenStdin {
					return false
				}
				select {
				case created <- c.Cmd:
				default:
				}
				return true
			}), testContainerName, testJSONOK)
			resp, err := s.runner.Run(ctx, withPrompt(start, "hi"))
			require.NoError(s.T(), err)
			require.Equal(s.T(), "ok", resp.Response)
			require.Equal(s.T(), "stdin closed", waitFor(s.T(), log.ended))
			cmd := waitFor(s.T(), created)
			for _, arg := range tc.want {
				require.Contains(s.T(), cmd, arg)
			}
			if tc.wantNot != "" {
				require.NotContains(s.T(), cmd, tc.wantNot)
			}
		})
	}
}

func (s *RunnerSuite) TestRunLiveCancelled() {
	tests := []struct {
		name string
		// finish ends the interrupted turn, given its grace timer.
		finish    func(proc *fakeLiveProc, grace *fakeTimer)
		wantResp  *agent.AgentResponse
		wantEnded string
		wantStop  bool
	}{
		{
			name: "the result still comes",
			finish: func(proc *fakeLiveProc, _ *fakeTimer) {
				proc.emit(s.T(), `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"sess-1"}`)
			},
			wantResp: &agent.AgentResponse{SessionID: "sess-1"},
		},
		{
			name:      "the process is stuck",
			finish:    func(_ *fakeLiveProc, grace *fakeTimer) { grace.f() },
			wantEnded: "interrupt timed out",
			wantStop:  true,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			clock := s.setupLive(0)
			log := newLiveHookLog()
			proc := newFakeLiveProc()
			start := liveReq(nil)
			s.startLive(start, log.hooks(nil), "live-1", proc)

			ctx, cancel := context.WithCancel(context.Background())
			run := s.goRun(ctx, withPrompt(start, "hi"))
			waitFor(s.T(), proc.stdin)
			proc.emit(s.T(), `{"type":"system","subtype":"init"}`)
			cancel()
			require.Equal(s.T(), string(interruptLine), waitFor(s.T(), proc.stdin))
			grace := waitFor(s.T(), clock.armed)
			require.Equal(s.T(), liveInterruptGrace, grace.d)

			tc.finish(proc, grace)
			got := waitFor(s.T(), run)
			require.ErrorIs(s.T(), got.err, context.Canceled)
			require.Equal(s.T(), tc.wantResp, got.resp)
			if tc.wantEnded != "" {
				require.Equal(s.T(), tc.wantEnded, waitFor(s.T(), log.ended))
			} else {
				requireNone(s.T(), log.ended)
			}
			if tc.wantStop {
				s.client.AssertCalled(s.T(), "ContainerStop", mock.Anything, "live-1")
			} else {
				s.client.AssertNotCalled(s.T(), "ContainerStop", mock.Anything, "live-1")
			}
			require.True(s.T(), grace.stopped.Load())
			_ = s.runner.StopLive("ch-1", "done")
		})
	}
}

func (s *RunnerSuite) TestLiveOwnTurns() {
	s.setupLive(0)
	log := newLiveHookLog()
	turns := make(chan string, 4)
	proc := newFakeLiveProc()
	start := liveReq(nil)
	s.startLive(start, log.hooks(turns), "live-1", proc)

	// Activity while no turn runs.
	proc.emit(s.T(), `{"type":"system","subtype":"status","status":"compacting","description":"busy"}`)
	require.Equal(s.T(), "compacting=busy", waitFor(s.T(), log.activity))

	// A turn the process starts when a background task ends.
	proc.emit(s.T(),
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"task done"}]}}`,
		`{"type":"result","result":"finished","session_id":"sess-1","num_turns":1}`,
	)
	waitFor(s.T(), log.begun)
	require.Equal(s.T(), "task done", waitFor(s.T(), turns))
	got := waitFor(s.T(), log.done)
	require.NoError(s.T(), got.err)
	require.Equal(s.T(), "finished", got.resp.Response)

	// One the process opens with an "init" while it drains a notification
	// ends with the flush result, which answers nothing.
	proc.emit(s.T(),
		`{"type":"system","subtype":"init"}`,
		`{"type":"result","origin":{"kind":"task-notification"},"num_turns":0,"session_id":"sess-1"}`,
	)
	waitFor(s.T(), log.begun)
	got = waitFor(s.T(), log.done)
	require.NoError(s.T(), got.err)
	require.Empty(s.T(), got.resp.Response)
	require.Eventually(s.T(), func() bool {
		info, _ := s.runner.LiveInfo("ch-1")
		return !info.Busy
	}, 5*time.Second, time.Millisecond)

	// One that comes without an "init", after a flush result that answers
	// none.
	proc.emit(s.T(),
		`{"type":"result","origin":{"kind":"task-notification"},"num_turns":0}`,
		`{"type":"result","is_error":true,"result":"failed","num_turns":1}`,
	)
	waitFor(s.T(), log.begun)
	got = waitFor(s.T(), log.done)
	require.EqualError(s.T(), got.err, "claude returned error: failed")
	require.Equal(s.T(), "failed", got.resp.Error)
	requireNone(s.T(), log.done)

	// A user turn waits for the one the process runs.
	proc.emit(s.T(), `{"type":"system","subtype":"init"}`)
	waitFor(s.T(), log.begun)
	require.Eventually(s.T(), func() bool {
		info, _ := s.runner.LiveInfo("ch-1")
		return info.Busy
	}, 5*time.Second, time.Millisecond)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.runner.Run(cancelled, withPrompt(liveReq(func(r *agent.AgentRequest) { r.SessionID = "sess-1" }), "now"))
	require.ErrorIs(s.T(), err, context.Canceled)
	require.Equal(s.T(), string(interruptLine), waitFor(s.T(), proc.stdin))

	ctx := doneSignalCtx{Context: context.Background(), waiting: make(chan struct{}, 1)}
	run := s.goRun(ctx, withPrompt(liveReq(func(r *agent.AgentRequest) { r.SessionID = "sess-1" }), "next"))
	waitFor(s.T(), ctx.waiting)
	proc.emit(s.T(), `{"type":"result","result":"bg","session_id":"sess-1","num_turns":1}`)
	require.Equal(s.T(), "bg", waitFor(s.T(), log.done).resp.Response)
	require.Equal(s.T(), string(userTurnLine("next")), waitFor(s.T(), proc.stdin))
	proc.emit(s.T(), `{"type":"result","result":"mine","session_id":"sess-1","num_turns":1}`)
	require.Equal(s.T(), "mine", waitFor(s.T(), run).resp.Response)

	// The process exits during a turn of its own.
	proc.emit(s.T(), `{"type":"system","subtype":"init"}`)
	waitFor(s.T(), log.begun)
	proc.close()
	require.Equal(s.T(), "exited", waitFor(s.T(), log.ended))
	got = waitFor(s.T(), log.done)
	require.ErrorIs(s.T(), got.err, errLiveEnded)
	require.Empty(s.T(), s.runner.LiveChannels())
}

func (s *RunnerSuite) TestLiveOwnTurnsUnreported() {
	tests := []struct {
		name string
		// answered is set when done still gets the unstreamed turn's answer.
		answered bool
		hooks    func(log *liveHookLog) agent.LiveHooks
	}{
		{name: "no hooks", hooks: func(*liveHookLog) agent.LiveHooks { return agent.LiveHooks{} }},
		{name: "no request", hooks: func(log *liveHookLog) agent.LiveHooks { return log.hooks(nil) }},
		{name: "busy channel", answered: true, hooks: func(log *liveHookLog) agent.LiveHooks {
			hooks := log.hooks(nil)
			hooks.BeginTurn = func() (*agent.AgentRequest, func(*agent.AgentResponse, error)) {
				return nil, func(resp *agent.AgentResponse, err error) { log.done <- liveRun{resp, err} }
			}
			return hooks
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLive(0)
			log := newLiveHookLog()
			proc := newFakeLiveProc()
			s.startLive(liveReq(nil), tc.hooks(log), "live-1", proc)
			proc.emit(s.T(),
				`{"type":"system","subtype":"status","status":"compacting"}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"before the turn"}]}}`,
				`{"type":"system","subtype":"init"}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"x"}]}}`,
				`{"type":"result","result":"r","session_id":"sess-9","num_turns":1}`,
			)
			// The result's session is the session's, once handled.
			require.Eventually(s.T(), func() bool {
				info, _ := s.runner.LiveInfo("ch-1")
				return !info.Busy && info.SessionID == "sess-9"
			}, 5*time.Second, time.Millisecond)
			got := s.liveTurn(withPrompt(liveReq(func(r *agent.AgentRequest) { r.SessionID = "sess-9" }), "hi"), proc,
				`{"type":"result","result":"ok","num_turns":1}`)
			require.NoError(s.T(), got.err)
			require.Equal(s.T(), "ok", got.resp.Response)
			if tc.answered {
				require.Equal(s.T(), "r", waitFor(s.T(), log.done).resp.Response)
			}
			requireNone(s.T(), log.done)
			require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
		})
	}
}

func (s *RunnerSuite) TestLiveStreamEnds() {
	tests := []struct {
		name string
		end  func(proc *fakeLiveProc)
		want string
	}{
		{name: "exit", end: func(p *fakeLiveProc) { _ = p.outW.Close() }, want: "exited"},
		{name: "broken stream", end: func(p *fakeLiveProc) { _ = p.outW.CloseWithError(errors.New("boom")) }, want: "exited: boom"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLive(0)
			log := newLiveHookLog()
			proc := newFakeLiveProc()
			start := liveReq(nil)
			s.startLive(start, log.hooks(nil), "live-1", proc)

			run := s.goRun(context.Background(), withPrompt(start, "hi"))
			waitFor(s.T(), proc.stdin)
			tc.end(proc)
			got := waitFor(s.T(), run)
			require.EqualError(s.T(), got.err, "live session: live session ended")
			require.Equal(s.T(), tc.want, waitFor(s.T(), log.ended))
			require.Empty(s.T(), s.runner.LiveChannels())
		})
	}
}

func (s *RunnerSuite) TestLiveIdle() {
	clock := s.setupLive(time.Minute)
	log := newLiveHookLog()
	proc := newFakeLiveProc()
	start := liveReq(nil)
	s.startLive(start, log.hooks(nil), "live-1", proc)
	first := waitFor(s.T(), clock.armed)
	require.Equal(s.T(), time.Minute, first.d)

	// A turn that leaves a background task running restarts the countdown.
	run := s.goRun(context.Background(), withPrompt(start, "hi"))
	waitFor(s.T(), proc.stdin)
	proc.emit(s.T(),
		`{"type":"system","subtype":"init"}`,
		`{"type":"system","subtype":"background_tasks_changed","tasks":[{"description":"watch"}]}`,
		`{"type":"result","result":"ok","num_turns":1}`,
	)
	require.NoError(s.T(), waitFor(s.T(), run).err)
	second := waitFor(s.T(), clock.armed)
	require.True(s.T(), first.stopped.Load())

	// The countdown from before the turn is stale.
	first.f()
	requireNone(s.T(), log.ended)

	// The background task counts as use.
	second.f()
	third := waitFor(s.T(), clock.armed)
	requireNone(s.T(), log.ended)

	proc.emit(s.T(), `{"type":"system","subtype":"background_tasks_changed","tasks":[]}`)
	require.Equal(s.T(), "background_tasks=", waitFor(s.T(), log.activity))
	third.f()
	require.Equal(s.T(), "idle", waitFor(s.T(), log.ended))
	s.client.AssertCalled(s.T(), "ContainerStop", mock.Anything, "live-1")
	require.True(s.T(), third.stopped.Load())
	require.Empty(s.T(), s.runner.LiveChannels())

	third.f()
	requireNone(s.T(), log.ended)
}

// A hold keeps the session from idling out, across its processes, until
// its last holder lets go.
func (s *RunnerSuite) TestLiveHold() {
	clock := s.setupLive(time.Minute)
	log := newLiveHookLog()
	s.runner.HoldLive("ch-1", "b", true)
	s.runner.HoldLive("ch-1", "a", true)
	s.runner.HoldLive("ch-2", "a", true)
	require.Equal(s.T(), map[string][]string{"ch-1": {"a", "b"}, "ch-2": {"a"}}, s.runner.LiveHolds())
	proc := newFakeLiveProc()
	s.startLive(liveReq(nil), log.hooks(nil), "live-1", proc)
	idle := waitFor(s.T(), clock.armed)

	idle.f()
	idle = waitFor(s.T(), clock.armed)
	requireNone(s.T(), log.ended)

	s.runner.HoldLive("ch-1", "a", false)
	idle.f()
	idle = waitFor(s.T(), clock.armed)
	requireNone(s.T(), log.ended)

	s.runner.HoldLive("ch-1", "b", false)
	s.runner.HoldLive("ch-1", "b", false)
	require.Equal(s.T(), map[string][]string{"ch-2": {"a"}}, s.runner.LiveHolds())
	idle.f()
	require.Equal(s.T(), "idle", waitFor(s.T(), log.ended))
}

// A prompt sent from outside the process's stdin goes to hooks.Prompt; the
// replays of Loop's own stdin lines, which carry no origin, don't.
func (s *RunnerSuite) TestLivePrompt() {
	s.setupLive(0)
	log := newLiveHookLog()
	proc := newFakeLiveProc()
	s.startLive(liveReq(nil), log.hooks(nil), "live-1", proc)
	proc.emit(s.T(),
		`{"type":"user","message":{"content":[{"type":"text","text":"from loop"}]},"isReplay":true}`,
		`{"type":"user","message":{"content":"not replayed"},"origin":{"kind":"human"}}`,
		`{"type":"user","message":{"content":"from a task"},"isReplay":true,"origin":{"kind":"task-notification"}}`,
		`{"type":"user","message":{"role":"user","content":"hello from outside"},"isReplay":true,"origin":{"kind":"human"}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"as blocks"}]},"isReplay":true,"origin":{"kind":"human"}}`,
	)
	require.Equal(s.T(), "hello from outside", waitFor(s.T(), log.prompts))
	require.Equal(s.T(), "as blocks", waitFor(s.T(), log.prompts))
	requireNone(s.T(), log.prompts)

	// Without the hook a prompt goes nowhere.
	s.runner.liveCallbacks(&liveSession{}).onPrompt("dropped")
}

// Control requests go to the process and come back with their answers,
// matched by id; system events Loop doesn't handle go to hooks.System.
func (s *RunnerSuite) TestSendLiveControl() {
	s.setupLive(0)
	log := newLiveHookLog()
	proc := newFakeLiveProc()
	s.startLive(liveReq(nil), log.hooks(nil), "live-1", proc)
	ctx := context.Background()

	type answer struct {
		body json.RawMessage
		err  error
	}
	send := func(ctx context.Context, request string) chan answer {
		out := make(chan answer, 1)
		go func() {
			body, err := s.runner.SendLiveControl(ctx, "ch-1", json.RawMessage(request))
			out <- answer{body, err}
		}()
		return out
	}

	got := send(ctx, `{"subtype":"set_mode","enabled":true}`)
	require.JSONEq(s.T(),
		`{"type":"control_request","request_id":"loop-control-1","request":{"subtype":"set_mode","enabled":true}}`,
		waitFor(s.T(), proc.stdin))
	proc.emit(s.T(),
		`{"type":"control_response","response":{"subtype":"success","request_id":"loop-interrupt"}}`,
		`{"type":"system","subtype":"init"}`,
		`{"type":"system","subtype":"mode_state","state":"ready"}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"loop-control-1","response":{"mode":"u"}}}`,
	)
	a := waitFor(s.T(), got)
	require.NoError(s.T(), a.err)
	require.JSONEq(s.T(), `{"mode":"u"}`, string(a.body))
	require.Equal(s.T(), `mode_state={"type":"system","subtype":"mode_state","state":"ready"}`, waitFor(s.T(), log.system))
	requireNone(s.T(), log.system)

	tests := []struct {
		name     string
		response string
		wantBody string
		wantErr  string
	}{
		{"an answer without a body", `{"subtype":"success","request_id":"%s"}`, `{}`, ""},
		{"an error", `{"subtype":"error","request_id":"%s","error":"nope"}`, "", "nope"},
		{"an error without a message", `{"subtype":"error","request_id":"%s"}`, "", "control request failed"},
		{"an answer that isn't one", `[]}`, "", "decoding the control response"},
	}
	for i, tc := range tests {
		s.Run(tc.name, func() {
			got := send(ctx, `{"subtype":"x"}`)
			waitFor(s.T(), proc.stdin)
			id := fmt.Sprintf("loop-control-%d", i+2)
			if strings.HasPrefix(tc.response, "[") {
				s.runner.liveSession("ch-1").controlAnswered(id, []byte(tc.response))
			} else {
				proc.emit(s.T(), `{"type":"control_response","response":`+fmt.Sprintf(tc.response, id)+`}`)
			}
			a := waitFor(s.T(), got)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), a.err, tc.wantErr)
				return
			}
			require.NoError(s.T(), a.err)
			require.JSONEq(s.T(), tc.wantBody, string(a.body))
		})
	}

	var ctlErr *LiveControlError
	_, err := s.runner.SendLiveControl(ctx, "ch-1", json.RawMessage(`{`))
	require.ErrorContains(s.T(), err, "encoding the control request")
	require.False(s.T(), errors.As(err, &ctlErr))

	cancelled, cancel := context.WithCancel(ctx)
	got = send(cancelled, `{"subtype":"x"}`)
	waitFor(s.T(), proc.stdin)
	cancel()
	require.ErrorIs(s.T(), waitFor(s.T(), got).err, context.Canceled)

	// The session ending fails the requests waiting on it.
	got = send(ctx, `{"subtype":"x"}`)
	waitFor(s.T(), proc.stdin)
	require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
	require.ErrorIs(s.T(), waitFor(s.T(), got).err, ErrNoLiveSession)
	_, err = s.runner.SendLiveControl(ctx, "ch-1", json.RawMessage(`{}`))
	require.ErrorIs(s.T(), err, ErrNoLiveSession)
}

// A session that ended but is still known to a caller takes no control
// requests, and one whose stdin is gone fails them.
func (s *RunnerSuite) TestSendLiveControlClosed() {
	s.setupLive(0)
	log := newLiveHookLog()
	proc := newFakeLiveProc()
	s.startLive(liveReq(nil), log.hooks(nil), "live-1", proc)
	_ = proc.inR.CloseWithError(errors.New("gone"))
	_, err := s.runner.SendLiveControl(context.Background(), "ch-1", json.RawMessage(`{}`))
	require.ErrorContains(s.T(), err, "writing the control request: gone")

	ls := s.runner.liveSession("ch-1")
	ls.mu.Lock()
	ls.closed = true
	ls.mu.Unlock()
	_, err = s.runner.SendLiveControl(context.Background(), "ch-1", json.RawMessage(`{}`))
	require.ErrorIs(s.T(), err, ErrNoLiveSession)
	ls.mu.Lock()
	ls.closed = false
	ls.mu.Unlock()
	require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
}

func (s *RunnerSuite) TestCloseLiveSessions() {
	s.setupLive(0)
	log := newLiveHookLog()
	for i, ch := range []string{"ch-1", "ch-2"} {
		id := fmt.Sprintf("live-%d", i+1)
		s.startLive(&agent.AgentRequest{ChannelID: ch, Live: true}, log.hooks(nil), id, newFakeLiveProc())
	}
	s.runner.CloseLiveSessions()
	require.Equal(s.T(), "shutdown", waitFor(s.T(), log.ended))
	require.Equal(s.T(), "shutdown", waitFor(s.T(), log.ended))
	s.client.AssertCalled(s.T(), "ContainerStop", mock.Anything, "live-1")
	s.client.AssertCalled(s.T(), "ContainerStop", mock.Anything, "live-2")
	require.Empty(s.T(), s.runner.LiveChannels())
}

func (s *RunnerSuite) TestNewDockerRunnerAfterFunc() {
	fired := make(chan struct{})
	r := NewDockerRunner(s.client, s.cfg, nil)
	r.afterFunc(time.Nanosecond, func() { close(fired) })
	waitFor(s.T(), fired)
	require.True(s.T(), r.afterFunc(time.Hour, func() {}).Stop())
}

func TestBuildLiveClaudeCmd(t *testing.T) {
	cfg := &config.Config{ClaudeBinPath: "claude", ClaudeBatchDisallowedTools: []string{"Monitor", "WebSearch"}}
	cmd := buildLiveClaudeCmd(cfg, "/mcp.json", &agent.AgentRequest{ChannelID: "ch-1", Prompt: "the prompt"})
	require.Equal(t, []string{"--input-format", "stream-json", "--replay-user-messages"}, cmd[len(cmd)-3:])
	require.NotContains(t, cmd, "the prompt")
	require.False(t, slices.ContainsFunc(cmd, func(a string) bool { return strings.Contains(a, "Monitor") }))
	require.True(t, slices.ContainsFunc(cmd, func(a string) bool { return strings.Contains(a, "WebSearch") }))
	require.Equal(t, []string{"Monitor", "WebSearch"}, cfg.ClaudeBatchDisallowedTools)
}

func TestClaudeResponseFlush(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		numTurns int
		want     bool
	}{
		{"empty result of its own", "task-notification", 0, true},
		{"result of its own that ran turns", "task-notification", 2, false},
		{"answer to a turn", "", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &claudeResponse{NumTurns: tc.numTurns}
			r.Origin.Kind = tc.origin
			require.Equal(t, tc.want, r.flush())
		})
	}
}

func TestStreamSystemTurnStart(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantStarts int
	}{
		{"init starts a turn", `{"type":"system","subtype":"init"}`, 1},
		{"other events are activity", `{"type":"system","subtype":"status","status":"compacting"}`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var st streamState
			starts := 0
			require.Nil(t, st.dispatch([]byte(tc.line), streamCallbacks{onTurnStart: func() { starts++ }}))
			require.Equal(t, tc.wantStarts, starts)
			var acts []string
			require.Nil(t, st.dispatch([]byte(tc.line), streamCallbacks{onActivity: func(a, _ string) { acts = append(acts, a) }}))
			if tc.wantStarts == 0 {
				require.Equal(t, []string{"compacting"}, acts)
			} else {
				require.Empty(t, acts)
			}
		})
	}
}

// blockingImageBuilder is a MissingImageBuilder whose build runs until
// release is closed.
type blockingImageBuilder struct {
	building chan struct{}
	release  chan struct{}
}

func (b *blockingImageBuilder) BuildMissing(_ context.Context, _ string, _ bool, _ []string, onBuild func()) error {
	onBuild()
	close(b.building)
	<-b.release
	return nil
}

func (s *RunnerSuite) TestStartLiveWaitsForImageBuildAlone() {
	s.setupLive(0)
	b := &blockingImageBuilder{building: make(chan struct{}), release: make(chan struct{})}
	s.runner.SetMissingImageBuilder(b)
	proc := newFakeLiveProc()
	created := s.expectLiveSpawn("live-1", proc)

	activities := make(chan [2]string, 4)
	req := liveReq(func(r *agent.AgentRequest) {
		r.OnActivity = func(activity, detail string) { activities <- [2]string{activity, detail} }
	})
	started := make(chan error, 1)
	go func() { started <- s.runner.StartLive(context.Background(), req, agent.LiveHooks{}) }()
	waitFor(s.T(), b.building)

	// The build says what the start waits for, and holds only its channel.
	require.Equal(s.T(), [2]string{"image_build", "Building the loop-agent:latest image"}, waitFor(s.T(), activities))
	require.ErrorIs(s.T(), s.runner.StartLive(context.Background(), liveReq(nil), agent.LiveHooks{}), ErrLiveSessionRunning)
	_, running := s.runner.LiveInfo("ch-2")
	require.False(s.T(), running)
	require.Empty(s.T(), s.runner.LiveChannels())

	close(b.release)
	require.NoError(s.T(), waitFor(s.T(), started))
	waitFor(s.T(), created)
	require.Equal(s.T(), [2]string{"image_ready", ""}, waitFor(s.T(), activities))
	require.Equal(s.T(), []string{"ch-1"}, s.runner.LiveChannels())
}

// Stopping a turn the process started by itself gives the process
// liveInterruptGrace to end it, then ends the session. The turn can be
// stopped as soon as BeginTurn is told of it, before BeginTurn returns.
func (s *RunnerSuite) TestLiveInterruptOwnTurn() {
	tests := []struct {
		name      string
		answer    bool
		inBegin   bool
		wantEnded string
	}{
		{name: "the process ends the turn", answer: true},
		{name: "stopped while BeginTurn runs", answer: true, inBegin: true},
		{name: "the process is stuck", wantEnded: "interrupt timed out"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			clock := s.setupLive(0)
			log := newLiveHookLog()
			turns := make(chan string, 4)
			proc := newFakeLiveProc()
			hooks := log.hooks(turns)
			begun := make(chan struct{})
			release := make(chan struct{})
			if begin := hooks.BeginTurn; tc.inBegin {
				hooks.BeginTurn = func() (*agent.AgentRequest, func(*agent.AgentResponse, error)) {
					close(begun)
					<-release
					return begin()
				}
			}
			s.startLive(liveReq(nil), hooks, "live-1", proc)

			proc.emit(s.T(), `{"type":"system","subtype":"init"}`)
			if tc.inBegin {
				waitFor(s.T(), begun)
			} else {
				waitFor(s.T(), log.begun)
			}
			require.NoError(s.T(), s.runner.InterruptLive("ch-1"))
			require.Equal(s.T(), string(interruptLine), waitFor(s.T(), proc.stdin))
			grace := waitFor(s.T(), clock.armed)
			require.Equal(s.T(), liveInterruptGrace, grace.d)
			close(release)

			if tc.answer {
				proc.emit(s.T(), `{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":1}`)
				require.Error(s.T(), waitFor(s.T(), log.done).err)
			}
			grace.f()
			if tc.wantEnded == "" {
				requireNone(s.T(), log.ended)
				s.client.AssertNotCalled(s.T(), "ContainerStop", mock.Anything, "live-1")
				require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
				return
			}
			require.Equal(s.T(), tc.wantEnded, waitFor(s.T(), log.ended))
			require.ErrorIs(s.T(), waitFor(s.T(), log.done).err, errLiveEnded)
			s.client.AssertCalled(s.T(), "ContainerStop", mock.Anything, "live-1")
		})
	}
}

// A turn given by a user line waits out its own interrupt (awaitLiveTurn),
// and a process whose stdin is gone can't be interrupted.
func (s *RunnerSuite) TestLiveInterruptNoGrace() {
	clock := s.setupLive(0)
	log := newLiveHookLog()
	proc := newFakeLiveProc()
	start := liveReq(nil)
	s.startLive(start, log.hooks(nil), "live-1", proc)

	run := s.goRun(context.Background(), withPrompt(start, "hi"))
	waitFor(s.T(), proc.stdin)
	require.NoError(s.T(), s.runner.InterruptLive("ch-1"))
	require.Equal(s.T(), string(interruptLine), waitFor(s.T(), proc.stdin))
	requireNone(s.T(), clock.armed)
	proc.emit(s.T(), `{"type":"result","result":"ok","num_turns":1}`)
	require.NoError(s.T(), waitFor(s.T(), run).err)

	_ = proc.inR.CloseWithError(errors.New("gone"))
	require.Error(s.T(), s.runner.InterruptLive("ch-1"))
	require.NoError(s.T(), s.runner.StopLive("ch-1", "done"))
}
