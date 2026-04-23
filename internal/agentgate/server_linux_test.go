//go:build linux

package agentgate

import (
	"context"
	"errors"
	"io"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

type NotifyTransportSuite struct {
	suite.Suite
}

func TestNotifyTransportSuite(t *testing.T) {
	suite.Run(t, new(NotifyTransportSuite))
}

// --- struct layout guards ---

func (s *NotifyTransportSuite) TestSeccompStructSizes() {
	s.Require().Equal(uintptr(sizeofSeccompNotif), unsafe.Sizeof(seccompNotif{}))
	s.Require().Equal(uintptr(sizeofSeccompNotifResp), unsafe.Sizeof(seccompNotifResp{}))
	s.Require().Equal(uintptr(sizeofSeccompData), unsafe.Sizeof(seccompData{}))
}

// --- syscallName ---

func (s *NotifyTransportSuite) TestSyscallNameKnownSyscalls() {
	cases := []struct {
		nr   int32
		want string
	}{
		{int32(unix.SYS_EXECVE), "execve"},
		{int32(unix.SYS_EXECVEAT), "execveat"},
		{int32(unix.SYS_CONNECT), "connect"},
		{int32(unix.SYS_OPENAT), syscallOpenat},
		{int32(unix.SYS_OPENAT2), syscallOpenat2},
		{int32(unix.SYS_RENAMEAT2), syscallRenameat2},
		{int32(unix.SYS_UNLINKAT), syscallUnlinkat},
		{int32(unix.SYS_LINKAT), syscallLinkat},
		{int32(unix.SYS_SYMLINKAT), syscallSymlinkat},
		{int32(unix.SYS_FCHMODAT), syscallFchmodat},
		{int32(unix.SYS_FCHOWNAT), syscallFchownat},
		{int32(unix.SYS_MKDIRAT), syscallMkdirat},
	}
	for _, c := range cases {
		got, ok := syscallName(c.nr)
		s.Require().True(ok, "nr %d should map", c.nr)
		s.Require().Equal(c.want, got)
	}
}

func (s *NotifyTransportSuite) TestSyscallNameUnknown() {
	_, ok := syscallName(0x7FFFFFFF)
	s.Require().False(ok)
}

// --- Recv ---

// recvIoctl returns an IoctlFunc that fills the passed seccomp_notif with the
// given payload on a RECV request. It captures the last request number and
// pointer so tests can assert on the ioctl call.
func recvIoctl(notif seccompNotif, errno unix.Errno) (IoctlFunc, *uintptr) {
	var gotReq uintptr
	fn := func(_ int, req uintptr, arg unsafe.Pointer) (int, unix.Errno) {
		gotReq = req
		if errno == 0 {
			*(*seccompNotif)(arg) = notif
		}
		return 0, errno
	}
	return fn, &gotReq
}

func (s *NotifyTransportSuite) TestRecvFillsTrapFromNotif() {
	notif := seccompNotif{
		ID:  1234,
		PID: 4321,
		Data: seccompData{
			NR:   int32(unix.SYS_EXECVE),
			Args: [6]uint64{0x1000, 0x2000, 0, 0, 0, 0},
		},
	}
	fn, gotReq := recvIoctl(notif, 0)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	trap, err := nt.Recv(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(uintptr(unix.SECCOMP_IOCTL_NOTIF_RECV), *gotReq)
	s.Require().Equal(uint64(1234), trap.ID)
	s.Require().Equal(4321, trap.PID)
	s.Require().Equal("execve", trap.Syscall)
	s.Require().Equal(uint64(0x1000), trap.Args[0])
	s.Require().Equal(uint64(0x2000), trap.Args[1])
}

func (s *NotifyTransportSuite) TestRecvContextCanceledShortCircuits() {
	fn := func(_ int, _ uintptr, _ unsafe.Pointer) (int, unix.Errno) {
		s.FailNow("ioctl should not be called when ctx is already canceled")
		return 0, 0
	}
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := nt.Recv(ctx)
	s.Require().ErrorIs(err, context.Canceled)
}

func (s *NotifyTransportSuite) TestRecvEBADFIsEOF() {
	fn, _ := recvIoctl(seccompNotif{}, unix.EBADF)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}
	_, err := nt.Recv(context.Background())
	s.Require().ErrorIs(err, io.EOF)
}

// scriptedRecvIoctl returns an IoctlFunc that returns `errno` for the first
// `nFails` calls, then succeeds and fills in `notif`. Tracks the total call
// count so tests can assert the retry loop ran the expected number of times.
func scriptedRecvIoctl(nFails int, errno unix.Errno, notif seccompNotif) (IoctlFunc, *int) {
	var calls int
	fn := func(_ int, _ uintptr, arg unsafe.Pointer) (int, unix.Errno) {
		calls++
		if calls <= nFails {
			return 0, errno
		}
		*(*seccompNotif)(arg) = notif
		return 0, 0
	}
	return fn, &calls
}

func (s *NotifyTransportSuite) TestRecvENOENTRetriesThenSucceeds() {
	notif := seccompNotif{ID: 7, PID: 88, Data: seccompData{NR: int32(unix.SYS_EXECVE)}}
	fn, calls := scriptedRecvIoctl(3, unix.ENOENT, notif)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	trap, err := nt.Recv(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(uint64(7), trap.ID)
	s.Require().Equal("execve", trap.Syscall)
	s.Require().Equal(4, *calls, "should retry 3× before succeeding")
}

func (s *NotifyTransportSuite) TestRecvEINTRRetriesThenSucceeds() {
	notif := seccompNotif{ID: 11, PID: 22, Data: seccompData{NR: int32(unix.SYS_CONNECT)}}
	fn, calls := scriptedRecvIoctl(2, unix.EINTR, notif)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	trap, err := nt.Recv(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(uint64(11), trap.ID)
	s.Require().Equal("connect", trap.Syscall)
	s.Require().Equal(3, *calls, "should retry 2× before succeeding")
}

func (s *NotifyTransportSuite) TestRecvRetryRespectsContextCancel() {
	// First EINTR triggers the retry loop; we cancel ctx before the next
	// iteration so the loop exits with context.Canceled instead of
	// spinning forever on a signal that never stops firing.
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	fn := func(_ int, _ uintptr, _ unsafe.Pointer) (int, unix.Errno) {
		calls++
		cancel()
		return 0, unix.EINTR
	}
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	_, err := nt.Recv(ctx)
	s.Require().ErrorIs(err, context.Canceled)
	s.Require().Equal(1, calls)
}

func (s *NotifyTransportSuite) TestRecvOtherErrnoPropagates() {
	fn, _ := recvIoctl(seccompNotif{}, unix.EINVAL)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}
	_, err := nt.Recv(context.Background())
	s.Require().Error(err)
	s.Require().NotErrorIs(err, io.EOF)
	s.Require().ErrorIs(err, unix.EINVAL)
}

func (s *NotifyTransportSuite) TestRecvUnknownSyscallNRGetsSyntheticName() {
	// 0x7FFFFFFE is outside our trap set; ensure the dispatcher will see a
	// synthetic "nr_<N>" name so its default branch denies rather than falls
	// through to a random string.
	const unknownNR int32 = 0x7FFFFFFE
	notif := seccompNotif{ID: 99, PID: 42, Data: seccompData{NR: unknownNR}}
	fn, _ := recvIoctl(notif, 0)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	trap, err := nt.Recv(context.Background())
	s.Require().NoError(err)
	s.Require().Equal("nr_2147483646", trap.Syscall)
	s.Require().Equal(uint64(99), trap.ID)
}

// --- Send ---

// sendCapture returns an IoctlFunc that records the notif_resp argument for
// inspection.
func sendCapture(errno unix.Errno) (IoctlFunc, *seccompNotifResp, *uintptr) {
	var captured seccompNotifResp
	var gotReq uintptr
	fn := func(_ int, req uintptr, arg unsafe.Pointer) (int, unix.Errno) {
		gotReq = req
		captured = *(*seccompNotifResp)(arg)
		return 0, errno
	}
	return fn, &captured, &gotReq
}

func (s *NotifyTransportSuite) TestSendAllowSetsContinueFlag() {
	fn, captured, gotReq := sendCapture(0)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	err := nt.Send(context.Background(), TrapResponse{ID: 42, Allow: true})
	s.Require().NoError(err)
	s.Require().Equal(uintptr(unix.SECCOMP_IOCTL_NOTIF_SEND), *gotReq)
	s.Require().Equal(uint64(42), captured.ID)
	s.Require().Equal(uint32(unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE), captured.Flags)
	s.Require().Equal(int32(0), captured.Error)
}

func (s *NotifyTransportSuite) TestSendDenyDefaultsToEPERM() {
	fn, captured, _ := sendCapture(0)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	err := nt.Send(context.Background(), TrapResponse{ID: 9, Allow: false})
	s.Require().NoError(err)
	s.Require().Equal(uint64(9), captured.ID)
	s.Require().Equal(uint32(0), captured.Flags)
	s.Require().Equal(-int32(unix.EPERM), captured.Error)
}

func (s *NotifyTransportSuite) TestSendDenyWithExplicitErrno() {
	fn, captured, _ := sendCapture(0)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	err := nt.Send(context.Background(), TrapResponse{
		ID:       11,
		Allow:    false,
		ErrorNum: int32(unix.EACCES),
	})
	s.Require().NoError(err)
	s.Require().Equal(-int32(unix.EACCES), captured.Error)
}

func (s *NotifyTransportSuite) TestSendContextCanceledShortCircuits() {
	fn := func(_ int, _ uintptr, _ unsafe.Pointer) (int, unix.Errno) {
		s.FailNow("ioctl should not be called when ctx is already canceled")
		return 0, 0
	}
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := nt.Send(ctx, TrapResponse{ID: 1, Allow: true})
	s.Require().ErrorIs(err, context.Canceled)
}

func (s *NotifyTransportSuite) TestSendIoctlErrorPropagates() {
	fn, _, _ := sendCapture(unix.EINVAL)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	err := nt.Send(context.Background(), TrapResponse{ID: 1, Allow: true})
	s.Require().Error(err)
	s.Require().ErrorIs(err, unix.EINVAL)
}

// scriptedSendIoctl returns an IoctlFunc that fails with `errno` for the
// first `nFails` calls, then succeeds. Captures the total call count.
func scriptedSendIoctl(nFails int, errno unix.Errno) (IoctlFunc, *int, *seccompNotifResp) {
	var calls int
	var captured seccompNotifResp
	fn := func(_ int, _ uintptr, arg unsafe.Pointer) (int, unix.Errno) {
		calls++
		if calls <= nFails {
			return 0, errno
		}
		captured = *(*seccompNotifResp)(arg)
		return 0, 0
	}
	return fn, &calls, &captured
}

func (s *NotifyTransportSuite) TestSendEINTRRetriesThenSucceeds() {
	fn, calls, captured := scriptedSendIoctl(2, unix.EINTR)
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	err := nt.Send(context.Background(), TrapResponse{ID: 42, Allow: true})
	s.Require().NoError(err)
	s.Require().Equal(3, *calls, "should retry 2× before succeeding")
	s.Require().Equal(uint64(42), captured.ID)
	s.Require().Equal(uint32(unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE), captured.Flags)
}

func (s *NotifyTransportSuite) TestSendENOENTReturnsNil() {
	// Tracee died between Recv and Send — no one to unblock. Send should
	// return nil so the dispatcher moves on to the next trap.
	var calls int
	fn := func(_ int, _ uintptr, _ unsafe.Pointer) (int, unix.Errno) {
		calls++
		return 0, unix.ENOENT
	}
	nt := &NotifyTransport{FD: 7, IoctlFn: fn}

	err := nt.Send(context.Background(), TrapResponse{ID: 1, Allow: false})
	s.Require().NoError(err)
	s.Require().Equal(1, calls, "ENOENT should not retry — tracee is gone")
}

// --- Close ---

func (s *NotifyTransportSuite) TestCloseDelegates() {
	var calledFD int
	fn := func(fd int) error {
		calledFD = fd
		return nil
	}
	nt := &NotifyTransport{FD: 17, CloseFn: fn}
	s.Require().NoError(nt.Close())
	s.Require().Equal(17, calledFD)
}

func (s *NotifyTransportSuite) TestCloseSurfacesError() {
	sentinel := errors.New("close-broken")
	nt := &NotifyTransport{FD: 17, CloseFn: func(int) error { return sentinel }}
	s.Require().ErrorIs(nt.Close(), sentinel)
}

// --- NewNotifyTransport ---

func (s *NotifyTransportSuite) TestNewNotifyTransportWiresDefaults() {
	nt := NewNotifyTransport(42)
	s.Require().Equal(42, nt.FD)
	s.Require().NotNil(nt.IoctlFn)
	s.Require().NotNil(nt.CloseFn)
}

// rawIoctl is exercised via NewNotifyTransport(-1).Recv; the invalid fd makes
// the real SYS_IOCTL return EBADF, which Recv maps to io.EOF. This exercises
// the production ioctl wrapper without needing a real notify fd.
func (s *NotifyTransportSuite) TestRawIoctlAgainstInvalidFD() {
	nt := NewNotifyTransport(-1)
	_, err := nt.Recv(context.Background())
	s.Require().ErrorIs(err, io.EOF)
}

// --- NewProcTraceeFactory ---

func (s *NotifyTransportSuite) TestProcTraceeFactoryReturnsProcTracee() {
	factory := NewProcTraceeFactory()
	t := factory(1234)
	s.Require().NotNil(t)
	pt, ok := t.(*ProcTracee)
	s.Require().True(ok, "factory must return *ProcTracee")
	s.Require().Equal(1234, pt.PID)
	s.Require().NotNil(pt.ReadMem)
	s.Require().NotNil(pt.Readlink)
	s.Require().NotNil(pt.EvalSymlinksFn)
}
