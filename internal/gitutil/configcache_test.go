package gitutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ConfigCacheSuite struct {
	suite.Suite
	home, repo string
	environ    []string
	cache      *configCache
	reads      int
	// mtime is the next modification time touch gives a file.
	mtime time.Time
}

func TestConfigCacheSuite(t *testing.T) {
	suite.Run(t, new(ConfigCacheSuite))
}

func (s *ConfigCacheSuite) SetupTest() {
	root, err := filepath.EvalSymlinks(s.T().TempDir())
	require.NoError(s.T(), err)
	s.home = filepath.Join(root, "home")
	s.repo = filepath.Join(root, "repo")
	require.NoError(s.T(), os.MkdirAll(s.home, 0o755))
	s.environ = []string{"HOME=" + s.home, "PATH=" + os.Getenv("PATH")}
	s.run(root, "init", "-q", "-b", "main", s.repo)
	s.run(s.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	s.mtime = time.Now().Add(-time.Hour)
	s.reads = 0
	s.cache = newConfigCache()
	git := s.cache.git
	s.cache.git = func(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
		if args[0] == "config" {
			s.reads++
		}
		return git(ctx, dir, env, args...)
	}
	// Every file was changed long before the read: none is racy.
	s.cache.now = func() time.Time { return time.Now().Add(time.Hour) }
}

// run runs git in dir with the suite's environment.
func (s *ConfigCacheSuite) run(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = s.environ
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, "git %v: %s", args, out)
}

// write writes path, then gives it a modification time of its own, so its
// stamp changes even within one clock tick.
func (s *ConfigCacheSuite) write(path, content string) {
	require.NoError(s.T(), os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(s.T(), os.WriteFile(path, []byte(content), 0o644))
	s.touch(path)
}

func (s *ConfigCacheSuite) touch(path string) {
	s.mtime = s.mtime.Add(time.Second)
	require.NoError(s.T(), os.Chtimes(path, s.mtime, s.mtime))
}

// filters lists the filter drivers whose clean command dir's config sets.
func (s *ConfigCacheSuite) filters(dir string) []string {
	var names []string
	for _, e := range s.cache.read(context.Background(), dir, s.environ) {
		if name, ok := strings.CutSuffix(strings.TrimPrefix(e.key, "filter."), ".clean"); ok {
			names = append(names, name)
		}
	}
	return names
}

// TestKeptWhileUnchanged: an unchanged repo is read once, a repo without
// any command keys too.
func (s *ConfigCacheSuite) TestKeptWhileUnchanged() {
	require.Empty(s.T(), s.filters(s.repo))
	require.Empty(s.T(), s.filters(s.repo))
	require.Equal(s.T(), 1, s.reads)

	s.run(s.repo, "config", "filter.a.clean", "x")
	s.touch(filepath.Join(s.repo, ".git", "config"))
	require.Equal(s.T(), []string{"a"}, s.filters(s.repo))
	require.Equal(s.T(), []string{"a"}, s.filters(s.repo))
	require.Equal(s.T(), 2, s.reads)
}

// TestChangesAreSeen: each change to a file git reads config from, or could,
// is seen on the next read.
func (s *ConfigCacheSuite) TestChangesAreSeen() {
	// Each case has a repo of its own.
	gitDir := func(name string) string { return filepath.Join(s.repo, ".git", name) }
	other := func(name string) string { return filepath.Join(filepath.Dir(s.repo), "other", name) }
	tests := []struct {
		name  string
		setup func()
		// change makes filter "evil" appear.
		change func()
	}{
		{
			name: "repo config edited",
			change: func() {
				s.run(s.repo, "config", "filter.evil.clean", "x")
				s.touch(gitDir("config"))
			},
		},
		{
			name:   "include created later",
			setup:  func() { s.run(s.repo, "config", "include.path", "../inc.cfg") },
			change: func() { s.write(filepath.Join(s.repo, "inc.cfg"), "[filter \"evil\"]\n\tclean = x\n") },
		},
		{
			name: "included file edited",
			setup: func() {
				s.write(filepath.Join(s.repo, "inc.cfg"), "[filter \"ok\"]\n\tsmudge = x\n")
				s.run(s.repo, "config", "include.path", "../inc.cfg")
			},
			change: func() { s.write(filepath.Join(s.repo, "inc.cfg"), "[filter \"evil\"]\n\tclean = x\n") },
		},
		{
			name: "nested include created later",
			setup: func() {
				s.write(other("a.cfg"), "[include]\n\tpath = b.cfg\n")
				s.run(s.repo, "config", "include.path", other("a.cfg"))
			},
			change: func() { s.write(other("b.cfg"), "[filter \"evil\"]\n\tclean = x\n") },
		},
		{
			name: "include through a symlink and up",
			setup: func() {
				require.NoError(s.T(), os.MkdirAll(other("deep"), 0o755))
				require.NoError(s.T(), os.Symlink(other("deep"), filepath.Join(s.repo, "link")))
				// git reads other/inc.cfg: ".." is taken after the link.
				s.run(s.repo, "config", "include.path", "../link/../inc.cfg")
			},
			change: func() { s.write(other("inc.cfg"), "[filter \"evil\"]\n\tclean = x\n") },
		},
		{
			name: "branch switched to one an include is on",
			setup: func() {
				s.write(filepath.Join(s.repo, "on.cfg"), "[filter \"evil\"]\n\tclean = x\n")
				s.run(s.repo, "config", "includeIf.onbranch:other.path", "../on.cfg")
			},
			change: func() {
				s.run(s.repo, "symbolic-ref", "HEAD", "refs/heads/other")
				s.touch(gitDir("HEAD"))
			},
		},
		{
			name:   "home include created later",
			setup:  func() { s.write(filepath.Join(s.home, ".gitconfig"), "[include]\n\tpath = ~/more.cfg\n") },
			change: func() { s.write(filepath.Join(s.home, "more.cfg"), "[filter \"evil\"]\n\tclean = x\n") },
		},
		{
			name:   "global config created",
			change: func() { s.write(filepath.Join(s.home, ".gitconfig"), "[filter \"evil\"]\n\tclean = x\n") },
		},
		{
			name:   "xdg config created",
			change: func() { s.write(filepath.Join(s.home, ".config", "git", "config"), "[filter \"evil\"]\n\tclean = x\n") },
		},
		{
			name: "worktree config created",
			setup: func() {
				s.run(s.repo, "config", "core.repositoryFormatVersion", "1")
				s.run(s.repo, "config", "extensions.worktreeConfig", "true")
			},
			change: func() { s.write(gitDir("config.worktree"), "[filter \"evil\"]\n\tclean = x\n") },
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.setup != nil {
				tc.setup()
				s.touch(gitDir("config"))
			}
			require.NotContains(s.T(), s.filters(s.repo), "evil")
			require.NotContains(s.T(), s.filters(s.repo), "evil")
			require.Equal(s.T(), 1, s.reads, "kept while unchanged")
			tc.change()
			require.Contains(s.T(), s.filters(s.repo), "evil")
		})
	}
}

// TestLinkedWorktree: a linked worktree's .git file, its git dir and the
// common config are all followed.
func (s *ConfigCacheSuite) TestLinkedWorktree() {
	wt := filepath.Join(filepath.Dir(s.repo), "wt")
	s.run(s.repo, "worktree", "add", "-q", "-b", "wt", wt)
	require.Empty(s.T(), s.filters(wt))
	require.Empty(s.T(), s.filters(wt))
	require.Equal(s.T(), 1, s.reads)

	s.run(s.repo, "config", "filter.evil.clean", "x")
	s.touch(filepath.Join(s.repo, ".git", "config"))
	require.Equal(s.T(), []string{"evil"}, s.filters(wt))
	require.Equal(s.T(), 2, s.reads)

	// Pointed at another repo's git dir.
	s.write(filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(s.repo, ".git")+"\n")
	require.Equal(s.T(), []string{"evil"}, s.filters(wt))
	require.Equal(s.T(), 3, s.reads)
}

// TestNotKept: reads that can't be followed run git every time.
func (s *ConfigCacheSuite) TestNotKept() {
	tests := []struct {
		name string
		// dir is where to read, the repo when nil.
		dir     func() string
		environ func() []string
		setup   func()
	}{
		{name: "git config from the environment", environ: func() []string {
			return append(slices.Clone(s.environ), "GIT_CONFIG_GLOBAL="+os.DevNull)
		}},
		{name: "no home", environ: func() []string { return []string{"PATH=" + os.Getenv("PATH")} }},
		{name: "below the repo's top", dir: func() string {
			sub := filepath.Join(s.repo, "sub")
			require.NoError(s.T(), os.MkdirAll(sub, 0o755))
			return sub
		}},
		{name: "not a repo", dir: func() string { return s.home }},
		{name: "include of another user's home", setup: func() { s.run(s.repo, "config", "include.path", "~root/x.cfg") }},
		{name: "include under git's prefix", setup: func() { s.run(s.repo, "config", "include.path", "%(prefix)/x.cfg") }},
		{name: "changed just before the read", setup: func() { s.cache.now = time.Now }},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			dir := s.repo
			if tc.dir != nil {
				dir = tc.dir()
			}
			if tc.setup != nil {
				tc.setup()
			}
			if tc.environ != nil {
				s.environ = tc.environ()
			}
			s.cache.read(context.Background(), dir, s.environ)
			s.cache.read(context.Background(), dir, s.environ)
			require.Equal(s.T(), 2, s.reads)
		})
	}
}

// TestBrokenConfig: a config git can't read gives no entries, and isn't
// kept.
func (s *ConfigCacheSuite) TestBrokenConfig() {
	s.write(filepath.Join(s.repo, ".git", "config"), "[broken\n")
	require.Nil(s.T(), s.cache.read(context.Background(), s.repo, s.environ))
	require.Empty(s.T(), s.cache.repos)
}

// TestStatErrors: a path that can't be stat'ed, when reading or checking,
// isn't taken as unchanged.
func (s *ConfigCacheSuite) TestStatErrors() {
	failing := ""
	s.cache.stat = func(p string) (os.FileInfo, error) {
		if failing != "" && strings.HasSuffix(p, failing) {
			return nil, os.ErrPermission
		}
		return os.Stat(p)
	}
	tests := []struct {
		name, failing string
	}{
		{"a dep", "/.gitconfig"},
		{"the repo top", "/repo"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			failing = tc.failing
			s.cache.read(context.Background(), s.repo, s.environ)
			require.Empty(s.T(), s.cache.repos)
		})
	}
	failing = ""
	s.cache.read(context.Background(), s.repo, s.environ)
	require.Len(s.T(), s.cache.repos, 1)
	failing = "/.gitconfig"
	reads := s.reads
	s.cache.read(context.Background(), s.repo, s.environ)
	require.Equal(s.T(), reads+1, s.reads, "a dep that can't be stat'ed is a change")
}

// TestUnfollowedOutput: git output naming what the cache can't follow
// isn't kept.
func (s *ConfigCacheSuite) TestUnfollowedOutput() {
	top := s.repo + "\n" + s.repo + "/.git\n.git\n"
	tests := []struct {
		name, config, revParse string
		statTop                func(string) (os.FileInfo, error)
	}{
		{name: "an entry not from a file", config: "command\x00command line:\x00filter.x.clean\nx\x00", revParse: top},
		{name: "an entry from no file", config: "local\x00file:\x00filter.x.clean\nx\x00", revParse: top},
		{name: "rev-parse short", revParse: s.repo + "\n"},
		{name: "another top", revParse: s.home + "\n" + s.repo + "/.git\n.git\n"},
		{name: "top not there", revParse: s.repo + "/gone\n" + s.repo + "/.git\n.git\n"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.cache = newConfigCache()
			s.cache.now = func() time.Time { return time.Now().Add(time.Hour) }
			s.cache.git = func(_ context.Context, _ string, _ []string, args ...string) ([]byte, error) {
				if args[0] == "config" {
					return []byte(tc.config), nil
				}
				return []byte(tc.revParse), nil
			}
			s.cache.read(context.Background(), s.repo, s.environ)
			require.Empty(s.T(), s.cache.repos)
		})
	}

	// The same output, followable, is kept, with a relative common dir.
	s.cache.git = func(_ context.Context, _ string, _ []string, args ...string) ([]byte, error) {
		if args[0] == "config" {
			return nil, errors.New("killed")
		}
		return []byte(top), nil
	}
	require.Nil(s.T(), s.cache.read(context.Background(), s.repo, s.environ))
	require.Empty(s.T(), s.cache.repos)
}

func (s *ConfigCacheSuite) TestIncludeTarget() {
	tests := []struct {
		path, want string
		ok         bool
	}{
		{"~/a.cfg", "/home/a.cfg", true},
		{"~", "", false},
		{"~root/a.cfg", "", false},
		{"%(prefix)/a.cfg", "", false},
		{"", "", false},
		{"/abs/a.cfg", "/abs/a.cfg", true},
		{"../a.cfg", "/repo/.git/../a.cfg", true},
	}
	for _, tc := range tests {
		s.Run(tc.path, func() {
			got, ok := includeTarget(tc.path, "/repo/.git/config", "/home")
			require.Equal(s.T(), tc.ok, ok)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *ConfigCacheSuite) TestEnvValue() {
	require.Equal(s.T(), "b", envValue([]string{"HOME=a", "X", "HOME=b"}, "HOME"))
	require.Empty(s.T(), envValue(nil, "HOME"))
}
