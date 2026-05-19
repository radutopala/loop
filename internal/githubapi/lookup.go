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

// FetchPRDiff returns the unified patch for a PR via `gh pr diff <n> --patch`.
// The returned bytes are the raw patch suitable for parseUnifiedDiff on the FE.
func (c *Client) FetchPRDiff(ctx context.Context, workdir, ghUser string, number int) ([]byte, error) {
	if workdir == "" || number <= 0 {
		return nil, fmt.Errorf("workdir and number are required")
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return nil, err
	}
	out, err := c.runner.Run(ctx, workdir, env,
		"pr", "diff", fmt.Sprintf("%d", number), "--patch",
	)
	if err != nil {
		return nil, err
	}
	return out, nil
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
// lines, "LEFT" for deleted lines.
func (c *Client) PostPRComment(ctx context.Context, workdir, ghUser string, slug RepoSlug, prNum int, commitID, path, side string, line int, body string) error {
	if workdir == "" || prNum <= 0 || commitID == "" || path == "" || line <= 0 {
		return fmt.Errorf("workdir, prNum, commitID, path, line are required")
	}
	if side == "" {
		side = "RIGHT"
	}
	env, err := c.tokenEnv(ctx, workdir, ghUser)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", slug.Owner, slug.Name, prNum)
	args := []string{
		"api", endpoint, "--method", "POST",
		"-f", "body=" + body,
		"-f", "path=" + path,
		"-F", fmt.Sprintf("line=%d", line),
		"-f", "commit_id=" + commitID,
		"-f", "side=" + side,
	}
	if _, err := c.runner.Run(ctx, workdir, env, args...); err != nil {
		return err
	}
	return nil
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
