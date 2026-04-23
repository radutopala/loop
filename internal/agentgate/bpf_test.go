//go:build linux

package agentgate

import (
	"errors"
	"runtime"
	"testing"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

type BPFSuite struct {
	suite.Suite
}

func TestBPFSuite(t *testing.T) {
	suite.Run(t, new(BPFSuite))
}

// -- auditArchFor --------------------------------------------------------------------

func (s *BPFSuite) TestAuditArchForAmd64() {
	got, err := auditArchFor("amd64")
	s.Require().NoError(err)
	s.Require().Equal(auditArchX86_64, got)
}

func (s *BPFSuite) TestAuditArchForArm64() {
	got, err := auditArchFor("arm64")
	s.Require().NoError(err)
	s.Require().Equal(auditArchAArch64, got)
}

func (s *BPFSuite) TestAuditArchForUnsupported() {
	_, err := auditArchFor("mips")
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "unsupported GOARCH")
}

// -- TrapSyscalls / DenySyscalls ----------------------------------------------------

func (s *BPFSuite) TestTrapSyscallsContainsExpectedNumbers() {
	trap := TrapSyscalls()
	s.Require().Contains(trap, uint32(unix.SYS_CONNECT))
	s.Require().Contains(trap, uint32(unix.SYS_EXECVE))
	s.Require().Contains(trap, uint32(unix.SYS_EXECVEAT))
	s.Require().Contains(trap, uint32(unix.SYS_OPENAT))
	s.Require().Contains(trap, uint32(unix.SYS_OPENAT2))
	s.Require().Contains(trap, uint32(unix.SYS_RENAMEAT2))
	s.Require().Contains(trap, uint32(unix.SYS_UNLINKAT))
	s.Require().Contains(trap, uint32(unix.SYS_LINKAT))
	s.Require().Contains(trap, uint32(unix.SYS_SYMLINKAT))
	s.Require().Contains(trap, uint32(unix.SYS_FCHMODAT))
	s.Require().Contains(trap, uint32(unix.SYS_FCHOWNAT))
	s.Require().Contains(trap, uint32(unix.SYS_MKDIRAT))
}

func (s *BPFSuite) TestDenySyscallsCoversIoUringFamily() {
	deny := DenySyscalls()
	s.Require().ElementsMatch([]uint32{
		uint32(unix.SYS_IO_URING_SETUP),
		uint32(unix.SYS_IO_URING_ENTER),
		uint32(unix.SYS_IO_URING_REGISTER),
	}, deny)
}

// -- buildFilter --------------------------------------------------------------------

const (
	ldW  = uint16(unix.BPF_LD | unix.BPF_W | unix.BPF_ABS)
	jeqK = uint16(unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K)
	retK = uint16(unix.BPF_RET | unix.BPF_K)
)

// assertArchPrologue checks that the first 4 instructions load+check arch and
// load nr — the shape every filter must start with.
func (s *BPFSuite) assertArchPrologue(prog []unix.SockFilter, arch uint32) {
	s.Require().GreaterOrEqual(len(prog), 4)

	// 0: ld [arch]
	s.Require().Equal(ldW, prog[0].Code)
	s.Require().Equal(sdOffsetArch, prog[0].K)

	// 1: jeq arch, jt=1, jf=0  (skip the KILL_PROCESS when arch matches)
	s.Require().Equal(jeqK, prog[1].Code)
	s.Require().Equal(uint8(1), prog[1].Jt)
	s.Require().Equal(uint8(0), prog[1].Jf)
	s.Require().Equal(arch, prog[1].K)

	// 2: ret KILL_PROCESS (taken when jeq falls through because arch mismatch)
	s.Require().Equal(retK, prog[2].Code)
	s.Require().Equal(seccompRetKillProcess, prog[2].K)

	// 3: ld [nr]
	s.Require().Equal(ldW, prog[3].Code)
	s.Require().Equal(sdOffsetNR, prog[3].K)
}

func (s *BPFSuite) TestBuildFilterEmptyTrapAndDeny() {
	prog := buildFilter(auditArchX86_64, nil, nil)
	s.assertArchPrologue(prog, auditArchX86_64)

	// Only the final RET_ALLOW follows the prologue.
	s.Require().Len(prog, 5)
	s.Require().Equal(retK, prog[4].Code)
	s.Require().Equal(seccompRetAllow, prog[4].K)
}

func (s *BPFSuite) TestBuildFilterTrapEntriesEmitJeqPlusUserNotif() {
	trap := []uint32{42, 99}
	prog := buildFilter(auditArchX86_64, trap, nil)
	s.assertArchPrologue(prog, auditArchX86_64)

	// 4 prologue + 2*2 trap pairs + 1 final ALLOW = 9.
	s.Require().Len(prog, 9)

	// prog[4]: jeq 42, jt=0, jf=1
	s.Require().Equal(jeqK, prog[4].Code)
	s.Require().Equal(uint8(0), prog[4].Jt)
	s.Require().Equal(uint8(1), prog[4].Jf)
	s.Require().Equal(uint32(42), prog[4].K)
	// prog[5]: ret USER_NOTIF
	s.Require().Equal(retK, prog[5].Code)
	s.Require().Equal(seccompRetUserNotif, prog[5].K)

	// prog[6..7]: same for 99.
	s.Require().Equal(uint32(99), prog[6].K)
	s.Require().Equal(seccompRetUserNotif, prog[7].K)

	// prog[8]: final RET_ALLOW.
	s.Require().Equal(seccompRetAllow, prog[8].K)
}

func (s *BPFSuite) TestBuildFilterDenyEntriesEmitJeqPlusErrnoEPERM() {
	deny := []uint32{100}
	prog := buildFilter(auditArchAArch64, nil, deny)
	s.assertArchPrologue(prog, auditArchAArch64)

	// 4 prologue + 1*2 deny + 1 allow = 7.
	s.Require().Len(prog, 7)

	// prog[4]: jeq 100
	s.Require().Equal(jeqK, prog[4].Code)
	s.Require().Equal(uint32(100), prog[4].K)
	// prog[5]: ret ERRNO | EPERM
	s.Require().Equal(retK, prog[5].Code)
	s.Require().Equal(seccompRetErrno|uint32(unix.EPERM), prog[5].K)
	// prog[6]: ret ALLOW
	s.Require().Equal(seccompRetAllow, prog[6].K)
}

func (s *BPFSuite) TestBuildFilterFullProgramFitsUnderLimit() {
	// seccomp filters are capped at 4096 BPF instructions.
	prog := buildFilter(auditArchX86_64, TrapSyscalls(), DenySyscalls())
	s.Require().Less(len(prog), 4096)
	s.Require().Equal(retK, prog[len(prog)-1].Code)
	s.Require().Equal(seccompRetAllow, prog[len(prog)-1].K)
}

// -- FilterInstaller ----------------------------------------------------------------

func (s *BPFSuite) TestNewFilterInstallerDefaults() {
	fi := NewFilterInstaller()
	s.Require().Equal(runtime.GOARCH, fi.GOARCH)
	s.Require().NotEmpty(fi.Trap)
	s.Require().NotEmpty(fi.Deny)
	s.Require().NotNil(fi.Prctl)
	s.Require().NotNil(fi.Seccomp)
}

func (s *BPFSuite) newTestInstaller() *FilterInstaller {
	return &FilterInstaller{
		GOARCH: "amd64",
		Trap:   []uint32{1, 2},
		Deny:   []uint32{3},
		Prctl: func(int, uintptr, uintptr, uintptr, uintptr) error {
			return nil
		},
		Seccomp: func(uint, uint, *unix.SockFprog) (uintptr, unix.Errno) {
			return 7, 0
		},
	}
}

func (s *BPFSuite) TestInstallRejectsUnsupportedArch() {
	fi := s.newTestInstaller()
	fi.GOARCH = "mips"
	// prctl / seccomp must not be called — catch via sentinel.
	fi.Prctl = func(int, uintptr, uintptr, uintptr, uintptr) error {
		s.T().Fatal("prctl should not run when arch is unsupported")
		return nil
	}
	fi.Seccomp = func(uint, uint, *unix.SockFprog) (uintptr, unix.Errno) {
		s.T().Fatal("seccomp should not run when arch is unsupported")
		return 0, 0
	}

	fd, err := fi.Install()
	s.Require().Error(err)
	s.Require().Equal(-1, fd)
	s.Require().Contains(err.Error(), "unsupported GOARCH")
}

func (s *BPFSuite) TestInstallPropagatesPrctlError() {
	fi := s.newTestInstaller()
	sentinel := errors.New("eacces")
	fi.Prctl = func(option int, a2, a3, a4, a5 uintptr) error {
		s.Require().Equal(unix.PR_SET_NO_NEW_PRIVS, option)
		s.Require().Equal(uintptr(1), a2)
		return sentinel
	}
	fi.Seccomp = func(uint, uint, *unix.SockFprog) (uintptr, unix.Errno) {
		s.T().Fatal("seccomp should not run when prctl fails")
		return 0, 0
	}

	fd, err := fi.Install()
	s.Require().Error(err)
	s.Require().Equal(-1, fd)
	s.Require().ErrorIs(err, sentinel)
	s.Require().Contains(err.Error(), "prctl(PR_SET_NO_NEW_PRIVS)")
}

func (s *BPFSuite) TestInstallPropagatesSeccompErrno() {
	fi := s.newTestInstaller()
	fi.Seccomp = func(uint, uint, *unix.SockFprog) (uintptr, unix.Errno) {
		return 0, unix.EINVAL
	}

	fd, err := fi.Install()
	s.Require().Error(err)
	s.Require().Equal(-1, fd)
	s.Require().ErrorIs(err, unix.EINVAL)
	s.Require().Contains(err.Error(), "seccomp(SET_MODE_FILTER")
}

func (s *BPFSuite) TestInstallHappyPathReturnsNotifyFd() {
	var sawPrctl bool
	fi := s.newTestInstaller()
	fi.Prctl = func(option int, a2, a3, a4, a5 uintptr) error {
		sawPrctl = true
		s.Require().Equal(unix.PR_SET_NO_NEW_PRIVS, option)
		return nil
	}
	fi.Seccomp = func(op, flags uint, prog *unix.SockFprog) (uintptr, unix.Errno) {
		// Confirm the call carries the expected op / flag bitmask and a
		// non-zero filter length.
		s.Require().Equal(uint(unix.SECCOMP_SET_MODE_FILTER), op)
		s.Require().NotZero(flags & uint(unix.SECCOMP_FILTER_FLAG_NEW_LISTENER))
		s.Require().NotZero(flags & uint(unix.SECCOMP_FILTER_FLAG_TSYNC))
		s.Require().NotZero(flags&uint(unix.SECCOMP_FILTER_FLAG_TSYNC_ESRCH),
			"TSYNC_ESRCH required when combining TSYNC + NEW_LISTENER (kernel EINVAL otherwise)")
		s.Require().NotNil(prog)
		s.Require().Greater(prog.Len, uint16(0))
		return 42, 0
	}

	fd, err := fi.Install()
	s.Require().NoError(err)
	s.Require().Equal(42, fd)
	s.Require().True(sawPrctl, "Install must call prctl before seccomp")
}

// -- rawSeccomp ---------------------------------------------------------------------

// TestRawSeccompKernelReturnsErrno confirms the production syscall wrapper
// forwards the kernel's errno. Calling seccomp(2) with a bogus op is safe —
// the kernel validates op before touching any args and returns EINVAL without
// side effects on the calling process.
func (s *BPFSuite) TestRawSeccompKernelReturnsErrno() {
	const bogusOp uint = 0xDEAD
	r, errno := rawSeccomp(bogusOp, 0, nil)
	s.Require().NotZero(uint(errno), "kernel must reject bogus op")
	s.Require().Equal(^uintptr(0), r, "syscall returns -1 on error")
}
