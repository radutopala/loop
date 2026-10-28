package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/forge"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/gitlabapi"
	"github.com/radutopala/loop/internal/prwatch"
	"github.com/radutopala/loop/internal/types"
)

type mockPRWatcher struct{ mock.Mock }

func (m *mockPRWatcher) Watch(channelID string) { m.Called(channelID) }

func (m *mockPRWatcher) Status(ctx context.Context, channelID string) *events.PREventData {
	d, _ := m.Called(ctx, channelID).Get(0).(*events.PREventData)
	return d
}

func (m *mockPRWatcher) Flags(ctx context.Context) (map[string]events.PRFlags, error) {
	args := m.Called(ctx)
	fl, _ := args.Get(0).(map[string]events.PRFlags)
	return fl, args.Error(1)
}

func (m *mockPRWatcher) SetAutoFix(ctx context.Context, channelID string, on *bool) error {
	return m.Called(ctx, channelID, on).Error(0)
}

func (m *mockPRWatcher) FixNow(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *mockPRWatcher) Rerun(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *mockPRWatcher) Log(ctx context.Context, channelID, checkID string) (string, error) {
	args := m.Called(ctx, channelID, checkID)
	return args.String(0), args.Error(1)
}

func (m *mockPRWatcher) Arm(ctx context.Context, channelID, method string) error {
	return m.Called(ctx, channelID, method).Error(0)
}

func (m *mockPRWatcher) CancelMerge(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *mockPRWatcher) MergeNow(ctx context.Context, channelID, method string) error {
	return m.Called(ctx, channelID, method).Error(0)
}

func (m *mockPRWatcher) AgentStatus(channelID string, d events.AgentStatusEventData) {
	m.Called(channelID, d)
}

func (m *mockPRWatcher) ChannelUpdated(d events.ChannelUpdatedData) { m.Called(d) }

// prMux serves the CI routes off s.srv.
func (s *ServerSuite) prMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr/status", s.srv.handleChannelPRStatus)
	mux.HandleFunc("POST /api/channels/{id}/pr/fix", s.srv.handleChannelPRFix)
	mux.HandleFunc("POST /api/channels/{id}/pr/rerun", s.srv.handleChannelPRRerun)
	mux.HandleFunc("GET /api/channels/{id}/pr/log", s.srv.handleChannelPRLog)
	mux.HandleFunc("PUT /api/channels/{id}/pr/auto-fix", s.srv.handleChannelPRAutoFix)
	mux.HandleFunc("POST /api/channels/{id}/pr/merge", s.srv.handlePRMerge)
	mux.HandleFunc("DELETE /api/channels/{id}/pr/merge", s.srv.handlePRMergeCancel)
	return mux
}

func (s *ServerSuite) servePR(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	s.prMux().ServeHTTP(w, req)
	return w
}

func (s *ServerSuite) TestPRRoutesNotConfigured() {
	for _, r := range [][2]string{
		{"GET", "/api/channels/th-1/pr/status"},
		{"POST", "/api/channels/th-1/pr/fix"},
		{"POST", "/api/channels/th-1/pr/rerun"},
		{"GET", "/api/channels/th-1/pr/log?check=9"},
		{"PUT", "/api/channels/th-1/pr/auto-fix"},
		{"POST", "/api/channels/th-1/pr/merge"},
		{"DELETE", "/api/channels/th-1/pr/merge"},
	} {
		require.Equal(s.T(), http.StatusNotImplemented, s.servePR(r[0], r[1], "{}").Code, r)
	}
}

func (s *ServerSuite) TestChannelPR() {
	ci := new(mockPRWatcher)
	s.srv.SetPRWatcher(ci)
	ci.On("Status", mock.Anything, "th-1").Return(&events.PREventData{ChannelID: "th-1", Kind: "pr", Number: 12}).Once()
	w := s.servePR("GET", "/api/channels/th-1/pr/status", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp prStatusResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(s.T(), resp.Present)
	require.Equal(s.T(), 12, resp.PR.Number)

	ci.On("Status", mock.Anything, "th-2").Return(nil).Once()
	w = s.servePR("GET", "/api/channels/th-2/pr/status", "")
	require.JSONEq(s.T(), `{"present":false}`, w.Body.String())
}

func (s *ServerSuite) TestChannelPRActions() {
	on := true
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		setup  func(ci *mockPRWatcher)
		want   int
	}{
		{name: "fix now", method: "POST", path: "/api/channels/th-1/pr/fix",
			setup: func(ci *mockPRWatcher) { ci.On("FixNow", mock.Anything, "th-1").Return(nil) }, want: http.StatusNoContent},
		{name: "fix without a failure", method: "POST", path: "/api/channels/th-1/pr/fix",
			setup: func(ci *mockPRWatcher) { ci.On("FixNow", mock.Anything, "th-1").Return(prwatch.ErrNoFailure) }, want: http.StatusConflict},
		{name: "fix without a PR", method: "POST", path: "/api/channels/th-1/pr/fix",
			setup: func(ci *mockPRWatcher) { ci.On("FixNow", mock.Anything, "th-1").Return(prwatch.ErrNoPR) }, want: http.StatusNotFound},
		{name: "rerun", method: "POST", path: "/api/channels/th-1/pr/rerun",
			setup: func(ci *mockPRWatcher) { ci.On("Rerun", mock.Anything, "th-1").Return(nil) }, want: http.StatusNoContent},
		{name: "rerun without a failure", method: "POST", path: "/api/channels/th-1/pr/rerun",
			setup: func(ci *mockPRWatcher) { ci.On("Rerun", mock.Anything, "th-1").Return(prwatch.ErrNoFailure) }, want: http.StatusConflict},
		{name: "log of an unknown check", method: "GET", path: "/api/channels/th-1/pr/log?check=9",
			setup: func(ci *mockPRWatcher) { ci.On("Log", mock.Anything, "th-1", "9").Return("", prwatch.ErrNoCheck) }, want: http.StatusNotFound},
		{name: "log the forge can't read", method: "GET", path: "/api/channels/th-1/pr/log?check=9",
			setup: func(ci *mockPRWatcher) { ci.On("Log", mock.Anything, "th-1", "9").Return("", errors.New("gh")) }, want: http.StatusBadGateway},
		{name: "auto-fix on", method: "PUT", path: "/api/channels/th-1/pr/auto-fix", body: `{"auto_fix":true}`,
			setup: func(ci *mockPRWatcher) { ci.On("SetAutoFix", mock.Anything, "th-1", &on).Return(nil) }, want: http.StatusNoContent},
		{name: "auto-fix back to the config's", method: "PUT", path: "/api/channels/th-1/pr/auto-fix", body: `{"auto_fix":null}`,
			setup: func(ci *mockPRWatcher) { ci.On("SetAutoFix", mock.Anything, "th-1", (*bool)(nil)).Return(nil) }, want: http.StatusNoContent},
		{name: "auto-fix bad body", method: "PUT", path: "/api/channels/th-1/pr/auto-fix", body: `{`, want: http.StatusBadRequest},
		{name: "merge when ready", method: "POST", path: "/api/channels/th-1/pr/merge", body: `{"when_ready":true,"method":"squash"}`,
			setup: func(ci *mockPRWatcher) { ci.On("Arm", mock.Anything, "th-1", "squash").Return(nil) }, want: http.StatusNoContent},
		{name: "merge now fails", method: "POST", path: "/api/channels/th-1/pr/merge", body: `{}`,
			setup: func(ci *mockPRWatcher) {
				ci.On("MergeNow", mock.Anything, "th-1", "").Return(errors.New("not mergeable"))
			}, want: http.StatusBadGateway},
		{name: "merge bad body", method: "POST", path: "/api/channels/th-1/pr/merge", body: `[`, want: http.StatusBadRequest},
		{name: "cancel merge", method: "DELETE", path: "/api/channels/th-1/pr/merge",
			setup: func(ci *mockPRWatcher) { ci.On("CancelMerge", mock.Anything, "th-1").Return(nil) }, want: http.StatusNoContent},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			ci := new(mockPRWatcher)
			if tt.setup != nil {
				tt.setup(ci)
			}
			s.srv.SetPRWatcher(ci)
			w := s.servePR(tt.method, tt.path, tt.body)
			require.Equal(s.T(), tt.want, w.Code, w.Body.String())
			ci.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestChannelPRLog() {
	ci := new(mockPRWatcher)
	s.srv.SetPRWatcher(ci)
	ci.On("Log", mock.Anything, "th-1", "9").Return("FAIL: TestX", nil).Once()
	w := s.servePR("GET", "/api/channels/th-1/pr/log?check=9", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.JSONEq(s.T(), `{"log":"FAIL: TestX"}`, w.Body.String())
	ci.AssertExpectations(s.T())
}

func (s *ServerSuite) TestObservePR() {
	s.srv.ObservePR(Event{Data: events.AgentStatusEventData{}}) // no watcher

	ci := new(mockPRWatcher)
	s.srv.SetPRWatcher(ci)
	run := events.AgentStatusEventData{Status: "running"}
	upd := events.ChannelUpdatedData{ChannelID: "th-1", Commit: "abc1234"}
	ci.On("AgentStatus", "th-1", run).Once()
	ci.On("ChannelUpdated", upd).Once()
	s.srv.ObservePR(Event{ChannelID: "th-1", Data: run})
	s.srv.ObservePR(Event{ChannelID: "th-1", Data: upd})
	s.srv.ObservePR(Event{ChannelID: "th-1", Data: events.MessageEventData{}})
	ci.AssertExpectations(s.T())
}

func (s *ServerSuite) TestPRTarget() {
	ctx := context.Background()
	s.store.On("GetChannel", mock.Anything, "err").Return(nil, errors.New("db")).Once()
	_, err := s.srv.PRTarget(ctx, "err")
	require.Error(s.T(), err)

	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	t, err := s.srv.PRTarget(ctx, "gone")
	require.NoError(s.T(), err)
	require.Nil(s.T(), t)

	// Not a git checkout: no branch.
	s.store.On("GetChannel", mock.Anything, "plain").Return(&db.Channel{ChannelID: "plain", DirPath: s.T().TempDir()}, nil)
	t, err = s.srv.PRTarget(ctx, "plain")
	require.NoError(s.T(), err)
	require.Nil(s.T(), t)

	dir := gitInitRepoWithBranch(s.T(), "feat-ci")
	s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", DirPath: dir}, nil)
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{GitHub: config.GitHubConfig{GHUser: "octo"}, PR: config.PRConfig{AutoFix: false, MaxAttempts: 5, PollSec: 10}}, nil
	}
	gh := new(mockGitHubLookup)
	s.srv.prLookup.client = gh

	gh.On("LookupPR", mock.Anything, dir, "octo", "feat-ci").Return(nil, errors.New("network")).Once()
	_, err = s.srv.PRTarget(ctx, "th-1")
	require.Error(s.T(), err)

	gh.On("LookupPR", mock.Anything, dir, "octo", "feat-ci").Return(nil, githubapi.ErrGhNotInstalled).Once()
	t, err = s.srv.PRTarget(ctx, "th-1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), t)

	gh.On("LookupPR", mock.Anything, dir, "octo", "feat-ci").Return(nil, nil).Once()
	t, err = s.srv.PRTarget(ctx, "th-1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), t)

	// The no-PR answer is cached; a new commit drops it.
	t, err = s.srv.PRTarget(ctx, "th-1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), t)
	s.srv.InvalidatePRCacheForDir(dir)

	gh.On("LookupPR", mock.Anything, dir, "octo", "feat-ci").Return(&githubapi.PRInfo{Number: 12, URL: "https://example.com/pr/12"}, nil).Once()
	t, err = s.srv.PRTarget(ctx, "th-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), &prwatch.Target{
		Kind: forge.KindPR,
		Ref:  forge.Ref{Dir: dir, Account: "octo", Number: 12},
		URL:  "https://example.com/pr/12",
		PR:   config.PRConfig{AutoFix: false, MaxAttempts: 5, PollSec: 10},
	}, t)
	gh.AssertExpectations(s.T())

	// Without a config, the defaults.
	s.srv.InvalidatePRCacheForDir(dir)
	s.srv.configs.load = func() (*config.Config, error) { return nil, errors.New("no config") }
	gh.On("LookupPR", mock.Anything, dir, "", "feat-ci").Return(&githubapi.PRInfo{Number: 12}, nil).Once()
	t, err = s.srv.PRTarget(ctx, "th-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), config.DefaultPRConfig(), t.PR)
}

func (s *ServerSuite) TestPRTargetGitLab() {
	dir := gitInitRepoWithBranch(s.T(), "feat-mr")
	setOrigin(s.T(), dir, "git@gitlab.example.com:example/group/project.git")
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{GitLab: config.GitLabConfig{GlabConfigDir: "~/glab-work"}}, nil
	}
	s.srv.sys = homeSystem{home: "/home/testuser"}
	gl := new(mockGitLabLookup)
	gl.On("IsGitLabHost", "gitlab.example.com", "/home/testuser/glab-work").Return(true)
	gl.On("LookupMR", mock.Anything, dir, "/home/testuser/glab-work", "feat-mr").
		Return(nil, gitlabapi.ErrGlabNotInstalled).Once()
	gl.On("LookupMR", mock.Anything, dir, "/home/testuser/glab-work", "feat-mr").
		Return(&githubapi.PRInfo{Number: 7, Kind: "mr"}, nil).Once()
	s.srv.prLookup.gitlab = gl
	s.store.On("GetChannel", mock.Anything, "th-mr").Return(&db.Channel{ChannelID: "th-mr", DirPath: dir}, nil)

	t, err := s.srv.PRTarget(context.Background(), "th-mr")
	require.NoError(s.T(), err)
	require.Nil(s.T(), t)
	t, err = s.srv.PRTarget(context.Background(), "th-mr")
	require.NoError(s.T(), err)
	require.Equal(s.T(), forge.KindMR, t.Kind)
	require.Equal(s.T(), forge.Ref{Dir: dir, Account: "/home/testuser/glab-work", Number: 7}, t.Ref)
}

func (s *ServerSuite) TestChannelHead() {
	ctx := context.Background()
	s.store.On("GetChannel", mock.Anything, "err").Return(nil, errors.New("db"))
	require.Empty(s.T(), s.srv.ChannelHead(ctx, "err"))
	s.store.On("GetChannel", mock.Anything, "nodir").Return(&db.Channel{ChannelID: "nodir"}, nil)
	require.Empty(s.T(), s.srv.ChannelHead(ctx, "nodir"))
	s.store.On("GetChannel", mock.Anything, "plain").Return(&db.Channel{ChannelID: "plain", DirPath: s.T().TempDir()}, nil)
	require.Empty(s.T(), s.srv.ChannelHead(ctx, "plain"))
	dir := gitInitRepoWithBranch(s.T(), "main")
	s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", DirPath: dir}, nil)
	require.Len(s.T(), s.srv.ChannelHead(ctx, "th-1"), 40)
}

func (s *ServerSuite) TestChannelPRWatchesPR() {
	dir := gitInitRepoWithBranch(s.T(), "feat-watch")
	gh := new(mockGitHubLookup)
	gh.On("LookupPR", mock.Anything, dir, "", "feat-watch").Return(&githubapi.PRInfo{Number: 3}, nil)
	s.srv.prLookup.client = gh
	ci := new(mockPRWatcher)
	ci.On("Watch", "th-w").Once()
	s.srv.SetPRWatcher(ci)
	s.store.On("GetChannel", mock.Anything, "th-w").Return(&db.Channel{ChannelID: "th-w", DirPath: dir}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/pr", s.srv.handleChannelPR)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/channels/th-w/pr", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	ci.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsPRFlags() {
	tests := []struct {
		name string
		ci   func() PRWatcher
		want map[string]events.PRFlags
	}{
		{name: "no watcher", ci: func() PRWatcher { return nil }, want: map[string]events.PRFlags{}},
		{name: "flags by thread", ci: func() PRWatcher {
			ci := new(mockPRWatcher)
			ci.On("Flags", mock.Anything).Return(map[string]events.PRFlags{"ch-2": {Attention: "conflict", UnresolvedThreads: 2}}, nil)
			return ci
		}, want: map[string]events.PRFlags{"ch-2": {Attention: "conflict", UnresolvedThreads: 2}}},
		{name: "lookup fails", ci: func() PRWatcher {
			ci := new(mockPRWatcher)
			ci.On("Flags", mock.Anything).Return(nil, errors.New("db"))
			return ci
		}, want: map[string]events.PRFlags{}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
				{ChannelID: "ch-1", Name: "app", DirPath: "/a", Platform: types.PlatformLocal},
				{ChannelID: "ch-2", Name: "thread", DirPath: "/a", ParentID: "ch-1", Platform: types.PlatformLocal},
			}, nil)
			s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)
			if ci := tt.ci(); ci != nil {
				s.srv.SetPRWatcher(ci)
			}
			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/channels", nil))
			require.Equal(s.T(), http.StatusOK, w.Code)
			var resp []channelResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			got := map[string]events.PRFlags{}
			for _, ch := range resp {
				if fl := (events.PRFlags{Attention: ch.PRAttention, UnresolvedThreads: ch.UnresolvedThreads}); fl != (events.PRFlags{}) {
					got[ch.ChannelID] = fl
				}
			}
			require.Equal(s.T(), tt.want, got)
		})
	}
}
