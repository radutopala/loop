package ciwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/forge"
)

type mockForge struct{ mock.Mock }

func (m *mockForge) CI(ctx context.Context, ref forge.Ref) (*forge.CIState, error) {
	args := m.Called(ctx, ref)
	st, _ := args.Get(0).(*forge.CIState)
	return st, args.Error(1)
}

func (m *mockForge) FailedChecks(ctx context.Context, ref forge.Ref) ([]forge.FailedCheck, error) {
	args := m.Called(ctx, ref)
	fcs, _ := args.Get(0).([]forge.FailedCheck)
	return fcs, args.Error(1)
}

func (m *mockForge) Log(ctx context.Context, ref forge.Ref, c forge.FailedCheck) (string, error) {
	args := m.Called(ctx, ref, c)
	return args.String(0), args.Error(1)
}

func (m *mockForge) Rerun(ctx context.Context, ref forge.Ref, c forge.FailedCheck) error {
	return m.Called(ctx, ref, c).Error(0)
}

func (m *mockForge) MergeSettings(ctx context.Context, ref forge.Ref) (forge.MergeSettings, error) {
	args := m.Called(ctx, ref)
	return args.Get(0).(forge.MergeSettings), args.Error(1)
}

func (m *mockForge) EnableAutoMerge(ctx context.Context, ref forge.Ref, method string) error {
	return m.Called(ctx, ref, method).Error(0)
}

func (m *mockForge) DisableAutoMerge(ctx context.Context, ref forge.Ref) error {
	return m.Called(ctx, ref).Error(0)
}

func (m *mockForge) Merge(ctx context.Context, ref forge.Ref, method string) error {
	return m.Called(ctx, ref, method).Error(0)
}

// memStore keeps CI states in memory.
type memStore struct {
	mu                       sync.Mutex
	rows                     map[string]db.CIState
	getErr, saveErr, listErr error
}

func (m *memStore) GetCIState(_ context.Context, channelID string) (*db.CIState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	st, ok := m.rows[channelID]
	if !ok {
		return nil, nil
	}
	return &st, nil
}

func (m *memStore) SaveCIState(_ context.Context, st *db.CIState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.rows[st.ChannelID] = *st
	return nil
}

func (m *memStore) ListCIStates(context.Context) ([]*db.CIState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []*db.CIState
	for _, st := range m.rows {
		out = append(out, &st)
	}
	return out, nil
}

func (m *memStore) get(id string) db.CIState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[id]
}

func (m *memStore) put(st db.CIState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[st.ChannelID] = st
}

const ch = "th1"

type WatcherSuite struct {
	suite.Suite
	ctx        context.Context
	store      *memStore
	forge      *mockForge
	w          *Watcher
	now        time.Time
	kind       string
	target     *Target
	resolveErr error
	head       string

	mu         sync.Mutex
	enqueued   []string
	notices    []string
	broadcasts []events.CIEventData
}

func TestWatcherSuite(t *testing.T) {
	suite.Run(t, new(WatcherSuite))
}

func (s *WatcherSuite) SetupTest() {
	s.setup(forge.KindPR)
}

// setup builds a Watcher over a fake forge of kind.
func (s *WatcherSuite) setup(kind string) {
	s.ctx = context.Background()
	s.store = &memStore{rows: map[string]db.CIState{}}
	s.forge = &mockForge{}
	s.now = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s.kind = kind
	s.target = &Target{Kind: kind, Ref: forge.Ref{Dir: "/w", Number: 12}, URL: "https://example.com/pr/12", CI: config.DefaultCIConfig()}
	s.resolveErr = nil
	s.head = ""
	s.enqueued, s.notices, s.broadcasts = nil, nil, nil
	s.w = New(Deps{
		Resolve: func(context.Context, string) (*Target, error) { return s.target, s.resolveErr },
		Head:    func(context.Context, string) string { return s.head },
		Forges:  map[string]forge.Forge{kind: s.forge},
		Store:   s.store,
		Enqueue: func(_ context.Context, _, content string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.enqueued = append(s.enqueued, content)
		},
		Notice: func(_ context.Context, _, content string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.notices = append(s.notices, content)
		},
		Broadcast: func(d events.CIEventData) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.broadcasts = append(s.broadcasts, d)
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s.w.now = func() time.Time { return s.now }
}

func (s *WatcherSuite) lastBroadcast() events.CIEventData {
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(s.T(), s.broadcasts)
	return s.broadcasts[len(s.broadcasts)-1]
}

func (s *WatcherSuite) settings() {
	s.forge.On("MergeSettings", mock.Anything, mock.Anything).Return(forge.MergeSettings{AutoMerge: true, Methods: []string{"squash", "merge"}}, nil).Maybe()
}

func (s *WatcherSuite) ci(head string, checks forge.CheckSummary) *mock.Call {
	return s.forge.On("CI", mock.Anything, s.target.Ref).Return(&forge.CIState{HeadSHA: head, Checks: checks, MergeState: forge.MergeBlocked}, nil)
}

var (
	failing = forge.CheckSummary{Total: 2, Passed: 1, Failed: 1}
	pending = forge.CheckSummary{Total: 2, Passed: 1, Pending: 1}
	green   = forge.CheckSummary{Total: 2, Passed: 2}
	lint    = forge.FailedCheck{ID: "77", RunID: "70", Name: "lint", Group: "ci", URL: "https://example.com/j/77"}
	infra   = forge.FailedCheck{ID: "78", RunID: "78", Name: "e2e", Group: "test", Infra: true}
)

func (s *WatcherSuite) poll() bool {
	keep, _ := s.w.poll(s.ctx, ch, s.w.entry(ch))
	return keep
}

// TestFixLoop sends a failure on the agent's commit to the agent, follows
// the fix run and clears on green, on both forges.
func (s *WatcherSuite) TestFixLoop() {
	for _, kind := range []string{forge.KindPR, forge.KindMR} {
		s.Run(kind, func() {
			s.setup(kind)
			s.settings()
			s.store.put(db.CIState{ChannelID: ch, Forge: kind, Number: 12, AgentSHAs: []string{"aaaaaaa1"}})
			s.ci("aaaaaaa1", failing).Once()
			s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
			s.forge.On("Log", mock.Anything, s.target.Ref, lint).Return("FAIL lint", nil).Once()

			require.True(s.T(), s.poll())
			st := s.store.get(ch)
			require.Equal(s.T(), events.CIAttentionFixing, st.Attention)
			require.Equal(s.T(), 1, st.Attempts)
			require.Len(s.T(), s.enqueued, 1)
			require.Contains(s.T(), s.enqueued[0], "CI failed on aaaaaaa for "+label(s.target))
			require.Contains(s.T(), s.enqueued[0], "FAIL lint")
			if kind == forge.KindMR {
				require.Contains(s.T(), s.enqueued[0], "`glab ci retry 77`")
			} else {
				require.Contains(s.T(), s.enqueued[0], "`gh run rerun 70 --failed`")
			}
			require.Equal(s.T(), events.CIAttentionFixing, s.lastBroadcast().Attention)

			// While the fix is in flight, a poll doesn't act.
			s.ci("aaaaaaa1", failing).Once()
			require.True(s.T(), s.poll())
			require.Len(s.T(), s.enqueued, 1)

			// The fix run commits a new head.
			s.head = "aaaaaaa1"
			s.w.agentStatus(s.ctx, ch, events.AgentStatusEventData{Status: "running"})
			s.head = "bbbbbbb2"
			s.w.agentStatus(s.ctx, ch, events.AgentStatusEventData{Status: "completed"})
			require.Contains(s.T(), s.store.get(ch).AgentSHAs, "bbbbbbb2")

			s.ci("bbbbbbb2", pending).Once()
			require.True(s.T(), s.poll())
			require.Empty(s.T(), s.store.get(ch).Attention)

			s.ci("bbbbbbb2", green).Once()
			require.False(s.T(), s.poll())
			st = s.store.get(ch)
			require.Zero(s.T(), st.Attempts)
			require.False(s.T(), st.Watching)
			s.forge.AssertExpectations(s.T())
		})
	}
}

// TestGuards flags a failure for the owner whenever a guard stops the
// auto-fix.
func (s *WatcherSuite) TestGuards() {
	off := false
	tests := []struct {
		name    string
		state   db.CIState
		cfg     func(*config.CIConfig)
		checks  []forge.FailedCheck
		log     string
		notice  string
		noCheck bool
	}{
		{name: "someone else's commit", state: db.CIState{AgentSHAs: []string{"other"}}},
		{name: "auto-fix off in config", state: db.CIState{AgentSHAs: []string{"h1"}}, cfg: func(c *config.CIConfig) { c.AutoFix = false }},
		{name: "auto-fix off for the thread", state: db.CIState{AgentSHAs: []string{"h1"}, AutoFix: &off}},
		{name: "attempts used up", state: db.CIState{AgentSHAs: []string{"h1"}, Attempts: 3, LastFailedSHA: "h0"}, notice: "after 3 attempts"},
		{name: "same failure again", state: db.CIState{AgentSHAs: []string{"h1"}, Attempts: 1, LastFailedSHA: "h0", LastLogHash: forge.LogHash("ci / lint\nFAIL\n")}, log: "FAIL", notice: "same failure came back"},
		{name: "the fix pushed nothing", state: db.CIState{AgentSHAs: []string{"h1"}, Attempts: 1, LastFailedSHA: "h1"}, notice: "pushed no new commit"},
		{name: "infra again after a re-run", state: db.CIState{AgentSHAs: []string{"h1"}, InfraRetriedSHA: "h1"}, checks: []forge.FailedCheck{infra}, notice: "infrastructure reasons after a re-run"},
	}
	for _, kind := range []string{forge.KindPR, forge.KindMR} {
		for _, tc := range tests {
			s.Run(kind+"/"+tc.name, func() {
				s.setup(kind)
				s.settings()
				if tc.cfg != nil {
					tc.cfg(&s.target.CI)
				}
				st := tc.state
				st.ChannelID, st.Forge, st.Number = ch, kind, 12
				s.store.put(st)
				s.ci("h1", failing).Once()
				checks := tc.checks
				if checks == nil {
					checks = []forge.FailedCheck{lint}
				}
				s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return(checks, nil).Once()
				s.forge.On("Log", mock.Anything, mock.Anything, mock.Anything).Return(tc.log, nil).Maybe()

				require.False(s.T(), s.poll())
				got := s.store.get(ch)
				require.Equal(s.T(), events.CIAttentionFailed, got.Attention)
				require.Equal(s.T(), "h1", got.LastFailedSHA)
				require.Empty(s.T(), s.enqueued)
				b := s.lastBroadcast()
				require.Equal(s.T(), label(s.target)+": "+checks[0].Name+" failed", b.Alert)
				require.Equal(s.T(), checks, b.Failed)
				if tc.notice == "" {
					require.Empty(s.T(), s.notices)
				} else {
					require.Len(s.T(), s.notices, 1)
					require.Contains(s.T(), s.notices[0], tc.notice)
				}

				// Flagged once: the next poll leaves it be.
				s.ci("h1", failing).Once()
				require.False(s.T(), s.poll())
				s.forge.AssertExpectations(s.T())

				// A new head that's still running clears the flag.
				s.ci("h2", pending).Once()
				require.True(s.T(), s.poll())
				require.Empty(s.T(), s.store.get(ch).Attention)
			})
		}
	}
}

// TestInfraRerun re-runs infra failures once.
func (s *WatcherSuite) TestInfraRerun() {
	s.setup(forge.KindMR)
	s.settings()
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindMR, Number: 12, AgentSHAs: []string{"h1"}})
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{infra}, nil).Once()
	s.forge.On("Rerun", mock.Anything, s.target.Ref, infra).Return(nil).Once()
	require.True(s.T(), s.poll())
	st := s.store.get(ch)
	require.Equal(s.T(), "h1", st.InfraRetriedSHA)
	require.Empty(s.T(), st.Attention)
	require.Contains(s.T(), s.notices[0], "re-running it once")

	// A failed re-run flags it.
	s.setup(forge.KindMR)
	s.settings()
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindMR, Number: 12, AgentSHAs: []string{"h1"}})
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{infra}, nil).Once()
	s.forge.On("Rerun", mock.Anything, s.target.Ref, infra).Return(errors.New("denied")).Once()
	require.False(s.T(), s.poll())
	require.Equal(s.T(), events.CIAttentionFailed, s.store.get(ch).Attention)
}

func (s *WatcherSuite) TestPollEnds() {
	s.settings()
	// Merged: nothing watched, nothing armed.
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Watching: true, Attention: "failed", MergeArmed: "squash", MergeVia: viaForge})
	s.forge.On("CI", mock.Anything, s.target.Ref).Return(&forge.CIState{HeadSHA: "h", Merged: true}, nil).Once()
	require.False(s.T(), s.poll())
	st := s.store.get(ch)
	require.Equal(s.T(), db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12}, st)
	require.True(s.T(), s.lastBroadcast().Merged)
	require.Nil(s.T(), s.lastBroadcast().MergeMethods, "no merge settings once merged")

	// No PR: cleared once, quietly after.
	s.store.put(db.CIState{ChannelID: ch, Watching: true})
	s.target = nil
	n := len(s.broadcasts)
	require.False(s.T(), s.poll())
	require.False(s.T(), s.store.get(ch).Watching)
	require.Equal(s.T(), events.CIEventData{ChannelID: ch}, s.lastBroadcast())
	require.False(s.T(), s.poll())
	require.Len(s.T(), s.broadcasts, n+1)
}

func (s *WatcherSuite) TestPollErrors() {
	s.settings()
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Watching: true})

	s.resolveErr = errors.New("gh down")
	require.True(s.T(), s.poll(), "keeps watching through a lookup error")
	s.resolveErr = nil

	s.target.Kind = "other"
	require.True(s.T(), s.poll(), "no forge for the kind")
	s.target.Kind = forge.KindPR

	s.forge.On("CI", mock.Anything, s.target.Ref).Return(nil, errors.New("boom")).Once()
	require.True(s.T(), s.poll())

	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, AgentSHAs: []string{"h1"}})
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return(nil, errors.New("boom")).Once()
	require.True(s.T(), s.poll())

	s.store.getErr = errors.New("db")
	require.False(s.T(), s.poll())
	s.store.getErr = nil

	s.store.saveErr = errors.New("db")
	s.ci("h1", green).Once()
	require.False(s.T(), s.poll())
	s.forge.AssertExpectations(s.T())
}

// TestNewPRStartsOver resets the counters for another PR, keeping the
// agent's commits and the thread's override.
func (s *WatcherSuite) TestNewPRStartsOver() {
	s.settings()
	on := true
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 3, AgentSHAs: []string{"h1"}, Attempts: 3, Attention: "failed", AutoFix: &on, MergeArmed: "merge"})
	s.ci("h1", pending).Once()
	s.poll()
	require.Equal(s.T(), db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, AgentSHAs: []string{"h1"}, AutoFix: &on, Watching: true}, s.store.get(ch))
}

func (s *WatcherSuite) TestLoopMerge() {
	s.forge.On("MergeSettings", mock.Anything, mock.Anything).Return(forge.MergeSettings{}, errors.New("no settings")).Maybe()
	armed := db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, AgentSHAs: []string{"h1", "h2"}, MergeArmed: "merge", MergeVia: viaLoop, ArmedSHA: "h1"}

	// The agent's new head moves the armed head along.
	s.store.put(armed)
	s.ci("h2", pending).Once()
	s.poll()
	require.Equal(s.T(), "h2", s.store.get(ch).ArmedSHA)

	// Green and clean: merged once.
	s.forge.On("CI", mock.Anything, s.target.Ref).Return(&forge.CIState{HeadSHA: "h2", Checks: green, MergeState: forge.MergeClean}, nil).Once()
	s.forge.On("Merge", mock.Anything, s.target.Ref, "merge").Return(nil).Once()
	require.True(s.T(), s.poll(), "watched until it reads merged")
	require.Empty(s.T(), s.store.get(ch).MergeArmed)

	// A failed merge disarms with a notice.
	s.store.put(armed)
	s.forge.On("CI", mock.Anything, s.target.Ref).Return(&forge.CIState{HeadSHA: "h1", Checks: green, MergeState: forge.MergeClean}, nil).Once()
	s.forge.On("Merge", mock.Anything, s.target.Ref, "merge").Return(errors.New("blocked")).Once()
	require.False(s.T(), s.poll())
	require.Contains(s.T(), s.notices[0], "Merge when ready failed: blocked")

	// Green but not clean: still armed.
	s.store.put(armed)
	s.ci("h1", green).Once()
	require.True(s.T(), s.poll())
	require.Equal(s.T(), "merge", s.store.get(ch).MergeArmed)

	// Someone else's head disarms, failing too.
	s.store.put(armed)
	s.ci("h9", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
	require.False(s.T(), s.poll())
	require.Empty(s.T(), s.store.get(ch).MergeArmed)
	require.Contains(s.T(), s.notices[1], "Merge when ready was cancelled: h9")
	s.forge.AssertExpectations(s.T())
}

func (s *WatcherSuite) TestAgentStatus() {
	// A thread's run told to its parent is ignored.
	s.w.agentStatus(s.ctx, "parent", events.AgentStatusEventData{Status: "running", ThreadID: ch})
	s.w.mu.Lock()
	_, ok := s.w.entries["parent"]
	s.w.mu.Unlock()
	require.False(s.T(), ok)

	// A run that didn't move the head records nothing.
	s.head = "h1"
	s.w.agentStatus(s.ctx, ch, events.AgentStatusEventData{Status: "running"})
	s.w.agentStatus(s.ctx, ch, events.AgentStatusEventData{Status: "pending"})
	s.w.agentStatus(s.ctx, ch, events.AgentStatusEventData{Status: "error", ThreadID: ch})
	require.Empty(s.T(), s.store.get(ch).AgentSHAs)
	e := s.w.entry(ch)
	require.Equal(s.T(), s.now, e.due, "kicked")

	// The async entry point.
	s.head = ""
	s.w.AgentStatus(ch, events.AgentStatusEventData{Status: "running"})
	require.Eventually(s.T(), func() bool {
		s.w.mu.Lock()
		defer s.w.mu.Unlock()
		return e.running
	}, time.Second, time.Millisecond)
}

func (s *WatcherSuite) TestChannelUpdated() {
	// No commit: a rename or the like.
	s.w.channelUpdated(s.ctx, events.ChannelUpdatedData{ChannelID: ch})
	require.True(s.T(), s.w.entry(ch).due.IsZero())

	// Someone else's commit: watched, not recorded.
	s.w.channelUpdated(s.ctx, events.ChannelUpdatedData{ChannelID: ch, Commit: "h1"})
	require.Empty(s.T(), s.store.get(ch).AgentSHAs)
	require.Equal(s.T(), s.now, s.w.entry(ch).due)

	// A commit while the agent runs is its own; told twice, recorded once.
	s.w.agentStatus(s.ctx, ch, events.AgentStatusEventData{Status: "running"})
	s.w.channelUpdated(s.ctx, events.ChannelUpdatedData{ChannelID: ch, Commit: "h2"})
	s.w.channelUpdated(s.ctx, events.ChannelUpdatedData{ChannelID: ch, Commit: "h2"})
	require.Equal(s.T(), []string{"h2"}, s.store.get(ch).AgentSHAs)

	// Just after the run, within the grace period, too.
	s.w.agentStatus(s.ctx, ch, events.AgentStatusEventData{Status: "completed"})
	s.now = s.now.Add(10 * time.Second)
	s.w.ChannelUpdated(events.ChannelUpdatedData{ChannelID: ch, Commit: "h3"})
	e := s.w.entry(ch)
	require.Eventually(s.T(), func() bool {
		s.w.mu.Lock()
		defer s.w.mu.Unlock()
		return e.until.Equal(s.now.Add(defaultLinger))
	}, time.Second, time.Millisecond)
	require.Len(s.T(), s.store.get(ch).AgentSHAs, 2)
}

func (s *WatcherSuite) TestRecordSHA() {
	var shas []string
	for i := range maxAgentSHAs {
		shas = append(shas, fmt.Sprint(i))
	}
	s.store.put(db.CIState{ChannelID: ch, AgentSHAs: shas})
	s.w.recordSHA(s.ctx, ch, "new")
	got := s.store.get(ch).AgentSHAs
	require.Len(s.T(), got, maxAgentSHAs)
	require.Equal(s.T(), "1", got[0])
	require.Equal(s.T(), "new", got[maxAgentSHAs-1])
	s.w.recordSHA(s.ctx, ch, "new")
	require.Equal(s.T(), got, s.store.get(ch).AgentSHAs, "recorded once")

	s.store.getErr = errors.New("db")
	s.w.recordSHA(s.ctx, ch, "newer")
	s.store.getErr = nil
	require.NotContains(s.T(), s.store.get(ch).AgentSHAs, "newer")
}

func (s *WatcherSuite) TestStatus() {
	s.settings()
	s.ci("h1", pending).Once()
	got := s.w.Status(s.ctx, ch)
	require.Equal(s.T(), "h1", got.HeadSHA)
	require.Equal(s.T(), []string{"squash", "merge"}, got.MergeMethods)
	require.Equal(s.T(), "squash", got.MergeMethod)
	require.True(s.T(), got.AutoFix)
	require.Equal(s.T(), 3, got.MaxAttempts)
	got.HeadSHA = "changed"
	require.Equal(s.T(), "h1", s.w.Status(s.ctx, ch).HeadSHA, "served from memory, a copy")

	s.target = nil
	require.Nil(s.T(), s.w.Status(s.ctx, "nopr"))
}

func (s *WatcherSuite) TestAttention() {
	s.store.put(db.CIState{ChannelID: "a", Attention: "failed"})
	s.store.put(db.CIState{ChannelID: "b"})
	got, err := s.w.Attention(s.ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[string]string{"a": "failed"}, got)

	s.store.listErr = errors.New("db")
	_, err = s.w.Attention(s.ctx)
	require.Error(s.T(), err)
}

func (s *WatcherSuite) TestSetAutoFix() {
	off := false
	require.NoError(s.T(), s.w.SetAutoFix(s.ctx, ch, &off))
	require.Equal(s.T(), &off, s.store.get(ch).AutoFix)
	require.Equal(s.T(), s.now, s.w.entry(ch).due)

	s.store.saveErr = errors.New("db")
	require.Error(s.T(), s.w.SetAutoFix(s.ctx, ch, nil))
	s.store.saveErr, s.store.getErr = nil, errors.New("db")
	require.Error(s.T(), s.w.SetAutoFix(s.ctx, ch, nil))
}

func (s *WatcherSuite) TestFixNow() {
	s.settings()
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Attention: "failed", LastFailedSHA: "h1"})
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
	s.forge.On("Log", mock.Anything, s.target.Ref, lint).Return("", errors.New("expired")).Once()
	require.NoError(s.T(), s.w.FixNow(s.ctx, ch))
	require.Contains(s.T(), s.enqueued[0], "(no log available)")
	require.Equal(s.T(), events.CIAttentionFixing, s.store.get(ch).Attention)
	require.Equal(s.T(), events.CIAttentionFixing, s.lastBroadcast().Attention)

	s.ci("h1", green).Once()
	require.ErrorIs(s.T(), s.w.FixNow(s.ctx, ch), ErrNoFailure)

	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return(nil, errors.New("boom")).Once()
	require.Error(s.T(), s.w.FixNow(s.ctx, ch))

	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{{Name: "x"}}, nil).Once()
	s.forge.On("Log", mock.Anything, s.target.Ref, forge.FailedCheck{Name: "x"}).Return("", nil).Once()
	s.store.saveErr = errors.New("db")
	require.Error(s.T(), s.w.FixNow(s.ctx, ch))
	s.store.saveErr = nil

	s.forge.On("CI", mock.Anything, s.target.Ref).Return(nil, errors.New("boom")).Once()
	require.Error(s.T(), s.w.FixNow(s.ctx, ch))

	s.target = nil
	require.ErrorIs(s.T(), s.w.FixNow(s.ctx, ch), ErrNoPR)
	s.store.getErr = errors.New("db")
	require.Error(s.T(), s.w.FixNow(s.ctx, ch))
	s.forge.AssertExpectations(s.T())
}

func (s *WatcherSuite) TestArm() {
	tests := []struct {
		name     string
		settings forge.MergeSettings
		method   string
		cfg      string
		enable   error
		ci       *forge.CIState
		wantVia  string
		want     string
		wantErr  string
	}{
		{name: "forge auto-merge", settings: forge.MergeSettings{AutoMerge: true, Methods: []string{"squash", "merge"}}, ci: &forge.CIState{HeadSHA: "h1"}, wantVia: viaForge, want: "squash"},
		{name: "configured method, Loop merges", settings: forge.MergeSettings{Methods: []string{"squash", "merge"}}, cfg: "merge", ci: &forge.CIState{HeadSHA: "h1"}, wantVia: viaLoop, want: "merge"},
		{name: "method not allowed", settings: forge.MergeSettings{Methods: []string{"squash"}}, method: "rebase", wantErr: "doesn't allow rebase"},
		{name: "closed", settings: forge.MergeSettings{Methods: []string{"merge"}}, ci: &forge.CIState{Closed: true}, wantErr: "no longer open"},
		{name: "enable fails", settings: forge.MergeSettings{AutoMerge: true, Methods: []string{"merge"}}, ci: &forge.CIState{HeadSHA: "h1"}, enable: errors.New("not allowed"), wantErr: "not allowed"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.setup(forge.KindPR)
			s.target.CI.MergeMethod = tc.cfg
			s.forge.On("MergeSettings", mock.Anything, s.target.Ref).Return(tc.settings, nil)
			if tc.ci != nil {
				s.forge.On("CI", mock.Anything, s.target.Ref).Return(tc.ci, nil)
			}
			s.forge.On("EnableAutoMerge", mock.Anything, s.target.Ref, mock.Anything).Return(tc.enable)
			err := s.w.Arm(s.ctx, ch, tc.method)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			st := s.store.get(ch)
			require.Equal(s.T(), tc.want, st.MergeArmed)
			require.Equal(s.T(), tc.wantVia, st.MergeVia)
			require.Equal(s.T(), "h1", st.ArmedSHA)
			require.True(s.T(), st.Watching)
		})
	}
}

func (s *WatcherSuite) TestArmErrors() {
	s.store.getErr = errors.New("db")
	require.Error(s.T(), s.w.Arm(s.ctx, ch, ""))
	s.store.getErr = nil

	s.resolveErr = errors.New("gh")
	require.Error(s.T(), s.w.Arm(s.ctx, ch, ""))
	s.resolveErr = nil

	s.forge.On("MergeSettings", mock.Anything, s.target.Ref).Return(forge.MergeSettings{}, errors.New("settings")).Once()
	require.ErrorContains(s.T(), s.w.Arm(s.ctx, ch, ""), "settings")

	s.forge.On("MergeSettings", mock.Anything, s.target.Ref).Return(forge.MergeSettings{Methods: []string{"merge"}}, nil)
	s.forge.On("CI", mock.Anything, s.target.Ref).Return(nil, errors.New("ci")).Once()
	require.ErrorContains(s.T(), s.w.Arm(s.ctx, ch, ""), "ci")

	s.forge.On("CI", mock.Anything, s.target.Ref).Return(&forge.CIState{HeadSHA: "h"}, nil).Once()
	s.store.saveErr = errors.New("db")
	require.ErrorContains(s.T(), s.w.Arm(s.ctx, ch, ""), "db")
}

func (s *WatcherSuite) TestCancelMerge() {
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, MergeArmed: "squash", MergeVia: viaForge})
	s.forge.On("DisableAutoMerge", mock.Anything, s.target.Ref).Return(errors.New("gh")).Once()
	require.ErrorContains(s.T(), s.w.CancelMerge(s.ctx, ch), "gh")
	s.forge.On("DisableAutoMerge", mock.Anything, s.target.Ref).Return(nil).Once()
	require.NoError(s.T(), s.w.CancelMerge(s.ctx, ch))
	require.Empty(s.T(), s.store.get(ch).MergeArmed)

	// Loop's own merge needs no forge call.
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, MergeArmed: "squash", MergeVia: viaLoop})
	require.NoError(s.T(), s.w.CancelMerge(s.ctx, ch))

	s.store.saveErr = errors.New("db")
	require.Error(s.T(), s.w.CancelMerge(s.ctx, ch))
	s.target = nil
	require.ErrorIs(s.T(), s.w.CancelMerge(s.ctx, ch), ErrNoPR)
	s.store.getErr = errors.New("db")
	require.Error(s.T(), s.w.CancelMerge(s.ctx, ch))
	s.forge.AssertExpectations(s.T())
}

func (s *WatcherSuite) TestMergeNow() {
	s.forge.On("MergeSettings", mock.Anything, s.target.Ref).Return(forge.MergeSettings{Methods: []string{"merge", "rebase"}}, nil)
	s.forge.On("Merge", mock.Anything, s.target.Ref, "rebase").Return(nil).Once()
	require.NoError(s.T(), s.w.MergeNow(s.ctx, ch, "rebase"))
	s.forge.On("Merge", mock.Anything, s.target.Ref, "merge").Return(errors.New("blocked")).Once()
	require.ErrorContains(s.T(), s.w.MergeNow(s.ctx, ch, ""), "blocked")
	require.ErrorContains(s.T(), s.w.MergeNow(s.ctx, ch, "squash"), "doesn't allow squash")
	s.target = nil
	require.ErrorIs(s.T(), s.w.MergeNow(s.ctx, ch, ""), ErrNoPR)
	s.store.getErr = errors.New("db")
	require.Error(s.T(), s.w.MergeNow(s.ctx, ch, ""))
	s.forge.AssertExpectations(s.T())
}

// TestPublishOnChange broadcasts a poll only when it tells something new.
func (s *WatcherSuite) TestPublishOnChange() {
	s.settings()
	s.ci("h1", pending).Twice()
	s.poll()
	s.poll()
	require.Len(s.T(), s.broadcasts, 1)
}

// TestRun restores the watched threads, polls them and stops with ctx.
func (s *WatcherSuite) TestRun() {
	s.settings()
	s.store.put(db.CIState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Watching: true, Attention: events.CIAttentionFixing, LastFailedSHA: "h1"})
	s.store.put(db.CIState{ChannelID: "idle"})
	s.ci("h1", failing)
	s.w.tick = time.Millisecond
	s.w.now = time.Now
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	go func() {
		s.w.Run(ctx)
		close(done)
	}()
	require.Eventually(s.T(), func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.broadcasts) > 0
	}, time.Second, time.Millisecond)
	cancel()
	<-done
	require.Equal(s.T(), events.CIAttentionFixing, s.store.get(ch).Attention, "the restored fix is still in flight")
	require.Empty(s.T(), s.enqueued)
	s.w.mu.Lock()
	_, ok := s.w.entries["idle"]
	s.w.mu.Unlock()
	require.False(s.T(), ok, "an idle thread isn't polled")

	// A failed listing starts with nothing.
	s.store.listErr = errors.New("db")
	ctx, cancel = context.WithCancel(s.ctx)
	cancel()
	s.w.Run(ctx)
}

// TestRunBusy stops while waiting for a free poll slot.
func (s *WatcherSuite) TestRunBusy() {
	s.w.parallel = 1
	s.w.tick = time.Millisecond
	block := make(chan struct{})
	s.target = nil
	s.w.Resolve = func(context.Context, string) (*Target, error) {
		<-block
		return nil, nil
	}
	s.w.Kick("a")
	s.w.Kick("b")
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	go func() {
		s.w.Run(ctx)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	close(block)
	<-done
}

func (s *WatcherSuite) TestScheduling() {
	// Watch schedules an unscheduled thread only.
	s.w.Watch(ch)
	e := s.w.entry(ch)
	require.Equal(s.T(), s.now, e.due)
	require.Equal(s.T(), s.now.Add(defaultLinger), e.until)
	s.now = s.now.Add(time.Second)
	s.w.Watch(ch)
	require.Equal(s.T(), s.now.Add(-time.Second), e.due)

	require.Equal(s.T(), []string{ch}, s.w.dueNow())
	require.Empty(s.T(), s.w.dueNow(), "already polling")

	// Within the linger window a thread with no PR stays scheduled.
	s.target = nil
	s.w.Poll(s.ctx, ch)
	require.Equal(s.T(), s.now.Add(time.Duration(config.DefaultCIPollSec)*time.Second), e.due)
	s.now = s.now.Add(time.Hour)
	require.Equal(s.T(), []string{ch}, s.w.dueNow())
	s.w.Poll(s.ctx, ch)
	require.True(s.T(), e.due.IsZero())
	require.Empty(s.T(), s.w.dueNow())
}

func (s *WatcherSuite) TestAgentCommit() {
	require.True(s.T(), agentCommit([]string{"abc1234"}, "abc1234def"))
	require.False(s.T(), agentCommit([]string{"", "abd"}, "abc1234def"))
	require.False(s.T(), agentCommit([]string{""}, ""))
}

func (s *WatcherSuite) TestPrompt() {
	var fcs []forge.FailedCheck
	for i := range maxLogs + 2 {
		fcs = append(fcs, forge.FailedCheck{Name: fmt.Sprintf("c%d", i)})
	}
	s.forge.On("Log", mock.Anything, mock.Anything, mock.Anything).Return("log", nil)
	got, hash := s.w.prompt(s.ctx, s.forge, s.target, "abc", fcs)
	require.Equal(s.T(), 2, strings.Count(got, "(log not included)"))
	require.Contains(s.T(), got, "c0:\n```\nlog\n```")
	require.Contains(s.T(), got, "`gh run rerun <run-id> --failed`")
	require.Len(s.T(), hash, 16)

	require.Equal(s.T(), "`glab ci retry <job-id>`", rerunHint(forge.KindMR, nil))
	require.Equal(s.T(), "`gh run rerun 1 --failed`, `gh run rerun 2 --failed`",
		rerunHint(forge.KindPR, []forge.FailedCheck{{RunID: "1"}, {RunID: "1"}, {RunID: "2"}}))
	require.Equal(s.T(), "PR #12: c0, c1, c2 and 4 more failed", alertText(s.target, fcs))
	require.Equal(s.T(), "PR #12: CI failed", alertText(s.target, nil))
	require.Equal(s.T(), "abc", short("abc"))
}
