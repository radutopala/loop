package dockerproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/types"
)

type SourceSuite struct {
	suite.Suite
}

func TestSourceSuite(t *testing.T) {
	suite.Run(t, new(SourceSuite))
}

// --- peerPIDFromContext ---

func (s *SourceSuite) TestPeerPIDFromContextReturnsStampedValue() {
	ctx := context.WithValue(context.Background(), peerPIDKey{}, 4242)
	require.Equal(s.T(), 4242, peerPIDFromContext(ctx))
}

func (s *SourceSuite) TestPeerPIDFromContextDefaultsToZero() {
	require.Equal(s.T(), 0, peerPIDFromContext(context.Background()))
}

func (s *SourceSuite) TestPeerPIDFromContextIgnoresWrongType() {
	// A non-int value under the same key must not panic; it just returns 0.
	ctx := context.WithValue(context.Background(), peerPIDKey{}, "nope")
	require.Equal(s.T(), 0, peerPIDFromContext(ctx))
}

// --- sourceForPeer ---

func (s *SourceSuite) TestSourceForPeerZeroPIDIsChat() {
	require.Equal(s.T(), "chat", sourceForPeer(0, func(int) string { return "terminal:x" }))
}

func (s *SourceSuite) TestSourceForPeerNilLookupIsChat() {
	require.Equal(s.T(), "chat", sourceForPeer(42, nil))
}

func (s *SourceSuite) TestSourceForPeerEmptyLookupIsChat() {
	require.Equal(s.T(), "chat", sourceForPeer(42, func(int) string { return "" }))
}

func (s *SourceSuite) TestSourceForPeerPassesThroughLookup() {
	got := sourceForPeer(42, func(pid int) string {
		require.Equal(s.T(), 42, pid)
		return "terminal:leaf-1"
	})
	require.Equal(s.T(), "terminal:leaf-1", got)
}

// --- connContextPeerPID ---

func (s *SourceSuite) TestConnContextPeerPIDIgnoresNonUnixConn() {
	// A net.Pipe() connection is not a *net.UnixConn, so the hook must
	// return the input context unchanged.
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	parent := context.Background()
	got := connContextPeerPID(parent, a)
	require.Equal(s.T(), parent, got)
}

func (s *SourceSuite) TestConnContextPeerPIDStampsOnUnixConn() {
	dir, err := os.MkdirTemp("", "loop-px-src")
	require.NoError(s.T(), err)
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "s")

	ln, err := net.Listen("unix", sock)
	require.NoError(s.T(), err)
	defer func() { _ = ln.Close() }()

	type result struct {
		pid int
		ok  bool
	}
	got := make(chan result, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			got <- result{}
			return
		}
		defer func() { _ = c.Close() }()
		ctx := connContextPeerPID(context.Background(), c)
		pid, ok := ctx.Value(peerPIDKey{}).(int)
		got <- result{pid: pid, ok: ok}
	}()

	client, err := net.Dial("unix", sock)
	require.NoError(s.T(), err)
	defer func() { _ = client.Close() }()

	select {
	case r := <-got:
		// On Linux the hook stamps a non-zero PID; on non-Linux builds
		// readPeerPID is the stub and returns 0, so the hook leaves the
		// context untouched (ok=false).
		if r.ok {
			require.Positive(s.T(), r.pid, "stamped PID should be positive")
		}
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out waiting for connContextPeerPID")
	}
}

// --- connContextWithReader (error / zero-PID branches via injected reader) ---

func (s *SourceSuite) TestConnContextWithReaderErrorLeavesCtxUntouched() {
	// We never call readPID for a non-unix conn — verified separately.
	// Here we drive the err != nil branch of the core. Building a real
	// *net.UnixConn without a peer is awkward, so we route through a
	// listener: the conn we hand to the core is a *net.UnixConn but our
	// injected reader claims the SO_PEERCRED syscall failed.
	dir, err := os.MkdirTemp("", "loop-px-err")
	require.NoError(s.T(), err)
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	require.NoError(s.T(), err)
	defer func() { _ = ln.Close() }()

	got := make(chan context.Context, 1)
	parent := context.Background()
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			got <- nil
			return
		}
		defer func() { _ = c.Close() }()
		got <- connContextWithReader(parent, c, func(*net.UnixConn) (int, error) {
			return 0, errors.New("SO_PEERCRED unavailable")
		})
	}()
	client, err := net.Dial("unix", sock)
	require.NoError(s.T(), err)
	defer func() { _ = client.Close() }()

	select {
	case ctx := <-got:
		require.Equal(s.T(), parent, ctx, "err path must return parent ctx unchanged")
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}
}

func (s *SourceSuite) TestConnContextWithReaderZeroPIDLeavesCtxUntouched() {
	dir, err := os.MkdirTemp("", "loop-px-zero")
	require.NoError(s.T(), err)
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	require.NoError(s.T(), err)
	defer func() { _ = ln.Close() }()

	got := make(chan context.Context, 1)
	parent := context.Background()
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			got <- nil
			return
		}
		defer func() { _ = c.Close() }()
		got <- connContextWithReader(parent, c, func(*net.UnixConn) (int, error) {
			return 0, nil // no error but no PID — e.g. non-Linux stub
		})
	}()
	client, err := net.Dial("unix", sock)
	require.NoError(s.T(), err)
	defer func() { _ = client.Close() }()

	select {
	case ctx := <-got:
		require.Equal(s.T(), parent, ctx, "zero-PID path must return parent ctx unchanged")
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}
}

// --- server integration: PeerSource attribution end-to-end ---

func (s *SourceSuite) TestServeHTTPDefaultsSourceToChatWithoutPeer() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stop()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/x$"}, Decision: types.DecisionApprove},
		}, nil)
	require.NoError(s.T(), err)

	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow}}
	srv, err := NewServer(ServerConfig{
		CID:        "cid-1",
		ChannelID:  "ch-1",
		Policy:     policy,
		Approver:   ap,
		DockerSock: sock,
		Now:        time.Now,
		// PeerSource left nil — server falls back to defaultPeerSource;
		// no peer PID on the bare httptest request → sourceForPeer("chat").
	})
	require.NoError(s.T(), err)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/x", nil))

	require.Equal(s.T(), http.StatusOK, rr.Code)
	require.Len(s.T(), ap.calls, 1)
	require.Equal(s.T(), "chat", ap.calls[0].Source)
}

func (s *SourceSuite) TestServeHTTPUsesPeerSourceLookup() {
	sock, stop := upstreamUnix(s.T(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stop()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/x$"}, Decision: types.DecisionApprove},
		}, nil)
	require.NoError(s.T(), err)

	ap := &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow}}
	var mu sync.Mutex
	var observedPID int
	srv, err := NewServer(ServerConfig{
		CID:        "cid-1",
		ChannelID:  "ch-1",
		Policy:     policy,
		Approver:   ap,
		DockerSock: sock,
		Now:        time.Now,
		PeerSource: func(pid int) string {
			mu.Lock()
			observedPID = pid
			mu.Unlock()
			return "terminal:leaf-42"
		},
	})
	require.NoError(s.T(), err)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), peerPIDKey{}, 9999))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	require.Equal(s.T(), http.StatusOK, rr.Code)
	require.Len(s.T(), ap.calls, 1)
	require.Equal(s.T(), "terminal:leaf-42", ap.calls[0].Source)
	mu.Lock()
	require.Equal(s.T(), 9999, observedPID)
	mu.Unlock()
}
