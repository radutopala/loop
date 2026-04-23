//go:build linux

package agentgate

import (
	"errors"
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
	s.Require().NotNil(t.EvalSymlinksFn)
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

func (s *ProcTraceeSuite) TestEvalSymlinksDelegatesToFn() {
	called := ""
	t := &ProcTracee{EvalSymlinksFn: func(p string) (string, error) {
		called = p
		return "/resolved", nil
	}}
	got, err := t.EvalSymlinks("/input")
	s.Require().NoError(err)
	s.Require().Equal("/resolved", got)
	s.Require().Equal("/input", called)
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
