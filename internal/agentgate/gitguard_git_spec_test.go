package agentgate

// Spec: whatever the agent writes, moves or deletes under a guarded root,
// once the git guard has had its say, real git run there afterwards runs no
// program and loads no file the guard didn't review. The commands are
// everyday local ones: status, diff, log -p, add -A, commit and checkout
// in the repo; status, diff and commit in its linked worktree, its
// submodule, and every directory the agent's ops touched outside a .git
// dir (a dir the agent turned into a git dir included). Fetching commands
// are out of scope, and so are git dirs that were already there: the
// fixture has none but .git and its modules and worktrees.
// The git is plain, not the daemon's hardened gitutil.Command: the guard
// protects the user's own git on the host, which has no such hardening.
//
// The world is a sandbox: HOME and GIT_CONFIG_GLOBAL point at an empty
// home, GIT_CONFIG_NOSYSTEM is set, and a fixture repo has a commit, a
// submodule and a linked worktree. A probe script appends to a marker
// file when anything executes it. Next to the repo, outside the roots, a
// lure git dir has a config whose core.fsmonitor and core.hooksPath run
// the probe, so a write that makes git load a config or git dir the guard
// never saw (include.path, commondir, a gitdir: pointer) also shows up as
// a probe run.
//
// The agent's ops go through a gitWriteGate, which for the real guard
// routes them the way the gate does (gateModel): a write to a guarded path
// can only arrive by a rename, so it goes to TreeRename and then Rename;
// one to an unguarded path happens in place; mkdir and delete are never
// stopped by the guard. No approver is set, so anything the guard would
// ask about is refused: the property is about what goes through silently.
// The spec can fail: TestSpecCanFail runs every danger seed through a gate
// that allows everything (allowAllGate) and requires a probe run for each.
//
// Trusted base: the git on PATH (2.47 when written; the test logs the
// version), /bin/sh running the probe, and gateModel's routing, which
// mirrors Server.handleFile's calls into the guard.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
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

// specGit returns the git binary for a differential spec, skipping the
// test when git isn't installed.
func specGit(tb testing.TB) string {
	tb.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		tb.Skip("git not installed")
	}
	return git
}

// specGitEnv is an environment where git reads no config but the repo's
// own: an empty HOME, an empty global config and no system config. Every
// inherited GIT_* variable is dropped, so none can point git elsewhere.
func specGitEnv(home string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_CONFIG_HOME=")
	})
	return append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=spec", "GIT_AUTHOR_EMAIL=spec@example.com",
		"GIT_COMMITTER_NAME=spec", "GIT_COMMITTER_EMAIL=spec@example.com",
	)
}

// gitWriteGate decides the agent's ops on the guarded root.
type gitWriteGate interface {
	// Write reports whether the agent may give path the bytes data.
	Write(path string, data []byte) bool
	// Move reports whether the agent may rename src to dst.
	Move(src, dst string) bool
}

// gateModel routes ops to a GitGuard the way Server.handleFile does.
type gateModel struct {
	guard *GitGuard
	stage string // where a write's bytes wait before the rename
}

func (m gateModel) Write(path string, data []byte) bool {
	if !m.guard.Protects(path) {
		return true // in place; the guard doesn't see it
	}
	// In place, a guarded path is refused or asks (a pointer); with no
	// approver, only a rename can bring content there.
	if err := os.WriteFile(m.stage, data, 0o600); err != nil {
		return false
	}
	return m.Move(m.stage, path)
}

func (m gateModel) Move(src, dst string) bool {
	r := GuardRename{TrapID: 1, PID: 1, Src: src, Dst: dst, Tracee: &FakeTracee{UID: os.Getuid(), GID: os.Getgid()}}
	if _, refused := m.guard.TreeRename(r); refused {
		return false
	}
	if !m.guard.Protects(dst) {
		return true
	}
	return m.guard.Rename(context.Background(), r).Performed
}

// allowAllGate is a broken guard: it lets every op through.
type allowAllGate struct{}

func (allowAllGate) Write(string, []byte) bool { return true }
func (allowAllGate) Move(string, string) bool  { return true }

// diskGuardFS reads what the guard asks for from disk, like OSGuardFS
// without the uid switch. Install does nothing: the spec performs an
// allowed op itself, with the same bytes.
type diskGuardFS struct{}

func (diskGuardFS) Snapshot(src, dst string, _, _ int) (*GuardSnapshot, error) {
	fi, err := os.Lstat(src)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, ErrGuardNotRegular
	}
	if fi.Size() > maxGuardedFileSize {
		return nil, ErrGuardTooLarge
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	snap := &GuardSnapshot{Src: src, Dst: dst, Data: data, Mode: fi.Mode()}
	switch dfi, err := os.Lstat(dst); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	case !dfi.Mode().IsRegular():
		return nil, ErrGuardNotRegular
	default:
		if snap.Old, err = os.ReadFile(dst); err != nil {
			return nil, err
		}
		snap.OldExists = true
	}
	return snap, nil
}

func (diskGuardFS) Install(*GuardSnapshot, bool) error { return nil }

func (diskGuardFS) IsRegular(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode().IsRegular(), nil
}

// gitSpecOp is one agent op from a script:
//
//	--- write <path>      the lines up to the next "---" are the content
//	--- mv <src> <dst>
//	--- rm <path>
//	--- mkdir <path>
//
// Paths are relative to the repo. In content, @PROBE@, @LURE@, @HOOKS@
// and @ROOT@ stand for the probe, the lure git dir, a hooks dir where every
// hook is the probe, and the repo.
type gitSpecOp struct {
	verb string
	args []string
	data []byte
}

func parseGitSpecScript(script string) []gitSpecOp {
	var ops []gitSpecOp
	for _, line := range strings.Split(script, "\n") {
		if rest, ok := strings.CutPrefix(line, "--- "); ok {
			f := strings.Fields(rest)
			if len(f) == 0 {
				continue
			}
			ops = append(ops, gitSpecOp{verb: f[0], args: f[1:]})
			continue
		}
		if n := len(ops); n > 0 {
			ops[n-1].data = append(append(ops[n-1].data, line...), '\n')
		}
	}
	return ops
}

// gitSpecHooks are the hooks the commands could run; the hooks dir has
// each of them as the probe.
var gitSpecHooks = []string{
	"pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit",
	"post-checkout", "post-merge", "pre-rebase", "post-rewrite", "reference-transaction",
	"post-index-change", "pre-auto-gc", "fsmonitor-watchman",
}

// gitSpecEnv is the sandbox for one fuzz worker or test.
type gitSpecEnv struct {
	git                         string
	env                         []string
	root, lure, hooks, probe    string
	marker, stage               string
	rootFixture, lureFixture    string
	replacements                *strings.Replacer
	fullCommands, shortCommands [][]string
}

func newGitSpecEnv(tb testing.TB) *gitSpecEnv {
	tb.Helper()
	git := specGit(tb)
	sb := tb.TempDir()
	e := &gitSpecEnv{
		git:         git,
		env:         specGitEnv(filepath.Join(sb, "home")),
		root:        filepath.Join(sb, "repo"),
		lure:        filepath.Join(sb, "lure"),
		hooks:       filepath.Join(sb, "hooks"),
		probe:       filepath.Join(sb, "probe"),
		marker:      filepath.Join(sb, "marker"),
		stage:       filepath.Join(sb, "stage"),
		rootFixture: filepath.Join(sb, "fixture", "repo"),
		lureFixture: filepath.Join(sb, "fixture", "lure"),
		fullCommands: [][]string{
			{"status", "--porcelain"},
			{"diff"},
			{"log", "-p", "-3"},
			{"add", "-A"},
			{"commit", "-q", "--allow-empty", "-m", "spec"},
			{"checkout", "-q", "-b", "spec-side"},
			{"checkout", "-q", "-"},
		},
		shortCommands: [][]string{
			{"status", "--porcelain"},
			{"diff"},
			{"commit", "-q", "--allow-empty", "-m", "spec"},
		},
	}
	e.replacements = strings.NewReplacer("@PROBE@", e.probe, "@LURE@", e.lure, "@HOOKS@", e.hooks, "@ROOT@", e.root)
	out, err := exec.Command(git, "--version").Output()
	require.NoError(tb, err)
	tb.Logf("trusted base: %s", bytes.TrimSpace(out))

	require.NoError(tb, os.MkdirAll(filepath.Join(sb, "home"), 0o755))
	require.NoError(tb, os.WriteFile(filepath.Join(sb, "home", ".gitconfig"), nil, 0o644))
	probe := "#!/bin/sh\necho \"$0 $* in $PWD\" >> '" + e.marker + "'\nexit 0\n"
	require.NoError(tb, os.WriteFile(e.probe, []byte(probe), 0o755))
	require.NoError(tb, os.MkdirAll(e.hooks, 0o755))
	for _, h := range gitSpecHooks {
		require.NoError(tb, os.WriteFile(filepath.Join(e.hooks, h), []byte(probe), 0o755))
	}

	upstream := filepath.Join(sb, "upstream")
	e.mustGit(tb, sb, "init", "-q", "-b", "main", upstream)
	require.NoError(tb, os.WriteFile(filepath.Join(upstream, "u.txt"), []byte("u\n"), 0o644))
	e.mustGit(tb, upstream, "add", "u.txt")
	e.mustGit(tb, upstream, "commit", "-q", "-m", "upstream")

	e.mustGit(tb, sb, "init", "-q", "-b", "main", e.root)
	require.NoError(tb, os.WriteFile(filepath.Join(e.root, "a.txt"), []byte("a\n"), 0o644))
	require.NoError(tb, os.WriteFile(filepath.Join(e.root, "b.txt"), []byte("b\n"), 0o644))
	e.mustGit(tb, e.root, "add", "a.txt", "b.txt")
	e.mustGit(tb, e.root, "commit", "-q", "-m", "init")
	e.mustGit(tb, e.root, "-c", "protocol.file.allow=always", "submodule", "add", "-q", upstream, "sub")
	e.mustGit(tb, e.root, "commit", "-q", "-m", "sub")
	exclude := filepath.Join(e.root, ".git", "info", "exclude")
	require.NoError(tb, os.WriteFile(exclude, []byte("wt/\n"), 0o644))
	e.mustGit(tb, e.root, "worktree", "add", "-q", "-b", "side", "wt")

	e.mustGit(tb, sb, "init", "-q", "--bare", e.lure)
	lureConfig := "[core]\n\trepositoryformatversion = 0\n\tbare = false\n" +
		"\tfsmonitor = " + e.probe + "\n\thooksPath = " + e.hooks + "\n"
	require.NoError(tb, os.WriteFile(filepath.Join(e.lure, "config"), []byte(lureConfig), 0o644))

	require.NoError(tb, os.CopyFS(e.rootFixture, os.DirFS(e.root)))
	require.NoError(tb, os.CopyFS(e.lureFixture, os.DirFS(e.lure)))
	return e
}

func (e *gitSpecEnv) mustGit(tb testing.TB, dir string, args ...string) {
	tb.Helper()
	cmd := exec.Command(e.git, args...)
	cmd.Dir, cmd.Env = dir, e.env
	out, err := cmd.CombinedOutput()
	require.NoError(tb, err, "git %v: %s", args, out)
}

// reset puts the repo and the lure back as the fixture left them; the
// fixture's absolute paths (a worktree's gitdir) stay valid.
func (e *gitSpecEnv) reset(tb testing.TB) {
	tb.Helper()
	for _, p := range [][2]string{{e.root, e.rootFixture}, {e.lure, e.lureFixture}} {
		require.NoError(tb, os.RemoveAll(p[0]))
		require.NoError(tb, os.CopyFS(p[0], os.DirFS(p[1])))
	}
	require.NoError(tb, os.RemoveAll(e.marker))
}

// path maps a script path into the repo. ok=false for the repo itself and
// anything outside it.
func (e *gitSpecEnv) path(rel string) (string, bool) {
	if !filepath.IsLocal(rel) || filepath.Clean(rel) == "." {
		return "", false
	}
	return filepath.Join(e.root, rel), true
}

// guard is the guard the gate runs, rooted at the repo, with no approver.
func (e *gitSpecEnv) guard() gateModel {
	return gateModel{guard: &GitGuard{Roots: []string{e.root}, FS: diskGuardFS{}}, stage: e.stage}
}

// maxGitSpecOps bounds a script's ops, so each fuzz run stays quick.
const maxGitSpecOps = 8

// apply performs the ops gate allows and returns the dirs they touched.
func (e *gitSpecEnv) apply(gate gitWriteGate, ops []gitSpecOp) []string {
	var touched []string
	for _, op := range ops {
		var paths []string
		for _, a := range op.args {
			p, ok := e.path(a)
			if !ok {
				paths = nil
				break
			}
			paths = append(paths, p)
		}
		switch {
		case op.verb == "write" && len(paths) == 1:
			data := []byte(e.replacements.Replace(string(op.data)))
			_ = os.MkdirAll(filepath.Dir(paths[0]), 0o755) // mkdir: never the guard's
			if gate.Write(paths[0], data) {
				mode := os.FileMode(0o644)
				if bytes.HasPrefix(data, []byte("#!")) {
					mode = 0o755 // chmod: never the guard's
				}
				_ = os.WriteFile(paths[0], data, mode)
			}
			touched = append(touched, filepath.Dir(paths[0]))
		case op.verb == "mv" && len(paths) == 2:
			_ = os.MkdirAll(filepath.Dir(paths[1]), 0o755)
			if gate.Move(paths[0], paths[1]) {
				_ = os.Rename(paths[0], paths[1])
			}
			touched = append(touched, filepath.Dir(paths[0]), paths[1], filepath.Dir(paths[1]))
		case op.verb == "rm" && len(paths) == 1:
			_ = os.RemoveAll(paths[0]) // delete: never the guard's
			touched = append(touched, filepath.Dir(paths[0]))
		case op.verb == "mkdir" && len(paths) == 1:
			_ = os.MkdirAll(paths[0], 0o755)
			touched = append(touched, paths[0], filepath.Dir(paths[0]))
		}
	}
	return touched
}

// runDirs is where the commands run: the repo, its worktree and submodule
// with the full or short list, then each touched dir outside a .git dir.
func (e *gitSpecEnv) runDirs(touched []string) [][2]any {
	dirs := [][2]any{
		{e.root, e.fullCommands},
		{filepath.Join(e.root, "wt"), e.shortCommands},
		{filepath.Join(e.root, "sub"), e.shortCommands},
	}
	seen := map[string]bool{e.root: true, filepath.Join(e.root, "wt"): true, filepath.Join(e.root, "sub"): true}
	for _, d := range touched {
		rel, err := filepath.Rel(e.root, d)
		if err != nil || seen[d] || slices.ContainsFunc(strings.Split(rel, string(filepath.Separator)), func(c string) bool {
			return strings.EqualFold(c, ".git")
		}) {
			continue
		}
		seen[d] = true
		dirs = append(dirs, [2]any{d, e.shortCommands})
	}
	return dirs
}

// check runs script through gate, then git, and returns what the probe
// logged: empty when nothing ran it.
func (e *gitSpecEnv) check(tb testing.TB, gate gitWriteGate, script string) string {
	tb.Helper()
	e.reset(tb)
	ops := parseGitSpecScript(script)
	if len(ops) > maxGitSpecOps {
		ops = ops[:maxGitSpecOps]
	}
	touched := e.apply(gate, ops)
	// The user edits tracked files, so status, diff and add have work.
	for _, f := range []string{"a.txt", "wt/a.txt", "sub/u.txt"} {
		if fh, err := os.OpenFile(filepath.Join(e.root, f), os.O_APPEND|os.O_WRONLY, 0); err == nil {
			_, _ = fh.WriteString("edit\n")
			_ = fh.Close()
		}
	}
	for _, d := range e.runDirs(touched) {
		dir := d[0].(string)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		for _, args := range d[1].([][]string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, e.git, args...)
			cmd.Dir, cmd.Env = dir, e.env
			_ = cmd.Run() // a failing command is fine; only the probe counts
			cancel()
			if got := e.probeLog(tb); got != "" {
				return got // one run is a counterexample; the rest can't undo it
			}
		}
	}
	return ""
}

// probeLog is what the probe has logged so far.
func (e *gitSpecEnv) probeLog(tb testing.TB) string {
	tb.Helper()
	got, err := os.ReadFile(e.marker)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	require.NoError(tb, err)
	return string(got)
}

// gitGuardDangerSeeds each make git run the probe once written as-is. The
// guard must stop every one; the broken guard must not.
var gitGuardDangerSeeds = []struct{ name, script string }{
	{"fsmonitor", "--- write .git/config\n[core]\n\tfsmonitor = @PROBE@\n"},
	{"hooksPath", "--- write .git/config\n[core]\n\thooksPath = @HOOKS@\n"},
	{"pre-commit hook", "--- write .git/hooks/pre-commit\n#!/bin/sh\n@PROBE@\n"},
	{"post-checkout hook", "--- write .git/hooks/post-checkout\n#!/bin/sh\n@PROBE@\n"},
	{"clean filter", "--- write .gitattributes\n* filter=spec\n--- write .git/config\n[filter \"spec\"]\n\tclean = @PROBE@\n\tsmudge = @PROBE@\n"},
	{"textconv", "--- write .git/info/attributes\n* diff=spec\n--- write .git/config\n[diff \"spec\"]\n\ttextconv = @PROBE@\n"},
	{"diff driver command", "--- write .gitattributes\n*.txt diff=spec\n--- write .git/config\n[diff \"spec\"]\n\tcommand = @PROBE@\n"},
	{"diff.external", "--- write .git/config\n[diff]\n\texternal = @PROBE@\n"},
	{"include.path", "--- write .git/config\n[include]\n\tpath = @LURE@/config\n"},
	{"includeIf", "--- write .git/config\n[includeIf \"gitdir:/\"]\n\tpath = @LURE@/config\n"},
	{"gpg.program", "--- write .git/config\n[commit]\n\tgpgSign = true\n[gpg]\n\tprogram = @PROBE@\n"},
	{"worktree config", "--- write .git/config\n[core]\n\trepositoryformatversion = 1\n[extensions]\n\tworktreeConfig = true\n--- write .git/config.worktree\n[core]\n\tfsmonitor = @PROBE@\n"},
	{"commondir", "--- write .git/commondir\n@LURE@\n"},
	{"worktree gitdir pointer", "--- write wt/.git\ngitdir: @LURE@\n"},
	{"submodule config", "--- write .git/modules/sub/config\n[core]\n\tworktree = ../../../sub\n\tfsmonitor = @PROBE@\n"},
	{"submodule hook", "--- write .git/modules/sub/hooks/pre-commit\n#!/bin/sh\n@PROBE@\n"},
	{"linked worktree commondir", "--- write .git/worktrees/wt/commondir\n@LURE@\n"},
	{"bare repo in a subdir", "--- mkdir evil/objects\n--- mkdir evil/refs\n--- write evil/HEAD\nref: refs/heads/main\n--- write evil/config\n[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = ..\n\tfsmonitor = @PROBE@\n"},
	{"bare repo at the root", "--- rm .git\n--- mkdir objects\n--- mkdir refs\n--- write HEAD\nref: refs/heads/main\n--- write config\n[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = sub\n\tfsmonitor = @PROBE@\n"},
	{"git dir moved out", "--- mv .git gd\n--- write gd/config\n[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = ..\n\tfsmonitor = @PROBE@\n"},
	{"submodule git dir moved out", "--- mv .git/modules/sub evil\n--- write evil/config\n[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = ../sub\n\tfsmonitor = @PROBE@\n"},
	{"HEAD moved out", "--- mkdir evil/objects\n--- mkdir evil/refs\n--- mv .git/HEAD evil/HEAD\n--- write evil/config\n[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = ..\n\tfsmonitor = @PROBE@\n"},
}

// gitSpecPoolSize is how many sandboxes the fuzz target keeps, so its seeds
// run in parallel; while fuzzing, inputs run one at a time per worker.
const gitSpecPoolSize = 4

// FuzzGitGuardMatchesGit checks the property for any script. The danger
// seeds above are the starting corpus, with more under
// testdata/fuzz.
func FuzzGitGuardMatchesGit(f *testing.F) {
	pool := make(chan *gitSpecEnv, gitSpecPoolSize)
	for range gitSpecPoolSize {
		pool <- newGitSpecEnv(f)
	}
	for _, s := range gitGuardDangerSeeds {
		f.Add(s.script)
	}
	f.Fuzz(func(t *testing.T, script string) {
		t.Parallel()
		e := <-pool
		defer func() { pool <- e }()
		if got := e.check(t, e.guard(), script); got != "" {
			t.Fatalf("git ran the probe after a script the guard allowed:\n%s\nprobe log:\n%s", script, got)
		}
	})
}

type GitGuardGitSpecSuite struct {
	suite.Suite
}

func TestGitGuardGitSpecSuite(t *testing.T) {
	suite.Run(t, new(GitGuardGitSpecSuite))
}

func (s *GitGuardGitSpecSuite) SetupSuite() {
	specGit(s.T())
}

// runEach runs check for each script in parallel, each in its own sandbox,
// and hands want the probe log.
func (s *GitGuardGitSpecSuite) runEach(cases []struct{ name, script string }, gate func(*gitSpecEnv) gitWriteGate, want func(t *testing.T, log string)) {
	for _, c := range cases {
		s.T().Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newGitSpecEnv(t)
			want(t, e.check(t, gate(e), c.script))
		})
	}
}

// The spec must be able to fail: with a guard that allows everything,
// every danger seed makes git run the probe. This also proves each seed
// reaches git the way its name says.
func (s *GitGuardGitSpecSuite) TestSpecCanFail() {
	s.runEach(gitGuardDangerSeeds, func(*gitSpecEnv) gitWriteGate { return allowAllGate{} }, func(t *testing.T, log string) {
		require.NotEmpty(t, log)
	})
}

// Writes git itself makes, and the guard lets through, still run nothing.
func (s *GitGuardGitSpecSuite) TestAllowedWritesRunNothing() {
	cases := []struct{ name, script string }{
		{"remote and branch keys", "--- write .git/config\n[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n\tlogallrefupdates = true\n[remote \"origin\"]\n\turl = https://example.com/a/b.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n[user]\n\tname = A\n\temail = a@example.com\n[pull]\n\trebase = interactive\n"},
		{"submodule worktree", "--- write .git/modules/sub/config\n[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tworktree = ../../../sub\n"},
		{"attributes without drivers", "--- write .gitattributes\n* filter=spec diff=spec merge=spec\n--- write .git/info/attributes\n* filter=lure diff=lure\n"},
		{"gitmodules update command", "--- write .gitmodules\n[submodule \"sub\"]\n\tpath = sub\n\turl = ./sub\n\tupdate = !@PROBE@\n"},
		{"alternates", "--- write .git/objects/info/alternates\n@LURE@/objects\n"},
		{"sample hook", "--- write .git/hooks/pre-commit.sample\n#!/bin/sh\n@PROBE@\n"},
	}
	s.runEach(cases, func(e *gitSpecEnv) gitWriteGate { return e.guard() }, func(t *testing.T, log string) {
		require.Empty(t, log)
	})
}
