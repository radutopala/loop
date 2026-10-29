package gitutil

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/osutil"
)

// configs keeps what readConfig read in each repo, for Environ.
var configs = newConfigCache()

// configCache keeps the config entries read in each repo, with the stat of
// every file they came from or could come from: the repo's .git and the
// files in its git dir that decide which config git reads, the global config
// files, every file entries came from, and every file an include names,
// there or not. While none of those stats changes, neither does what git
// reads, so it isn't run again. A repo the agent can write gets no further
// than any write it makes: that changes a stat.
type configCache struct {
	// git runs git with args in dir, with env.
	git  func(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)
	stat func(string) (os.FileInfo, error)
	now  func() time.Time
	mu   sync.Mutex
	// repos holds each repo's entries by its dir and the HOME and
	// XDG_CONFIG_HOME git found the global config by.
	repos map[string]repoConfig
}

// repoConfig is what a repo's config read gave, and the files it depends on.
type repoConfig struct {
	entries []configEntry
	deps    map[string]fileState
}

// fileState is what a path's stat says: missing, a dir, or a file and its
// stamp. A dir's own stat changes with every file added to it, so only its
// being one counts: the files in it that matter are deps of their own.
type fileState struct {
	exists, dir bool
	stamp       osutil.FileStamp
}

// racyWindow is how far before a read a file's change makes its stat
// unusable to tell a later write by: one in the same clock tick may leave
// the stat as it was. Generous, for filesystems whose clock lags the wall
// clock.
const racyWindow = 2 * time.Second

func newConfigCache() *configCache {
	return &configCache{git: runGit, stat: os.Stat, now: time.Now, repos: map[string]repoConfig{}}
}

func runGit(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd.Output()
}

// includeKeys matches the keys that include another config file.
const includeKeys = `include\.path|includeif\..+\.path`

// read lists the entries of dir's config matching commandKeys, in the order
// git reads them, git run with environ hardened. `git config` only reads
// config files — it runs no hooks, filters or fsmonitor — so it is safe to
// ask before hardening. A non-repo dir, or one without such keys, yields
// none.
func (c *configCache) read(ctx context.Context, dir string, environ []string) []configEntry {
	env := buildEnviron(environ, nil)
	home, xdg := envValue(environ, "HOME"), envValue(environ, "XDG_CONFIG_HOME")
	key := dir + "\x00" + home + "\x00" + xdg
	// Paths are joined with "/" here, so not on Windows.
	cacheable := runtime.GOOS != "windows" && home != "" && ownGitEnv(env)
	if cacheable {
		if entries, ok := c.cached(key); ok {
			return entries
		}
	}
	start := c.now()
	out, err := c.git(ctx, dir, env, "config", "--null", "--show-scope", "--show-origin", "--get-regexp", "^("+commandKeys+"|"+includeKeys+")$")
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1 || len(out) > 0) {
		// Exit 1 without output is no matching key; anything else, no
		// config read.
		return nil
	}
	all := parseConfig(string(out))
	entries := make([]configEntry, 0, len(all))
	for _, e := range all {
		if !e.include() {
			entries = append(entries, e)
		}
	}
	if !cacheable {
		return entries
	}
	deps, ok := c.deps(ctx, dir, env, home, xdg, all)
	if !ok {
		return entries
	}
	for _, s := range deps {
		// Changed while git read it, or so shortly before that a further
		// change may not show.
		if s.exists && !s.dir && s.stamp.ChangedSince(start.Add(-racyWindow)) {
			return entries
		}
	}
	c.mu.Lock()
	c.repos[key] = repoConfig{entries: entries, deps: deps}
	c.mu.Unlock()
	return entries
}

// cached returns the entries last read under key if no file they depend on
// changed since.
func (c *configCache) cached(key string) ([]configEntry, bool) {
	c.mu.Lock()
	rc, ok := c.repos[key]
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	for path, was := range rc.deps {
		if s, ok := c.state(path); !ok || s != was {
			return nil, false
		}
	}
	return rc.entries, true
}

// deps stats every file the config read in dir depends on, all its entries
// given, the global config found by home and xdg. False when they can't all
// be known: dir isn't a repo's top, a path can't be stat'ed, or an include
// names a file in a way not followed here.
func (c *configCache) deps(ctx context.Context, dir string, env []string, home, xdg string, all []configEntry) (map[string]fileState, bool) {
	if xdg == "" {
		xdg = home + "/.config"
	}
	paths := []string{dir + "/.git", home + "/.gitconfig", xdg + "/git/config"}
	gitDir, commonDir, ok := c.gitDirs(ctx, dir, env)
	if !ok {
		return nil, false
	}
	paths = append(paths, gitDir+"/commondir", gitDir+"/config.worktree", gitDir+"/HEAD", commonDir+"/config")
	for _, e := range all {
		origin, ok := strings.CutPrefix(e.origin, "file:")
		if !ok || origin == "" {
			return nil, false
		}
		if !strings.HasPrefix(origin, "/") {
			origin = dir + "/" + origin
		}
		paths = append(paths, origin)
		if e.include() {
			target, ok := includeTarget(e.value, origin, home)
			if !ok {
				return nil, false
			}
			paths = append(paths, target)
		}
	}
	deps := make(map[string]fileState, len(paths))
	for _, p := range paths {
		s, ok := c.state(p)
		if !ok {
			return nil, false
		}
		deps[p] = s
	}
	return deps, true
}

// gitDirs returns the git dir and common dir of the repo whose top is dir.
// False when dir isn't the top of one.
func (c *configCache) gitDirs(ctx context.Context, dir string, env []string) (gitDir, commonDir string, ok bool) {
	out, err := c.git(ctx, dir, env, "rev-parse", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if err != nil || len(lines) != 3 {
		return "", "", false
	}
	top, err := c.stat(lines[0])
	if err != nil {
		return "", "", false
	}
	at, err := c.stat(dir)
	if err != nil || !os.SameFile(top, at) {
		return "", "", false
	}
	commonDir = lines[2]
	if !strings.HasPrefix(commonDir, "/") {
		commonDir = dir + "/" + commonDir
	}
	return lines[1], commonDir, true
}

// state stats path. False on an error other than its not existing.
func (c *configCache) state(path string) (fileState, bool) {
	fi, err := c.stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fileState{}, true
	case err != nil:
		return fileState{}, false
	case fi.IsDir():
		return fileState{exists: true, dir: true}, true
	}
	return fileState{exists: true, stamp: osutil.StampOf(fi)}, true
}

// includeTarget returns the file an include of path in the config file at
// origin reads, as git finds it: joined, not cleaned, so a ".." after a
// symlink goes where it goes for git. False for a form not followed here.
func includeTarget(path, origin, home string) (string, bool) {
	switch {
	case strings.HasPrefix(path, "~/"):
		return home + path[1:], true
	case path == "" || strings.HasPrefix(path, "~") || strings.HasPrefix(path, "%("):
		return "", false
	case strings.HasPrefix(path, "/"):
		return path, true
	}
	return origin[:strings.LastIndexByte(origin, '/')+1] + path, true
}

// ownGitEnv reports whether env sets no git variable but the ones
// buildEnviron does: any other may point git at other config or another
// repo, which the cache doesn't follow.
func ownGitEnv(env []string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_") && !inheritedGitOverride(kv) {
			return false
		}
	}
	return true
}

// envValue returns the value of name in environ, the last one set.
func envValue(environ []string, name string) string {
	v := ""
	for _, kv := range environ {
		if k, val, ok := strings.Cut(kv, "="); ok && k == name {
			v = val
		}
	}
	return v
}
