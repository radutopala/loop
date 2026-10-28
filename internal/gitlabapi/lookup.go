// Package gitlabapi shells out to the `glab` CLI to find a branch's GitLab
// merge request. Like githubapi it is a thin wrapper: glab handles auth and
// repo discovery from the git remote, so this package owns only process
// invocation (with an injectable Runner for tests), the account choice via
// GLAB_CONFIG_DIR, and mapping the MR onto githubapi.PRInfo.
package gitlabapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/radutopala/loop/internal/forge"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/gitutil"
)

// KindMR marks a PRInfo that describes a GitLab merge request.
const KindMR = "mr"

// ErrGlabNotInstalled signals the glab binary couldn't be located. Callers
// should treat it as "GitLab integration unavailable", not a failure.
var ErrGlabNotInstalled = errors.New("glab CLI not installed")

// Runner executes a glab subcommand in workdir with the given env overrides.
type Runner interface {
	Run(ctx context.Context, workdir string, env []string, args ...string) ([]byte, error)
}

type execRunner struct {
	bin string
	// proxyEnv, when set, gives the proxy variables for each call: glab
	// reads its proxy only from the environment.
	proxyEnv func() []string
}

// NewExecRunner returns a Runner that shells out to `bin`.
func NewExecRunner(bin string) Runner { return execRunner{bin: bin} }

func (e execRunner) Run(ctx context.Context, workdir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.bin, args...)
	cmd.Dir = workdir
	// glab runs git in workdir, an agent-writable repo; the hardened env
	// carries gitutil's config into those git processes.
	cmd.Env = gitutil.Environ(ctx, workdir)
	if e.proxyEnv != nil {
		cmd.Env = append(cmd.Env, e.proxyEnv()...)
	}
	// No update check: it is a network call on every lookup.
	cmd.Env = append(cmd.Env, "GLAB_CHECK_UPDATE=false")
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var pe *exec.Error
		if errors.As(err, &pe) && errors.Is(pe.Err, exec.ErrNotFound) {
			return nil, ErrGlabNotInstalled
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("glab %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// Client looks up merge requests for a workdir.
type Client struct {
	runner Runner
	// defaultConfigDir is glab's config dir when none is configured.
	defaultConfigDir string
	readFile         func(string) ([]byte, error)
	// originURL reads a checkout's origin remote URL; nil runs git.
	originURL func(ctx context.Context, dir string) (string, error)
	// now is the clock the merge settings cache reads; nil is time.Now.
	now      func() time.Time
	settings forge.SettingsCache

	mu       sync.Mutex
	projects map[string]string // project id per checkout dir
}

// NewClient builds a Client backed by the real glab binary. proxyEnv, if
// non-nil, is called per glab invocation for the proxy variables to set.
func NewClient(proxyEnv func() []string) *Client {
	return NewClientWithRunner(execRunner{bin: "glab", proxyEnv: proxyEnv}, DefaultConfigDir(os.Getenv, os.UserConfigDir), os.ReadFile)
}

// NewClientWithRunner builds a Client with injected dependencies — for tests.
func NewClientWithRunner(r Runner, defaultConfigDir string, readFile func(string) ([]byte, error)) *Client {
	return &Client{runner: r, defaultConfigDir: defaultConfigDir, readFile: readFile}
}

// DefaultConfigDir is where glab keeps its config: GLAB_CONFIG_DIR when
// set, else glab-cli under the user config dir (~/Library/Application
// Support on macOS, ~/.config elsewhere). "" when neither resolves.
func DefaultConfigDir(getenv func(string) string, userConfigDir func() (string, error)) string {
	if d := getenv("GLAB_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := userConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "glab-cli")
}

// IsGitLabHost reports whether host is a GitLab instance: its name says
// gitlab, or glab has it configured in configDir (the default dir when
// empty), which covers self-hosted instances under other names.
func (c *Client) IsGitLabHost(host, configDir string) bool {
	if host == "" {
		return false
	}
	if strings.Contains(strings.ToLower(host), "gitlab") {
		return true
	}
	if configDir == "" {
		configDir = c.defaultConfigDir
	}
	if configDir == "" {
		return false
	}
	data, err := c.readFile(filepath.Join(configDir, "config.yml"))
	if err != nil {
		return false
	}
	var cfg struct {
		Hosts map[string]any `yaml:"hosts"`
	}
	if yaml.Unmarshal(data, &cfg) != nil {
		return false
	}
	_, ok := cfg.Hosts[host]
	return ok
}

// LookupMR returns the most recently updated merge request whose source
// branch is `branch`, in any state, or nil when there is none. configDir,
// if non-empty, is passed as GLAB_CONFIG_DIR so the lookup uses the account
// logged in there.
func (c *Client) LookupMR(ctx context.Context, workdir, configDir, branch string) (*githubapi.PRInfo, error) {
	if workdir == "" || branch == "" {
		return nil, nil
	}
	mrs, err := c.listMRs(ctx, workdir, configDir,
		"--source-branch", branch,
		"--all",
		"--order", "updated_at", "--sort", "desc",
		"--per-page", "1",
	)
	if err != nil || len(mrs) == 0 || mrs[0].Number == 0 {
		return nil, err
	}
	return &mrs[0], nil
}

// ListOpenMRs returns the open merge requests of the project backing
// workdir, at most 100, like githubapi's ListOpenPRs.
func (c *Client) ListOpenMRs(ctx context.Context, workdir, configDir string) ([]githubapi.PRInfo, error) {
	if workdir == "" {
		return nil, fmt.Errorf("workdir is required")
	}
	return c.listMRs(ctx, workdir, configDir, "--per-page", "100")
}

// CheckoutMR checks out merge request iid in workdir the way `glab mr
// checkout` does: a local branch named after its source branch that tracks
// it, so a push from workdir updates the MR.
func (c *Client) CheckoutMR(ctx context.Context, workdir, configDir string, iid int) error {
	if workdir == "" || iid <= 0 {
		return fmt.Errorf("workdir and MR number are required")
	}
	_, err := c.runner.Run(ctx, workdir, glabEnv(configDir), "mr", "checkout", strconv.Itoa(iid))
	return err
}

// listMRs runs `glab mr list` with filters and returns the MRs it lists.
func (c *Client) listMRs(ctx context.Context, workdir, configDir string, filters ...string) ([]githubapi.PRInfo, error) {
	args := append(append([]string{"mr", "list"}, filters...), "--output", "json")
	out, err := c.runner.Run(ctx, workdir, glabEnv(configDir), args...)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		IID          int    `json:"iid"`
		WebURL       string `json:"web_url"`
		TargetBranch string `json:"target_branch"`
		SourceBranch string `json:"source_branch"`
		State        string `json:"state"`
		Title        string `json:"title"`
		Draft        bool   `json:"draft"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing glab mr list output: %w", err)
	}
	mrs := make([]githubapi.PRInfo, 0, len(raw))
	for _, mr := range raw {
		mrs = append(mrs, githubapi.PRInfo{
			Number:  mr.IID,
			URL:     mr.WebURL,
			BaseRef: mr.TargetBranch,
			HeadRef: mr.SourceBranch,
			State:   mr.State,
			Title:   mr.Title,
			IsDraft: mr.Draft,
			Kind:    KindMR,
		})
	}
	return mrs, nil
}

// glabEnv points glab at configDir's account, or at its own without one.
func glabEnv(configDir string) []string {
	if configDir == "" {
		return nil
	}
	return []string{"GLAB_CONFIG_DIR=" + configDir}
}
