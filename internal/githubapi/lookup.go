// Package githubapi shells out to the `gh` CLI to answer questions about a
// repo's pull requests. It is intentionally a thin wrapper — `gh` already
// handles auth, repo discovery from the git remote, and JSON formatting,
// so this package owns only:
//
//   - Process invocation with injectable Runner for tests.
//   - Optional per-call token override via `gh auth token --user <name>`,
//     so multi-account users can pin a Loop project to a specific account
//     without mutating global gh state (i.e. without `gh auth switch`).
//   - "No PR found" sentinel: a nil PRInfo with nil error, so callers
//     can branch cleanly instead of string-matching error messages.
package githubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// PRInfo is the subset of pull request fields the UI needs.
type PRInfo struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	BaseRef string `json:"base_ref"`
	HeadRef string `json:"head_ref"`
	State   string `json:"state"`
	Title   string `json:"title,omitempty"`
	IsDraft bool   `json:"is_draft,omitempty"`
}

// ErrGhNotInstalled signals the gh binary couldn't be located. Callers
// should treat this as "GitHub integration unavailable" rather than as a
// hard failure — the UI can still function without PR awareness.
var ErrGhNotInstalled = errors.New("gh CLI not installed")

// Runner executes a gh subcommand in workdir with the given env overrides
// (appended to os.Environ in the default implementation). The split lets
// tests fake exec without touching the filesystem or shelling out.
type Runner interface {
	Run(ctx context.Context, workdir string, env []string, args ...string) ([]byte, error)
}

type execRunner struct {
	// bin is the binary to invoke. Defaults to "gh" via NewClient. Tests
	// construct execRunner with a different bin to exercise the exec path
	// against a deterministic stand-in (e.g. /bin/false).
	bin string
}

// NewExecRunner returns a Runner that shells out to `bin`. Pass "gh" for
// production; pass an alternative for tests that need to exercise the
// real exec path without depending on gh being installed.
func NewExecRunner(bin string) Runner { return execRunner{bin: bin} }

func (e execRunner) Run(ctx context.Context, workdir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.bin, args...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var pe *exec.Error
		if errors.As(err, &pe) && errors.Is(pe.Err, exec.ErrNotFound) {
			return nil, ErrGhNotInstalled
		}
		// gh prints the failure to stderr; surface it so callers/logs see
		// "no pull requests found" or "not a git repository" rather than
		// an opaque exit code.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// Client looks up PRs for a workdir.
type Client struct {
	runner Runner
}

// NewClient builds a Client backed by the real gh binary.
func NewClient() *Client { return &Client{runner: execRunner{bin: "gh"}} }

// NewClientWithRunner builds a Client with an injected runner — for tests.
func NewClientWithRunner(r Runner) *Client { return &Client{runner: r} }

// LookupPR returns the open PR whose head branch matches `branch`, or nil
// if no open PR exists. ghUser, if non-empty, is resolved to a token via
// `gh auth token --user <name>` and passed as GH_TOKEN to the lookup call.
//
// Returns ErrGhNotInstalled (wrapped) when gh isn't on PATH; that's the
// only error callers typically want to treat specially.
func (c *Client) LookupPR(ctx context.Context, workdir, ghUser, branch string) (*PRInfo, error) {
	if workdir == "" || branch == "" {
		return nil, nil
	}

	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return nil, err
	}

	out, err := c.runner.Run(ctx, workdir, env,
		"pr", "view", branch,
		"--json", "number,url,baseRefName,headRefName,state,title,isDraft",
	)
	if err != nil {
		if errors.Is(err, ErrGhNotInstalled) {
			return nil, err
		}
		// gh exits non-zero with "no pull requests found" when there's no
		// PR for the branch — treat that as nil, nil so the UI can render
		// the no-PR state without surfacing an error.
		if strings.Contains(err.Error(), "no pull requests found") ||
			strings.Contains(err.Error(), "no open pull requests") {
			return nil, nil
		}
		return nil, err
	}

	var raw struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		BaseRefName string `json:"baseRefName"`
		HeadRefName string `json:"headRefName"`
		State       string `json:"state"`
		Title       string `json:"title"`
		IsDraft     bool   `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	if raw.Number == 0 {
		return nil, nil
	}
	return &PRInfo{
		Number:  raw.Number,
		URL:     raw.URL,
		BaseRef: raw.BaseRefName,
		HeadRef: raw.HeadRefName,
		State:   raw.State,
		Title:   raw.Title,
		IsDraft: raw.IsDraft,
	}, nil
}

// FetchPRByNumber returns PR metadata for the given PR number (no branch
// lookup). Same shape as LookupPR but addressed by number — used by the
// review panel where the user types the PR number directly.
func (c *Client) FetchPRByNumber(ctx context.Context, workdir, ghUser string, number int) (*PRInfo, error) {
	if workdir == "" || number <= 0 {
		return nil, nil
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return nil, err
	}
	out, err := c.runner.Run(ctx, workdir, env,
		"pr", "view", fmt.Sprintf("%d", number),
		"--json", "number,url,baseRefName,headRefName,state,title,isDraft",
	)
	if err != nil {
		if errors.Is(err, ErrGhNotInstalled) {
			return nil, err
		}
		return nil, err
	}
	var raw struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		BaseRefName string `json:"baseRefName"`
		HeadRefName string `json:"headRefName"`
		State       string `json:"state"`
		Title       string `json:"title"`
		IsDraft     bool   `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	if raw.Number == 0 {
		return nil, nil
	}
	return &PRInfo{
		Number:  raw.Number,
		URL:     raw.URL,
		BaseRef: raw.BaseRefName,
		HeadRef: raw.HeadRefName,
		State:   raw.State,
		Title:   raw.Title,
		IsDraft: raw.IsDraft,
	}, nil
}

// RepoSlug is the owner+name pair gh resolves from the workdir's remote.
type RepoSlug struct {
	Owner string
	Name  string
}

// FetchRepoSlug runs `gh repo view --json owner,name` to discover the repo
// the workdir's git remote points at. Needed for pushing review comments
// via `gh api repos/{owner}/{name}/pulls/{n}/comments`.
func (c *Client) FetchRepoSlug(ctx context.Context, workdir, ghUser string) (*RepoSlug, error) {
	if workdir == "" {
		return nil, fmt.Errorf("workdir is required")
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return nil, err
	}
	out, err := c.runner.Run(ctx, workdir, env, "repo", "view", "--json", "owner,name")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh repo view output: %w", err)
	}
	if raw.Owner.Login == "" || raw.Name == "" {
		return nil, fmt.Errorf("empty owner/name in gh repo view output")
	}
	return &RepoSlug{Owner: raw.Owner.Login, Name: raw.Name}, nil
}

// PostPRComment files a single-line review comment on a PR via the gh API.
// commitID is the head SHA of the PR (required by the GitHub API to anchor
// the comment to a specific revision). side is "RIGHT" for added/modified
// lines, "LEFT" for deleted lines. Returns the GitHub-assigned comment id
// (0 if gh returned a body we couldn't parse — non-fatal: the push still
// succeeded, only the id is lost, which only matters for later deletion).
//
// Fallback: GitHub returns HTTP 422 when the anchor (path, line) is not
// part of the PR's diff at commitID — common when the review agent flags
// a bug on an unchanged line inside a touched function. In that case we
// retry as a general PR conversation comment via the issues endpoint, with
// the file:line prepended to the body so the anchor isn't lost. Returns
// 0 for the comment id on fallback (different id namespace; the loop UI
// can't currently delete conversation comments by id).
func (c *Client) PostPRComment(ctx context.Context, workdir, ghUser string, slug RepoSlug, prNum int, commitID, path, side string, line int, body string) (int64, error) {
	if workdir == "" || prNum <= 0 || commitID == "" || path == "" || line <= 0 {
		return 0, fmt.Errorf("workdir, prNum, commitID, path, line are required")
	}
	if side == "" {
		side = "RIGHT"
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return 0, err
	}
	id, err := c.postPRInlineComment(ctx, workdir, env, slug, prNum, commitID, path, side, line, body)
	if err == nil {
		return id, nil
	}
	if !isOutOfDiff422(err) {
		return 0, err
	}
	return c.postPRConversationComment(ctx, workdir, env, slug, prNum, path, side, line, body)
}

func (c *Client) postPRInlineComment(ctx context.Context, workdir string, env []string, slug RepoSlug, prNum int, commitID, path, side string, line int, body string) (int64, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", slug.Owner, slug.Name, prNum)
	args := []string{
		"api", endpoint, "--method", "POST",
		"-f", "body=" + body,
		"-f", "path=" + path,
		"-F", fmt.Sprintf("line=%d", line),
		"-f", "commit_id=" + commitID,
		"-f", "side=" + side,
	}
	out, err := c.runner.Run(ctx, workdir, env, args...)
	if err != nil {
		return 0, err
	}
	var raw struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(out, &raw)
	return raw.ID, nil
}

func (c *Client) postPRConversationComment(ctx context.Context, workdir string, env []string, slug RepoSlug, prNum int, path, side string, line int, body string) (int64, error) {
	sideLabel := ""
	if side == "LEFT" {
		sideLabel = " (deleted)"
	}
	prefixed := fmt.Sprintf("**%s:L%d%s** _(posted as PR conversation — line not in diff)_\n\n%s", path, line, sideLabel, body)
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/comments", slug.Owner, slug.Name, prNum)
	args := []string{
		"api", endpoint, "--method", "POST",
		"-f", "body=" + prefixed,
	}
	if _, err := c.runner.Run(ctx, workdir, env, args...); err != nil {
		return 0, err
	}
	return 0, nil
}

// isOutOfDiff422 detects the GitHub validation error that fires when the
// anchor line is not part of the PR diff at commitID. gh surfaces it as
// "Validation Failed (HTTP 422)" in stderr; the execRunner wraps that into
// the error string. Match conservatively — only on the 422 substring — so
// unrelated 422s (rare on this endpoint) also trigger the fallback rather
// than failing the push outright.
func isOutOfDiff422(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "HTTP 422")
}

// DeletePRReviewComment deletes a single inline review comment via the gh
// API. commentID is the numeric GitHub-assigned id (NOT the loop-local
// "gh-<n>" / agent uuid). Returns nil if the comment is already gone
// (404) — callers can rely on best-effort idempotency.
func (c *Client) DeletePRReviewComment(ctx context.Context, workdir, ghUser string, slug RepoSlug, commentID int64) error {
	if workdir == "" || commentID <= 0 || slug.Owner == "" || slug.Name == "" {
		return fmt.Errorf("workdir, slug, and commentID are required")
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/comments/%d", slug.Owner, slug.Name, commentID)
	if _, err := c.runner.Run(ctx, workdir, env, "api", endpoint, "--method", "DELETE"); err != nil {
		// gh surfaces a 404 as a stderr message containing "Not Found";
		// treat that as success so re-deleting an already-gone comment is
		// idempotent.
		if strings.Contains(err.Error(), "Not Found") || strings.Contains(err.Error(), "HTTP 404") {
			return nil
		}
		return err
	}
	return nil
}

// ListOpenPRs returns all open pull requests in the repo backing workdir.
// Drafts are included (the FE flags them visually). The list is capped
// at 100 — repos with more open PRs are an edge case for a review-panel
// picker and pagination would dwarf the rest of the UX.
func (c *Client) ListOpenPRs(ctx context.Context, workdir, ghUser string) ([]PRInfo, error) {
	if workdir == "" {
		return nil, fmt.Errorf("workdir is required")
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return nil, err
	}
	out, err := c.runner.Run(ctx, workdir, env,
		"pr", "list", "--state", "open", "--limit", "100",
		"--json", "number,url,baseRefName,headRefName,state,title,isDraft",
	)
	if err != nil {
		if errors.Is(err, ErrGhNotInstalled) {
			return nil, err
		}
		return nil, err
	}
	var raw []struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		BaseRefName string `json:"baseRefName"`
		HeadRefName string `json:"headRefName"`
		State       string `json:"state"`
		Title       string `json:"title"`
		IsDraft     bool   `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr list output: %w", err)
	}
	prs := make([]PRInfo, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PRInfo{
			Number:  r.Number,
			URL:     r.URL,
			BaseRef: r.BaseRefName,
			HeadRef: r.HeadRefName,
			State:   r.State,
			Title:   r.Title,
			IsDraft: r.IsDraft,
		})
	}
	return prs, nil
}

// PRReviewComment is a single inline review comment already filed on a PR
// via GitHub's web UI / API. The review panel loads these alongside the
// agent's pending comments so the user sees both side-by-side in the diff
// view. Line is the right-side line number (or original_line if the
// comment is outdated against the current head); Line is 0 when GitHub
// could not anchor the comment to any line on the latest commit.
// Resolved reflects whether the comment's review thread has been resolved
// on GitHub — derived from the parent thread, not from the comment row
// itself (the REST /pulls/{n}/comments endpoint omits this field, which
// is why we fetch via GraphQL).
type PRReviewComment struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	Body      string `json:"body"`
	Author    string `json:"author,omitempty"`
	URL       string `json:"url,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Outdated  bool   `json:"outdated,omitempty"`
	Resolved  bool   `json:"resolved,omitempty"`
}

// reviewCommentsGraphQLQuery fetches every review thread on a PR and the
// comments within each thread. We need the GraphQL path (not the REST
// pulls/{n}/comments endpoint) because thread resolution status is a
// property of PullRequestReviewThread, not of the individual comment row.
// diffSide lives on PullRequestReviewThread, not on the individual
// PullRequestReviewComment row — every comment in a thread shares the
// same side, so the schema models it once at the thread level. Earlier
// versions of this query asked for diffSide on the comment node and
// the entire call failed with `Field 'diffSide' doesn't exist on type
// 'PullRequestReviewComment'`.
const reviewCommentsGraphQLQuery = `query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      reviewThreads(first:100){
        nodes{
          isResolved
          diffSide
          comments(first:100){
            nodes{
              databaseId
              path
              line
              originalLine
              body
              url
              createdAt
              outdated
              author{login}
            }
          }
        }
      }
    }
  }
}`

// FetchPRReviewComments lists existing inline review comments on a PR via
// `gh api graphql`. The list is capped at 100 threads × 100 comments per
// thread — beyond that a PR review is unwieldy enough that the user is
// better served on github.com directly.
func (c *Client) FetchPRReviewComments(ctx context.Context, workdir, ghUser string, slug RepoSlug, prNum int) ([]PRReviewComment, error) {
	if workdir == "" || prNum <= 0 || slug.Owner == "" || slug.Name == "" {
		return nil, fmt.Errorf("workdir, slug, and prNum are required")
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return nil, err
	}
	args := []string{
		"api", "graphql",
		"-F", "owner=" + slug.Owner,
		"-F", "name=" + slug.Name,
		"-F", fmt.Sprintf("number=%d", prNum),
		"-f", "query=" + reviewCommentsGraphQLQuery,
	}
	out, err := c.runner.Run(ctx, workdir, env, args...)
	if err != nil {
		if errors.Is(err, ErrGhNotInstalled) {
			return nil, err
		}
		return nil, err
	}
	var raw struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool   `json:"isResolved"`
							DiffSide   string `json:"diffSide"`
							Comments   struct {
								Nodes []struct {
									DatabaseID   int64  `json:"databaseId"`
									Path         string `json:"path"`
									Line         *int   `json:"line"`
									OriginalLine *int   `json:"originalLine"`
									Body         string `json:"body"`
									URL          string `json:"url"`
									CreatedAt    string `json:"createdAt"`
									Outdated     bool   `json:"outdated"`
									Author       struct {
										Login string `json:"login"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh api graphql reviewThreads output: %w", err)
	}
	var comments []PRReviewComment
	for _, t := range raw.Data.Repository.PullRequest.ReviewThreads.Nodes {
		for _, r := range t.Comments.Nodes {
			line, outdated := 0, r.Outdated
			if r.Line != nil {
				line = *r.Line
			} else if r.OriginalLine != nil {
				line = *r.OriginalLine
				// The GraphQL outdated flag already covers "line is null"
				// cases, but force it true here too so callers don't have to
				// reason about the two paths.
				outdated = true
			}
			side := t.DiffSide
			if side == "" {
				side = "RIGHT"
			}
			comments = append(comments, PRReviewComment{
				ID:        r.DatabaseID,
				Path:      r.Path,
				Line:      line,
				Side:      side,
				Body:      r.Body,
				Author:    r.Author.Login,
				URL:       r.URL,
				CreatedAt: r.CreatedAt,
				Outdated:  outdated,
				Resolved:  t.IsResolved,
			})
		}
	}
	return comments, nil
}

// FetchPRHeadSHA returns the head commit SHA for a PR, required when
// posting review comments anchored to a specific revision.
func (c *Client) FetchPRHeadSHA(ctx context.Context, workdir, ghUser string, number int) (string, error) {
	if workdir == "" || number <= 0 {
		return "", fmt.Errorf("workdir and number are required")
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return "", err
	}
	out, err := c.runner.Run(ctx, workdir, env,
		"pr", "view", fmt.Sprintf("%d", number), "--json", "headRefOid",
	)
	if err != nil {
		return "", err
	}
	var raw struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return "", fmt.Errorf("parsing gh pr view output: %w", err)
	}
	if raw.HeadRefOid == "" {
		return "", fmt.Errorf("empty headRefOid")
	}
	return raw.HeadRefOid, nil
}

// tokenEnv resolves the GH_TOKEN env override for the configured ghUser.
// Empty ghUser → no override (gh uses its currently-active account).
func (c *Client) tokenEnv(ctx context.Context, workdir, ghUser string) ([]string, error) {
	if ghUser == "" {
		return nil, nil
	}
	out, err := c.runner.Run(ctx, workdir, nil, "auth", "token", "--user", ghUser)
	if err != nil {
		if errors.Is(err, ErrGhNotInstalled) {
			return nil, err
		}
		return nil, fmt.Errorf("reading gh token for user %q: %w", ghUser, err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return nil, fmt.Errorf("gh auth token --user %q returned empty token", ghUser)
	}
	return []string{"GH_TOKEN=" + token}, nil
}
