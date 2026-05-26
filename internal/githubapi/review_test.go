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
	prListOut, prListErr     any
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
	case len(args) >= 2 && args[0] == "pr" && args[1] == "list":
		return asBytes(d.prListOut), asErr(d.prListErr)
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
			_, err := c.PostPRComment(context.Background(), "", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 1, "b")
			return err
		}},
		{"zero prNum", func() error {
			_, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 0, "sha", "p", "RIGHT", 1, "b")
			return err
		}},
		{"empty commitID", func() error {
			_, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "", "p", "RIGHT", 1, "b")
			return err
		}},
		{"empty path", func() error {
			_, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "", "RIGHT", 1, "b")
			return err
		}},
		{"zero line", func() error {
			_, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 0, "b")
			return err
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
	id, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "acme", Name: "widgets"}, 5, "deadbeef", "foo.go", "RIGHT", 10, "looks good")
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(123), id)
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
	id, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "", 1, "b")
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(0), id)
	require.Contains(s.T(), r.calls[0].args, "side=RIGHT")
}

func (s *ReviewAPISuite) TestPostPRCommentUnparseableBodySurfacesError() {
	// gh returned exit 0 but a non-JSON body — the comment is on GitHub but
	// loop has no id to record. Surface the decode error so callers see
	// the integration drift instead of silently logging a phantom comment.
	r := &dispatchRunner{apiOut: []byte(`not-json`)}
	c := NewClientWithRunner(r)
	id, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 1, "b")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding gh api response")
	require.Equal(s.T(), int64(0), id)
}

func (s *ReviewAPISuite) TestPostPRCommentRunError() {
	r := &dispatchRunner{apiErr: errors.New("boom")}
	c := NewClientWithRunner(r)
	_, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 1, "b")
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestPostPRCommentTokenFailure() {
	r := &dispatchRunner{tokenErr: errors.New("no token")}
	c := NewClientWithRunner(r)
	_, err := c.PostPRComment(context.Background(), "/tmp", "u", RepoSlug{Owner: "o", Name: "n"}, 1, "sha", "p", "RIGHT", 1, "b")
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestPostPRCommentFallsBackOn422() {
	r := &endpointRunner{
		responses: map[string]apiResponse{
			"repos/acme/widgets/pulls/5/comments":  {err: errors.New("gh api ...: HTTP 422: Validation Failed")},
			"repos/acme/widgets/issues/5/comments": {out: []byte(``)},
		},
	}
	c := NewClientWithRunner(r)
	id, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "acme", Name: "widgets"}, 5, "deadbeef", "engine.go", "RIGHT", 526, "review body")
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(0), id)
	require.Len(s.T(), r.calls, 2)
	require.Equal(s.T(), "repos/acme/widgets/pulls/5/comments", r.calls[0].args[1])
	require.Equal(s.T(), "repos/acme/widgets/issues/5/comments", r.calls[1].args[1])
	require.Contains(s.T(), r.calls[1].args, "body=**engine.go:L526** _(posted as PR conversation — line not in diff)_\n\nreview body")
}

func (s *ReviewAPISuite) TestPostPRCommentFallback422LeftSide() {
	r := &endpointRunner{
		responses: map[string]apiResponse{
			"repos/o/n/pulls/3/comments":  {err: errors.New("gh: Validation Failed (HTTP 422)")},
			"repos/o/n/issues/3/comments": {out: []byte(``)},
		},
	}
	c := NewClientWithRunner(r)
	_, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 3, "sha", "f.go", "LEFT", 7, "removed-line note")
	require.NoError(s.T(), err)
	require.Contains(s.T(), r.calls[1].args, "body=**f.go:L7 (deleted)** _(posted as PR conversation — line not in diff)_\n\nremoved-line note")
}

func (s *ReviewAPISuite) TestPostPRCommentFallback422AlsoFails() {
	r := &endpointRunner{
		responses: map[string]apiResponse{
			"repos/o/n/pulls/3/comments":  {err: errors.New("HTTP 422")},
			"repos/o/n/issues/3/comments": {err: errors.New("server down")},
		},
	}
	c := NewClientWithRunner(r)
	_, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 3, "sha", "f.go", "RIGHT", 1, "b")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "server down")
}

func (s *ReviewAPISuite) TestPostPRCommentNon422ErrorDoesNotFallBack() {
	r := &endpointRunner{
		responses: map[string]apiResponse{
			"repos/o/n/pulls/3/comments": {err: errors.New("HTTP 500: Internal Server Error")},
		},
	}
	c := NewClientWithRunner(r)
	_, err := c.PostPRComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 3, "sha", "f.go", "RIGHT", 1, "b")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "500")
	require.Len(s.T(), r.calls, 1, "should not retry on non-422 errors")
}

// endpointRunner routes `gh api <endpoint>` calls by endpoint and records
// each call. Unlike dispatchRunner it lets a single test stub two
// different api endpoints (used for the inline → conversation fallback
// test).
type endpointRunner struct {
	calls     []fakeCall
	responses map[string]apiResponse
}

type apiResponse struct {
	out []byte
	err error
}

func (r *endpointRunner) Run(_ context.Context, workdir string, env []string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, fakeCall{workdir: workdir, env: env, args: args})
	if len(args) >= 2 && args[0] == "api" {
		if resp, ok := r.responses[args[1]]; ok {
			return resp.out, resp.err
		}
		return nil, errors.New("unstubbed endpoint: " + args[1])
	}
	// Token lookup falls through with empty success so tokenEnv returns nil env.
	return nil, nil
}

// ── DeletePRReviewComment ───────────────────────────────────────────────

func (s *ReviewAPISuite) TestDeletePRReviewCommentInvalidArgs() {
	c := NewClientWithRunner(&dispatchRunner{})
	tests := []struct {
		name string
		fn   func() error
	}{
		{"empty workdir", func() error {
			return c.DeletePRReviewComment(context.Background(), "", "", RepoSlug{Owner: "o", Name: "n"}, 1)
		}},
		{"zero id", func() error {
			return c.DeletePRReviewComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 0)
		}},
		{"empty owner", func() error {
			return c.DeletePRReviewComment(context.Background(), "/tmp", "", RepoSlug{Owner: "", Name: "n"}, 1)
		}},
		{"empty name", func() error {
			return c.DeletePRReviewComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: ""}, 1)
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Error(s.T(), tt.fn())
		})
	}
}

func (s *ReviewAPISuite) TestDeletePRReviewCommentHappyPath() {
	r := &dispatchRunner{apiOut: []byte(``)}
	c := NewClientWithRunner(r)
	err := c.DeletePRReviewComment(context.Background(), "/tmp", "", RepoSlug{Owner: "acme", Name: "widgets"}, 42)
	require.NoError(s.T(), err)
	require.Len(s.T(), r.calls, 1)
	args := r.calls[0].args
	require.Equal(s.T(), []string{"api", "repos/acme/widgets/pulls/comments/42", "--method", "DELETE"}, args)
}

func (s *ReviewAPISuite) TestDeletePRReviewCommentTreats404AsSuccess() {
	r := &dispatchRunner{apiErr: errors.New("gh api ...: HTTP 404: Not Found")}
	c := NewClientWithRunner(r)
	require.NoError(s.T(), c.DeletePRReviewComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1))
}

func (s *ReviewAPISuite) TestDeletePRReviewCommentOtherErrorPropagates() {
	r := &dispatchRunner{apiErr: errors.New("boom")}
	c := NewClientWithRunner(r)
	require.Error(s.T(), c.DeletePRReviewComment(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1))
}

func (s *ReviewAPISuite) TestDeletePRReviewCommentTokenFailure() {
	r := &dispatchRunner{tokenErr: errors.New("no token")}
	c := NewClientWithRunner(r)
	require.Error(s.T(), c.DeletePRReviewComment(context.Background(), "/tmp", "u", RepoSlug{Owner: "o", Name: "n"}, 1))
}

// ── ListOpenPRs ──────────────────────────────────────────────────────────

func (s *ReviewAPISuite) TestListOpenPRsRequiresWorkdir() {
	c := NewClientWithRunner(&dispatchRunner{})
	_, err := c.ListOpenPRs(context.Background(), "", "")
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestListOpenPRsHappyPath() {
	r := &dispatchRunner{prListOut: []byte(`[
		{"number":1,"url":"u1","baseRefName":"main","headRefName":"f1","state":"OPEN","title":"T1","isDraft":false},
		{"number":2,"url":"u2","baseRefName":"main","headRefName":"f2","state":"OPEN","title":"T2","isDraft":true}
	]`)}
	c := NewClientWithRunner(r)
	prs, err := c.ListOpenPRs(context.Background(), "/tmp", "")
	require.NoError(s.T(), err)
	require.Len(s.T(), prs, 2)
	require.Equal(s.T(), 1, prs[0].Number)
	require.Equal(s.T(), "T2", prs[1].Title)
	require.True(s.T(), prs[1].IsDraft)
	require.Equal(s.T(), "main", prs[0].BaseRef)

	args := r.calls[len(r.calls)-1].args
	require.Equal(s.T(), "pr", args[0])
	require.Equal(s.T(), "list", args[1])
	require.Contains(s.T(), args, "--state")
	require.Contains(s.T(), args, "open")
}

func (s *ReviewAPISuite) TestListOpenPRsEmpty() {
	r := &dispatchRunner{prListOut: []byte(`[]`)}
	c := NewClientWithRunner(r)
	prs, err := c.ListOpenPRs(context.Background(), "/tmp", "")
	require.NoError(s.T(), err)
	require.Empty(s.T(), prs)
}

func (s *ReviewAPISuite) TestListOpenPRsGhNotInstalled() {
	r := &dispatchRunner{prListErr: ErrGhNotInstalled}
	c := NewClientWithRunner(r)
	_, err := c.ListOpenPRs(context.Background(), "/tmp", "")
	require.ErrorIs(s.T(), err, ErrGhNotInstalled)
}

func (s *ReviewAPISuite) TestListOpenPRsRunError() {
	r := &dispatchRunner{prListErr: errors.New("boom")}
	c := NewClientWithRunner(r)
	_, err := c.ListOpenPRs(context.Background(), "/tmp", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "boom")
}

func (s *ReviewAPISuite) TestListOpenPRsMalformedJSON() {
	r := &dispatchRunner{prListOut: []byte(`{nope`)}
	c := NewClientWithRunner(r)
	_, err := c.ListOpenPRs(context.Background(), "/tmp", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing gh pr list")
}

func (s *ReviewAPISuite) TestListOpenPRsTokenFailure() {
	r := &dispatchRunner{tokenErr: errors.New("no token")}
	c := NewClientWithRunner(r)
	_, err := c.ListOpenPRs(context.Background(), "/tmp", "u")
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

// ── FetchPRReviewComments ────────────────────────────────────────────────

func (s *ReviewAPISuite) TestFetchPRReviewCommentsInvalidArgs() {
	c := NewClientWithRunner(&dispatchRunner{})
	_, err := c.FetchPRReviewComments(context.Background(), "", "", RepoSlug{Owner: "o", Name: "n"}, 1)
	require.Error(s.T(), err)
	_, err = c.FetchPRReviewComments(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 0)
	require.Error(s.T(), err)
	_, err = c.FetchPRReviewComments(context.Background(), "/tmp", "", RepoSlug{Owner: "", Name: "n"}, 1)
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestFetchPRReviewCommentsHappyPath() {
	r := &dispatchRunner{apiOut: []byte(`{
		"data": {"repository": {"pullRequest": {"reviewThreads": {"nodes": [
			{"isResolved": false, "diffSide": "RIGHT", "comments": {"nodes": [
				{"databaseId":1,"path":"a.go","line":10,"originalLine":null,"body":"fix me","url":"u1","createdAt":"2026-01-01T00:00:00Z","outdated":false,"author":{"login":"alice"}}
			]}},
			{"isResolved": true, "diffSide": "", "comments": {"nodes": [
				{"databaseId":2,"path":"b.go","line":null,"originalLine":5,"body":"old","url":"u2","createdAt":"","outdated":false,"author":{"login":"bob"}}
			]}}
		]}}}}
	}`)}
	c := NewClientWithRunner(r)
	comments, err := c.FetchPRReviewComments(context.Background(), "/tmp", "", RepoSlug{Owner: "acme", Name: "widgets"}, 7)
	require.NoError(s.T(), err)
	require.Len(s.T(), comments, 2)
	require.Equal(s.T(), int64(1), comments[0].ID)
	require.Equal(s.T(), "a.go", comments[0].Path)
	require.Equal(s.T(), 10, comments[0].Line)
	require.Equal(s.T(), "RIGHT", comments[0].Side)
	require.Equal(s.T(), "alice", comments[0].Author)
	require.False(s.T(), comments[0].Outdated)
	require.False(s.T(), comments[0].Resolved)
	require.Equal(s.T(), 5, comments[1].Line)
	require.True(s.T(), comments[1].Outdated)
	require.True(s.T(), comments[1].Resolved)
	require.Equal(s.T(), "RIGHT", comments[1].Side) // default
	args := r.calls[len(r.calls)-1].args
	require.Equal(s.T(), "api", args[0])
	require.Equal(s.T(), "graphql", args[1])
	require.Contains(s.T(), args, "owner=acme")
	require.Contains(s.T(), args, "name=widgets")
	require.Contains(s.T(), args, "number=7")
}

func (s *ReviewAPISuite) TestFetchPRReviewCommentsResolvedThreadFlagsAllComments() {
	r := &dispatchRunner{apiOut: []byte(`{
		"data": {"repository": {"pullRequest": {"reviewThreads": {"nodes": [
			{"isResolved": true, "diffSide": "RIGHT", "comments": {"nodes": [
				{"databaseId":11,"path":"x.go","line":1,"body":"first","author":{"login":"a"}},
				{"databaseId":12,"path":"x.go","line":1,"body":"reply","author":{"login":"b"}}
			]}}
		]}}}}
	}`)}
	c := NewClientWithRunner(r)
	comments, err := c.FetchPRReviewComments(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1)
	require.NoError(s.T(), err)
	require.Len(s.T(), comments, 2)
	require.True(s.T(), comments[0].Resolved)
	require.True(s.T(), comments[1].Resolved)
}

func (s *ReviewAPISuite) TestFetchPRReviewCommentsEmpty() {
	r := &dispatchRunner{apiOut: []byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`)}
	c := NewClientWithRunner(r)
	comments, err := c.FetchPRReviewComments(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1)
	require.NoError(s.T(), err)
	require.Empty(s.T(), comments)
}

func (s *ReviewAPISuite) TestFetchPRReviewCommentsGhNotInstalled() {
	r := &dispatchRunner{apiErr: ErrGhNotInstalled}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRReviewComments(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1)
	require.ErrorIs(s.T(), err, ErrGhNotInstalled)
}

func (s *ReviewAPISuite) TestFetchPRReviewCommentsRunError() {
	r := &dispatchRunner{apiErr: errors.New("boom")}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRReviewComments(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1)
	require.Error(s.T(), err)
}

func (s *ReviewAPISuite) TestFetchPRReviewCommentsMalformedJSON() {
	r := &dispatchRunner{apiOut: []byte(`{nope`)}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRReviewComments(context.Background(), "/tmp", "", RepoSlug{Owner: "o", Name: "n"}, 1)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing gh api")
}

func (s *ReviewAPISuite) TestFetchPRReviewCommentsTokenFailure() {
	r := &dispatchRunner{tokenErr: errors.New("no token")}
	c := NewClientWithRunner(r)
	_, err := c.FetchPRReviewComments(context.Background(), "/tmp", "u", RepoSlug{Owner: "o", Name: "n"}, 1)
	require.Error(s.T(), err)
}
