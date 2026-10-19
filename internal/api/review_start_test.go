package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/review"
	"github.com/radutopala/loop/internal/workflow"
)

type ReviewStartSuite struct {
	suite.Suite
	srv   *Server
	store *MockChannelLister
	gh    *mockGitHubReview
	wt    *mockPR
	rs    *review.Store
	eng   *MockWorkflowEngine
	mux   *http.ServeMux
}

func TestReviewStartSuite(t *testing.T) {
	suite.Run(t, new(ReviewStartSuite))
}

func (s *ReviewStartSuite) SetupTest() {
	s.srv = newServerForReviewTests(s.T())
	s.store = s.srv.store.(*MockChannelLister)
	s.gh = new(mockGitHubReview)
	s.wt = newMockPR()
	s.rs = review.NewStore()
	s.eng = new(MockWorkflowEngine)
	s.srv.review.setBackends(s.gh, s.rs, s.wt)
	s.srv.SetWorkflowEngine(s.eng)
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Review: config.ReviewConfig{Enabled: true}}, nil
	}
	s.srv.configs.loadProject = func(string, *config.Config) (*config.Config, error) { return nil, nil }
	s.srv.configs.loadWorktree = func(string, string, *config.Config) (*config.Config, error) { return nil, nil }
	s.mux = s.srv.buildMux()
}

func (s *ReviewStartSuite) start(body any) *httptest.ResponseRecorder {
	buf, err := json.Marshal(body)
	require.NoError(s.T(), err)
	req := httptest.NewRequest(http.MethodPost, "/api/channels/ch1/review/start", bytes.NewReader(buf))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func (s *ReviewStartSuite) channel(model, effort string) {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo", ModelOverride: model, EffortOverride: effort}, nil)
}

func (s *ReviewStartSuite) noRuns() {
	s.eng.On("ListRuns", mock.Anything, "ch1", 50, 0).Return([]*db.WorkflowRun{}, nil)
}

func (s *ReviewStartSuite) readySession(pr int) {
	s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: pr}, WorktreePath: "/repo/.worktrees/pr-7-ch1", Status: review.StatusReady})
}

func (s *ReviewStartSuite) loadMocks() {
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(&githubapi.PRInfo{Number: 7, BaseRef: "main"}, nil)
	s.gh.On("FetchPRHeadSHA", mock.Anything, "/repo", "", 7).Return("abc", nil)
	s.wt.On("Add", mock.Anything, "/repo", 7, "ch1").Return("/repo/.worktrees/pr-7-ch1", nil)
	s.gh.On("FetchRepoSlug", mock.Anything, "/repo", "").Return((*githubapi.RepoSlug)(nil), errors.New("no slug"))
	s.wt.On("Diff", mock.Anything, "/repo", "/repo/.worktrees/pr-7-ch1", "main", mock.Anything).Return([]byte("diff"), nil)
}

func (s *ReviewStartSuite) TestNotConfigured() {
	s.srv.workflowEngine = nil
	require.Equal(s.T(), http.StatusNotImplemented, s.start(map[string]any{"pr_number": 7}).Code)

	s.srv.SetWorkflowEngine(s.eng)
	s.srv.review.sessions = nil
	require.Equal(s.T(), http.StatusNotImplemented, s.start(map[string]any{"pr_number": 7}).Code)

	s.srv.store = nil
	require.Equal(s.T(), http.StatusNotImplemented, s.start(map[string]any{"pr_number": 7}).Code)
}

func (s *ReviewStartSuite) TestBadRequest() {
	tests := []struct {
		name string
		body any
	}{
		{"not an object", "nope"},
		{"no pr", map[string]any{}},
		{"unknown mode", map[string]any{"pr_number": 7, "mode": "fix"}},
		{"too many iterations", map[string]any{"pr_number": 7, "max_iterations": 11}},
		{"negative iterations", map[string]any{"pr_number": 7, "max_iterations": -1}},
		{"unknown effort", map[string]any{"pr_number": 7, "effort": "huge"}},
		{"unknown scope", map[string]any{"pr_number": 7, "scope": "partial"}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), http.StatusBadRequest, s.start(tt.body).Code)
		})
	}
}

func (s *ReviewStartSuite) TestChannelLookup() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, errors.New("db down")).Once()
	require.Equal(s.T(), http.StatusInternalServerError, s.start(map[string]any{"pr_number": 7}).Code)

	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil).Once()
	require.Equal(s.T(), http.StatusNotFound, s.start(map[string]any{"pr_number": 7}).Code)
}

func (s *ReviewStartSuite) TestBusy() {
	tests := []struct {
		name  string
		setup func()
		want  string
	}{
		{"another start", func() { s.srv.reviewStarting.Store("ch1", struct{}{}) }, "a review is starting for this channel"},
		{"run in flight", func() { s.srv.review.registerReviewRun("ch1", func() {}) }, "review run in flight for this channel"},
		{"session reviewing", func() {
			s.rs.Put("ch1", &review.Session{Status: review.StatusReviewing})
		}, "review session is reviewing"},
		{"workflow paused", func() {
			s.eng.On("ListRuns", mock.Anything, "ch1", 50, 0).Return([]*db.WorkflowRun{
				{ID: "w0", WorkflowName: "deploy", Status: db.WorkflowRunStatusRunning},
				{ID: "w1", WorkflowName: reviewLoopWorkflow, Status: db.WorkflowRunStatusCompleted},
				{ID: "w2", WorkflowName: reviewFixLoopWorkflow, Status: db.WorkflowRunStatusPaused},
			}, nil)
		}, "review workflow run w2 is paused"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.channel("", "")
			tt.setup()
			w := s.start(map[string]any{"pr_number": 7})
			require.Equal(s.T(), http.StatusConflict, w.Code)
			require.Contains(s.T(), w.Body.String(), tt.want)
		})
	}
}

func (s *ReviewStartSuite) TestLoadsAndStartsWithTheChannelsAgent() {
	s.channel("opus", "high")
	s.noRuns()
	s.loadMocks()
	s.eng.On("StartRun", mock.Anything, workflow.StartRunOptions{
		WorkflowName: reviewLoopWorkflow,
		ChannelID:    "ch1",
		DirPath:      "/repo",
		Inputs:       map[string]string{"max_iterations": "3", "pr": "7"},
	}).Return("run-1", nil)

	w := s.start(map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusCreated, w.Code, w.Body.String())
	var resp reviewStartResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "run-1", resp.RunID)
	require.Equal(s.T(), reviewLoopWorkflow, resp.WorkflowName)
	require.Equal(s.T(), "opus", resp.Model)
	require.Equal(s.T(), "high", resp.Effort)
	sess := s.rs.Get("ch1")
	require.Equal(s.T(), review.StatusReady, sess.Status)
	require.Equal(s.T(), "opus", sess.Model)
	require.Equal(s.T(), "high", sess.Effort)
	_, starting := s.srv.reviewStarting.Load("ch1")
	require.False(s.T(), starting)
}

func (s *ReviewStartSuite) TestKeepsTheLoadedPR() {
	s.channel("opus", "high")
	s.eng.On("ListRuns", mock.Anything, "ch1", 50, 0).Return(nil, errors.New("db down"))
	s.readySession(7)
	s.rs.StartReview("ch1", "rev-0")
	s.rs.AddReviewRun("ch1", 3)
	s.srv.review.newReviewID = func() string { return "rev-1" }
	s.eng.On("StartRun", mock.Anything, workflow.StartRunOptions{
		WorkflowName: reviewFixLoopWorkflow,
		ChannelID:    "ch1",
		DirPath:      "/repo",
		Inputs:       map[string]string{"max_iterations": "5", "pr": "7"},
	}).Return("run-2", nil)

	w := s.start(map[string]any{"pr_number": 7, "mode": "review-fix", "max_iterations": 5, "model": "sonnet", "effort": "low"})
	require.Equal(s.T(), http.StatusCreated, w.Code, w.Body.String())
	s.gh.AssertNotCalled(s.T(), "FetchPRByNumber", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	sess := s.rs.Get("ch1")
	require.Equal(s.T(), "sonnet", sess.Model)
	require.Equal(s.T(), "low", sess.Effort)
	// The loop's runs start a new review, with none of the last one's runs.
	require.Equal(s.T(), "rev-1", sess.ReviewID)
	require.Empty(s.T(), sess.ReviewRuns)
}

func (s *ReviewStartSuite) TestReloadsAnotherPR() {
	s.channel("", "")
	s.noRuns()
	s.readySession(3)
	s.wt.On("Remove", mock.Anything, "/repo", "/repo/.worktrees/pr-7-ch1").Return(nil)
	s.loadMocks()
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Review: config.ReviewConfig{Enabled: true, MaxIterations: 6}}, nil
	}
	s.eng.On("StartRun", mock.Anything, mock.MatchedBy(func(o workflow.StartRunOptions) bool {
		return o.Inputs["max_iterations"] == "6"
	})).Return("run-3", nil)

	w := s.start(map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusCreated, w.Code, w.Body.String())
	require.Equal(s.T(), 7, s.rs.Get("ch1").PR.Number)
}

func (s *ReviewStartSuite) TestLoadFails() {
	s.channel("", "")
	s.noRuns()
	s.gh.On("FetchPRByNumber", mock.Anything, "/repo", "", 7).Return(nil, nil)

	w := s.start(map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusNotFound, w.Code)
	s.eng.AssertNotCalled(s.T(), "StartRun", mock.Anything, mock.Anything)
}

func (s *ReviewStartSuite) TestResolveDirFails() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1"}, nil)
	s.noRuns()
	s.readySession(7)

	w := s.start(map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "has no dir_path")
}

func (s *ReviewStartSuite) TestStartRunFails() {
	s.channel("", "")
	s.noRuns()
	s.readySession(7)
	s.eng.On("StartRun", mock.Anything, mock.Anything).Return("", errors.New("no such workflow"))

	w := s.start(map[string]any{"pr_number": 7})
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "no such workflow")
}

func (s *ReviewStartSuite) TestFork() {
	tests := []struct {
		name      string
		fork      string
		wantMode  review.ForkMode
		wantSID   string
		startMode review.ForkMode
		startSID  string
	}{
		{"empty keeps the session's choice", "", review.ForkCustom, "keep-me", review.ForkCustom, "keep-me"},
		{"chat", "chat", review.ForkCurrent, "", review.ForkCustom, "old"},
		{"fresh", " fresh ", review.ForkNone, "", review.ForkCurrent, ""},
		{"session id", " sess-42 ", review.ForkCustom, "sess-42", review.ForkCurrent, ""},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.channel("", "")
			s.noRuns()
			s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, WorktreePath: "/repo/.worktrees/pr-7-ch1", Status: review.StatusReady, ForkMode: tt.startMode, ForkSessionID: tt.startSID})
			s.eng.On("StartRun", mock.Anything, mock.Anything).Return("run-1", nil)

			w := s.start(map[string]any{"pr_number": 7, "fork": tt.fork})
			require.Equal(s.T(), http.StatusCreated, w.Code, w.Body.String())
			var resp reviewStartResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(s.T(), tt.wantMode, resp.Session.ForkMode)
			require.Equal(s.T(), tt.wantSID, resp.Session.ForkSessionID)
		})
	}
}

func (s *ReviewStartSuite) TestScope() {
	tests := []struct {
		name  string
		scope string
		want  review.Scope
	}{
		{"empty keeps the session's choice", "", review.ScopeFull},
		{"auto", "auto", review.ScopeAuto},
		{"incremental", "incremental", review.ScopeIncremental},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.channel("", "")
			s.noRuns()
			s.rs.Put("ch1", &review.Session{PR: &githubapi.PRInfo{Number: 7}, WorktreePath: "/repo/.worktrees/pr-7-ch1", Status: review.StatusReady, Scope: review.ScopeFull})
			s.eng.On("StartRun", mock.Anything, mock.Anything).Return("run-1", nil)

			w := s.start(map[string]any{"pr_number": 7, "scope": tt.scope})
			require.Equal(s.T(), http.StatusCreated, w.Code, w.Body.String())
			require.Equal(s.T(), tt.want, s.rs.Get("ch1").Scope)
		})
	}
}
