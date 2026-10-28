package prwatch

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

// mockForge is a testify mock, but for its unresolved review threads,
// which are threads and threadsErr.
type mockForge struct {
	mock.Mock
	threads    []string
	threadsErr error
}

func (m *mockForge) Status(ctx context.Context, ref forge.Ref) (*forge.Status, error) {
	args := m.Called(ctx, ref)
	st, _ := args.Get(0).(*forge.Status)
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

func (m *mockForge) UnresolvedThreads(context.Context, forge.Ref) ([]string, error) {
	return m.threads, m.threadsErr
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
	rows                     map[string]db.PRState
	getErr, saveErr, listErr error
}

func (m *memStore) GetPRState(_ context.Context, channelID string) (*db.PRState, error) {
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

func (m *memStore) SavePRState(_ context.Context, st *db.PRState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.rows[st.ChannelID] = *st
	return nil
}

func (m *memStore) ListPRStates(context.Context) ([]*db.PRState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []*db.PRState
	for _, st := range m.rows {
		out = append(out, &st)
	}
	return out, nil
}

func (m *memStore) get(id string) db.PRState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[id]
}

func (m *memStore) put(st db.PRState) {
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
	broadcasts []events.PREventData
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
	s.store = &memStore{rows: map[string]db.PRState{}}
	s.forge = &mockForge{}
	s.now = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s.kind = kind
	s.target = &Target{Kind: kind, Ref: forge.Ref{Dir: "/w", Number: 12}, URL: "https://example.com/pr/12", PR: config.DefaultPRConfig()}
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
		Broadcast: func(d events.PREventData) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.broadcasts = append(s.broadcasts, d)
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s.w.now = func() time.Time { return s.now }
}

func (s *WatcherSuite) lastBroadcast() events.PREventData {
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(s.T(), s.broadcasts)
	return s.broadcasts[len(s.broadcasts)-1]
}

func (s *WatcherSuite) settings() {
	s.forge.On("MergeSettings", mock.Anything, mock.Anything).Return(forge.MergeSettings{AutoMerge: true, Methods: []string{"squash", "merge"}}, nil).Maybe()
}

func (s *WatcherSuite) ci(head string, checks forge.CheckSummary) *mock.Call {
	return s.forge.On("Status", mock.Anything, s.target.Ref).Return(&forge.Status{HeadSHA: head, Checks: checks, MergeState: forge.MergeBlocked}, nil)
}

var (
	failing = forge.CheckSummary{Total: 2, Passed: 1, Failed: 1}
	pending = forge.CheckSummary{Total: 2, Passed: 1, Pending: 1}
	green   = forge.CheckSummary{Total: 2, Passed: 2}
	lint    = forge.FailedCheck{ID: "77", RunID: "70", Name: "lint", Group: "ci", URL: "https://example.com/j/77"}
	infra   = forge.FailedCheck{ID: "78", RunID: "78", Name: "e2e", Group: "test", Infra: true}
)

func (s *WatcherSuite) poll() bool {
	keep, _, _ := s.w.poll(s.ctx, ch, s.w.entry(ch))
	return keep
}

// conflicting has the forge report head conflicting with main.
func (s *WatcherSuite) conflicting(head string) *mock.Call {
	return s.forge.On("Status", mock.Anything, s.target.Ref).Return(&forge.Status{HeadSHA: head, BaseRef: "main", Checks: green, MergeState: forge.MergeConflict}, nil)
}

// TestConflict flags a merge conflict for the owner, whoever made the
// commit, on both forges: it sends the agent nothing, alerts no one and
// clears once the conflict is gone.
func (s *WatcherSuite) TestConflict() {
	tests := []struct {
		name string
		kind string
		shas []string
	}{
		{name: "the agent's commit on a PR", kind: forge.KindPR, shas: []string{"h1"}},
		{name: "the agent's commit on an MR", kind: forge.KindMR, shas: []string{"h1"}},
		{name: "someone else's commit", kind: forge.KindPR, shas: []string{"other"}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.setup(tc.kind)
			s.settings()
			s.store.put(db.PRState{ChannelID: ch, Forge: tc.kind, Number: 12, AgentSHAs: tc.shas, Reason: events.PRReasonNotAgent, Failed: []forge.FailedCheck{lint}})
			s.conflicting("h1").Twice()
			for range 2 {
				require.False(s.T(), s.poll())
				st := s.store.get(ch)
				require.Equal(s.T(), events.PRAttentionConflict, st.Attention)
				require.Empty(s.T(), st.Reason)
				require.Nil(s.T(), st.Failed)
				require.Zero(s.T(), st.Attempts)
				require.True(s.T(), st.Open)
				require.Empty(s.T(), s.enqueued)
				require.Empty(s.T(), s.notices)
				require.Empty(s.T(), s.lastBroadcast().Alert)
				require.Equal(s.T(), "main", s.lastBroadcast().BaseRef)
				require.Equal(s.T(), events.PRAttentionConflict, s.lastBroadcast().Attention)
			}

			// The conflict is gone: the flag clears.
			s.ci("h1", pending).Once()
			require.True(s.T(), s.poll())
			require.Empty(s.T(), s.store.get(ch).Attention)
			s.forge.AssertExpectations(s.T())
		})
	}
}

// TestUnresolvedThreads counts the unresolved review threads, on both
// forges, without alerting anyone, and forgets them once the PR/MR is merged.
func (s *WatcherSuite) TestUnresolvedThreads() {
	for _, kind := range []string{forge.KindPR, forge.KindMR} {
		s.Run(kind, func() {
			s.setup(kind)
			s.settings()
			s.store.put(db.PRState{ChannelID: ch, Forge: kind, Number: 12})
			s.forge.threads = []string{"T1"}
			s.ci("h1", green).Once()
			s.poll()
			require.Empty(s.T(), s.lastBroadcast().Alert)
			require.Equal(s.T(), 1, s.lastBroadcast().UnresolvedThreads)
			require.Equal(s.T(), []string{"T1"}, s.store.get(ch).Threads)
			require.Empty(s.T(), s.store.get(ch).Attention, "threads don't flag the thread")

			// A new thread: the count follows, still no alert.
			s.forge.threads = []string{"T1", "T2"}
			s.ci("h1", green).Once()
			s.poll()
			require.Empty(s.T(), s.lastBroadcast().Alert)
			require.Equal(s.T(), 2, s.lastBroadcast().UnresolvedThreads)

			// One resolved.
			s.forge.threads = []string{"T2"}
			s.ci("h1", green).Once()
			s.poll()
			require.Equal(s.T(), 1, s.lastBroadcast().UnresolvedThreads)

			// A listing error keeps what was known.
			s.forge.threads, s.forge.threadsErr = nil, errors.New("boom")
			s.ci("h1", green).Once()
			s.poll()
			require.Equal(s.T(), []string{"T2"}, s.store.get(ch).Threads)
			s.forge.threadsErr = nil

			// Merged: forgotten.
			s.forge.On("Status", mock.Anything, s.target.Ref).Return(&forge.Status{HeadSHA: "h1", Merged: true}, nil).Once()
			s.poll()
			require.Nil(s.T(), s.store.get(ch).Threads)
			require.Zero(s.T(), s.lastBroadcast().UnresolvedThreads)
			s.forge.AssertExpectations(s.T())
		})
	}
}

// TestUnresolvedThreadsWithFailure leaves a failure's alert to the failure.
func (s *WatcherSuite) TestUnresolvedThreadsWithFailure() {
	s.settings()
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12})
	s.forge.threads = []string{"T1"}
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
	s.poll()
	require.Equal(s.T(), "PR #12: lint failed", s.lastBroadcast().Alert)
	require.Equal(s.T(), 1, s.lastBroadcast().UnresolvedThreads)
}

// TestConflictWaitsForFix leaves a conflict be while a fix for a failure is
// in flight.
func (s *WatcherSuite) TestConflictWaitsForFix() {
	s.settings()
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, AgentSHAs: []string{"h1"}, MergeArmed: "squash", MergeVia: viaLoop, ArmedSHA: "h0"})
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
	s.forge.On("Log", mock.Anything, s.target.Ref, lint).Return("FAIL", nil).Once()
	require.True(s.T(), s.poll())
	s.conflicting("h1").Once()
	require.True(s.T(), s.poll())
	require.Len(s.T(), s.enqueued, 1)
	st := s.store.get(ch)
	require.Equal(s.T(), events.PRReasonFixSent, st.Reason)
	require.Equal(s.T(), "h1", st.ArmedSHA, "the armed head follows the agent's")
	s.forge.AssertExpectations(s.T())
}

// TestIdlePoll polls an open PR/MR slowly once it isn't watched, and stops
// once it's closed or gone.
func (s *WatcherSuite) TestIdlePoll() {
	s.settings()
	s.target.PR.IdlePollSec = 600
	idle := 600 * time.Second
	e := s.w.entry(ch)
	s.w.Kick(ch)
	s.ci("h1", green).Once()
	s.w.Poll(s.ctx, ch)
	require.Equal(s.T(), s.now.Add(time.Duration(s.target.PR.PollSec)*time.Second), e.due, "within the linger")
	require.True(s.T(), s.store.get(ch).Open)

	s.now = s.now.Add(time.Hour)
	s.ci("h1", green).Once()
	s.w.Poll(s.ctx, ch)
	require.Equal(s.T(), s.now.Add(idle), e.due)

	// Errors keep polling an open PR/MR.
	s.forge.On("Status", mock.Anything, s.target.Ref).Return(nil, errors.New("gh")).Once()
	s.w.Poll(s.ctx, ch)
	require.Equal(s.T(), s.now.Add(idle), e.due)
	s.resolveErr = errors.New("gh")
	s.w.Poll(s.ctx, ch)
	require.Equal(s.T(), s.now.Add(time.Duration(config.DefaultPRIdlePollSec)*time.Second), e.due)
	s.resolveErr = nil

	s.forge.On("Status", mock.Anything, s.target.Ref).Return(&forge.Status{HeadSHA: "h1", Closed: true}, nil).Once()
	s.w.Poll(s.ctx, ch)
	require.True(s.T(), e.due.IsZero())
	require.False(s.T(), s.store.get(ch).Open)

	// A PR/MR that's gone is no longer open.
	s.store.put(db.PRState{ChannelID: ch, Open: true})
	s.target = nil
	s.w.Poll(s.ctx, ch)
	require.True(s.T(), e.due.IsZero())
	require.False(s.T(), s.store.get(ch).Open)
	s.forge.AssertExpectations(s.T())
}

// TestFixLoop sends a failure on the agent's commit to the agent, follows
// the fix run and clears on green, on both forges.
func (s *WatcherSuite) TestFixLoop() {
	for _, kind := range []string{forge.KindPR, forge.KindMR} {
		s.Run(kind, func() {
			s.setup(kind)
			s.settings()
			s.store.put(db.PRState{ChannelID: ch, Forge: kind, Number: 12, AgentSHAs: []string{"aaaaaaa1"}})
			s.ci("aaaaaaa1", failing).Once()
			s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
			s.forge.On("Log", mock.Anything, s.target.Ref, lint).Return("FAIL lint", nil).Once()

			require.True(s.T(), s.poll())
			st := s.store.get(ch)
			require.Equal(s.T(), events.PRAttentionFixing, st.Attention)
			require.Equal(s.T(), events.PRReasonFixSent, st.Reason)
			require.Equal(s.T(), 1, st.Attempts)
			require.Len(s.T(), s.enqueued, 1)
			require.Contains(s.T(), s.enqueued[0], "CI failed on aaaaaaa for "+label(s.target))
			require.Contains(s.T(), s.enqueued[0], "FAIL lint")
			// The closing paragraph is one line: the chat breaks at each newline.
			require.Contains(s.T(), s.enqueued[0], "unrelated to this branch (flaky test, infra, base branch), don't change code — re-run it with `")
			if kind == forge.KindMR {
				require.Contains(s.T(), s.enqueued[0], "`glab ci retry 77`")
			} else {
				require.Contains(s.T(), s.enqueued[0], "`gh run rerun 70 --failed`")
			}
			require.Equal(s.T(), events.PRAttentionFixing, s.lastBroadcast().Attention)

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
		state   db.PRState
		cfg     func(*config.PRConfig)
		checks  []forge.FailedCheck
		log     string
		notice  string
		reason  string
		noCheck bool
	}{
		{name: "someone else's commit", state: db.PRState{AgentSHAs: []string{"other"}}, reason: events.PRReasonNotAgent},
		{name: "auto-fix off in config", state: db.PRState{AgentSHAs: []string{"h1"}}, cfg: func(c *config.PRConfig) { c.AutoFix = false }, reason: events.PRReasonAutoFixOff},
		{name: "auto-fix off for the thread", state: db.PRState{AgentSHAs: []string{"h1"}, AutoFix: &off}, reason: events.PRReasonAutoFixOff},
		{name: "attempts used up", state: db.PRState{AgentSHAs: []string{"h1"}, Attempts: 3, LastFailedSHA: "h0"}, notice: "after 3 attempts", reason: events.PRReasonMaxAttempts},
		{name: "same failure again", state: db.PRState{AgentSHAs: []string{"h1"}, Attempts: 1, LastFailedSHA: "h0", LastLogHash: forge.LogHash("ci / lint\nFAIL\n")}, log: "FAIL", notice: "same failure came back", reason: events.PRReasonSameFailure},
		{name: "the fix pushed nothing", state: db.PRState{AgentSHAs: []string{"h1"}, Attempts: 1, LastFailedSHA: "h1"}, notice: "pushed no new commit", reason: events.PRReasonNoNewCommit},
		{name: "infra again after a re-run", state: db.PRState{AgentSHAs: []string{"h1"}, InfraRetriedSHA: "h1"}, checks: []forge.FailedCheck{infra}, notice: "infrastructure reasons after a re-run", reason: events.PRReasonInfraAgain},
	}
	for _, kind := range []string{forge.KindPR, forge.KindMR} {
		for _, tc := range tests {
			s.Run(kind+"/"+tc.name, func() {
				s.setup(kind)
				s.settings()
				if tc.cfg != nil {
					tc.cfg(&s.target.PR)
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
				require.Equal(s.T(), events.PRAttentionFailed, got.Attention)
				require.Equal(s.T(), "h1", got.LastFailedSHA)
				require.Equal(s.T(), tc.reason, got.Reason)
				require.Equal(s.T(), checks, got.Failed)
				require.Empty(s.T(), s.enqueued)
				b := s.lastBroadcast()
				require.Equal(s.T(), label(s.target)+": "+checks[0].Name+" failed", b.Alert)
				require.Equal(s.T(), checks, b.Failed)
				require.Equal(s.T(), tc.reason, b.Reason)
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
				got = s.store.get(ch)
				require.Empty(s.T(), got.Attention)
				require.Empty(s.T(), got.Reason)
				require.Nil(s.T(), got.Failed)
			})
		}
	}
}

// TestBackfill fills in the reason and the failed checks of a failure
// flagged before Loop kept them, once.
func (s *WatcherSuite) TestBackfill() {
	s.settings()
	s.head = "c0ffee1"
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Attention: events.PRAttentionFailed, LastFailedSHA: "h1"})
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return(nil, errors.New("gh")).Once()
	require.False(s.T(), s.poll())
	got := s.store.get(ch)
	require.Equal(s.T(), events.PRReasonNotAgent, got.Reason)
	require.Nil(s.T(), got.Failed)

	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
	require.False(s.T(), s.poll())
	b := s.lastBroadcast()
	require.Equal(s.T(), []forge.FailedCheck{lint}, b.Failed)
	require.Equal(s.T(), events.PRReasonNotAgent, b.Reason)
	require.Equal(s.T(), "c0ffee1", b.CheckoutSHA)

	// Both kept: no more listing.
	s.ci("h1", failing).Once()
	require.False(s.T(), s.poll())
	s.forge.AssertExpectations(s.T())
}

// TestInfraRerun re-runs infra failures once.
func (s *WatcherSuite) TestInfraRerun() {
	s.setup(forge.KindMR)
	s.settings()
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindMR, Number: 12, AgentSHAs: []string{"h1"}})
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
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindMR, Number: 12, AgentSHAs: []string{"h1"}})
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{infra}, nil).Once()
	s.forge.On("Rerun", mock.Anything, s.target.Ref, infra).Return(errors.New("denied")).Once()
	require.False(s.T(), s.poll())
	require.Equal(s.T(), events.PRAttentionFailed, s.store.get(ch).Attention)
}

func (s *WatcherSuite) TestPollEnds() {
	s.settings()
	// Merged: nothing watched, nothing armed.
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Watching: true, Attention: "failed", MergeArmed: "squash", MergeVia: viaForge})
	s.forge.On("Status", mock.Anything, s.target.Ref).Return(&forge.Status{HeadSHA: "h", Merged: true}, nil).Once()
	require.False(s.T(), s.poll())
	st := s.store.get(ch)
	require.Equal(s.T(), db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12}, st)
	require.True(s.T(), s.lastBroadcast().Merged)
	require.Nil(s.T(), s.lastBroadcast().MergeMethods, "no merge settings once merged")

	// No PR: cleared once, quietly after.
	s.store.put(db.PRState{ChannelID: ch, Watching: true})
	s.target = nil
	n := len(s.broadcasts)
	require.False(s.T(), s.poll())
	require.False(s.T(), s.store.get(ch).Watching)
	require.Equal(s.T(), events.PREventData{ChannelID: ch}, s.lastBroadcast())
	require.False(s.T(), s.poll())
	require.Len(s.T(), s.broadcasts, n+1)
}

func (s *WatcherSuite) TestPollErrors() {
	s.settings()
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Watching: true})

	s.resolveErr = errors.New("gh down")
	require.True(s.T(), s.poll(), "keeps watching through a lookup error")
	s.resolveErr = nil

	s.target.Kind = "other"
	require.True(s.T(), s.poll(), "no forge for the kind")
	s.target.Kind = forge.KindPR

	s.forge.On("Status", mock.Anything, s.target.Ref).Return(nil, errors.New("boom")).Once()
	require.True(s.T(), s.poll())

	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, AgentSHAs: []string{"h1"}})
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
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 3, AgentSHAs: []string{"h1"}, Attempts: 3, Attention: "failed", AutoFix: &on, MergeArmed: "merge"})
	s.ci("h1", pending).Once()
	s.poll()
	require.Equal(s.T(), db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, AgentSHAs: []string{"h1"}, AutoFix: &on, Watching: true, Open: true}, s.store.get(ch))
}

func (s *WatcherSuite) TestLoopMerge() {
	s.forge.On("MergeSettings", mock.Anything, mock.Anything).Return(forge.MergeSettings{}, errors.New("no settings")).Maybe()
	armed := db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, AgentSHAs: []string{"h1", "h2"}, MergeArmed: "merge", MergeVia: viaLoop, ArmedSHA: "h1"}

	// The agent's new head moves the armed head along.
	s.store.put(armed)
	s.ci("h2", pending).Once()
	s.poll()
	require.Equal(s.T(), "h2", s.store.get(ch).ArmedSHA)

	// Green and clean: merged once.
	s.forge.On("Status", mock.Anything, s.target.Ref).Return(&forge.Status{HeadSHA: "h2", Checks: green, MergeState: forge.MergeClean}, nil).Once()
	s.forge.On("Merge", mock.Anything, s.target.Ref, "merge").Return(nil).Once()
	require.True(s.T(), s.poll(), "watched until it reads merged")
	require.Empty(s.T(), s.store.get(ch).MergeArmed)

	// A failed merge disarms with a notice.
	s.store.put(armed)
	s.forge.On("Status", mock.Anything, s.target.Ref).Return(&forge.Status{HeadSHA: "h1", Checks: green, MergeState: forge.MergeClean}, nil).Once()
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
	s.store.put(db.PRState{ChannelID: ch, AgentSHAs: shas})
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

func (s *WatcherSuite) TestFlags() {
	s.store.put(db.PRState{ChannelID: "a", Attention: "failed"})
	s.store.put(db.PRState{ChannelID: "b"})
	s.store.put(db.PRState{ChannelID: "c", Attention: "conflict"})
	s.store.put(db.PRState{ChannelID: "d", Attention: "fixing", Threads: []string{"T1", "T2"}})
	s.store.put(db.PRState{ChannelID: "e", Threads: []string{"T1"}})
	got, err := s.w.Flags(s.ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), map[string]events.PRFlags{
		"a": {Attention: events.PRAttentionFailed},
		"c": {Attention: events.PRAttentionConflict},
		"d": {Attention: events.PRAttentionFixing, UnresolvedThreads: 2},
		"e": {UnresolvedThreads: 1},
	}, got)

	s.store.listErr = errors.New("db")
	_, err = s.w.Flags(s.ctx)
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
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Attention: "failed", LastFailedSHA: "h1"})
	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
	s.forge.On("Log", mock.Anything, s.target.Ref, lint).Return("", errors.New("expired")).Once()
	require.NoError(s.T(), s.w.FixNow(s.ctx, ch))
	require.Contains(s.T(), s.enqueued[0], "(no log available)")
	require.Equal(s.T(), events.PRAttentionFixing, s.store.get(ch).Attention)
	require.Equal(s.T(), events.PRReasonFixAsked, s.store.get(ch).Reason)
	require.Equal(s.T(), events.PRAttentionFixing, s.lastBroadcast().Attention)

	s.ci("h1", green).Once()
	require.ErrorIs(s.T(), s.w.FixNow(s.ctx, ch), ErrNoFailure)

	// A merge conflict is the owner's: it never goes to the agent.
	s.conflicting("h1").Once()
	require.ErrorIs(s.T(), s.w.FixNow(s.ctx, ch), ErrNoFailure)
	require.Len(s.T(), s.enqueued, 1)

	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return(nil, errors.New("boom")).Once()
	require.Error(s.T(), s.w.FixNow(s.ctx, ch))

	s.ci("h1", failing).Once()
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{{Name: "x"}}, nil).Once()
	s.forge.On("Log", mock.Anything, s.target.Ref, forge.FailedCheck{Name: "x"}).Return("", nil).Once()
	s.store.saveErr = errors.New("db")
	require.Error(s.T(), s.w.FixNow(s.ctx, ch))
	s.store.saveErr = nil

	s.forge.On("Status", mock.Anything, s.target.Ref).Return(nil, errors.New("boom")).Once()
	require.Error(s.T(), s.w.FixNow(s.ctx, ch))

	s.target = nil
	require.ErrorIs(s.T(), s.w.FixNow(s.ctx, ch), ErrNoPR)
	s.store.getErr = errors.New("db")
	require.Error(s.T(), s.w.FixNow(s.ctx, ch))
	s.forge.AssertExpectations(s.T())
}

// TestRerun re-runs each failed GitHub run once, and each failed GitLab job.
func (s *WatcherSuite) TestRerun() {
	other := forge.FailedCheck{ID: "79", RunID: "70", Name: "vet"}
	for _, kind := range []string{forge.KindPR, forge.KindMR} {
		s.Run(kind, func() {
			s.setup(kind)
			s.store.put(db.PRState{ChannelID: ch, Forge: kind, Number: 12})
			s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint, other, infra}, nil).Once()
			s.forge.On("Rerun", mock.Anything, s.target.Ref, lint).Return(nil).Once()
			if kind == forge.KindMR {
				s.forge.On("Rerun", mock.Anything, s.target.Ref, other).Return(nil).Once()
			}
			s.forge.On("Rerun", mock.Anything, s.target.Ref, infra).Return(nil).Once()
			require.NoError(s.T(), s.w.Rerun(s.ctx, ch))
			s.forge.AssertExpectations(s.T())
		})
	}
}

func (s *WatcherSuite) TestRerunErrors() {
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12})
	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return([]forge.FailedCheck{lint}, nil).Once()
	s.forge.On("Rerun", mock.Anything, s.target.Ref, lint).Return(errors.New("gh")).Once()
	require.EqualError(s.T(), s.w.Rerun(s.ctx, ch), "gh")

	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return(nil, nil).Once()
	require.ErrorIs(s.T(), s.w.Rerun(s.ctx, ch), ErrNoFailure)

	s.forge.On("FailedChecks", mock.Anything, s.target.Ref).Return(nil, errors.New("boom")).Once()
	require.EqualError(s.T(), s.w.Rerun(s.ctx, ch), "boom")

	s.target = nil
	require.ErrorIs(s.T(), s.w.Rerun(s.ctx, ch), ErrNoPR)
	s.store.getErr = errors.New("db")
	require.Error(s.T(), s.w.Rerun(s.ctx, ch))
	s.forge.AssertExpectations(s.T())
}

// TestLog reads the log of one of the failed checks CI last reported.
func (s *WatcherSuite) TestLog() {
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Failed: []forge.FailedCheck{infra, lint}})
	s.forge.On("Log", mock.Anything, s.target.Ref, lint).Return("FAIL lint", nil).Once()
	got, err := s.w.Log(s.ctx, ch, "77")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "FAIL lint", got)

	_, err = s.w.Log(s.ctx, ch, "1")
	require.ErrorIs(s.T(), err, ErrNoCheck)

	s.target = nil
	_, err = s.w.Log(s.ctx, ch, "77")
	require.ErrorIs(s.T(), err, ErrNoPR)
	s.store.getErr = errors.New("db")
	_, err = s.w.Log(s.ctx, ch, "77")
	require.Error(s.T(), err)
	s.forge.AssertExpectations(s.T())
}

func (s *WatcherSuite) TestArm() {
	tests := []struct {
		name     string
		settings forge.MergeSettings
		method   string
		cfg      string
		enable   error
		ci       *forge.Status
		wantVia  string
		want     string
		wantErr  string
	}{
		{name: "forge auto-merge", settings: forge.MergeSettings{AutoMerge: true, Methods: []string{"squash", "merge"}}, ci: &forge.Status{HeadSHA: "h1"}, wantVia: viaForge, want: "squash"},
		{name: "configured method, Loop merges", settings: forge.MergeSettings{Methods: []string{"squash", "merge"}}, cfg: "merge", ci: &forge.Status{HeadSHA: "h1"}, wantVia: viaLoop, want: "merge"},
		{name: "method not allowed", settings: forge.MergeSettings{Methods: []string{"squash"}}, method: "rebase", wantErr: "doesn't allow rebase"},
		{name: "closed", settings: forge.MergeSettings{Methods: []string{"merge"}}, ci: &forge.Status{Closed: true}, wantErr: "no longer open"},
		{name: "enable fails", settings: forge.MergeSettings{AutoMerge: true, Methods: []string{"merge"}}, ci: &forge.Status{HeadSHA: "h1"}, enable: errors.New("not allowed"), wantErr: "not allowed"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.setup(forge.KindPR)
			s.target.PR.MergeMethod = tc.cfg
			s.forge.On("MergeSettings", mock.Anything, s.target.Ref).Return(tc.settings, nil)
			if tc.ci != nil {
				s.forge.On("Status", mock.Anything, s.target.Ref).Return(tc.ci, nil)
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
	s.forge.On("Status", mock.Anything, s.target.Ref).Return(nil, errors.New("ci")).Once()
	require.ErrorContains(s.T(), s.w.Arm(s.ctx, ch, ""), "ci")

	s.forge.On("Status", mock.Anything, s.target.Ref).Return(&forge.Status{HeadSHA: "h"}, nil).Once()
	s.store.saveErr = errors.New("db")
	require.ErrorContains(s.T(), s.w.Arm(s.ctx, ch, ""), "db")
}

func (s *WatcherSuite) TestCancelMerge() {
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, MergeArmed: "squash", MergeVia: viaForge})
	s.forge.On("DisableAutoMerge", mock.Anything, s.target.Ref).Return(errors.New("gh")).Once()
	require.ErrorContains(s.T(), s.w.CancelMerge(s.ctx, ch), "gh")
	s.forge.On("DisableAutoMerge", mock.Anything, s.target.Ref).Return(nil).Once()
	require.NoError(s.T(), s.w.CancelMerge(s.ctx, ch))
	require.Empty(s.T(), s.store.get(ch).MergeArmed)

	// Loop's own merge needs no forge call.
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, MergeArmed: "squash", MergeVia: viaLoop})
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
	s.store.put(db.PRState{ChannelID: ch, Forge: forge.KindPR, Number: 12, Watching: true, Attention: events.PRAttentionFixing, LastFailedSHA: "h1"})
	s.store.put(db.PRState{ChannelID: "idle"})
	s.store.put(db.PRState{ChannelID: "open", Open: true})
	s.ci("h1", failing)
	s.w.Resolve = func(_ context.Context, id string) (*Target, error) {
		if id == "open" {
			return nil, nil
		}
		return s.target, nil
	}
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
	require.Equal(s.T(), events.PRAttentionFixing, s.store.get(ch).Attention, "the restored fix is still in flight")
	require.Empty(s.T(), s.enqueued)
	s.w.mu.Lock()
	_, ok := s.w.entries["idle"]
	s.w.mu.Unlock()
	require.False(s.T(), ok, "an idle thread isn't polled")
	require.False(s.T(), s.store.get("open").Open, "an open PR/MR is polled")

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
	require.Equal(s.T(), s.now.Add(time.Duration(config.DefaultPRPollSec)*time.Second), e.due)
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
