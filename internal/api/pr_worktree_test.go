package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/githubapi"
)

func (s *ServerSuite) TestChannelOpenPRs() {
	tests := []struct {
		name     string
		origin   string
		noDir    bool
		setup    func(dir string, gh *mockGitHubLookup, gl *mockGitLabLookup)
		wantCode int
		wantBody string
	}{
		{
			name: "GitHub PRs",
			setup: func(dir string, gh *mockGitHubLookup, gl *mockGitLabLookup) {
				gl.On("IsGitLabHost", "github.com", "").Return(false)
				gh.On("ListOpenPRs", mock.Anything, dir, "").
					Return([]githubapi.PRInfo{{Number: 12, Title: "Fix it", HeadRef: "feat/x", BaseRef: "main", Kind: "pr"}}, nil)
			},
			wantCode: http.StatusOK,
			wantBody: `"number":12`,
		},
		{
			name:   "GitLab MRs",
			origin: "https://gitlab.com/group/repo.git",
			setup: func(dir string, gh *mockGitHubLookup, gl *mockGitLabLookup) {
				gl.On("IsGitLabHost", "gitlab.com", "").Return(true)
				gl.On("ListOpenMRs", mock.Anything, dir, "").
					Return([]githubapi.PRInfo{{Number: 7, Kind: "mr"}}, nil)
			},
			wantCode: http.StatusOK,
			wantBody: `"kind":"mr"`,
		},
		{
			name:     "no origin",
			origin:   "-",
			setup:    func(string, *mockGitHubLookup, *mockGitLabLookup) {},
			wantCode: http.StatusOK,
			wantBody: `{"prs":[]}`,
		},
		{
			name:     "no dir",
			noDir:    true,
			setup:    func(string, *mockGitHubLookup, *mockGitLabLookup) {},
			wantCode: http.StatusOK,
			wantBody: `{"prs":[]}`,
		},
		{
			name: "list fails",
			setup: func(dir string, gh *mockGitHubLookup, gl *mockGitLabLookup) {
				gl.On("IsGitLabHost", "github.com", "").Return(false)
				gh.On("ListOpenPRs", mock.Anything, dir, "").Return(nil, errors.New("gh: rate limited"))
			},
			wantCode: http.StatusBadGateway,
			wantBody: "rate limited",
		},
	}
	for i, tt := range tests {
		s.Run(tt.name, func() {
			id := fmt.Sprintf("ch-pr%d", i)
			dir := gitInitRepoWithBranch(s.T(), "main")
			switch tt.origin {
			case "":
			case "-":
				setOrigin(s.T(), dir, "")
			default:
				setOrigin(s.T(), dir, tt.origin)
			}
			gh, gl := new(mockGitHubLookup), new(mockGitLabLookup)
			tt.setup(dir, gh, gl)
			s.srv.prLookup.client = gh
			s.srv.prLookup.gitlab = gl
			ch := &db.Channel{ChannelID: id, DirPath: dir}
			if tt.noDir {
				ch.DirPath = ""
			}
			s.store.On("GetChannel", mock.Anything, id).Return(ch, nil)

			rec := s.testRequest("GET", "/api/channels/"+id+"/prs", "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			gh.AssertExpectations(s.T())
			gl.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestChannelOpenPRsChannelErrors() {
	tests := []struct {
		name     string
		ch       *db.Channel
		err      error
		wantCode int
	}{
		{"not found", nil, nil, http.StatusNotFound},
		{"store error", nil, errors.New("db down"), http.StatusInternalServerError},
	}
	for i, tt := range tests {
		s.Run(tt.name, func() {
			id := fmt.Sprintf("ch-pr%d", i)
			s.store.On("GetChannel", mock.Anything, id).Return(tt.ch, tt.err)
			rec := s.testRequest("GET", "/api/channels/"+id+"/prs", "")
			require.Equal(s.T(), tt.wantCode, rec.Code)
		})
	}
}

func (s *ServerSuite) TestChannelOpenPRsNotConfigured() {
	srv := NewServer(nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/prs", srv.handleChannelOpenPRs)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/ch-x/prs", nil))
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// checkoutBranch is a CheckoutPR/CheckoutMR mock action that puts the
// worktree on branch, as gh and glab do.
func checkoutBranch(s *ServerSuite, branch string) func(mock.Arguments) {
	return func(args mock.Arguments) {
		cmd := exec.Command("git", "checkout", "-b", branch)
		cmd.Dir = args.String(1)
		require.NoError(s.T(), cmd.Run())
	}
}

func (s *ServerSuite) TestCreateWorktreeFromPR() {
	tests := []struct {
		name       string
		origin     string
		body       string
		setup      func(gh *mockGitHubLookup, gl *mockGitLabLookup, wt string)
		wantWT     string
		wantThread string
	}{
		{
			name: "GitHub PR, default name",
			body: `{"channel_id":"CH","branch":"main","pr":12}`,
			setup: func(gh *mockGitHubLookup, gl *mockGitLabLookup, wt string) {
				gl.On("IsGitLabHost", "github.com", "").Return(false)
				gh.On("CheckoutPR", mock.Anything, wt, "", 12).Return(nil).Run(checkoutBranch(s, "feat/x"))
			},
			wantWT:     "pr-12",
			wantThread: "pr-12 (feat/x)",
		},
		{
			name:   "GitLab MR, given name",
			origin: "https://gitlab.com/group/repo.git",
			body:   `{"channel_id":"CH","pr":7,"name":"review"}`,
			setup: func(gh *mockGitHubLookup, gl *mockGitLabLookup, wt string) {
				gl.On("IsGitLabHost", "gitlab.com", "").Return(true)
				gl.On("CheckoutMR", mock.Anything, wt, "", 7).Return(nil).Run(checkoutBranch(s, "fix-y"))
			},
			wantWT:     "review",
			wantThread: "review (fix-y)",
		},
	}
	for i, tt := range tests {
		s.Run(tt.name, func() {
			id := fmt.Sprintf("ch-pr%d", i)
			dir := gitInitRepoWithBranch(s.T(), "main")
			if tt.origin != "" {
				setOrigin(s.T(), dir, tt.origin)
			}
			wt := filepath.Join(dir, ".worktrees", tt.wantWT)
			gh, gl := new(mockGitHubLookup), new(mockGitLabLookup)
			tt.setup(gh, gl, wt)
			s.srv.prLookup.client = gh
			s.srv.prLookup.gitlab = gl
			s.srv.sys = s.sys
			s.store.On("GetChannel", mock.Anything, id).Return(&db.Channel{ChannelID: id, DirPath: dir}, nil)
			s.threads.On("CreateThread", mock.Anything, id, tt.wantThread, "", "").Return(id+"-wt", nil)
			s.store.On("GetChannel", mock.Anything, id+"-wt").
				Return(&db.Channel{ChannelID: id + "-wt", DirPath: dir, ParentID: id}, nil)
			s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)

			rec := s.testRequest("POST", "/api/worktrees", strings.ReplaceAll(tt.body, "CH", id))
			require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
			var resp createWorktreeResponse
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Equal(s.T(), wt, resp.WorktreePath)
			require.DirExists(s.T(), wt)
			gh.AssertExpectations(s.T())
			gl.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestCreateWorktreeFromPRFails() {
	tests := []struct {
		name     string
		origin   string
		setup    func(gh *mockGitHubLookup, gl *mockGitLabLookup)
		wantBody string
	}{
		{
			name: "checkout fails",
			setup: func(gh *mockGitHubLookup, gl *mockGitLabLookup) {
				gl.On("IsGitLabHost", "github.com", "").Return(false)
				gh.On("CheckoutPR", mock.Anything, mock.Anything, "", 12).Return(errors.New("no such PR"))
			},
			wantBody: "no such PR",
		},
		{
			name:     "no origin",
			origin:   "-",
			setup:    func(*mockGitHubLookup, *mockGitLabLookup) {},
			wantBody: "no GitHub or GitLab origin",
		},
	}
	for i, tt := range tests {
		s.Run(tt.name, func() {
			id := fmt.Sprintf("ch-pr%d", i)
			dir := gitInitRepoWithBranch(s.T(), "main")
			if tt.origin == "-" {
				setOrigin(s.T(), dir, "")
			}
			gh, gl := new(mockGitHubLookup), new(mockGitLabLookup)
			tt.setup(gh, gl)
			s.srv.prLookup.client = gh
			s.srv.prLookup.gitlab = gl
			s.srv.sys = s.sys
			s.store.On("GetChannel", mock.Anything, id).Return(&db.Channel{ChannelID: id, DirPath: dir}, nil)

			rec := s.testRequest("POST", "/api/worktrees", `{"channel_id":"`+id+`","branch":"main","pr":12}`)
			require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			require.NoDirExists(s.T(), filepath.Join(dir, ".worktrees", "pr-12"))
		})
	}
}
