//go:build linux

package agentgate

import (
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

type HandshakeSuite struct {
	suite.Suite
}

func TestHandshakeSuite(t *testing.T) {
	suite.Run(t, new(HandshakeSuite))
}

// socketPair returns two connected *net.UnixConn endpoints backed by a real
// kernel socketpair.
func socketPair(t testing.TB) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	return fdToUnixConn(t, fds[0], "a"), fdToUnixConn(t, fds[1], "b")
}

func fdToUnixConn(t testing.TB, fd int, name string) *net.UnixConn {
	t.Helper()
	f := os.NewFile(uintptr(fd), name)
	c, err := net.FileConn(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	uc, ok := c.(*net.UnixConn)
	require.True(t, ok)
	return uc
}

func memfdForTest(t testing.TB) int {
	t.Helper()
	fd, err := unix.MemfdCreate("loop-agentgate-test", 0)
	require.NoError(t, err)
	return fd
}

// --- ReceiveHandshake ---

func (s *HandshakeSuite) TestReceiveHandshakeHappyPath() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	fd := memfdForTest(s.T())
	defer func() { _ = unix.Close(fd) }()

	body := []byte("chan-abc")
	msg := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(msg[:4], uint32(len(body)))
	copy(msg[4:], body)
	oob := unix.UnixRights(fd)
	_, _, err := client.WriteMsgUnix(msg, oob, nil)
	s.Require().NoError(err)

	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	ch, gotFD, err := ReceiveHandshake(server)
	s.Require().NoError(err)
	s.Require().Equal("chan-abc", ch)
	s.Require().GreaterOrEqual(gotFD, 0)
	_ = unix.Close(gotFD)
}

func (s *HandshakeSuite) TestReceiveHandshakeReadError() {
	client, server := socketPair(s.T())
	s.Require().NoError(client.Close())
	s.Require().NoError(server.Close())

	_, _, err := ReceiveHandshake(server)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "read handshake")
}

func (s *HandshakeSuite) TestReceiveHandshakeTooShort() {
	client, server := socketPair(s.T())
	defer func() { _ = server.Close() }()

	_, err := client.Write([]byte{0x01, 0x02})
	s.Require().NoError(err)
	s.Require().NoError(client.Close())

	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	_, _, err = ReceiveHandshake(server)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "too short")
}

func (s *HandshakeSuite) TestReceiveHandshakeBodyLenOutOfRange() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	fd := memfdForTest(s.T())
	defer func() { _ = unix.Close(fd) }()

	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(HandshakeMax+1))
	oob := unix.UnixRights(fd)
	_, _, err := client.WriteMsgUnix(hdr, oob, nil)
	s.Require().NoError(err)

	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	_, _, err = ReceiveHandshake(server)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "out of range")
}

func (s *HandshakeSuite) TestReceiveHandshakeBodyTruncated() {
	client, server := socketPair(s.T())
	defer func() { _ = server.Close() }()

	// Claim body length 100 but send only 10 bytes of body.
	hdr := make([]byte, 4+10)
	binary.BigEndian.PutUint32(hdr[:4], uint32(100))
	for i := 4; i < len(hdr); i++ {
		hdr[i] = byte(i)
	}
	fd := memfdForTest(s.T())
	defer func() { _ = unix.Close(fd) }()
	oob := unix.UnixRights(fd)
	_, _, err := client.WriteMsgUnix(hdr, oob, nil)
	s.Require().NoError(err)
	s.Require().NoError(client.Close())

	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	_, _, err = ReceiveHandshake(server)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "truncated")
}

func (s *HandshakeSuite) TestReceiveHandshakeNoSCMRights() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	body := []byte("x")
	msg := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(msg[:4], uint32(len(body)))
	copy(msg[4:], body)
	// No OOB — regular Write.
	_, err := client.Write(msg)
	s.Require().NoError(err)

	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	_, _, err = ReceiveHandshake(server)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "no scm messages")
}

// --- parseSCMRightsFD ---

func (s *HandshakeSuite) TestParseSCMRightsFDMalformed() {
	// Craft a buffer with Cmsghdr.Len field set to 0 (invalid — must be at
	// least CmsgLen(0)). ParseSocketControlMessage rejects with EINVAL.
	buf := make([]byte, unix.CmsgLen(0))
	// Len is the first field; on linux amd64 the struct is {Len uint64, ...}.
	binary.LittleEndian.PutUint64(buf[:8], 0)
	_, err := parseSCMRightsFD(buf)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "parse scm")
}

func (s *HandshakeSuite) TestParseSCMRightsFDEmpty() {
	_, err := parseSCMRightsFD(nil)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "no scm messages")
}

func (s *HandshakeSuite) TestParseSCMRightsFDWrongCmsgType() {
	// Craft a well-formed cmsghdr but with Level != SOL_SOCKET. Parses OK but
	// ParseUnixRights rejects with EINVAL, exercising the "parse unix rights"
	// branch.
	payload := 4 // one int fd payload
	buf := make([]byte, unix.CmsgSpace(payload))
	binary.LittleEndian.PutUint64(buf[:8], uint64(unix.CmsgLen(payload)))
	// Level (int32) at offset 8, Type (int32) at offset 12.
	binary.LittleEndian.PutUint32(buf[8:12], uint32(unix.SOL_IP))
	binary.LittleEndian.PutUint32(buf[12:16], 0)

	_, err := parseSCMRightsFD(buf)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "parse unix rights")
}

func (s *HandshakeSuite) TestParseSCMRightsFDMultipleFdsClosesAndErrors() {
	fd1 := memfdForTest(s.T())
	fd2 := memfdForTest(s.T())
	oob := unix.UnixRights(fd1, fd2)

	// Round-trip through a socketpair so the kernel dup's the fds into the
	// receiver — parseSCMRightsFD must close those received dups, not our
	// original fd1/fd2.
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	_, _, err := client.WriteMsgUnix([]byte{0x00}, oob, nil)
	s.Require().NoError(err)

	buf := make([]byte, 1)
	recvOOB := make([]byte, unix.CmsgSpace(4*2))
	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	_, oobN, _, _, err := server.ReadMsgUnix(buf, recvOOB)
	s.Require().NoError(err)

	_, err = parseSCMRightsFD(recvOOB[:oobN])
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "expected 1 fd")

	// Our originals are still open — verify by closing them here without error.
	s.Require().NoError(unix.Close(fd1))
	s.Require().NoError(unix.Close(fd2))
}

// --- SendAck ---

func (s *HandshakeSuite) TestSendAckHappyPath() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	s.Require().NoError(SendAck(client))

	buf := make([]byte, 1)
	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	n, err := server.Read(buf)
	s.Require().NoError(err)
	s.Require().Equal(1, n)
	s.Require().Equal(byte(AckByte), buf[0])
}

func (s *HandshakeSuite) TestSendAckErrorPropagates() {
	client, server := socketPair(s.T())
	s.Require().NoError(client.Close())
	s.Require().NoError(server.Close())

	err := SendAck(client)
	s.Require().Error(err)
}
