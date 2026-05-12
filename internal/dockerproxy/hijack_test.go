package dockerproxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/types"
)

type HijackSuite struct {
	suite.Suite
}

func TestHijackSuite(t *testing.T) {
	suite.Run(t, new(HijackSuite))
}

// upstreamHijackEcho simulates Docker's exec/start: responds 200 with a
// Hijacked connection header, then echoes received bytes back to the client.
func upstreamHijackEcho(t *testing.T) (string, func(), *sync.WaitGroup) {
	t.Helper()
	sock := shortSockPath(t, "docker.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	var wg sync.WaitGroup

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				r := bufio.NewReader(c)
				// Discard the request headers.
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" {
						break
					}
				}
				// Respond with 200 + hijacked-style connection.
				resp := "HTTP/1.1 200 OK\r\n" +
					"Content-Type: application/vnd.docker.raw-stream\r\n" +
					"Connection: Upgrade\r\n\r\n"
				if _, err := c.Write([]byte(resp)); err != nil {
					return
				}
				// Echo bytes back.
				_, _ = io.Copy(c, r)
			}(conn)
		}
	}()

	return sock, func() {
		_ = ln.Close()
		_ = os.Remove(sock)
	}, &wg
}

// proxyListener wraps a Server behind a real unix socket so we can send a raw
// HTTP request with `Connection: Upgrade` — httptest's ResponseRecorder
// doesn't implement http.Hijacker.
func (s *HijackSuite) proxyListener(policy *Policy, upstreamSock string) (string, func()) {
	sock := shortSockPath(s.T(), "proxy.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(s.T(), err)

	srv, err := NewServer(ServerConfig{
		CID:        "cid-h",
		ChannelID:  "ch-h",
		Policy:     policy,
		Approver:   &fakeApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow}},
		DockerSock: upstreamSock,
		Now:        time.Now,
	})
	require.NoError(s.T(), err)
	httpd := &http.Server{Handler: srv, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = httpd.Serve(ln) }()
	return sock, func() {
		_ = httpd.Close()
		_ = ln.Close()
	}
}

func (s *HijackSuite) TestHijackAttach() {
	upstream, stopUp, wg := upstreamHijackEcho(s.T())
	defer func() { stopUp(); wg.Wait() }()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/[^/]+/attach$"}, Decision: types.DecisionAllow},
		}, nil)
	require.NoError(s.T(), err)
	proxySock, stopProxy := s.proxyListener(policy, upstream)
	defer stopProxy()

	client, err := net.Dial("unix", proxySock)
	require.NoError(s.T(), err)
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))

	// Write an HTTP request line + headers (no body, open-ended connection).
	req := "POST /containers/abc/attach?stream=1 HTTP/1.1\r\n" +
		"Host: docker\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: Upgrade\r\n\r\n"
	_, err = client.Write([]byte(req))
	require.NoError(s.T(), err)

	r := bufio.NewReader(client)
	// Read status line + headers.
	sawStatus := false
	for {
		line, err := r.ReadString('\n')
		require.NoError(s.T(), err)
		if !sawStatus {
			require.Contains(s.T(), line, "200 OK")
			sawStatus = true
		}
		if line == "\r\n" {
			break
		}
	}
	// Now write a payload and read it back echoed.
	_, err = client.Write([]byte("hello\n"))
	require.NoError(s.T(), err)
	got := make([]byte, 6)
	_, err = io.ReadFull(r, got)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello\n", string(got))

	// Half-close the client write side so upstream's io.Copy returns and the
	// upstream goroutine can exit cleanly. Without this, the test hangs in the
	// deferred wg.Wait().
	if uc, ok := client.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
}

// TestHijackRelaysFullUpstreamAfterClientHalfClose covers the real-world
// `docker run` (no -i) case: the client sends request headers then no body,
// half-closing its write side immediately. Upstream streams container
// stdout/stderr afterwards. With the original `<-done` (single wait), the
// client→upstream goroutine finishing first triggered the deferred
// upstream.Close() before the upstream→client copy could drain the payload —
// so `docker run`'s output silently vanished. This test fails without the
// `<-done; <-done` pair in hijack.go.
func (s *HijackSuite) TestHijackRelaysFullUpstreamAfterClientHalfClose() {
	sock := shortSockPath(s.T(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	s.Require().NoError(err)
	defer func() { _ = ln.Close() }()

	payload := strings.Repeat("x", 4096)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\n" +
			"Content-Type: application/vnd.docker.raw-stream\r\n" +
			"Connection: Upgrade\r\n\r\n"))
		// Block until the proxy half-closes upstream's write side (triggered
		// when client→upstream io.Copy sees EOF). Then stream the payload —
		// simulates container stdout/stderr arriving AFTER stdin closes.
		_, _ = io.Copy(io.Discard, r)
		// Small pause so the buggy proxy (if it returned early) has time to
		// Close() the conn before we try to write. Makes the failure
		// deterministic on any scheduler.
		time.Sleep(50 * time.Millisecond)
		_, _ = c.Write([]byte(payload))
	}()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/[^/]+/attach$"}, Decision: types.DecisionAllow},
		}, nil)
	s.Require().NoError(err)
	proxySock, stopProxy := s.proxyListener(policy, sock)
	defer stopProxy()

	client, err := net.Dial("unix", proxySock)
	s.Require().NoError(err)
	defer client.Close()
	s.Require().NoError(client.SetDeadline(time.Now().Add(5 * time.Second)))

	req := "POST /containers/abc/attach HTTP/1.1\r\nHost: docker\r\nContent-Length: 0\r\n\r\n"
	_, err = client.Write([]byte(req))
	s.Require().NoError(err)

	// Half-close write side: mirrors `docker run` with no stdin. The proxy's
	// client→upstream goroutine will see EOF and finish first.
	uc, ok := client.(*net.UnixConn)
	s.Require().True(ok)
	s.Require().NoError(uc.CloseWrite())

	r := bufio.NewReader(client)
	for {
		line, err := r.ReadString('\n')
		s.Require().NoError(err)
		if line == "\r\n" {
			break
		}
	}
	got, err := io.ReadAll(r)
	s.Require().NoError(err)
	s.Require().Equal(payload, string(got))

	<-serverDone
}

func (s *HijackSuite) TestHijackUpstreamUnreachable() {
	missing := shortSockPath(s.T(), "nothing.sock")
	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/[^/]+/attach$"}, Decision: types.DecisionAllow},
		}, nil)
	require.NoError(s.T(), err)
	proxySock, stop := s.proxyListener(policy, missing)
	defer stop()

	client, err := net.Dial("unix", proxySock)
	require.NoError(s.T(), err)
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	req := "POST /containers/abc/attach HTTP/1.1\r\nHost: docker\r\nContent-Length: 0\r\n\r\n"
	_, err = client.Write([]byte(req))
	require.NoError(s.T(), err)
	r := bufio.NewReader(client)
	line, err := r.ReadString('\n')
	require.NoError(s.T(), err)
	require.Contains(s.T(), line, "502")
}

// Ensure the non-hijacker response writer path returns 500 explicitly. We
// exercise that by invoking the hijack handler with httptest.Recorder, which
// doesn't implement http.Hijacker.
func (s *HijackSuite) TestHijackNoHijackerReturns500() {
	srv, err := NewServer(ServerConfig{
		CID:        "x",
		Policy:     func() *Policy { p, _ := CompilePolicy(types.DecisionAllow, nil, nil); return p }(),
		Approver:   &fakeApprover{},
		DockerSock: "/tmp/ignored",
	})
	require.NoError(s.T(), err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/containers/abc/attach", nil)
	srv.hijackProxy(rr, req)
	require.Equal(s.T(), http.StatusInternalServerError, rr.Code)
}

// upstreamAcceptAndClose binds a unix socket that immediately closes each
// accepted conn without sending any HTTP response. This triggers the
// readRawHeaders error branch in hijackProxy.
func upstreamAcceptAndClose(t *testing.T) (string, func()) {
	t.Helper()
	sock := shortSockPath(t, "close.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return sock, func() {
		_ = ln.Close()
		_ = os.Remove(sock)
	}
}

// When upstream closes before sending headers, hijackProxy must respond 502
// rather than panicking or hanging.
func (s *HijackSuite) TestHijackUpstreamClosesBeforeHeaders() {
	upstream, stop := upstreamAcceptAndClose(s.T())
	defer stop()

	policy, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"POST"}, Paths: []string{"^/containers/[^/]+/attach$"}, Decision: types.DecisionAllow},
		}, nil)
	require.NoError(s.T(), err)
	proxySock, stopProxy := s.proxyListener(policy, upstream)
	defer stopProxy()

	client, err := net.Dial("unix", proxySock)
	require.NoError(s.T(), err)
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))

	req := "POST /containers/abc/attach HTTP/1.1\r\nHost: docker\r\nContent-Length: 0\r\n\r\n"
	_, err = client.Write([]byte(req))
	require.NoError(s.T(), err)

	line, err := bufio.NewReader(client).ReadString('\n')
	require.NoError(s.T(), err)
	require.Contains(s.T(), line, "502")
}

// Unit-test readRawHeaders directly for the EOF-before-blank-line branch.
func (s *HijackSuite) TestReadRawHeadersReturnsErrOnTruncated() {
	r := bufio.NewReader(strings.NewReader("HTTP/1.1 200 OK\r\n"))
	_, err := readRawHeaders(r)
	require.Error(s.T(), err)
}

// Unit-test closeWrite's fall-through branch: a conn that doesn't implement
// CloseWrite should get a full Close() instead.
func (s *HijackSuite) TestCloseWriteFallsBackToFullClose() {
	c := &fullCloseConn{}
	closeWrite(c)
	require.True(s.T(), c.closed)
}

// fullCloseConn implements net.Conn but not CloseWrite, so closeWrite must fall
// through to Close().
type fullCloseConn struct {
	closed bool
}

func (f *fullCloseConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (f *fullCloseConn) Write(p []byte) (int, error)        { return len(p), nil }
func (f *fullCloseConn) Close() error                       { f.closed = true; return nil }
func (f *fullCloseConn) LocalAddr() net.Addr                { return nil }
func (f *fullCloseConn) RemoteAddr() net.Addr               { return nil }
func (f *fullCloseConn) SetDeadline(_ time.Time) error      { return nil }
func (f *fullCloseConn) SetReadDeadline(_ time.Time) error  { return nil }
func (f *fullCloseConn) SetWriteDeadline(_ time.Time) error { return nil }
