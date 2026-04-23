//go:build linux

package agentgate

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// AckByte is the 1-byte success marker loop-syscallwrap expects after the
// server has registered the notify fd. Any other byte (or short read) means
// the registration failed.
const AckByte = 0x01

// HandshakeMax caps the channel-id body. 4 KiB is far beyond any realistic
// channel id; anything larger is treated as a malformed/malicious client.
const HandshakeMax = 4096

// ReceiveHandshake reads the wire format described in cmd/loop-syscallwrap:
//
//	[4 bytes big-endian length N] [N bytes UTF-8 channel-id]
//
// plus a single SCM_RIGHTS control message carrying the notify fd. The
// returned fd is owned by the caller — close it via unix.Close on error.
func ReceiveHandshake(conn *net.UnixConn) (string, int, error) {
	buf := make([]byte, 4+HandshakeMax)
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobN, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return "", -1, fmt.Errorf("read handshake: %w", err)
	}
	if n < 4 {
		return "", -1, fmt.Errorf("handshake too short: %d bytes", n)
	}
	bodyLen := int(binary.BigEndian.Uint32(buf[:4]))
	if bodyLen > HandshakeMax {
		return "", -1, fmt.Errorf("handshake body length out of range: %d", bodyLen)
	}
	if 4+bodyLen > n {
		return "", -1, fmt.Errorf("handshake body truncated: want %d got %d", bodyLen, n-4)
	}
	channelID := string(buf[4 : 4+bodyLen])

	fd, err := parseSCMRightsFD(oob[:oobN])
	if err != nil {
		return "", -1, err
	}
	return channelID, fd, nil
}

// parseSCMRightsFD expects exactly one fd out of a single SCM_RIGHTS control
// message. More than one fd is a protocol violation — close any received to
// avoid a kernel-side fd leak and surface the error to the caller.
func parseSCMRightsFD(oob []byte) (int, error) {
	scms, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return -1, fmt.Errorf("parse scm: %w", err)
	}
	if len(scms) == 0 {
		return -1, errors.New("no scm messages")
	}
	fds, err := unix.ParseUnixRights(&scms[0])
	if err != nil {
		return -1, fmt.Errorf("parse unix rights: %w", err)
	}
	if len(fds) != 1 {
		for _, f := range fds {
			_ = unix.Close(f)
		}
		return -1, fmt.Errorf("expected 1 fd, got %d", len(fds))
	}
	return fds[0], nil
}

// SendAck writes the single AckByte back to the handshake peer. Any write
// error is returned so the caller can release the notify fd. Exported so
// the in-container loop-syscallwrap parent can reuse it against its
// socketpair peer-end.
func SendAck(conn *net.UnixConn) error {
	_, err := conn.Write([]byte{AckByte})
	return err
}
