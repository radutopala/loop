package dockerproxy

import (
	"context"
	"net"
)

// peerPIDKey is the context key under which the ConnContext hook stores the
// SO_PEERCRED-derived peer PID. Read via peerPIDFromContext from the request
// handler.
type peerPIDKey struct{}

// PeerSourceLookup maps a unix-socket peer PID inside the agent container to
// an approval-source identifier:
//
//   - "" — attribution not possible (non-Linux, missing /proc, no marker found).
//     The caller treats an empty result as the chat agent so chat is the
//     default source.
//   - "terminal:<leafId>" — the peer (or one of its ancestors) carries
//     LOOP_TERMINAL_LEAF=<leafId> in its environment, which terminal-pane
//     exec'd shells receive via [terminal.Manager.CreateSessionWithEnv].
//
// On Linux the default implementation walks /proc starting at peerPID,
// reading each process's environ for the marker and following PPid up to
// init. On non-Linux builds the stub returns "".
type PeerSourceLookup func(peerPID int) string

// peerPIDFromContext returns the SO_PEERCRED-derived peer PID stored in ctx
// by the ConnContext hook, or 0 when no PID has been recorded (e.g. a
// non-unix conn, or the platform stub).
func peerPIDFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(peerPIDKey{}).(int); ok {
		return v
	}
	return 0
}

// connContextPeerPID is the ConnContext hook the dockerproxy installs on
// its http.Server. For unix-domain connections it reads SO_PEERCRED and
// stamps the peer PID onto the conn-level context; subsequent requests on
// the same conn inherit it via r.Context().
func connContextPeerPID(ctx context.Context, c net.Conn) context.Context {
	return connContextWithReader(ctx, c, readPeerPID)
}

// connContextWithReader is the testable core of connContextPeerPID. readPID
// is the SO_PEERCRED reader; tests inject stubs to drive the err / zero-PID
// branches without spawning real unix-socket pairs whose peer is always alive.
func connContextWithReader(ctx context.Context, c net.Conn, readPID func(*net.UnixConn) (int, error)) context.Context {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return ctx
	}
	pid, err := readPID(uc)
	if err != nil || pid == 0 {
		return ctx
	}
	return context.WithValue(ctx, peerPIDKey{}, pid)
}

// sourceForPeer maps a peer PID to an approval-source string with a "chat"
// fallback when the lookup is unavailable or returns no marker. It is the
// canonical way to derive the Source field for an ApprovalRequest.
func sourceForPeer(peerPID int, lookup PeerSourceLookup) string {
	if peerPID == 0 || lookup == nil {
		return "chat"
	}
	if s := lookup(peerPID); s != "" {
		return s
	}
	return "chat"
}
