package api

import (
	"github.com/radutopala/loop/internal/config"
)

// configResolver owns layered config resolution for a workspace: global →
// project → worktree, where the innermost layer that sets a value wins. It
// was extracted from Server (which previously carried the three loader
// fields plus two near-identical merge helpers) so config-layer questions
// have one home and consumers depend on exactly this.
//
// The loaders are injectable for tests; nil fields fall back to the config
// package's real loaders.
type configResolver struct {
	load         func() (*config.Config, error)
	loadProject  func(string, *config.Config) (*config.Config, error)
	loadWorktree func(string, string, *config.Config) (*config.Config, error)
}

// WithConfigLoaders sets the loaders the server resolves configs with: load
// for the global config, project and worktree for the layers over it. Ones
// that keep what they parsed save reparsing unchanged files on every
// request.
func WithConfigLoaders(load func() (*config.Config, error), project func(string, *config.Config) (*config.Config, error), worktree func(string, string, *config.Config) (*config.Config, error)) Option {
	return func(s *Server) {
		s.configs.load, s.configs.loadProject, s.configs.loadWorktree = load, project, worktree
	}
}

// merged returns the layered config for the workdir: global for "", global →
// project for a plain workdir, global → parent project → worktree when
// parentDirPath is set. Returns nil on a global-load error; project/worktree
// layer errors fall back to the outer layer (a broken inner config should
// not hide the global one).
func (c *configResolver) merged(workdir, parentDirPath string) *config.Config {
	_, merged := c.layered(workdir, parentDirPath)
	return merged
}

// layered is merged, with the global config it merged over: both nil on a
// global-load error.
func (c *configResolver) layered(workdir, parentDirPath string) (global, merged *config.Config) {
	load := c.load
	if load == nil {
		load = config.Load
	}
	cfg, err := load()
	if err != nil || cfg == nil {
		return nil, nil
	}
	switch {
	case workdir != "" && parentDirPath != "":
		if pc, perr := c.worktreeLoader()(workdir, parentDirPath, cfg); perr == nil && pc != nil {
			return cfg, pc
		}
	case workdir != "":
		if pc, perr := c.projectLoader()(workdir, cfg); perr == nil && pc != nil {
			return cfg, pc
		}
	}
	return cfg, cfg
}

// projectLoader returns the project config loader, defaulting to the config
// package's.
func (c *configResolver) projectLoader() func(string, *config.Config) (*config.Config, error) {
	if c.loadProject == nil {
		return config.LoadProjectConfig
	}
	return c.loadProject
}

// worktreeLoader returns the worktree config loader, defaulting to the config
// package's.
func (c *configResolver) worktreeLoader() func(string, string, *config.Config) (*config.Config, error) {
	if c.loadWorktree == nil {
		return config.LoadWorktreeProjectConfig
	}
	return c.loadWorktree
}

// ghUser returns the gh CLI user for the channel's workdir, or "" (use gh's
// active account) on any load error.
func (c *configResolver) ghUser(workdir, parentDirPath string) string {
	return ghUserOf(c.merged(workdir, parentDirPath))
}

// ghUserOf returns cfg's gh CLI user, "" for a nil cfg.
func ghUserOf(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.GitHub.GHUser
}

// glabConfigDir returns the glab config dir for the channel's workdir, or ""
// (glab's default) on any load error.
func (c *configResolver) glabConfigDir(workdir, parentDirPath string) string {
	return glabConfigDirOf(c.merged(workdir, parentDirPath))
}

// glabConfigDirOf returns cfg's glab config dir, "" for a nil cfg.
func glabConfigDirOf(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.GitLab.GlabConfigDir
}

// reviewEnabled mirrors ghUser for the review.enabled flag. Returns false on
// any config-load error so a broken config doesn't silently expose the panel.
func (c *configResolver) reviewEnabled(workdir, parentDirPath string) bool {
	return c.review(workdir, parentDirPath).enabled
}

// reviewMaxIterations mirrors ghUser for review.max_iterations: the passes a
// review runs when its start gives none. The default on a config-load error.
func (c *configResolver) reviewMaxIterations(workdir, parentDirPath string) int {
	return c.review(workdir, parentDirPath).maxIterations
}

// reviewSettings is what the channel list shows of a workdir's review
// config.
type reviewSettings struct {
	enabled       bool
	maxIterations int
}

// review returns reviewEnabled and reviewMaxIterations from one load of the
// workdir's config.
func (c *configResolver) review(workdir, parentDirPath string) reviewSettings {
	cfg := c.merged(workdir, parentDirPath)
	if cfg == nil {
		return reviewSettings{maxIterations: config.DefaultReviewMaxIterations}
	}
	return reviewSettings{enabled: cfg.Review.Enabled, maxIterations: cfg.Review.ReviewIterations()}
}

// reviewUpdateBase mirrors ghUser for review.update_base: whether a review
// may fast-forward the base branch where it is checked out. False on a
// config-load error.
func (c *configResolver) reviewUpdateBase(workdir, parentDirPath string) bool {
	cfg := c.merged(workdir, parentDirPath)
	return cfg != nil && cfg.Review.UpdateBase
}

// claudeEffort mirrors ghUser for claude_effort, the reasoning effort a
// container runs at when the request sets none.
func (c *configResolver) claudeEffort(workdir, parentDirPath string) string {
	cfg := c.merged(workdir, parentDirPath)
	if cfg == nil {
		return ""
	}
	return cfg.ClaudeEffort
}

// reviewVerify mirrors ghUser for review.verify and review.verify_prompt:
// whether a review ends with the verification step, and its instructions.
// On a config-load error the step stays on, its default.
func (c *configResolver) reviewVerify(workdir, parentDirPath string) verifyStep {
	cfg := c.merged(workdir, parentDirPath)
	if cfg == nil {
		return verifyStep{on: true}
	}
	return verifyStep{on: cfg.Review.Verify, prompt: cfg.Review.VerifyPrompt}
}
