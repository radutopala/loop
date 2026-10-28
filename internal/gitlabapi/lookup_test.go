package gitlabapi

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/githubapi"
)

// fakeRunner records calls and replays a canned response.
type fakeRunner struct {
	calls []fakeCall
	out   []byte
	err   error
}

type fakeCall struct {
	workdir string
	env     []string
	args    []string
}

func (f *fakeRunner) Run(_ context.Context, workdir string, env []string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, fakeCall{workdir: workdir, env: env, args: args})
	return f.out, f.err
}

type GitlabAPISuite struct {
	suite.Suite
}

func TestGitlabAPISuite(t *testing.T) {
	suite.Run(t, new(GitlabAPISuite))
}

func noFile(string) ([]byte, error) { return nil, os.ErrNotExist }

func (s *GitlabAPISuite) TestLookupMR() {
	mrJSON := `[{"iid":4,"web_url":"https://gitlab.example.com/g/r/-/merge_requests/4","target_branch":"main","source_branch":"feat","state":"opened","title":"Fix","draft":true}]`
	tests := []struct {
		name      string
		workdir   string
		branch    string
		configDir string
		out       string
		err       error
		want      *githubapi.PRInfo
		wantErr   string
		wantEnv   []string
		noCall    bool
	}{
		{name: "empty workdir", branch: "feat", noCall: true},
		{name: "empty branch", workdir: "/w", noCall: true},
		{
			name: "found", workdir: "/w", branch: "feat", out: mrJSON,
			want: &githubapi.PRInfo{Number: 4, URL: "https://gitlab.example.com/g/r/-/merge_requests/4", BaseRef: "main", HeadRef: "feat", State: "opened", Title: "Fix", IsDraft: true, Kind: KindMR},
		},
		{name: "config dir passed as env", workdir: "/w", branch: "feat", configDir: "/cfg", out: `[]`, wantEnv: []string{"GLAB_CONFIG_DIR=/cfg"}},
		{name: "no MR", workdir: "/w", branch: "feat", out: `[]`},
		{name: "zero iid", workdir: "/w", branch: "feat", out: `[{"iid":0}]`},
		{name: "runner error", workdir: "/w", branch: "feat", err: errors.New("glab mr list: 401"), wantErr: "401"},
		{name: "malformed JSON", workdir: "/w", branch: "feat", out: `{`, wantErr: "parsing glab mr list output"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			r := &fakeRunner{out: []byte(tt.out), err: tt.err}
			c := NewClientWithRunner(r, "", noFile)
			got, err := c.LookupMR(context.Background(), tt.workdir, tt.configDir, tt.branch)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
			if tt.noCall {
				require.Empty(s.T(), r.calls)
				return
			}
			require.Len(s.T(), r.calls, 1)
			require.Equal(s.T(), tt.workdir, r.calls[0].workdir)
			require.Equal(s.T(), tt.wantEnv, r.calls[0].env)
			require.Equal(s.T(), []string{
				"mr", "list", "--source-branch", tt.branch, "--all",
				"--order", "updated_at", "--sort", "desc", "--per-page", "1", "--output", "json",
			}, r.calls[0].args)
		})
	}
}

func (s *GitlabAPISuite) TestListOpenMRs() {
	tests := []struct {
		name      string
		workdir   string
		configDir string
		out       string
		err       error
		want      []githubapi.PRInfo
		wantErr   string
		wantEnv   []string
	}{
		{name: "empty workdir", wantErr: "workdir is required"},
		{
			name: "listed", workdir: "/w", configDir: "/cfg", wantEnv: []string{"GLAB_CONFIG_DIR=/cfg"},
			out: `[{"iid":4,"web_url":"u4","target_branch":"main","source_branch":"feat","state":"opened","title":"Fix","draft":true},{"iid":5,"source_branch":"b"}]`,
			want: []githubapi.PRInfo{
				{Number: 4, URL: "u4", BaseRef: "main", HeadRef: "feat", State: "opened", Title: "Fix", IsDraft: true, Kind: KindMR},
				{Number: 5, HeadRef: "b", Kind: KindMR},
			},
		},
		{name: "none", workdir: "/w", out: `[]`, want: []githubapi.PRInfo{}},
		{name: "runner error", workdir: "/w", err: errors.New("glab mr list: 401"), wantErr: "401"},
		{name: "malformed JSON", workdir: "/w", out: `{`, wantErr: "parsing glab mr list output"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			r := &fakeRunner{out: []byte(tt.out), err: tt.err}
			c := NewClientWithRunner(r, "", noFile)
			got, err := c.ListOpenMRs(context.Background(), tt.workdir, tt.configDir)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
			require.Len(s.T(), r.calls, 1)
			require.Equal(s.T(), tt.wantEnv, r.calls[0].env)
			require.Equal(s.T(), []string{"mr", "list", "--per-page", "100", "--output", "json"}, r.calls[0].args)
		})
	}
}

func (s *GitlabAPISuite) TestCheckoutMR() {
	tests := []struct {
		name    string
		workdir string
		iid     int
		err     error
		wantErr string
	}{
		{name: "checked out", workdir: "/w", iid: 7},
		{name: "no workdir", iid: 7, wantErr: "required"},
		{name: "no iid", workdir: "/w", wantErr: "required"},
		{name: "runner error", workdir: "/w", iid: 7, err: errors.New("already checked out"), wantErr: "already checked out"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			r := &fakeRunner{err: tt.err}
			c := NewClientWithRunner(r, "", noFile)
			err := c.CheckoutMR(context.Background(), tt.workdir, "/cfg", tt.iid)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), []fakeCall{{workdir: "/w", env: []string{"GLAB_CONFIG_DIR=/cfg"}, args: []string{"mr", "checkout", "7"}}}, r.calls)
		})
	}
}

func (s *GitlabAPISuite) TestDefaultConfigDir() {
	tests := []struct {
		name    string
		env     string
		base    string
		baseErr error
		want    string
	}{
		{name: "GLAB_CONFIG_DIR wins", env: "/custom", base: "/home/u/.config", want: "/custom"},
		{name: "user config dir", base: "/home/u/Library/Application Support", want: "/home/u/Library/Application Support/glab-cli"},
		{name: "unresolvable", baseErr: errors.New("no home"), want: ""},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			getenv := func(k string) string {
				require.Equal(s.T(), "GLAB_CONFIG_DIR", k)
				return tt.env
			}
			got := DefaultConfigDir(getenv, func() (string, error) { return tt.base, tt.baseErr })
			require.Equal(s.T(), tt.want, got)
		})
	}
}

func (s *GitlabAPISuite) TestIsGitLabHost() {
	cfg := "hosts:\n    gitlab.com:\n        user: a\n    lab.example.org:\n        user: b\n"
	tests := []struct {
		name       string
		host       string
		configDir  string
		defaultDir string
		files      map[string]string
		want       bool
	}{
		{name: "empty host", host: "", want: false},
		{name: "name says gitlab", host: "GitLab.example.com", want: true},
		{name: "configured in the default dir", host: "lab.example.org", defaultDir: "/d", files: map[string]string{"/d/config.yml": cfg}, want: true},
		{name: "configured in the given dir", host: "lab.example.org", configDir: "/c", defaultDir: "/d", files: map[string]string{"/c/config.yml": cfg}, want: true},
		{name: "not configured", host: "github.com", defaultDir: "/d", files: map[string]string{"/d/config.yml": cfg}, want: false},
		{name: "no config dir", host: "lab.example.org", want: false},
		{name: "unreadable config", host: "lab.example.org", defaultDir: "/d", want: false},
		{name: "malformed config", host: "lab.example.org", defaultDir: "/d", files: map[string]string{"/d/config.yml": "hosts: ["}, want: false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			read := func(p string) ([]byte, error) {
				if b, ok := tt.files[p]; ok {
					return []byte(b), nil
				}
				return nil, os.ErrNotExist
			}
			c := NewClientWithRunner(&fakeRunner{}, tt.defaultDir, read)
			require.Equal(s.T(), tt.want, c.IsGitLabHost(tt.host, tt.configDir))
		})
	}
}

func (s *GitlabAPISuite) TestExecRunnerBinaryNotFoundMapsToSentinel() {
	_, err := NewExecRunner("loop-no-such-binary-xyzzy-glab").Run(context.Background(), s.T().TempDir(), nil)
	require.ErrorIs(s.T(), err, ErrGlabNotInstalled)
}

func (s *GitlabAPISuite) TestExecRunnerSetsEnv() {
	// Proxy variables first, the update check off, then the caller's env.
	r := execRunner{bin: "/bin/sh", proxyEnv: func() []string {
		return []string{"HTTPS_PROXY=http://proxy:3128", "FOO=proxy"}
	}}
	out, err := r.Run(context.Background(), s.T().TempDir(), []string{"FOO=bar"},
		"-c", `echo "$HTTPS_PROXY $GLAB_CHECK_UPDATE $FOO"`)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://proxy:3128 false bar\n", string(out))
}

func (s *GitlabAPISuite) TestExecRunnerErrors() {
	falsePath, err := exec.LookPath("false")
	require.NoError(s.T(), err)
	tests := []struct {
		name string
		bin  string
		args []string
		want string
	}{
		{name: "stderr surfaces", bin: "/bin/sh", args: []string{"-c", "echo boom >&2; exit 2"}, want: "glab -c echo boom >&2; exit 2: boom"},
		{name: "exit status without stderr", bin: falsePath, args: []string{"x"}, want: "glab x: exit status 1"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := NewExecRunner(tt.bin).Run(context.Background(), s.T().TempDir(), nil, tt.args...)
			require.EqualError(s.T(), err, tt.want)
		})
	}
}

func (s *GitlabAPISuite) TestNewClient() {
	proxyEnv := func() []string { return []string{"HTTPS_PROXY=http://proxy:3128"} }
	c := NewClient(proxyEnv)
	r, ok := c.runner.(execRunner)
	require.True(s.T(), ok)
	require.Equal(s.T(), "glab", r.bin)
	require.Equal(s.T(), proxyEnv(), r.proxyEnv())
	require.Equal(s.T(), DefaultConfigDir(os.Getenv, os.UserConfigDir), c.defaultConfigDir)

	// readFile is the real one.
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "config.yml"), []byte("hosts:\n  lab.example.org: {}\n"), 0o600))
	require.True(s.T(), c.IsGitLabHost("lab.example.org", dir))
}
