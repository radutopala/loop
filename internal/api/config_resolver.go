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

// merged returns the layered config for the workdir: global for "", global →
// project for a plain workdir, global → parent project → worktree when
// parentDirPath is set. Returns nil on a global-load error; project/worktree
// layer errors fall back to the outer layer (a broken inner config should
// not hide the global one).
func (c *configResolver) merged(workdir, parentDirPath string) *config.Config {
	load := c.load
	if load == nil {
		load = config.Load
	}
	cfg, err := load()
	if err != nil || cfg == nil {
		return nil
	}
	switch {
	case workdir != "" && parentDirPath != "":
		loadWorktree := c.loadWorktree
		if loadWorktree == nil {
			loadWorktree = config.LoadWorktreeProjectConfig
		}
		if pc, perr := loadWorktree(workdir, parentDirPath, cfg); perr == nil && pc != nil {
			return pc
		}
	case workdir != "":
		loadProject := c.loadProject
		if loadProject == nil {
			loadProject = config.LoadProjectConfig
		}
		if pc, perr := loadProject(workdir, cfg); perr == nil && pc != nil {
			return pc
		}
	}
	return cfg
}

// ghUser returns the gh CLI user for the channel's workdir, or "" (use gh's
// active account) on any load error.
func (c *configResolver) ghUser(workdir, parentDirPath string) string {
	cfg := c.merged(workdir, parentDirPath)
	if cfg == nil {
		return ""
	}
	return cfg.GitHub.GHUser
}

// reviewEnabled mirrors ghUser for the review.enabled flag. Returns false on
// any config-load error so a broken config doesn't silently expose the panel.
func (c *configResolver) reviewEnabled(workdir, parentDirPath string) bool {
	cfg := c.merged(workdir, parentDirPath)
	if cfg == nil {
		return false
	}
	return cfg.Review.Enabled
}
