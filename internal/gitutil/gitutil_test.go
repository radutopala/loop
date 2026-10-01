package gitutil

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GitUtilSuite struct {
	suite.Suite
}

func TestGitUtilSuite(t *testing.T) {
	suite.Run(t, new(GitUtilSuite))
}

func (s *GitUtilSuite) SetupTest() {
	// Keep the machine's own git config out of the picture: a global
	// credential helper or hooksPath would blur what the repo planted.
	s.T().Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	s.T().Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

// git runs plain, unhardened git — the way the repo was attacked before.
func (s *GitUtilSuite) git(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, "git %v: %s", args, out)
	return string(out)
}

// markers lists the files planted commands created in markerDir.
func (s *GitUtilSuite) markers(markerDir string) []string {
	entries, err := os.ReadDir(markerDir)
	require.NoError(s.T(), err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// plantedRepo builds a repo whose config, hooks and .gitattributes each try
// to run a command that drops a marker file into the returned marker dir:
// fsmonitor, hooks (both .git/hooks and a repo-set core.hooksPath), a
// required filter with clean/smudge/process commands, diff.external, a diff
// driver command and textconv, and core.askPass. a.bin and t.txt carry
// unstaged changes with a bumped mtime so status must re-read them.
func (s *GitUtilSuite) plantedRepo() (repo, markerDir string) {
	root := s.T().TempDir()
	repo = filepath.Join(root, "repo")
	markerDir = filepath.Join(root, "markers")
	require.NoError(s.T(), os.MkdirAll(markerDir, 0o755))
	s.git(root, "init", "-q", "-b", "main", repo)
	s.git(repo, "config", "user.email", "test@example.com")
	s.git(repo, "config", "user.name", "Test")
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "a.bin"), []byte("payload\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "t.txt"), []byte("text\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.bin filter=evil diff=evil\n"), 0o644))
	s.git(repo, "add", ".")
	s.git(repo, "commit", "-qm", "init")

	touch := func(name string) string { return "touch '" + filepath.Join(markerDir, name) + "'" }
	// core.askPass is exec'd without a shell, so it needs a script.
	askPass := filepath.Join(root, "askpass.sh")
	require.NoError(s.T(), os.WriteFile(askPass, []byte("#!/bin/sh\n"+touch("askpass")+"\n"), 0o755))
	for key, value := range map[string]string{
		"filter.evil.clean":    touch("clean") + "; cat",
		"filter.evil.smudge":   touch("smudge") + "; cat",
		"filter.evil.process":  touch("process"),
		"filter.evil.required": "true",
		"core.fsmonitor":       touch("fsmonitor") + "; false",
		"diff.external":        touch("external"),
		"diff.evil.command":    touch("diffcommand"),
		"diff.evil.textconv":   touch("textconv") + "; cat",
		"core.askPass":         askPass,
	} {
		s.git(repo, "config", key, value)
	}
	hookDir := filepath.Join(repo, ".git", "evilhooks")
	require.NoError(s.T(), os.MkdirAll(hookDir, 0o755))
	for _, dir := range []string{filepath.Join(repo, ".git", "hooks"), hookDir} {
		for _, hook := range []string{"post-checkout", "reference-transaction", "post-index-change", "pre-auto-gc"} {
			script := fmt.Sprintf("#!/bin/sh\n%s\n", touch("hook-"+hook))
			require.NoError(s.T(), os.WriteFile(filepath.Join(dir, hook), []byte(script), 0o755))
		}
	}
	s.git(repo, "config", "core.hooksPath", hookDir)

	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "a.bin"), []byte("payload\nmore\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "t.txt"), []byte("text\nmore\n"), 0o644))
	future := time.Now().Add(2 * time.Second)
	for _, f := range []string{"a.bin", "t.txt"} {
		require.NoError(s.T(), os.Chtimes(filepath.Join(repo, f), future, future))
	}
	require.Empty(s.T(), s.markers(markerDir), "setup must not trip the plants")
	return repo, markerDir
}

// TestPlantedCommandsNeverRun runs every git invocation shape the daemon uses
// against a booby-trapped repo. Plain git trips at least one plant for each
// (proving the plant is live); the hardened command trips none and still
// succeeds, which also proves an emptied filter command is a no-op.
func (s *GitUtilSuite) TestPlantedCommandsNeverRun() {
	noDiffDrivers := []string{"--no-ext-diff", "--no-textconv"}
	cases := []struct {
		name string
		args []string
		// plain is how unhardened git is attacked; nil reuses args.
		plain []string
		check func(repo, out string)
	}{
		{
			name: "status",
			args: []string{"status", "--porcelain=v2", "--branch", "--untracked-files=all", "-z"},
			check: func(_, out string) {
				require.Contains(s.T(), out, "a.bin")
				require.Contains(s.T(), out, "t.txt")
			},
		},
		{
			name: "diff",
			args: append([]string{"diff"}, noDiffDrivers...),
			// Without the flags, unhardened diff runs diff.external.
			plain: []string{"diff"},
			check: func(_, out string) {
				// The clean filter is a no-op: a.bin diffs as its raw bytes.
				require.Contains(s.T(), out, "+more")
				require.Contains(s.T(), out, "a.bin")
			},
		},
		{
			name:  "diff numstat",
			args:  append([]string{"diff", "--numstat", "-z"}, noDiffDrivers...),
			plain: []string{"diff", "--numstat", "-z"},
			check: func(_, out string) { require.Contains(s.T(), out, "1\t0\ta.bin") },
		},
		{
			name:  "show",
			args:  append([]string{"show"}, append(noDiffDrivers, "HEAD")...),
			plain: []string{"show", "--ext-diff", "HEAD"},
			check: func(_, out string) { require.Contains(s.T(), out, "+payload") },
		},
		{
			name:  "show blob",
			args:  []string{"show", "--no-textconv", "HEAD:a.bin"},
			plain: []string{"show", "--textconv", "HEAD:a.bin"},
			check: func(_, out string) { require.Equal(s.T(), "payload\n", out) },
		},
		{
			name:  "log patch",
			args:  append([]string{"log", "-p"}, noDiffDrivers...),
			plain: []string{"log", "-p", "--ext-diff"},
			check: func(_, out string) { require.Contains(s.T(), out, "+payload") },
		},
		{
			name: "checkout path",
			args: []string{"checkout", "--", "a.bin"},
			check: func(repo, _ string) {
				data, err := os.ReadFile(filepath.Join(repo, "a.bin"))
				require.NoError(s.T(), err)
				require.Equal(s.T(), "payload\n", string(data), "smudge must be a no-op")
			},
		},
		{
			name: "checkout branch",
			args: []string{"checkout", "-b", "feature"},
		},
		{
			name: "worktree add",
			args: []string{"worktree", "add", "-b", "wt", "../wt", "main"},
			check: func(repo, _ string) {
				data, err := os.ReadFile(filepath.Join(repo, "..", "wt", "a.bin"))
				require.NoError(s.T(), err)
				require.Equal(s.T(), "payload\n", string(data))
			},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			plain := tc.plain
			if plain == nil {
				plain = tc.args
			}
			repo, markerDir := s.plantedRepo()
			cmd := exec.Command("git", plain...)
			cmd.Dir = repo
			_ = cmd.Run() // Tripped plants may make it fail; only the markers matter.
			require.NotEmpty(s.T(), s.markers(markerDir), "plain git %v must trip a plant", plain)

			repo, markerDir = s.plantedRepo()
			out, err := Command(context.Background(), repo, tc.args...).Output()
			require.NoError(s.T(), err)
			require.Empty(s.T(), s.markers(markerDir))
			if tc.check != nil {
				tc.check(repo, string(out))
			}
		})
	}
}

// TestEnvironReachesNestedGit covers the gh path: a tool that spawns git on
// its own passes the hardening through by inheriting Environ.
func (s *GitUtilSuite) TestEnvironReachesNestedGit() {
	repo, markerDir := s.plantedRepo()
	cmd := exec.Command("sh", "-c", "git status --porcelain && git diff --no-ext-diff --no-textconv && git checkout -q -b nested")
	cmd.Dir = repo
	cmd.Env = Environ(context.Background(), repo)
	out, err := cmd.CombinedOutput()
	require.NoError(s.T(), err, string(out))
	require.Empty(s.T(), s.markers(markerDir))
}

// TestDiffWithoutFlagsFailsClosed documents why diff.external is emptied
// rather than left alone: a diff that forgets --no-ext-diff errors out
// instead of running the repo's command.
func (s *GitUtilSuite) TestDiffWithoutFlagsFailsClosed() {
	repo, markerDir := s.plantedRepo()
	require.Error(s.T(), Command(context.Background(), repo, "diff", "--", "t.txt").Run())
	require.Empty(s.T(), s.markers(markerDir))
}

// TestAskPassNeverRuns fetches from a remote that demands authentication with
// no credential helper configured, so git falls back to prompting — which
// tries core.askPass first.
func (s *GitUtilSuite) TestAskPassNeverRuns() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	plant := func() (string, string) {
		repo, markerDir := s.plantedRepo()
		s.git(repo, "remote", "add", "origin", srv.URL+"/repo.git")
		return repo, markerDir
	}

	repo, markerDir := plant()
	plain := exec.Command("git", "fetch", "-q", "origin")
	plain.Dir = repo
	_ = plain.Run()
	require.Contains(s.T(), s.markers(markerDir), "askpass")

	repo, markerDir = plant()
	require.Error(s.T(), Command(context.Background(), repo, "fetch", "-q", "origin").Run(),
		"with no prompt available git must give up")
	require.Empty(s.T(), s.markers(markerDir))
}

// TestAlternateRefsCommandNeverRuns fetches into a repo that borrows objects
// from an alternate and names a core.alternateRefsCommand to list its refs.
func (s *GitUtilSuite) TestAlternateRefsCommandNeverRuns() {
	for _, hardened := range []bool{false, true} {
		repo, markerDir := s.plantedRepo()
		clone := filepath.Join(filepath.Dir(repo), "clone")
		s.git(filepath.Dir(repo), "clone", "-q", "--shared", repo, clone)
		s.git(clone, "config", "core.alternateRefsCommand", "touch '"+filepath.Join(markerDir, "altrefs")+"'")
		out, err := Command(context.Background(), repo, "commit", "-qam", "next").CombinedOutput()
		require.NoError(s.T(), err, string(out))
		require.Empty(s.T(), s.markers(markerDir))

		if !hardened {
			cmd := exec.Command("git", "fetch", "-q", "origin")
			cmd.Dir = clone
			_ = cmd.Run()
			require.Contains(s.T(), s.markers(markerDir), "altrefs")
			continue
		}
		out, err = Command(context.Background(), clone, "fetch", "-q", "origin").CombinedOutput()
		require.NoError(s.T(), err, string(out))
		require.Empty(s.T(), s.markers(markerDir))
		require.Equal(s.T(), s.git(repo, "rev-parse", "HEAD"), s.git(clone, "rev-parse", "origin/main"))
	}
}

// TestExtProtocolBlocked makes sure an ext:: remote can't run its command,
// even when the repo re-allows the protocol.
func (s *GitUtilSuite) TestExtProtocolBlocked() {
	plant := func() (string, string) {
		repo, markerDir := s.plantedRepo()
		s.git(repo, "config", "protocol.ext.allow", "always")
		s.git(repo, "remote", "add", "evil", "ext::sh -c touch% "+filepath.Join(markerDir, "ext"))
		return repo, markerDir
	}
	repo, markerDir := plant()
	plain := exec.Command("git", "fetch", "-q", "evil")
	plain.Dir = repo
	_ = plain.Run()
	require.Contains(s.T(), s.markers(markerDir), "ext")

	repo, markerDir = plant()
	require.Error(s.T(), Command(context.Background(), repo, "fetch", "-q", "evil").Run())
	require.Empty(s.T(), s.markers(markerDir))
}

func (s *GitUtilSuite) TestCommandSetsDirAndArgs() {
	cmd := Command(context.Background(), "/some/dir", "status", "-z")
	require.Equal(s.T(), "/some/dir", cmd.Dir)
	require.Equal(s.T(), []string{"git", "status", "-z"}, cmd.Args)
	require.NotContains(s.T(), cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "system config is the host's")
}

// TestRepoSnapshotsConfig covers Open: every command a Repo makes shares the
// environment read at Open, and appending to one command's Env leaves the
// next command's alone.
func (s *GitUtilSuite) TestRepoSnapshotsConfig() {
	repo := s.T().TempDir()
	s.git(repo, "init", "-q")
	r := Open(context.Background(), repo)
	require.Equal(s.T(), repo, r.Dir())
	require.NotContains(s.T(), strings.Join(r.env, "\n"), "filter.planted")

	// A filter added after Open isn't in the snapshot: callers open a new
	// Repo after anything that may change the config.
	s.git(repo, "config", "filter.planted.clean", "touch planted")
	first := r.Command(context.Background(), "status")
	require.Equal(s.T(), repo, first.Dir)
	require.Equal(s.T(), r.env, first.Env)
	require.Contains(s.T(), strings.Join(Command(context.Background(), repo, "status").Env, "\n"), "filter.planted.clean")

	first.Env = append(first.Env, "EXTRA=1")
	second := r.Command(context.Background(), "diff")
	require.Equal(s.T(), []string{"git", "diff"}, second.Args)
	require.Equal(s.T(), r.env, second.Env)
	require.NotContains(s.T(), second.Env, "EXTRA=1")
}

// TestStatusRefreshesIndex guards the poller's cost: once a file's stat data
// in the index is stale, hardened `git status` must write the refreshed index
// back, or every later call re-hashes the file.
func (s *GitUtilSuite) TestStatusRefreshesIndex() {
	s.T().Setenv("GIT_OPTIONAL_LOCKS", "0")
	repo := s.T().TempDir()
	s.git(repo, "init", "-q")
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644))
	s.git(repo, "add", "a.txt")
	s.git(repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "a")

	// Same content, older mtime: the index entry no longer matches.
	old := time.Now().Add(-time.Hour)
	require.NoError(s.T(), os.Chtimes(filepath.Join(repo, "a.txt"), old, old))
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	require.NoError(s.T(), err)

	require.NoError(s.T(), Command(context.Background(), repo, "status", "--porcelain").Run())
	after, err := os.ReadFile(index)
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), before, after, "status should rewrite the index with the refreshed stat data")
}

// filterNames lists the filter drivers repoOverrides disarms in repo.
func filterNames(repo string) []string {
	var names []string
	for _, kv := range repoOverrides(readConfig(context.Background(), repo)) {
		if name, ok := strings.CutSuffix(strings.TrimPrefix(kv[0], "filter."), ".clean"); ok && strings.HasPrefix(kv[0], "filter.") {
			names = append(names, name)
		}
	}
	return names
}

func (s *GitUtilSuite) TestReadConfigNonRepo() {
	require.Empty(s.T(), readConfig(context.Background(), s.T().TempDir()))
}

func (s *GitUtilSuite) TestFilterDriversFollowsIncludes() {
	repo, _ := s.plantedRepo()
	inc := filepath.Join(filepath.Dir(repo), "extra.gitconfig")
	require.NoError(s.T(), os.WriteFile(inc, []byte("[filter \"a.b c\"]\n\tclean = x\n"), 0o644))
	s.git(repo, "config", "include.path", inc)
	require.Equal(s.T(), []string{"evil", "a.b c"}, filterNames(repo))
}

// TestGlobalFilterDriversKeepWorking covers git-lfs style drivers the user
// installed in their global config: those are the host's own and must still
// run, unless the repo touches the driver, which disarms all of it.
func (s *GitUtilSuite) TestGlobalFilterDriversKeepWorking() {
	repo, markerDir := s.plantedRepo()
	global := filepath.Join(filepath.Dir(repo), "global.gitconfig")
	touch := func(name string) string { return "touch '" + filepath.Join(markerDir, name) + "'; cat" }
	cfg := fmt.Sprintf("[filter \"host\"]\n\tclean = %s\n[filter \"lfs\"]\n\tclean = %s\n", touch("host"), touch("lfs"))
	require.NoError(s.T(), os.WriteFile(global, []byte(cfg), 0o644))
	s.T().Setenv("GIT_CONFIG_GLOBAL", global)
	s.git(repo, "config", "filter.lfs.smudge", "cat")
	require.Equal(s.T(), []string{"evil", "lfs"}, filterNames(repo))

	attrs := "*.bin filter=evil diff=evil\n*.host filter=host\n*.lfs filter=lfs\n"
	require.NoError(s.T(), os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte(attrs), 0o644))
	for _, f := range []string{"x.host", "x.lfs"} {
		require.NoError(s.T(), os.WriteFile(filepath.Join(repo, f), []byte("data\n"), 0o644))
	}
	out, err := Command(context.Background(), repo, "add", "x.host", "x.lfs").CombinedOutput()
	require.NoError(s.T(), err, string(out))
	require.Equal(s.T(), []string{"host"}, s.markers(markerDir))
}

// TestCredentialHelpers fetches from a remote that demands authentication.
// Helpers the repo adds, generic or for the remote's URL, never run; the
// host's system and global helpers still do, in git's order.
func (s *GitUtilSuite) TestCredentialHelpers() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	// A helper appends its name to a log, so the order it ran in shows.
	helper := func(markerDir, name string) string {
		return "!echo " + name + " >> '" + filepath.Join(markerDir, "helpers") + "'; true"
	}
	plant := func() (string, string) {
		repo, markerDir := s.plantedRepo()
		root := filepath.Dir(repo)
		system := filepath.Join(root, "system.gitconfig")
		global := filepath.Join(root, "global.gitconfig")
		require.NoError(s.T(), os.WriteFile(system, fmt.Appendf(nil, "[credential]\n\thelper = %q\n", helper(markerDir, "system")), 0o644))
		require.NoError(s.T(), os.WriteFile(global, fmt.Appendf(nil, "[credential %q]\n\thelper = %q\n", srv.URL, helper(markerDir, "global")), 0o644))
		s.T().Setenv("GIT_CONFIG_SYSTEM", system)
		s.T().Setenv("GIT_CONFIG_GLOBAL", global)
		s.git(repo, "remote", "add", "origin", srv.URL+"/repo.git")
		s.git(repo, "config", "credential.helper", helper(markerDir, "repo"))
		s.git(repo, "config", "credential."+srv.URL+".helper", helper(markerDir, "repo-url"))
		return repo, markerDir
	}
	helpersRun := func(markerDir string) string {
		data, _ := os.ReadFile(filepath.Join(markerDir, "helpers"))
		return string(data)
	}

	repo, markerDir := plant()
	plain := exec.Command("git", "fetch", "-q", "origin")
	plain.Dir = repo
	plain.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	_ = plain.Run()
	require.Contains(s.T(), helpersRun(markerDir), "repo")

	repo, markerDir = plant()
	require.Error(s.T(), Command(context.Background(), repo, "fetch", "-q", "origin").Run())
	require.Equal(s.T(), "system\nglobal\n", helpersRun(markerDir))
}

// TestRepoTransportCommandsNeverRun plants the other keys that name a program
// git runs to reach a remote: the ssh command, the remote's upload-pack, and
// the git:// proxy.
func (s *GitUtilSuite) TestRepoTransportCommandsNeverRun() {
	cases := []struct {
		name  string
		plant func(repo, touch string)
	}{
		{
			name: "core.sshCommand",
			plant: func(repo, touch string) {
				s.git(repo, "remote", "add", "origin", "ssh://127.0.0.1:1/repo.git")
				s.git(repo, "config", "core.sshCommand", touch+"; false")
			},
		},
		{
			name: "remote uploadpack",
			plant: func(repo, touch string) {
				upstream := filepath.Join(filepath.Dir(repo), "upstream")
				s.git(filepath.Dir(repo), "clone", "-q", "--bare", repo, upstream)
				s.git(repo, "remote", "add", "origin", upstream)
				s.git(repo, "config", "remote.origin.uploadpack", touch+"; git-upload-pack")
			},
		},
		{
			name: "core.gitProxy",
			plant: func(repo, touch string) {
				script := filepath.Join(filepath.Dir(repo), "proxy.sh")
				require.NoError(s.T(), os.WriteFile(script, []byte("#!/bin/sh\n"+touch+"\n"), 0o755))
				s.git(repo, "remote", "add", "origin", "git://127.0.0.1:1/repo.git")
				s.git(repo, "config", "core.gitProxy", script)
			},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			plant := func() (string, string) {
				repo, markerDir := s.plantedRepo()
				tc.plant(repo, "touch '"+filepath.Join(markerDir, "transport")+"'")
				return repo, markerDir
			}
			repo, markerDir := plant()
			plain := exec.Command("git", "fetch", "-q", "origin")
			plain.Dir = repo
			_ = plain.Run()
			require.Contains(s.T(), s.markers(markerDir), "transport")

			repo, markerDir = plant()
			require.Error(s.T(), Command(context.Background(), repo, "fetch", "-q", "origin").Run())
			require.Empty(s.T(), s.markers(markerDir))
		})
	}
}

func (s *GitUtilSuite) TestBuildEnviron() {
	env := buildEnviron([]string{
		"PATH=/bin",
		"GIT_EXTERNAL_DIFF=evil",
		"GIT_EXEC_PATH=/evil",
		"GIT_CONFIG_PARAMETERS='core.fsmonitor'='evil'",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=evil",
		"GIT_PAGER=less",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=kept",
	}, [][2]string{{"filter.lfs.required", "false"}})

	require.Equal(s.T(), []string{"PATH=/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=kept"}, env[:3])
	require.Equal(s.T(), hardenedEnv, env[3:3+len(hardenedEnv)])
	cfg := env[3+len(hardenedEnv):]
	n := len(hardenedConfig) + 1
	require.Equal(s.T(), fmt.Sprintf("GIT_CONFIG_COUNT=%d", n), cfg[0])
	require.Len(s.T(), cfg, 1+2*n)
	require.Contains(s.T(), cfg, "GIT_CONFIG_KEY_0=core.fsmonitor")
	require.Contains(s.T(), cfg, "GIT_CONFIG_VALUE_0=false")
	require.Contains(s.T(), cfg, fmt.Sprintf("GIT_CONFIG_KEY_%d=filter.lfs.required", n-1))
	require.Contains(s.T(), cfg, fmt.Sprintf("GIT_CONFIG_VALUE_%d=false", n-1))
}

func (s *GitUtilSuite) TestParseConfig() {
	cases := []struct {
		name string
		in   string
		want []configEntry
	}{
		{name: "empty", in: "", want: nil},
		{
			name: "entries",
			in:   "system\x00credential.helper\nosxkeychain\x00local\x00filter.x.required\x00local\x00core.sshcommand\nssh -i a\nb\x00",
			want: []configEntry{
				{scope: "system", key: "credential.helper", value: "osxkeychain"},
				{scope: "local", key: "filter.x.required"},
				{scope: "local", key: "core.sshcommand", value: "ssh -i a\nb"},
			},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, parseConfig(tc.in))
		})
	}
}

func (s *GitUtilSuite) TestRepoOverrides() {
	filter := func(name string) [][2]string {
		return [][2]string{
			{"filter." + name + ".clean", ""},
			{"filter." + name + ".smudge", ""},
			{"filter." + name + ".process", ""},
			{"filter." + name + ".required", "false"},
		}
	}
	cases := []struct {
		name string
		in   []configEntry
		want [][2]string
	}{
		{name: "empty", in: nil, want: nil},
		{
			name: "filters: deduped, host ones and malformed keys skipped",
			in: []configEntry{
				{scope: "local", key: "filter.lfs.clean"},
				{scope: "worktree", key: "filter.lfs.smudge"},
				{scope: "system", key: "filter.a.clean"},
				{scope: "global", key: "filter.b.clean"},
				{scope: "local", key: "filter.a.b.process"},
				{scope: "local", key: "filter.clean"},
				{scope: "local", key: "filter..clean"},
			},
			want: append(filter("lfs"), filter("a.b")...),
		},
		{
			name: "host helpers alone are left as they are",
			in: []configEntry{
				{scope: "system", key: "credential.helper", value: "osxkeychain"},
				{scope: "global", key: "credential.https://h.helper", value: "gh"},
			},
			want: nil,
		},
		{
			name: "a repo helper clears every helper key, then the host's come back in order",
			in: []configEntry{
				{scope: "system", key: "credential.helper", value: "osxkeychain"},
				{scope: "global", key: "credential.https://h.helper", value: "gh"},
				{scope: "local", key: "credential.https://evil.helper", value: "!evil"},
				{scope: "global", key: "credential.helper", value: "store"},
			},
			want: [][2]string{
				{"credential.helper", ""},
				{"credential.https://h.helper", ""},
				{"credential.https://evil.helper", ""},
				{"credential.helper", "osxkeychain"},
				{"credential.https://h.helper", "gh"},
				{"credential.helper", "store"},
			},
		},
		{
			name: "repo transport commands",
			in: []configEntry{
				{scope: "local", key: "core.sshcommand", value: "evil"},
				{scope: "local", key: "core.sshcommand", value: "evil2"},
				{scope: "local", key: "core.gitproxy", value: "evil"},
				{scope: "local", key: "core.gitproxy", value: "evil for x"},
				{scope: "local", key: "remote.origin.uploadpack", value: "evil"},
				{scope: "local", key: "remote.Up.Stream.receivepack", value: "evil"},
			},
			want: [][2]string{
				{"core.sshCommand", "ssh"},
				{"protocol.git.allow", "never"},
				{"protocol.file.allow", "never"},
			},
		},
		{
			name: "the host's ssh command wins over the repo's",
			in: []configEntry{
				{scope: "global", key: "core.sshcommand", value: "ssh -F host"},
				{scope: "local", key: "core.sshcommand", value: "evil"},
			},
			want: [][2]string{{"core.sshCommand", "ssh -F host"}},
		},
		{
			name: "host transport commands are left as they are",
			in: []configEntry{
				{scope: "global", key: "core.sshcommand", value: "ssh -F host"},
				{scope: "system", key: "core.gitproxy", value: "proxy"},
				{scope: "global", key: "remote.origin.uploadpack", value: "up"},
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, repoOverrides(tc.in))
		})
	}
}
