//go:build linux

package agentgate

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

type ProcTraceeSuite struct {
	suite.Suite
}

func TestProcTraceeSuite(t *testing.T) {
	suite.Run(t, new(ProcTraceeSuite))
}

// --- memBuf: table-driven fake for ReadMem. Maps remote addr → bytes. ---

type memBuf struct {
	pages map[uintptr][]byte
	err   error
}

func (m *memBuf) read(pid int, local []unix.Iovec, remote []unix.RemoteIovec, _ uint) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	r := remote[0]
	page, ok := m.pages[r.Base]
	if !ok {
		return 0, unix.ESRCH
	}
	want := min(r.Len, len(page))
	dst := unsafePtrToSlice(local[0].Base, int(local[0].Len))
	copy(dst, page[:want])
	_ = pid
	return want, nil
}

// unsafePtrToSlice converts a *byte + length into a []byte without the
// reflect/runtime gymnastics — safe for test code because the caller owns the
// backing array.
func unsafePtrToSlice(ptr *byte, n int) []byte {
	return (*[1 << 20]byte)(unsafe.Pointer(ptr))[:n:n]
}

// --- NewProcTracee ---

func (s *ProcTraceeSuite) TestNewProcTraceeWiresDefaults() {
	t := NewProcTracee(42)
	s.Require().Equal(42, t.PID)
	s.Require().NotNil(t.ReadMem)
	s.Require().NotNil(t.Readlink)
	s.Require().NotNil(t.Lstat)
	s.Require().NotNil(t.ReadlinkPath)
	s.Require().NotNil(t.ReadFile)
}

// --- ReadString ---

func (s *ProcTraceeSuite) TestReadStringZeroAddrReturnsEmpty() {
	t := &ProcTracee{PID: 1}
	got, err := t.ReadString(0)
	s.Require().NoError(err)
	s.Require().Equal("", got)
}

func (s *ProcTraceeSuite) TestReadStringStopsAtNull() {
	buf := make([]byte, 32)
	copy(buf, []byte("/bin/ls\x00junkjunkjunkjunk"))
	m := &memBuf{pages: map[uintptr][]byte{0x1000: buf}}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	got, err := t.ReadString(0x1000)
	s.Require().NoError(err)
	s.Require().Equal("/bin/ls", got)
}

func (s *ProcTraceeSuite) TestReadStringNoNullReturnsFullBuffer() {
	// A PATHMAX-sized page of all 'A's with no terminator. ReadString must
	// return the full buffer (not loop forever).
	page := make([]byte, PATHMAX)
	for i := range page {
		page[i] = 'A'
	}
	m := &memBuf{pages: map[uintptr][]byte{0x2000: page}}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	got, err := t.ReadString(0x2000)
	s.Require().NoError(err)
	s.Require().Len(got, PATHMAX)
}

func (s *ProcTraceeSuite) TestReadStringESRCHReturnsGone() {
	m := &memBuf{err: unix.ESRCH}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadString(0x1000)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadStringENOENTReturnsGone() {
	m := &memBuf{err: unix.ENOENT}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadString(0x1000)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadStringEFAULTFallsThroughToPartialRead() {
	// EFAULT + a non-zero n: we should return whatever was read before the
	// fault. The test injects a short page followed by EFAULT — the read
	// function returns (n=0, EFAULT) in reality; to simulate the "we got
	// bytes then hit a boundary" path we use a separate fake.
	firstCall := true
	readFn := func(_ int, local []unix.Iovec, _ []unix.RemoteIovec, _ uint) (int, error) {
		if firstCall {
			firstCall = false
			dst := unsafePtrToSlice(local[0].Base, int(local[0].Len))
			copy(dst, []byte("hi\x00"))
			return 3, unix.EFAULT
		}
		return 0, unix.ESRCH
	}
	t := &ProcTracee{PID: 1, ReadMem: readFn}
	got, err := t.ReadString(0x1000)
	s.Require().NoError(err)
	s.Require().Equal("hi", got)
}

func (s *ProcTraceeSuite) TestReadStringEFAULTWithZeroReadReturnsGone() {
	readFn := func(_ int, _ []unix.Iovec, _ []unix.RemoteIovec, _ uint) (int, error) {
		return 0, unix.EFAULT
	}
	t := &ProcTracee{PID: 1, ReadMem: readFn}
	_, err := t.ReadString(0x1000)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadStringWrapsOtherErrors() {
	sentinel := errors.New("pvm: some other error")
	m := &memBuf{err: sentinel}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadString(0x1000)
	s.Require().Error(err)
	s.Require().ErrorIs(err, sentinel)
}

// --- ReadBytes ---

func (s *ProcTraceeSuite) TestReadBytesRejectsNonPositiveN() {
	t := &ProcTracee{PID: 1}
	_, err := t.ReadBytes(0x1000, 0)
	s.Require().Error(err)
	_, err = t.ReadBytes(0x1000, -3)
	s.Require().Error(err)
}

func (s *ProcTraceeSuite) TestReadBytesZeroAddrReturnsGone() {
	t := &ProcTracee{PID: 1}
	_, err := t.ReadBytes(0, 8)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadBytesHappyPath() {
	page := []byte{0x01, 0x00, 0x2f, 0x74, 0x6d, 0x70, 0x00, 0x00}
	m := &memBuf{pages: map[uintptr][]byte{0x1000: page}}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	got, err := t.ReadBytes(0x1000, 8)
	s.Require().NoError(err)
	s.Require().Equal(page, got)
}

func (s *ProcTraceeSuite) TestReadBytesESRCHReturnsGone() {
	m := &memBuf{err: unix.ESRCH}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadBytes(0x1000, 8)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadBytesENOENTReturnsGone() {
	m := &memBuf{err: unix.ENOENT}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadBytes(0x1000, 8)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadBytesShortReadReturnsGone() {
	page := []byte{0xAA, 0xBB}
	m := &memBuf{pages: map[uintptr][]byte{0x1000: page}}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadBytes(0x1000, 8)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadBytesWrapsOtherErrors() {
	sentinel := errors.New("pvm other")
	m := &memBuf{err: sentinel}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadBytes(0x1000, 8)
	s.Require().ErrorIs(err, sentinel)
}

// --- ReadPointerArray ---

func (s *ProcTraceeSuite) TestReadPointerArrayZeroAddrReturnsNil() {
	t := &ProcTracee{PID: 1}
	got, err := t.ReadPointerArray(0, 16)
	s.Require().NoError(err)
	s.Require().Nil(got)
}

func (s *ProcTraceeSuite) TestReadPointerArrayHappyPath() {
	// argv layout: [p1, p2, NULL] where p1→"/bin/git", p2→"push"
	const (
		argvAddr uintptr = 0x1000
		p1Addr   uintptr = 0x2000
		p2Addr   uintptr = 0x3000
	)
	m := &memBuf{pages: map[uintptr][]byte{
		argvAddr:      leWord(p1Addr),
		argvAddr + 8:  leWord(p2Addr),
		argvAddr + 16: leWord(0),
		p1Addr:        append([]byte("/bin/git"), 0),
		p2Addr:        append([]byte("push"), 0),
	}}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	got, err := t.ReadPointerArray(argvAddr, 0) // 0 → default ArgvMax
	s.Require().NoError(err)
	s.Require().Equal([]string{"/bin/git", "push"}, got)
}

func (s *ProcTraceeSuite) TestReadPointerArrayRespectsMaxEntries() {
	// Three entries, NULL never reached — we must stop at maxEntries.
	const argvAddr uintptr = 0x1000
	const p1 uintptr = 0x2000
	const p2 uintptr = 0x3000
	const p3 uintptr = 0x4000
	m := &memBuf{pages: map[uintptr][]byte{
		argvAddr:      leWord(p1),
		argvAddr + 8:  leWord(p2),
		argvAddr + 16: leWord(p3),
		p1:            append([]byte("a"), 0),
		p2:            append([]byte("b"), 0),
		p3:            append([]byte("c"), 0),
	}}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	got, err := t.ReadPointerArray(argvAddr, 2)
	s.Require().NoError(err)
	s.Require().Equal([]string{"a", "b"}, got)
}

func (s *ProcTraceeSuite) TestReadPointerArrayESRCHReturnsGone() {
	m := &memBuf{err: unix.ESRCH}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadPointerArray(0x1000, 4)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadPointerArrayENOENTReturnsGone() {
	m := &memBuf{err: unix.ENOENT}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadPointerArray(0x1000, 4)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadPointerArrayShortReadReturnsGone() {
	// ReadMem returns n=0, nil — less than wordSize → ErrTraceeGone.
	readFn := func(_ int, _ []unix.Iovec, _ []unix.RemoteIovec, _ uint) (int, error) {
		return 0, nil
	}
	t := &ProcTracee{PID: 1, ReadMem: readFn}
	_, err := t.ReadPointerArray(0x1000, 4)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestReadPointerArrayWrapsUnknownErrors() {
	sentinel := errors.New("pvm err")
	m := &memBuf{err: sentinel}
	t := &ProcTracee{PID: 1, ReadMem: m.read}
	_, err := t.ReadPointerArray(0x1000, 4)
	s.Require().ErrorIs(err, sentinel)
}

func (s *ProcTraceeSuite) TestReadPointerArrayStringReadErrorPropagates() {
	// Pointer walk succeeds but the referenced string read returns a
	// non-gone, non-EFAULT error — must propagate.
	const argvAddr uintptr = 0x1000
	const p1 uintptr = 0x2000
	sentinel := errors.New("x")
	callCount := 0
	readFn := func(_ int, local []unix.Iovec, remote []unix.RemoteIovec, _ uint) (int, error) {
		callCount++
		if remote[0].Base == argvAddr {
			dst := unsafePtrToSlice(local[0].Base, int(local[0].Len))
			copy(dst, leWord(p1))
			return 8, nil
		}
		return 0, sentinel
	}
	t := &ProcTracee{PID: 1, ReadMem: readFn}
	_, err := t.ReadPointerArray(argvAddr, 4)
	s.Require().ErrorIs(err, sentinel)
}

// --- ResolveDirfd ---

func (s *ProcTraceeSuite) TestResolveDirfdNumericFd() {
	var gotPath string
	rl := func(path string, buf []byte) (int, error) {
		gotPath = path
		n := copy(buf, "/work/sub")
		return n, nil
	}
	t := &ProcTracee{PID: 99, Readlink: rl}
	got, err := t.ResolveDirfd(5)
	s.Require().NoError(err)
	s.Require().Equal("/work/sub", got)
	s.Require().Equal("/proc/99/fd/5", gotPath)
}

func (s *ProcTraceeSuite) TestResolveDirfdATFDCWDReadsCwd() {
	var gotPath string
	rl := func(path string, buf []byte) (int, error) {
		gotPath = path
		n := copy(buf, "/home/agent")
		return n, nil
	}
	t := &ProcTracee{PID: 99, Readlink: rl}
	got, err := t.ResolveDirfd(AtFDCWD)
	s.Require().NoError(err)
	s.Require().Equal("/home/agent", got)
	s.Require().Equal("/proc/99/cwd", gotPath)
}

func (s *ProcTraceeSuite) TestResolveDirfdESRCHReturnsGone() {
	rl := func(string, []byte) (int, error) { return 0, unix.ESRCH }
	t := &ProcTracee{PID: 1, Readlink: rl}
	_, err := t.ResolveDirfd(5)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestResolveDirfdENOENTReturnsGone() {
	rl := func(string, []byte) (int, error) { return 0, unix.ENOENT }
	t := &ProcTracee{PID: 1, Readlink: rl}
	_, err := t.ResolveDirfd(5)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *ProcTraceeSuite) TestResolveDirfdWrapsUnknownErrors() {
	sentinel := errors.New("rl err")
	rl := func(string, []byte) (int, error) { return 0, sentinel }
	t := &ProcTracee{PID: 1, Readlink: rl}
	_, err := t.ResolveDirfd(5)
	s.Require().ErrorIs(err, sentinel)
}

// --- EvalSymlinks ---

// fakeLinkFS is an in-memory tree for EvalSymlinks' walk: links maps a
// symlink to its target, dirs/files list the plain entries. Anything else
// is ENOENT.
type fakeLinkFS struct {
	links   map[string]string
	plain   map[string]bool
	status  string
	lstats  []string
	lstatFn func(string) error
	readErr error
}

type fakeFileInfo struct {
	os.FileInfo
	mode os.FileMode
}

func (f fakeFileInfo) Mode() os.FileMode { return f.mode }

func (f *fakeLinkFS) lstat(p string) (os.FileInfo, error) {
	f.lstats = append(f.lstats, p)
	if f.lstatFn != nil {
		if err := f.lstatFn(p); err != nil {
			return nil, err
		}
	}
	if _, ok := f.links[p]; ok {
		return fakeFileInfo{mode: os.ModeSymlink | 0o777}, nil
	}
	if f.plain[p] {
		return fakeFileInfo{mode: 0o644}, nil
	}
	return nil, &os.PathError{Op: "lstat", Path: p, Err: unix.ENOENT}
}

func (f *fakeLinkFS) readlink(p string) (string, error) {
	if f.readErr != nil {
		return "", f.readErr
	}
	return f.links[p], nil
}

func (f *fakeLinkFS) readFile(p string) ([]byte, error) {
	if f.status == "" {
		return nil, &os.PathError{Op: "open", Path: p, Err: unix.ENOENT}
	}
	return []byte(f.status), nil
}

func (f *fakeLinkFS) tracee(pid int) *ProcTracee {
	return &ProcTracee{PID: pid, Lstat: f.lstat, ReadlinkPath: f.readlink, ReadFile: f.readFile}
}

func plainSet(paths ...string) map[string]bool {
	out := map[string]bool{}
	for _, p := range paths {
		out[p] = true
	}
	return out
}

func (s *ProcTraceeSuite) TestEvalSymlinksWalk() {
	cases := []struct {
		name  string
		links map[string]string
		plain []string
		in    string
		want  string
	}{
		{"plain path", nil, []string{"/work", "/work/a"}, "/work/a", "/work/a"},
		{"root", nil, nil, "/", "/"},
		{"dot, dotdot and double slashes", nil, []string{"/work", "/work/a", "/work/b"}, "//work/./a/../b/", "/work/b"},
		{"dotdot above root", nil, []string{"/work"}, "/../work", "/work"},
		{"relative link", map[string]string{"/work/l": "a"}, []string{"/work", "/work/a", "/work/a/x"}, "/work/l/x", "/work/a/x"},
		{"absolute link", map[string]string{"/work/l": "/etc"}, []string{"/work", "/etc", "/etc/x"}, "/work/l/x", "/etc/x"},
		{"link with dotdot", map[string]string{"/work/sub/l": "../a"}, []string{"/work", "/work/sub", "/work/a"}, "/work/sub/l", "/work/a"},
		{"chained links", map[string]string{"/a": "/b", "/b": "c", "/c": "/work"}, []string{"/work", "/work/f"}, "/a/f", "/work/f"},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			fs := &fakeLinkFS{links: c.links, plain: plainSet(c.plain...)}
			got, err := fs.tracee(1).EvalSymlinks(c.in)
			s.Require().NoError(err)
			s.Require().Equal(c.want, got)
		})
	}
}

func (s *ProcTraceeSuite) TestEvalSymlinksPinsProcSelfToTracee() {
	// The tracee (thread 78 of process 77) reopens its fd 5 via /dev/fd/5.
	// /dev/fd links to /proc/self/fd; walked in the gate, /proc/self would
	// be the gate. It must become /proc/77, whose fd 5 is the tracee's.
	fs := &fakeLinkFS{
		links: map[string]string{
			"/dev/fd":                "/proc/self/fd",
			"/proc/77/fd/5":          "/work/.git/config",
			"/proc/77/task/78/fd/3":  "/work/.git/hooks/pre-commit",
			"/proc/77/task/78/fd/4":  "/proc/self/fd/5",
			"/proc/thread-self-link": "/proc/thread-self",
		},
		plain: plainSet("/dev", "/proc", "/proc/77", "/proc/77/fd", "/proc/77/task", "/proc/77/task/78", "/proc/77/task/78/fd",
			"/work", "/work/.git", "/work/.git/config", "/work/.git/hooks", "/work/.git/hooks/pre-commit"),
		status: "Name:\tgit\nTgid:\t77\nPid:\t78\n",
	}
	cases := []struct {
		in, want string
	}{
		{"/dev/fd/5", "/work/.git/config"},
		{"/proc/self/fd/5", "/work/.git/config"},
		{"/proc/thread-self/fd/3", "/work/.git/hooks/pre-commit"},
		{"/proc/thread-self-link/fd/3", "/work/.git/hooks/pre-commit"},
		{"/proc/thread-self/fd/4", "/work/.git/config"},
	}
	for _, c := range cases {
		got, err := fs.tracee(78).EvalSymlinks(c.in)
		s.Require().NoError(err, c.in)
		s.Require().Equal(c.want, got, c.in)
	}
	s.Require().NotContains(fs.lstats, "/proc/self")
	s.Require().NotContains(fs.lstats, "/proc/thread-self")
}

func (s *ProcTraceeSuite) TestEvalSymlinksErrors() {
	sentinel := errors.New("boom")
	cases := []struct {
		name string
		fs   *fakeLinkFS
		in   string
		want error
	}{
		{"missing component", &fakeLinkFS{plain: plainSet("/work")}, "/work/nope/x", unix.ENOENT},
		{"lstat error", &fakeLinkFS{lstatFn: func(string) error { return sentinel }}, "/work", sentinel},
		{"readlink error", &fakeLinkFS{links: map[string]string{"/l": "/x"}, readErr: sentinel}, "/l", sentinel},
		{"symlink loop", &fakeLinkFS{links: map[string]string{"/a": "/b", "/b": "/a"}}, "/a", unix.ELOOP},
		{"self loop", &fakeLinkFS{links: map[string]string{"/a": "a"}}, "/a/x", unix.ELOOP},
		{"tracee gone while pinning /proc/self", &fakeLinkFS{plain: plainSet("/proc")}, "/proc/self/fd/1", ErrTraceeGone},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			_, err := c.fs.tracee(1).EvalSymlinks(c.in)
			s.Require().ErrorIs(err, c.want)
		})
	}
}

func (s *ProcTraceeSuite) TestEvalSymlinksHopLimit() {
	// A chain of exactly maxSymlinkHops links resolves; one more is ELOOP.
	chain := func(n int) *fakeLinkFS {
		fs := &fakeLinkFS{links: map[string]string{}, plain: plainSet("/end")}
		for i := range n {
			next := "/end"
			if i+1 < n {
				next = "/l" + strconv.Itoa(i+1)
			}
			fs.links["/l"+strconv.Itoa(i)] = next
		}
		return fs
	}
	got, err := chain(maxSymlinkHops).tracee(1).EvalSymlinks("/l0")
	s.Require().NoError(err)
	s.Require().Equal("/end", got)
	_, err = chain(maxSymlinkHops + 1).tracee(1).EvalSymlinks("/l0")
	s.Require().ErrorIs(err, unix.ELOOP)
}

func (s *ProcTraceeSuite) TestEvalSymlinksRejectsRelativePath() {
	_, err := (&fakeLinkFS{}).tracee(1).EvalSymlinks("rel/path")
	s.Require().ErrorContains(err, "not absolute")
}

func (s *ProcTraceeSuite) TestEvalSymlinksNoTgid() {
	fs := &fakeLinkFS{plain: plainSet("/proc"), status: "Name:\tx\n"}
	_, err := fs.tracee(9).EvalSymlinks("/proc/self/fd/1")
	s.Require().ErrorContains(err, "no Tgid in /proc/9/status")
}

func (s *ProcTraceeSuite) TestEvalSymlinksRealProcessFd() {
	// Resolve our own fd through /proc/self/fd/N the way the gate would for
	// a tracee: the walk pins /proc/self to /proc/<pid> and follows the
	// magic link to the file.
	dir, err := filepath.EvalSymlinks(s.T().TempDir())
	s.Require().NoError(err)
	path := filepath.Join(dir, "target")
	f, err := os.Create(path)
	s.Require().NoError(err)
	defer f.Close()

	tr := NewProcTracee(os.Getpid())
	got, err := tr.EvalSymlinks("/proc/self/fd/" + strconv.Itoa(int(f.Fd())))
	s.Require().NoError(err)
	s.Require().Equal(path, got)
}

// --- Creds / status ---

func (s *ProcTraceeSuite) TestCredsParsesFsIDs() {
	var gotPath string
	t := &ProcTracee{PID: 55, ReadFile: func(p string) ([]byte, error) {
		gotPath = p
		return []byte("Name:\tgit\nUid:\t1000\t1001\t1002\t1003\nGid:\t20\t21\t22\t23\nGroups:\t\n"), nil
	}}
	uid, gid, err := t.Creds()
	s.Require().NoError(err)
	s.Require().Equal(1003, uid)
	s.Require().Equal(23, gid)
	s.Require().Equal("/proc/55/status", gotPath)
}

func (s *ProcTraceeSuite) TestCredsErrors() {
	sentinel := errors.New("read boom")
	cases := []struct {
		name    string
		status  string
		readErr error
		want    error
		msg     string
	}{
		{name: "process gone (ESRCH)", readErr: unix.ESRCH, want: ErrTraceeGone},
		{name: "process gone (ENOENT)", readErr: &os.PathError{Op: "open", Err: unix.ENOENT}, want: ErrTraceeGone},
		{name: "other read error", readErr: sentinel, want: sentinel},
		{name: "no Uid line", status: "Gid:\t1\t1\t1\t1\n", msg: "malformed Uid line"},
		{name: "short Uid line", status: "Uid:\t1\t1\t1\n", msg: "malformed Uid line"},
		{name: "bad Gid line", status: "Uid:\t1\t1\t1\t1\nGid:\t1\n", msg: "malformed Gid line"},
		{name: "non-numeric uid", status: "Uid:\t1\t1\t1\tx\nGid:\t1\t1\t1\t1\n", msg: "invalid syntax"},
		{name: "non-numeric gid", status: "Uid:\t1\t1\t1\t1\nGid:\t1\t1\t1\tx\n", msg: "invalid syntax"},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			t := &ProcTracee{PID: 1, ReadFile: func(string) ([]byte, error) {
				if c.readErr != nil {
					return nil, c.readErr
				}
				return []byte(c.status), nil
			}}
			_, _, err := t.Creds()
			if c.want != nil {
				s.Require().ErrorIs(err, c.want)
			} else {
				s.Require().ErrorContains(err, c.msg)
			}
		})
	}
}

func (s *ProcTraceeSuite) TestCredsRealProcess() {
	uid, gid, err := NewProcTracee(os.Getpid()).Creds()
	s.Require().NoError(err)
	s.Require().Equal(os.Getuid(), uid)
	s.Require().Equal(os.Getgid(), gid)
}

// --- bytesToPtrLE ---

func (s *ProcTraceeSuite) TestBytesToPtrLE() {
	s.Require().Equal(uintptr(0), bytesToPtrLE([]byte{0, 0, 0, 0, 0, 0, 0, 0}))
	s.Require().Equal(uintptr(0x1234), bytesToPtrLE([]byte{0x34, 0x12, 0, 0, 0, 0, 0, 0}))
	// All FF: largest value expressible in the lower bytes
	s.Require().Equal(uintptr(0xFFFFFFFFFFFFFFFF), bytesToPtrLE([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}))
}

// --- readlinkDefault (production wrapper) — run against /proc/self/exe to
// confirm the real call works without touching another process. ---

func (s *ProcTraceeSuite) TestReadlinkDefaultWorksAgainstProcSelf() {
	buf := make([]byte, PATHMAX)
	n, err := readlinkDefault("/proc/self/exe", buf)
	s.Require().NoError(err)
	s.Require().Greater(n, 0)
	s.Require().NotEmpty(string(buf[:n]))
}

// --- helpers ---

func leWord(v uintptr) []byte {
	b := make([]byte, 8)
	for i := range b {
		b[i] = byte(v >> (8 * i))
	}
	return b
}
