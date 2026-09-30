package agentgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type GitGuardSuite struct {
	suite.Suite
	fs       *fakeGuardFS
	auditor  *collectAuditor
	approver *stubApprover
	guard    *GitGuard
	now      time.Time
}

func TestGitGuardSuite(t *testing.T) {
	suite.Run(t, new(GitGuardSuite))
}

// fakeGuardFS records what the guard asked for and returns canned results.
type fakeGuardFS struct {
	snap       *GuardSnapshot
	snapErr    error
	installErr error
	regular    map[string]bool
	regularErr error

	snapshots      int
	gotSrc, gotDst string
	gotUID, gotGID int
	installed      *GuardSnapshot
	installs       int
	gotNoReplace   bool
	regularAsked   []string
}

func (f *fakeGuardFS) Snapshot(src, dst string, uid, gid int) (*GuardSnapshot, error) {
	f.snapshots++
	f.gotSrc, f.gotDst, f.gotUID, f.gotGID = src, dst, uid, gid
	if f.snapErr != nil {
		return nil, f.snapErr
	}
	return f.snap, nil
}

func (f *fakeGuardFS) IsRegular(path string) (bool, error) {
	f.regularAsked = append(f.regularAsked, path)
	if f.regularErr != nil {
		return false, f.regularErr
	}
	return f.regular[path], nil
}

func (f *fakeGuardFS) Install(snap *GuardSnapshot, noReplace bool) error {
	f.installs++
	f.installed, f.gotNoReplace = snap, noReplace
	return f.installErr
}

const gitInitConfig = "[core]\n" +
	"\trepositoryformatversion = 0\n" +
	"\tfilemode = true\n" +
	"\tbare = false\n" +
	"\tlogallrefupdates = true\n"

func (s *GitGuardSuite) SetupTest() {
	s.now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	s.fs = &fakeGuardFS{}
	s.auditor = &collectAuditor{}
	s.approver = &stubApprover{out: Outcome{Decision: types.DecisionAllow, Actor: "user-1"}}
	s.guard = &GitGuard{
		Roots:    []string{"/work"},
		FS:       s.fs,
		Approver: s.approver,
		Auditor:  s.auditor,
		Now:      func() time.Time { return s.now },
	}
}

func (s *GitGuardSuite) rename(src, dst string, flags uint64) GuardRename {
	return GuardRename{
		TrapID: 7, PID: 42, ChannelID: "ch-1", Syscall: syscallRenameat2,
		Src: src, Dst: dst, Flags: flags,
		Tracee: &FakeTracee{UID: 501, GID: 20},
	}
}

func (s *GitGuardSuite) requireDenied(got TrapResponse, errno syscall.Errno) {
	s.Require().Equal(uint64(7), got.ID)
	s.Require().False(got.Allow)
	s.Require().False(got.Performed)
	s.Require().Equal(int32(errno), got.ErrorNum)
}

func (s *GitGuardSuite) requirePerformed(got TrapResponse) {
	s.Require().Equal(TrapResponse{ID: 7, Performed: true}, got)
}

// --- classification ---

func (s *GitGuardSuite) TestClassify() {
	cases := []struct {
		name  string
		roots []string
		path  string
		want  gitPathKind
	}{
		{"git dir entry", nil, "/work/.git", gitEntry},
		{"nested repo entry", nil, "/work/sub/.git", gitEntry},
		{"config", nil, "/work/.git/config", gitConfig},
		{"config.worktree", nil, "/work/.git/config.worktree", gitConfig},
		{"commondir", nil, "/work/.git/commondir", gitPointer},
		{"hooks root", nil, "/work/.git/hooks", gitHooksRoot},
		{"hook", nil, "/work/.git/hooks/pre-commit", gitHook},
		{"sample hook", nil, "/work/.git/hooks/pre-commit.sample", gitNone},
		{"sample hook upper case", nil, "/work/.git/hooks/PRE-PUSH.SAMPLE", gitNone},
		{"sample name deeper under hooks", nil, "/work/.git/hooks/dir/x.sample", gitHook},
		{"HEAD", nil, "/work/.git/HEAD", gitNone},
		{"object", nil, "/work/.git/objects/ab/cdef", gitNone},
		{"config.lock", nil, "/work/.git/config.lock", gitNone},
		{"submodule config", nil, "/work/.git/modules/sub/config", gitConfig},
		{"nested submodule hook", nil, "/work/.git/modules/a/modules/b/hooks/post-checkout", gitHook},
		{"submodule hooks root", nil, "/work/.git/modules/sub/hooks", gitHooksRoot},
		{"submodule HEAD", nil, "/work/.git/modules/sub/HEAD", gitNone},
		{"worktree commondir", nil, "/work/.git/worktrees/wt/commondir", gitPointer},
		{"worktree config.worktree", nil, "/work/.git/worktrees/wt/config.worktree", gitConfig},
		{"worktree HEAD", nil, "/work/.git/worktrees/wt/HEAD", gitNone},
		{"upper case hook", nil, "/work/.GIT/HOOKS/pre-commit", gitHook},
		{"mixed case config", nil, "/work/.Git/Config", gitConfig},
		{"kelvin sign folds to k", nil, "/work/.git/hoo\u212As/pre-commit", gitHook},
		{"unclean path", nil, "/work/x/../.git/./config", gitConfig},
		{".git inside .git", nil, "/work/.git/.git", gitEntry},
		{"other dir under .git", nil, "/work/.git/refs/heads/hooks", gitNone},
		{"config outside .git", nil, "/work/notgit/config", gitNone},
		{"root itself", nil, "/work", gitNone},
		{"outside the roots", nil, "/tmp/repo/.git/config", gitNone},
		{"sibling sharing a prefix", nil, "/workspace/.git/config", gitNone},
		{"parent of a root", []string{"/work/sub"}, "/work", gitNone},
		{"relative root never matches", []string{"relroot"}, "/work/.git/config", gitNone},
		{"second root matches", []string{"/other", "/work/"}, "/work/.git/config", gitConfig},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			if c.roots != nil {
				s.guard.Roots = c.roots
			}
			s.Require().Equal(c.want, s.guard.classify(c.path))
			s.Require().Equal(c.want != gitNone, s.guard.Protects(c.path))
		})
	}
}

func (s *GitGuardSuite) TestNilGuardProtectsNothing() {
	var g *GitGuard
	s.Require().False(g.Protects("/work/.git/config"))
}

func (s *GitGuardSuite) TestAllowsInPlace() {
	mkdirat, _ := SyscallByName(syscallMkdirat)
	mknodat, _ := SyscallByName(syscallMknodat)
	openat, _ := SyscallByName(syscallOpenat)
	cases := []struct {
		name string
		spec SyscallSpec
		op   string
		want bool
	}{
		{"mkdir", mkdirat, OpCreate, true},
		{"mknod", mknodat, OpCreate, false},
		{"open create", openat, OpCreate, false},
		{"write", openat, OpWrite, false},
		{"link", openat, OpLink, false},
		{"read", openat, OpRead, true},
		{"delete", openat, OpDelete, true},
		{"chmod", openat, OpChmod, true},
		{"chown", openat, OpChown, true},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.Require().Equal(c.want, AllowsInPlace(c.spec, c.op))
		})
	}
}

func (s *GitGuardSuite) TestRefuseAuditsAndDenies() {
	got := s.guard.Refuse(7, 42, "ch-1", OpWrite, "/work/.git/config")
	s.requireDenied(got, syscall.EPERM)
	s.Require().Equal([]AuditEntry{{
		Ts: s.now, Channel: "ch-1", PID: 42, Kind: "file",
		Target: "write /work/.git/config", RuleID: gitGuardRuleID,
		Decision: string(types.DecisionDeny),
	}}, s.auditor.entries)
}

func (s *GitGuardSuite) TestRefuseWithoutAuditorOrClock() {
	g := &GitGuard{Roots: []string{"/work"}}
	got := g.Refuse(7, 42, "ch-1", OpCreate, "/work/.git/hooks/x")
	s.requireDenied(got, syscall.EPERM)
	s.Require().WithinDuration(time.Now(), g.now(), time.Minute)
}

// --- InsideGitDir / TreeRename ---

func (s *GitGuardSuite) TestInsideGitDir() {
	cases := []struct {
		path string
		want bool
	}{
		{"/work/.git/refs/heads/main", true},
		{"/work/.git/modules/foo", true},
		{"/work/sub/.GIT/index", true},
		{"/work/.git/config", true},
		{"/work/.git", false},
		{"/work/sub/.git", false},
		{"/work/src/main.go", false},
		{"/work", false},
		{"/tmp/r/.git/refs/heads/main", false},
	}
	for _, c := range cases {
		s.Run(c.path, func() {
			s.Require().Equal(c.want, s.guard.InsideGitDir(c.path))
		})
	}
	var nilGuard *GitGuard
	s.Require().False(nilGuard.InsideGitDir("/work/.git/refs/heads/main"))
}

func (s *GitGuardSuite) TestTreeRename() {
	cases := []struct {
		name       string
		src, dst   string
		flags      uint64
		regular    bool
		regularErr error
		refused    bool
		errno      syscall.Errno
		asked      bool
	}{
		{name: "outside any git dir", src: "/work/a", dst: "/work/b"},
		{name: "exchange outside any git dir", src: "/work/a", dst: "/work/b", flags: renameExchange},
		{name: "source inside, plain rename out", src: "/work/.git/modules/foo", dst: "/work/foo"},
		{name: "file into a git dir", src: "/work/.git/refs/heads/main.lock", dst: "/work/.git/refs/heads/main", regular: true, asked: true},
		{name: "noreplace file into a git dir", src: "/work/.git/x.lock", dst: "/work/.git/x", flags: renameNoReplace, regular: true, asked: true},
		{name: "directory into a git dir", src: "/work/staging", dst: "/work/.git/modules/foo", refused: true, errno: syscall.EPERM, asked: true},
		{name: "exchange into a git dir", src: "/work/a", dst: "/work/.git/refs/heads/main", flags: renameExchange, refused: true, errno: syscall.EPERM},
		{name: "exchange out of a git dir", src: "/work/.git/modules/foo", dst: "/work/foo", flags: renameExchange, refused: true, errno: syscall.EPERM},
		{name: "source vanished", src: "/work/gone", dst: "/work/.git/refs/heads/main", regularErr: syscall.ENOENT, refused: true, errno: syscall.ENOENT, asked: true},
		{name: "stat fails oddly", src: "/work/a", dst: "/work/.git/refs/heads/main", regularErr: errors.New("weird"), refused: true, errno: syscall.EPERM, asked: true},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			s.fs.regular = map[string]bool{c.src: c.regular}
			s.fs.regularErr = c.regularErr
			resp, refused := s.guard.TreeRename(s.rename(c.src, c.dst, c.flags))
			s.Require().Equal(c.refused, refused)
			if c.refused {
				s.requireDenied(resp, c.errno)
				s.Require().Len(s.auditor.entries, 1)
				s.Require().Equal("write "+c.dst, s.auditor.entries[0].Target)
			} else {
				s.Require().Equal(TrapResponse{}, resp)
				s.Require().Empty(s.auditor.entries)
			}
			if c.asked {
				s.Require().Equal([]string{c.src}, s.fs.regularAsked)
			} else {
				s.Require().Empty(s.fs.regularAsked)
			}
		})
	}
}

// --- Rename ---

func (s *GitGuardSuite) TestRenameRefusesExchangeAndWhiteout() {
	for _, flags := range []uint64{renameExchange, renameWhiteout, renameExchange | renameNoReplace} {
		s.Run(fmt.Sprintf("flags=%#x", flags), func() {
			s.SetupTest()
			got := s.guard.Rename(context.Background(), s.rename("/work/a", "/work/.git/config", flags))
			s.requireDenied(got, syscall.EPERM)
			s.Require().Zero(s.fs.snapshots)
			s.Require().Len(s.auditor.entries, 1)
			s.Require().Equal("write /work/.git/config", s.auditor.entries[0].Target)
		})
	}
}

func (s *GitGuardSuite) TestRenameRefusesWhenCredsFail() {
	r := s.rename("/work/a", "/work/.git/config", 0)
	r.Tracee = &FakeTracee{CredsErr: ErrTraceeGone}
	got := s.guard.Rename(context.Background(), r)
	s.requireDenied(got, syscall.EPERM)
	s.Require().Zero(s.fs.snapshots)
}

func (s *GitGuardSuite) TestRenameSnapshotErrorsMapToErrno() {
	cases := []struct {
		name string
		err  error
		want syscall.Errno
	}{
		{"enoent", syscall.ENOENT, syscall.ENOENT},
		{"wrapped eacces", fmt.Errorf("open: %w", syscall.EACCES), syscall.EACCES},
		{"zero errno", syscall.Errno(0), syscall.EPERM},
		{"not regular", ErrGuardNotRegular, syscall.EPERM},
		{"too large", ErrGuardTooLarge, syscall.EPERM},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			s.fs.snapErr = c.err
			got := s.guard.Rename(context.Background(), s.rename("/work/.git/config.lock", "/work/.git/config", 0))
			s.requireDenied(got, c.want)
			s.Require().Equal("/work/.git/config.lock", s.fs.gotSrc)
			s.Require().Equal("/work/.git/config", s.fs.gotDst)
			s.Require().Equal(501, s.fs.gotUID)
			s.Require().Equal(20, s.fs.gotGID)
			s.Require().Zero(s.fs.installs)
			s.Require().Len(s.auditor.entries, 1)
			s.Require().Equal(string(types.DecisionDeny), s.auditor.entries[0].Decision)
			s.Require().Equal(map[string]string{"error": c.err.Error()}, s.auditor.entries[0].Extra)
		})
	}
}

func (s *GitGuardSuite) TestRenameRefusesUnreviewableContent() {
	cases := []struct {
		name      string
		data, old string
	}{
		{"new content has CR", "[core]\r\n", ""},
		{"current content has a bidi override", "ok\n", "a\u202eb\n"},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			s.fs.snap = &GuardSnapshot{Dst: "/work/.git/hooks/pre-commit", Data: []byte(c.data), Old: []byte(c.old)}
			got := s.guard.Rename(context.Background(), s.rename("/work/x", "/work/.git/hooks/pre-commit", 0))
			s.requireDenied(got, syscall.EPERM)
			s.Require().Zero(s.fs.installs)
			s.Require().Empty(s.approver.got.Kind, "approver must not be asked")
		})
	}
}

func (s *GitGuardSuite) TestRenameInstallsSafeConfigSilently() {
	cases := []struct {
		name      string
		flags     uint64
		noReplace bool
	}{
		{"plain rename", 0, false},
		{"noreplace", renameNoReplace, true},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			s.fs.snap = &GuardSnapshot{Dst: "/work/.git/config", Data: []byte(gitInitConfig)}
			got := s.guard.Rename(context.Background(), s.rename("/work/.git/config.lock", "/work/.git/config", c.flags))
			s.requirePerformed(got)
			s.Require().Equal(1, s.fs.installs)
			s.Require().Same(s.fs.snap, s.fs.installed)
			s.Require().Equal(c.noReplace, s.fs.gotNoReplace)
			s.Require().Empty(s.approver.got.Kind, "safe keys must not prompt")
			s.Require().Equal([]AuditEntry{{
				Ts: s.now, Channel: "ch-1", PID: 42, Kind: "file",
				Target: "write /work/.git/config", RuleID: gitGuardRuleID,
				Decision: string(types.DecisionAllow),
				Extra:    map[string]string{"reason": "safe-keys"},
			}}, s.auditor.entries)
		})
	}
}

func (s *GitGuardSuite) TestRenameSafeConfigInstallErrorMapsErrno() {
	s.fs.snap = &GuardSnapshot{Dst: "/work/.git/config", Data: []byte(gitInitConfig)}
	s.fs.installErr = syscall.EEXIST
	got := s.guard.Rename(context.Background(), s.rename("/work/.git/config.lock", "/work/.git/config", renameNoReplace))
	s.requireDenied(got, syscall.EEXIST)
	s.Require().Len(s.auditor.entries, 2)
	s.Require().Equal(string(types.DecisionDeny), s.auditor.entries[1].Decision)
}

func (s *GitGuardSuite) TestRenameWithoutApproverRefuses() {
	s.guard.Approver = nil
	s.fs.snap = &GuardSnapshot{Dst: "/work/.git/hooks/pre-commit", Data: []byte("#!/bin/sh\necho hi\n")}
	got := s.guard.Rename(context.Background(), s.rename("/work/x", "/work/.git/hooks/pre-commit", 0))
	s.requireDenied(got, syscall.EPERM)
	s.Require().Zero(s.fs.installs)
}

func (s *GitGuardSuite) TestRenameAsksWithDiffAndInstallsOnAllow() {
	data := []byte("#!/bin/sh\necho hi\n")
	s.fs.snap = &GuardSnapshot{Dst: "/work/.git/hooks/pre-commit", Data: data}
	s.guard.PeerSource = func(int) string { return "terminal:leaf-1" }

	got := s.guard.Rename(context.Background(), s.rename("/work/x", "/work/.git/hooks/pre-commit", 0))
	s.requirePerformed(got)
	s.Require().Equal(1, s.fs.installs)
	s.Require().False(s.fs.gotNoReplace)

	sum := sha256.Sum256(data)
	req := s.approver.got
	s.Require().Equal("file", req.Kind)
	s.Require().Equal("write /work/.git/hooks/pre-commit", req.Target)
	s.Require().Equal("terminal:leaf-1", req.Source)
	s.Require().Equal("git hook write — review the script", req.Message)
	s.Require().Equal("file:git:/work/.git/hooks/pre-commit:"+hex.EncodeToString(sum[:]), req.CacheKey)
	s.Require().Equal(gitGuardDiff(s.fs.snap), req.Details["diff"])
	s.Require().Contains(req.Details["diff"], "+echo hi")

	s.Require().Equal([]AuditEntry{
		{Ts: s.now, Channel: "ch-1", PID: 42, Kind: "file", Target: "write /work/.git/hooks/pre-commit", RuleID: gitGuardRuleID, Event: "request"},
		{Ts: s.now, Channel: "ch-1", PID: 42, Kind: "file", Target: "write /work/.git/hooks/pre-commit", RuleID: gitGuardRuleID, Decision: string(types.DecisionAllow), PromptedWho: "user-1"},
	}, s.auditor.entries)
}

func (s *GitGuardSuite) TestRenameAsksForUnsafeConfig() {
	s.fs.snap = &GuardSnapshot{
		Dst:  "/work/.git/config",
		Old:  []byte(gitInitConfig),
		Data: []byte(gitInitConfig + "\tfsmonitor = ./evil\n"), OldExists: true,
	}
	got := s.guard.Rename(context.Background(), s.rename("/work/.git/config.lock", "/work/.git/config", 0))
	s.requirePerformed(got)
	s.Require().Equal("git config change — review the diff", s.approver.got.Message)
	s.Require().Equal("chat", s.approver.got.Source)
	s.Require().Contains(s.approver.got.Details["diff"], "+\tfsmonitor = ./evil")
}

func (s *GitGuardSuite) TestRenameAsksForPointer() {
	s.fs.snap = &GuardSnapshot{Dst: "/work/sub/.git", Data: []byte("gitdir: ../elsewhere\n")}
	got := s.guard.Rename(context.Background(), s.rename("/work/sub/.git.tmp", "/work/sub/.git", 0))
	s.requirePerformed(got)
	s.Require().Equal("git dir pointer write — review the target", s.approver.got.Message)
}

func (s *GitGuardSuite) TestRenameDeniedByApprover() {
	s.approver.out = Outcome{Decision: types.DecisionDeny, Actor: "user-1"}
	s.fs.snap = &GuardSnapshot{Dst: "/work/.git/hooks/pre-commit", Data: []byte("x\n")}
	got := s.guard.Rename(context.Background(), s.rename("/work/x", "/work/.git/hooks/pre-commit", 0))
	s.requireDenied(got, syscall.EPERM)
	s.Require().Zero(s.fs.installs)
	last := s.auditor.entries[len(s.auditor.entries)-1]
	s.Require().Equal(string(types.DecisionDeny), last.Decision)
	s.Require().Equal("user-1", last.PromptedWho)
}

func (s *GitGuardSuite) TestRenameApprovedInstallErrorMapsErrno() {
	s.fs.snap = &GuardSnapshot{Dst: "/work/.git/hooks/pre-commit", Data: []byte("x\n")}
	s.fs.installErr = errors.New("disk on fire")
	got := s.guard.Rename(context.Background(), s.rename("/work/x", "/work/.git/hooks/pre-commit", 0))
	s.requireDenied(got, syscall.EPERM)
	last := s.auditor.entries[len(s.auditor.entries)-1]
	s.Require().Equal(map[string]string{"error": "disk on fire"}, last.Extra)
}

// --- card rendering ---

func (s *GitGuardSuite) TestGitGuardMessage() {
	cases := []struct {
		kind gitPathKind
		want string
	}{
		{gitConfig, "git config change — review the diff"},
		{gitHook, "git hook write — review the script"},
		{gitHooksRoot, "git hook write — review the script"},
		{gitPointer, "git dir pointer write — review the target"},
		{gitEntry, "git dir pointer write — review the target"},
	}
	for _, c := range cases {
		s.Require().Equal(c.want, gitGuardMessage(c.kind))
	}
}

func (s *GitGuardSuite) TestGitGuardDiff() {
	cases := []struct {
		name string
		snap GuardSnapshot
		want string
	}{
		{
			name: "new file diffs against /dev/null",
			snap: GuardSnapshot{Dst: "/work/.git/hooks/h", Data: []byte("a\n")},
			// difflib.SplitLines always appends a final "\n" element, hence
			// the trailing blank context line.
			want: "--- /dev/null\n+++ /work/.git/hooks/h\n@@ -1 +1,2 @@\n+a\n \n",
		},
		{
			name: "existing file diffs against its current content",
			snap: GuardSnapshot{Dst: "/work/.git/config", Old: []byte("a\n"), Data: []byte("b\n"), OldExists: true},
			want: "--- /work/.git/config (current)\n+++ /work/.git/config\n@@ -1,2 +1,2 @@\n-a\n+b\n \n",
		},
		{
			name: "identical content",
			snap: GuardSnapshot{Dst: "/work/.git/config", Old: []byte("a\n"), Data: []byte("a\n"), OldExists: true},
			want: "(no change)",
		},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.Require().Equal(c.want, gitGuardDiff(&c.snap))
		})
	}
}

func (s *GitGuardSuite) TestReviewableText() {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", true},
		{"tabs and newlines", "[core]\n\tbare = false\n", true},
		{"non-ascii letters", "user = Ștefan Müller\n", true},
		{"invalid utf-8", "\xff\xfe", false},
		{"carriage return", "a\r\n", false},
		{"nul", "a\x00b", false},
		{"escape", "\x1b[31m", false},
		{"del", "a\x7f", false},
		{"c1 control", "a\u0085b", false},
		{"right-to-left override", "a\u202eb", false},
		{"zero-width space", "a\u200bb", false},
		{"zero-width joiner", "a\u200db", false},
		{"byte order mark", "\ufeffa", false},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.Require().Equal(c.want, reviewableText([]byte(c.in)))
		})
	}
}

// --- config parsing ---

func (s *GitGuardSuite) TestParseGitConfigStrict() {
	got, ok := parseGitConfigStrict([]byte("# comment\n; other\n\n[Core]\n\tBare = false\n\tsymlinks\n[remote \"Origin\"]\n  url = https://h/x\n"))
	s.Require().True(ok)
	s.Require().Equal([]gitConfigEntry{
		{section: "core", key: "bare", value: "false"},
		{section: "core", key: "symlinks"},
		{section: "remote", sub: "Origin", key: "url", value: "https://h/x"},
	}, got)

	for _, bad := range []string{
		"bare = false\n",               // key before any section
		"[core] bare = false\n",        // key on the header line
		"[core.sub]\n",                 // old dotted subsection form
		"[remote \"o\\\"x\"]\n",        // escaped quote in subsection
		"[core]\n\tpager = \"less\"\n", // quoted value
		"[core]\n\tname = a\\\n\tb\n",  // backslash continuation
		"[core]\n\tbare = false # c\n", // trailing comment
		"[core]\n\tbare = false ; c\n", // trailing comment
		"[core]\n\t1bad = x\n",         // key must start with a letter
		"[core]\r\n\tbare = false\r\n", // CRLF
	} {
		_, ok := parseGitConfigStrict([]byte(bad))
		s.Require().Falsef(ok, "%q must not parse", bad)
	}
}

func (s *GitGuardSuite) TestSafeGitConfigEntry() {
	cases := []struct {
		name string
		e    gitConfigEntry
		want bool
	}{
		{"core.bare", gitConfigEntry{section: "core", key: "bare", value: "false"}, true},
		{"user.email", gitConfigEntry{section: "user", key: "email", value: "a@b"}, true},
		{"core.fsmonitor", gitConfigEntry{section: "core", key: "fsmonitor", value: "x"}, false},
		{"core with a subsection", gitConfigEntry{section: "core", sub: "x", key: "bare"}, false},
		{"remote without a subsection", gitConfigEntry{section: "remote", key: "url", value: "https://h/x"}, false},
		{"unknown section", gitConfigEntry{section: "alias", key: "x", value: "!sh"}, false},
		{"branch remote", gitConfigEntry{section: "branch", sub: "main", key: "remote", value: "origin"}, true},
		{"remote fetch", gitConfigEntry{section: "remote", sub: "origin", key: "fetch", value: "+refs/heads/*:refs/remotes/origin/*"}, true},
		{"https url", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "https://github.com/o/r.git"}, true},
		{"http url", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "http://h/r"}, true},
		{"ssh url", gitConfigEntry{section: "remote", sub: "origin", key: "pushurl", value: "ssh://git@github.com/o/r.git"}, true},
		{"git url", gitConfigEntry{section: "submodule", sub: "lib", key: "url", value: "git://h/r"}, true},
		{"scp-like url", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "git@github.com:o/r.git"}, true},
		{"local path", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "/tmp/evil"}, false},
		{"relative path", gitConfigEntry{section: "submodule", sub: "lib", key: "url", value: "../lib"}, false},
		{"file url", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "file:///tmp/evil"}, false},
		{"ext transport", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "ext::sh -c evil"}, false},
		{"helper transport", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "fd::3"}, false},
		{"leading dash", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "-oProxyCommand=evil"}, false},
		{"scp-like host with leading dash", gitConfigEntry{section: "remote", sub: "origin", key: "url", value: "git@-oProxyCommand=x:r"}, false},
		{"empty url", gitConfigEntry{section: "remote", sub: "origin", key: "url"}, false},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.Require().Equal(c.want, safeGitConfigEntry(c.e))
		})
	}
}

func (s *GitGuardSuite) TestSafeGitConfigChange() {
	const remote = "[remote \"origin\"]\n" +
		"\turl = https://github.com/o/r.git\n" +
		"\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	cases := []struct {
		name     string
		old, new string
		want     bool
	}{
		{"git init", "", gitInitConfig, true},
		{"git remote add", gitInitConfig, gitInitConfig + remote, true},
		{"git branch -u", gitInitConfig + remote, gitInitConfig + remote + "[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n", true},
		{"git config user.*", gitInitConfig, gitInitConfig + "[user]\n\tname = A B\n\temail = a@b.c\n", true},
		{"changing a safe value", gitInitConfig, "[core]\n\tbare = true\n", true},
		{"removing an unsafe entry", gitInitConfig + "\tfsmonitor = x\n", gitInitConfig, true},
		{"unchanged unsafe entry", gitInitConfig + "\tfsmonitor = x\n", gitInitConfig + "\tfsmonitor = x\n\tbare = true\n", true},
		{"duplicating an unsafe entry", gitInitConfig + "\tfsmonitor = x\n", gitInitConfig + "\tfsmonitor = x\n\tfsmonitor = x\n", false},
		{"core.fsmonitor", gitInitConfig, gitInitConfig + "\tfsmonitor = ./evil\n", false},
		{"core.hooksPath", gitInitConfig, gitInitConfig + "\thooksPath = /tmp/h\n", false},
		{"core.sshCommand", gitInitConfig, gitInitConfig + "\tsshCommand = evil\n", false},
		{"include.path", gitInitConfig, gitInitConfig + "[include]\n\tpath = /tmp/x\n", false},
		{"remote.origin.uploadpack", gitInitConfig, gitInitConfig + "[remote \"origin\"]\n\tuploadpack = evil\n", false},
		{"alias with a shell", gitInitConfig, gitInitConfig + "[alias]\n\tx = !sh\n", false},
		{"filter driver", gitInitConfig, gitInitConfig + "[filter \"x\"]\n\tclean = evil\n", false},
		{"local remote url", gitInitConfig, gitInitConfig + "[remote \"o\"]\n\turl = /tmp/evil\n", false},
		{"quoted value", gitInitConfig, gitInitConfig + "[user]\n\tname = \"A B\"\n", false},
		{"backslash continuation", gitInitConfig, gitInitConfig + "[user]\n\tname = A\\\n\tB\n", false},
		{"key on the header line", gitInitConfig, "[core] bare = false\n", false},
		{"dotted subsection form", gitInitConfig, "[remote.origin]\n\turl = https://h/r\n", false},
		{"CRLF file", "", "[core]\r\n\tbare = false\r\n", false},
		{"unparseable old file", "[core] x = y\n", gitInitConfig, false},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.Require().Equal(c.want, safeGitConfigChange([]byte(c.old), []byte(c.new)))
		})
	}
}
