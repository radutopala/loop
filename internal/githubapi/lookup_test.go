package githubapi

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// fakeRunner records calls and replays canned responses keyed on the first
// two args ("pr view" vs "auth token"). Tests rely on positional matching
// since gh's CLI is positional and we only invoke two distinct subcommands.
type fakeRunner struct {
	calls    []fakeCall
	prOut    []byte
	prErr    error
	tokenOut []byte
	tokenErr error
}

type fakeCall struct {
	workdir string
	env     []string
	args    []string
}

func (f *fakeRunner) Run(_ context.Context, workdir string, env []string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, fakeCall{workdir: workdir, env: env, args: args})
	if len(args) >= 2 && args[0] == "auth" && args[1] == "token" {
		return f.tokenOut, f.tokenErr
	}
	return f.prOut, f.prErr
}

type GithubAPISuite struct {
	suite.Suite
}

func TestGithubAPISuite(t *testing.T) {
	suite.Run(t, new(GithubAPISuite))
}

func (s *GithubAPISuite) TestLookupPRReturnsParsedPR() {
	r := &fakeRunner{
		prOut: []byte(`{
			"number": 42,
			"url": "https://github.com/owner/repo/pull/42",
			"baseRefName": "main",
			"headRefName": "feature-x",
			"state": "OPEN",
			"title": "Add feature X",
			"isDraft": false
		}`),
	}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "", "feature-x")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), pr)
	require.Equal(s.T(), 42, pr.Number)
	require.Equal(s.T(), "main", pr.BaseRef)
	require.Equal(s.T(), "feature-x", pr.HeadRef)
	require.Equal(s.T(), "OPEN", pr.State)
	require.Equal(s.T(), "Add feature X", pr.Title)
	require.False(s.T(), pr.IsDraft)
	require.Len(s.T(), r.calls, 1)
	require.Equal(s.T(), "/tmp/repo", r.calls[0].workdir)
	require.Equal(s.T(), []string{"pr", "view", "feature-x", "--json", "number,url,baseRefName,headRefName,state,title,isDraft"}, r.calls[0].args)
	require.Nil(s.T(), r.calls[0].env)
}

func (s *GithubAPISuite) TestLookupPREmptyArgsReturnsNil() {
	c := NewClientWithRunner(&fakeRunner{})
	pr, err := c.LookupPR(context.Background(), "", "", "branch")
	require.NoError(s.T(), err)
	require.Nil(s.T(), pr)

	pr, err = c.LookupPR(context.Background(), "/tmp", "", "")
	require.NoError(s.T(), err)
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRNoPRFound() {
	r := &fakeRunner{prErr: errors.New("gh pr view: no pull requests found for branch \"feature-x\"")}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "", "feature-x")
	require.NoError(s.T(), err)
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRNoOpenPRsVariant() {
	r := &fakeRunner{prErr: errors.New("no open pull requests in OWNER/REPO match branch")}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "", "feature-x")
	require.NoError(s.T(), err)
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRGhNotInstalled() {
	r := &fakeRunner{prErr: ErrGhNotInstalled}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "", "feature-x")
	require.ErrorIs(s.T(), err, ErrGhNotInstalled)
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRGenericError() {
	r := &fakeRunner{prErr: errors.New("gh pr view: not a git repository")}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "", "feature-x")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not a git repository")
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRMalformedJSON() {
	r := &fakeRunner{prOut: []byte(`{not json`)}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "", "feature-x")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing gh pr view output")
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRZeroNumberTreatedAsNoPR() {
	r := &fakeRunner{prOut: []byte(`{"number": 0}`)}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "", "feature-x")
	require.NoError(s.T(), err)
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRWithGhUserUsesTokenEnv() {
	r := &fakeRunner{
		tokenOut: []byte("ghp_abc123\n"),
		prOut: []byte(`{
			"number": 7,
			"url": "https://github.com/owner/repo/pull/7",
			"baseRefName": "main",
			"headRefName": "feat",
			"state": "OPEN"
		}`),
	}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "radutopala", "feat")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), pr)
	require.Equal(s.T(), 7, pr.Number)

	require.Len(s.T(), r.calls, 2)
	require.Equal(s.T(), []string{"auth", "token", "--user", "radutopala"}, r.calls[0].args)
	require.Equal(s.T(), []string{"GH_TOKEN=ghp_abc123"}, r.calls[1].env)
}

func (s *GithubAPISuite) TestLookupPRTokenLookupFailure() {
	r := &fakeRunner{tokenErr: errors.New("not logged in")}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "radutopala", "feat")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading gh token")
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRTokenGhNotInstalled() {
	r := &fakeRunner{tokenErr: ErrGhNotInstalled}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "radutopala", "feat")
	require.ErrorIs(s.T(), err, ErrGhNotInstalled)
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestLookupPRTokenEmpty() {
	r := &fakeRunner{tokenOut: []byte("   \n")}
	c := NewClientWithRunner(r)
	pr, err := c.LookupPR(context.Background(), "/tmp/repo", "radutopala", "feat")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "empty token")
	require.Nil(s.T(), pr)
}

func (s *GithubAPISuite) TestExecRunnerBinaryNotFoundMapsToSentinel() {
	r := NewExecRunner("loop-no-such-binary-xyzzy-blah")
	_, err := r.Run(context.Background(), s.T().TempDir(), nil)
	require.ErrorIs(s.T(), err, ErrGhNotInstalled)
}

func (s *GithubAPISuite) TestExecRunnerSuccess() {
	// `true` exits 0 with no output — exercises the happy path of
	// execRunner.Run end-to-end. Resolve via PATH so macOS (/usr/bin/true)
	// and Linux (/bin/true) both work.
	truePath, err := exec.LookPath("true")
	if err != nil {
		s.T().Skip("no `true` binary on PATH")
	}
	r := NewExecRunner(truePath)
	out, err := r.Run(context.Background(), s.T().TempDir(), []string{"FOO=bar"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), out)
}

func (s *GithubAPISuite) TestExecRunnerBubblesStderrOnFailure() {
	// `false` exits 1 with empty stderr; the error message should fall
	// back to err.Error() rather than be empty.
	falsePath, err := exec.LookPath("false")
	if err != nil {
		s.T().Skip("no `false` binary on PATH")
	}
	r := NewExecRunner(falsePath)
	_, err = r.Run(context.Background(), s.T().TempDir(), nil, "ignored-arg")
	require.Error(s.T(), err)
	require.True(s.T(),
		strings.Contains(err.Error(), "exit status") || strings.Contains(err.Error(), "ignored-arg"),
		"expected exit status in error, got: %s", err.Error(),
	)
}

func (s *GithubAPISuite) TestExecRunnerCapturesStderr() {
	// /bin/sh -c 'echo boom >&2; exit 2' writes to stderr and exits non-zero.
	// The runner should embed the stderr text in the returned error.
	r := NewExecRunner("/bin/sh")
	_, err := r.Run(context.Background(), s.T().TempDir(), nil, "-c", "echo boom >&2; exit 2")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "boom")
}

func (s *GithubAPISuite) TestNewClientReal() {
	c := NewClient()
	require.NotNil(s.T(), c)
	require.NotNil(s.T(), c.runner)
}
