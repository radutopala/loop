//go:build linux

package agentgate

import (
	"context"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/radutopala/loop/internal/types"
)

// Spec: a file rule names a file, not a spelling. Every path the kernel
// resolves to a denied file must get the rule's decision, for every
// operation the rule lists — including creating a file that doesn't exist
// yet, which the kernel puts where the resolved parent (or a dangling
// link) leads.
//
// The world is an in-memory tree walked by ProcTracee.EvalSymlinks, the
// resolver the gate runs in production. Aliases exercised: duplicate
// slashes, "." and "..", paths relative to the cwd, an
// absolute and a relative directory link, a link to "/", and a dangling
// link to the file to be created.
//
// Trusted base: the fake tree's Lstat/Readlink, and the syscall table's
// argument layout. Rules are matched as written: a rule spelled through a
// directory link (/var/run/… where /var/run -> /run) names nothing, so
// rules are written in their resolved form.
const (
	sshDir     = "/home/agent/.ssh"
	sshExists  = sshDir + "/authorized_keys"
	sshMissing = sshDir + "/rc"
)

func fileAliasWorld() *fakeLinkFS {
	return &fakeLinkFS{
		links: map[string]string{
			"/tmp/s":   sshDir,
			"/tmp/rel": "../home/agent/.ssh",
			"/tmp/r":   sshMissing,
			"/work/up": "/",
		},
		plain: plainSet("/home", "/home/agent", sshDir, sshExists, "/tmp", "/work"),
	}
}

// walkTracee reads strings and dirfds from the fake and resolves links with
// ProcTracee's walk over a fake tree.
type walkTracee struct {
	*FakeTracee
	walk *ProcTracee
}

func (t walkTracee) EvalSymlinks(path string) (string, error) { return t.walk.EvalSymlinks(path) }

// fileAlias is one spelling of a target. only, when set, names the one trap
// the alias applies to.
type fileAlias struct{ name, path, cwd, only string }

// aliases spells target every way the world allows.
func aliases(target, leaf string) []fileAlias {
	out := []fileAlias{
		{"canonical", target, "/", ""},
		{"double slash", "/home//agent/.ssh//" + leaf, "/", ""},
		{"dot", "/home/agent/./.ssh/" + leaf, "/", ""},
		{"dotdot", "/tmp/../home/agent/.ssh/" + leaf, "/", ""},
		{"relative to cwd", ".ssh/" + leaf, "/home/agent", ""},
		{"dir link", "/tmp/s/" + leaf, "/", ""},
		{"relative dir link", "/tmp/rel/" + leaf, "/", ""},
		{"link to root", "/work/up/home/agent/.ssh/" + leaf, "/", ""},
		{"dir link relative to cwd", "s/" + leaf, "/tmp", ""},
	}
	if target == sshMissing {
		// Only open(O_CREAT) follows a dangling last link: mkdir fails with
		// EEXIST, and rename replaces the link itself.
		out = append(out, fileAlias{"dangling link to the file", "/tmp/r", "/", "create"})
	}
	return out
}

func (s *ServerSuite) fileAliasServer(tr Tracee) *Server {
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{{
		Paths:      []string{"**/.ssh/**"},
		Operations: []string{OpRead, OpWrite, OpCreate, OpDelete},
		Decision:   types.DecisionDeny,
	}})
	return &Server{Factory: func(int) Tracee { return tr }, File: NewFileHandler(policy, nil, 8)}
}

// fileAliasTraps builds the traps that act on the path at 0x100: reads and writes of an
// existing file, creates (open, mkdir, rename onto) of a missing one, and
// deleting an existing one.
func fileAliasTraps(exists bool) map[string]Trap {
	const p, dst = 0x100, 0x200
	if exists {
		return map[string]Trap{
			"read":   {Syscall: syscallOpenat, Args: [6]uint64{atFdcwd, p, unix.O_RDONLY}},
			"write":  {Syscall: syscallOpenat, Args: [6]uint64{atFdcwd, p, unix.O_WRONLY}},
			"unlink": {Syscall: syscallUnlinkat, Args: [6]uint64{atFdcwd, p, 0}},
		}
	}
	return map[string]Trap{
		"create":      {Syscall: syscallOpenat, Args: [6]uint64{atFdcwd, p, unix.O_CREAT | unix.O_WRONLY}},
		"mkdir":       {Syscall: syscallMkdirat, Args: [6]uint64{atFdcwd, p, 0o700}},
		"rename onto": {Syscall: syscallRenameat2, Args: [6]uint64{atFdcwd, dst, atFdcwd, p}},
	}
}

func (s *ServerSuite) TestFileRuleDeniesEverySpelling() {
	for _, target := range []struct {
		path, leaf string
		exists     bool
	}{
		{sshExists, "authorized_keys", true},
		{sshMissing, "rc", false},
	} {
		for _, alias := range aliases(target.path, target.leaf) {
			for op, trap := range fileAliasTraps(target.exists) {
				if alias.only != "" && alias.only != op {
					continue
				}
				s.Run(target.leaf+"/"+alias.name+"/"+op, func() {
					tr := walkTracee{
						FakeTracee: &FakeTracee{
							Strings: map[uintptr]string{0x100: alias.path, 0x200: "/tmp/new"},
							Dirfds:  map[int32]string{AtFDCWD: alias.cwd},
						},
						walk: fileAliasWorld().tracee(1),
					}
					got := s.fileAliasServer(tr).Dispatch(context.Background(), trap)
					s.Require().False(got.Allow, "%s %q from cwd %s reached %s", op, alias.path, alias.cwd, target.path)
					s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
				})
			}
		}
	}
}

// The spec must be able to fail: judged by its spelling alone (a tracee
// that resolves no links), some alias reaches the file.
func (s *ServerSuite) TestFileRuleSpecCanFail() {
	reached := 0
	for _, alias := range aliases(sshMissing, "rc") {
		tr := &FakeTracee{
			Strings: map[uintptr]string{0x100: alias.path},
			Dirfds:  map[int32]string{AtFDCWD: alias.cwd},
		}
		got := s.fileAliasServer(tr).Dispatch(context.Background(), fileAliasTraps(false)["create"])
		if got.Allow {
			reached++
		}
	}
	s.Require().Positive(reached)
}
