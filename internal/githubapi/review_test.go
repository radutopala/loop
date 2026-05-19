package githubapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// dispatchRunner is a flexible fake that routes by the leading subcommand
// keyword (pr view, repo view, api). Each route can return canned bytes
// or an error.
type dispatchRunner struct {
	calls []fakeCall

	tokenOut, tokenErr       any // []byte or error
	prViewOut, prViewErr     any
	repoViewOut, repoViewErr any
	apiOut, apiErr           any
}

func (d *dispatchRunner) Run(_ context.Context, workdir string, env []string, args ...string) ([]byte, error) {
	d.calls = append(d.calls, fakeCall{workdir: workdir, env: env, args: args})
	join := strings.Join(args, " ")
	switch {
	case len(args) >= 2 && args[0] == "auth" && args[1] == "token":
		return asBytes(d.tokenOut), asErr(d.tokenErr)
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		return asBytes(d.prViewOut), asErr(d.prViewErr)
	case len(args) >= 2 && args[0] == "repo" && args[1] == "view":
		return asBytes(d.repoViewOut), asErr(d.repoViewErr)
	case len(args) >= 1 && args[0] == "api":
		return asBytes(d.apiOut), asErr(d.apiErr)
	default:
		return nil, errors.New("unexpected gh args: " + join)
	}
}

func asBytes(v any) []byte {
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		return b
	}
	return nil
}
func asErr(v any) error {
	if v == nil {
		return nil
	}
	if e, ok := v.(error); ok {
		return e
	}
	return nil
}

type ReviewAPISuite struct {
	suite.Suite
}

func TestReviewAPISuite(t *testing.T) {
	suite.Run(t, new(ReviewAPISuite))
}

// ── FetchPRByNumber ──────────────────────────────────────────────────────

func (s *ReviewAPISuite) TestFetchPRByNumberInvalidArgs() {
	c := NewClientWithRunner(&dispatchRunner{})
	pr, err := c.FetchPRByNumber(context.Background(), "", "", 1)
	require.NoError(s.T(), err)
	require.Nil(s.T(), pr)

	pr, err = c.FetchPRByNumber(context.Background(), "/tmp", "", 0)
	require.NoError(s.T(), err)
	require.Nil(s.T(), pr)
}

func (s *ReviewAPISuite) TestFetchPRByNumberHappyPath() {
	r := &dispatchRunner{prViewOut: []byte(`{
		"number": 42,
		"url": "https://github.com/x/y/pull/42",
		"baseRefName": "main",
		"headRefName": "feat",
		"state": "OPEN",
		"title": "T",
		"isDraft": true
	}`)}
	c := NewClientWithRunner(r)
	pr, err := c.FetchPRByNumber(context.Background(), "/tmp", "", 42)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), pr)
	require.Equal(s.T(), 42, pr.Number)
	require.Equal(s.T(), "main", pr.BaseRef)
	require.True(s.T(), pr.IsDraft)
}

func (s *ReviewAPISuite) TestFetchPRByNumberZeroNumberReturnsNil() {
	r := &dispatchRunner{prViewOut: []byte(`{"number": 0}`)}
	c := NewClientWithRunner(r)
	pr, err := c.FetchPRByNumber(context.Background(), "/tmp", "", 1)
	require.NoError(s.T(), err)
	require.Nil(s.T(), pr)
}

func (s *ReviewAPISuite) TestFetchPRByNumberGhNotInstalled() {
	r := &dispatchRunner{prViewErr: ErrGhNotInstalled}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRByNumber(context.Background(), "/tmp", "", 1)
	require.ErrorIs(s.T(), err, ErrGhNotInstalled)
}

func (s *ReviewAPISuite) TestFetchPRByNumberGenericError() {
	r := &dispatchRunner{prViewErr: errors.New("boom")}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRByNumber(context.Background(), "/tmp", "", 1)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "boom")
}

func (s *ReviewAPISuite) TestFetchPRByNumberMalformedJSON() {
	r := &dispatchRunner{prViewOut: []byte(`{nope`)}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRByNumber(context.Background(), "/tmp", "", 1)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing gh pr view")
}

func (s *ReviewAPISuite) TestFetchPRByNumberTokenLookupFailure() {
	r := &dispatchRunner{tokenErr: errors.New("nope")}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRByNumber(context.Background(), "/tmp", "u", 1)
	require.Error(s.T(), err)
}

// ── FetchRepoSlug ────────────────────────────────────────────────────────

func (s *ReviewAPISuite) TestFetchRepoSlugInvalidWorkdir() {
	c := NewClientWithRunner(&dispatchRunner{})
	_, err := c.FetchRepoSlug(context.Background(), "", "")
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestFetchRepoSlugHappyPath() {
	r := &dispatchRunner{repoViewOut: []byte(`{"owner":{"login":"acme"},"name":"widgets"}`)}
	c := NewClientWithRunner(r)
	slug, err := c.FetchRepoSlug(context.Background(), "/tmp", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "acme", slug.Owner)
	require.Equal(s.T(), "widgets", slug.Name)
}

func (s *ReviewAPISuite) TestFetchRepoSlugEmptyFields() {
	r := &dispatchRunner{repoViewOut: []byte(`{"owner":{"login":""},"name":""}`)}
	c := NewClientWithRunner(r)
	_, err := c.FetchRepoSlug(context.Background(), "/tmp", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "empty owner")
}

func (s *ReviewAPISuite) TestFetchRepoSlugMalformedJSON() {
	r := &dispatchRunner{repoViewOut: []byte(`{nope`)}
	c := NewClientWithRunner(r)
	_, err := c.FetchRepoSlug(context.Background(), "/tmp", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing gh repo view")
}

func (s *ReviewAPISuite) TestFetchRepoSlugRunError() {
	r := &dispatchRunner{repoViewErr: errors.New("boom")}
	c := NewClientWithRunner(r)
	_, err := c.FetchRepoSlug(context.Background(), "/tmp", "")
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestFetchRepoSlugTokenFailure() {
	r := &dispatchRunner{tokenErr: errors.New("no token")}
	c := NewClientWithRunner(r)
	_, err := c.FetchRepoSlug(context.Background(), "/tmp", "u")
	require.Error(s.T(), err)
}

// ── PostPRComment ────────────────────────────────────────────────────────

func (s *ReviewAPISuite) TestPostPRCommentInvalidArgs() {
	c := NewClientWithRunner(&dispatchRunner{})
	tests := []struct {
		name string
		fn   func() error
	}{
		{"empty workdir", func() error {
			return c.PostPRComment(context.Background(), "", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 1, "b")
		}},
		{"zero prNum", func() error {
			return c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 0, "sha", "p", "RIGHT", 1, "b")
		}},
		{"empty commitID", func() error {
			return c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "", "p", "RIGHT", 1, "b")
		}},
		{"empty path", func() error {
			return c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "", "RIGHT", 1, "b")
		}},
		{"zero line", func() error {
			return c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 0, "b")
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Error(s.T(), tt.fn())
		})
	}
}

func (s *ReviewAPISuite) TestPostPRCommentHappyPath() {
	r := &dispatchRunner{apiOut: []byte(`{"id":123}`)}
	c := NewClientWithRunner(r)
	err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "acme", Name: "widgets"}, 5, "deadbeef", "foo.go", "RIGHT", 10, "looks good")
	require.NoError(s.T(), err)
	require.Len(s.T(), r.calls, 1)
	args := r.calls[0].args
	require.Equal(s.T(), "api", args[0])
	require.Equal(s.T(), "repos/acme/widgets/pulls/5/comments", args[1])
	require.Contains(s.T(), args, "body=looks good")
	require.Contains(s.T(), args, "path=foo.go")
	require.Contains(s.T(), args, "line=10")
	require.Contains(s.T(), args, "commit_id=deadbeef")
	require.Contains(s.T(), args, "side=RIGHT")
}

func (s *ReviewAPISuite) TestPostPRCommentDefaultsSideToRight() {
	r := &dispatchRunner{apiOut: []byte(`{}`)}
	c := NewClientWithRunner(r)
	err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "", 1, "b")
	require.NoError(s.T(), err)
	require.Contains(s.T(), r.calls[0].args, "side=RIGHT")
}

func (s *ReviewAPISuite) TestPostPRCommentRunError() {
	r := &dispatchRunner{apiErr: errors.New("boom")}
	c := NewClientWithRunner(r)
	err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 1, "b")
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestPostPRCommentTokenFailure() {
	r := &dispatchRunner{tokenErr: errors.New("no token")}
	c := NewClientWithRunner(r)
	err := c.PostPRComment(context.Background(), "/tmp", "u", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 1, "b")
	require.Error(s.T(), err)
}

// ── FetchPRHeadSHA ───────────────────────────────────────────────────────

func (s *ReviewAPISuite) TestFetchPRHeadSHAInvalidArgs() {
	c := NewClientWithRunner(&dispatchRunner{})
	_, err := c.FetchPRHeadSHA(context.Background(), "", "", 1)
	require.Error(s.T(), err)
	_, err = c.FetchPRHeadSHA(context.Background(), "/tmp", "", 0)
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestFetchPRHeadSHAHappyPath() {
	r := &dispatchRunner{prViewOut: []byte(`{"headRefOid":"abc123"}`)}
	c := NewClientWithRunner(r)
	sha, err := c.FetchPRHeadSHA(context.Background(), "/tmp", "", 1)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "abc123", sha)
}

func (s *ReviewAPISuite) TestFetchPRHeadSHAEmpty() {
	r := &dispatchRunner{prViewOut: []byte(`{"headRefOid":""}`)}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRHeadSHA(context.Background(), "/tmp", "", 1)
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestFetchPRHeadSHAMalformed() {
	r := &dispatchRunner{prViewOut: []byte(`{nope`)}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRHeadSHA(context.Background(), "/tmp", "", 1)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing gh pr view")
}

func (s *ReviewAPISuite) TestFetchPRHeadSHARunError() {
	r := &dispatchRunner{prViewErr: errors.New("boom")}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRHeadSHA(context.Background(), "/tmp", "", 1)
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestFetchPRHeadSHATokenFailure() {
	r := &dispatchRunner{tokenErr: errors.New("no token")}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRHeadSHA(context.Background(), "/tmp", "u", 1)
	require.Error(s.T(), err)
}
