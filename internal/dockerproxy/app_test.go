package dockerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/types"
)

type AppSuite struct {
	suite.Suite
	tempDir string
}

func TestAppSuite(t *testing.T) {
	suite.Run(t, new(AppSuite))
}

func (s *AppSuite) SetupTest() {
	s.tempDir = s.T().TempDir()
}

// minimalPolicyJSON returns a valid DockerProxyConfig JSON document.
func (s *AppSuite) minimalPolicyJSON() []byte {
	cfg := config.DockerProxyConfig{
		Enabled:         true,
		DefaultDecision: types.DecisionDeny,
		HTTPRules: []types.HTTPServiceRule{
			{Methods: []string{"GET"}, Paths: []string{"^/_ping$"}, Decision: types.DecisionAllow},
		},
	}
	b, err := json.Marshal(cfg)
	require.NoError(s.T(), err)
	return b
}

// stubApprover satisfies Approver without any network and returns a fixed Outcome.
type stubApprover struct {
	outcome agentgate.Outcome
}

func (a *stubApprover) Request(_ context.Context, _ string, _ agentgate.ApprovalRequest) agentgate.Outcome {
	return a.outcome
}

// stubListener is a minimal net.Listener that blocks Accept until Close.
type stubListener struct {
	closed chan struct{}
	once   sync.Once
	addr   stubAddr
}

type stubAddr struct{}

func (stubAddr) Network() string { return "unix" }
func (stubAddr) String() string  { return "stub" }

func newStubListener() *stubListener {
	return &stubListener{closed: make(chan struct{})}
}

func (l *stubListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *stubListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *stubListener) Addr() net.Addr { return l.addr }

// baseApp returns an app with all deps stubbed to safe defaults for a run
// that reaches serve() without touching the filesystem or network.
func (s *AppSuite) baseApp(env map[string]string, policy []byte) (*app, *stubListener, *int) {
	ln := newStubListener()
	chmodCalls := 0
	return &app{
		getenv: func(k string) string { return env[k] },
		readFile: func(path string) ([]byte, error) {
			require.Equal(s.T(), env[envPolicyFile], path)
			return policy, nil
		},
		removeAll:  func(_ string) error { return nil },
		listenUnix: func(_ string) (net.Listener, error) { return ln, nil },
		chmod: func(path string, mode os.FileMode) error {
			chmodCalls++
			require.Equal(s.T(), env[envSocket], path)
			require.Equal(s.T(), os.FileMode(0o666), mode)
			return nil
		},
		serve: func(ctx context.Context, _ net.Listener, h http.Handler) error {
			require.NotNil(s.T(), h)
			<-ctx.Done()
			return nil
		},
		notifyContext: context.WithCancel,
		newApprover: func(apiURL, token string) Approver {
			require.Equal(s.T(), env[envAPIURL], apiURL)
			require.Equal(s.T(), env[envToken], token)
			return &stubApprover{outcome: agentgate.Outcome{Decision: types.DecisionAllow}}
		},
	}, ln, &chmodCalls
}

func (s *AppSuite) minimalEnv() map[string]string {
	return map[string]string{
		envSocket:     "/var/run/docker.sock",
		envPolicyFile: filepath.Join(s.tempDir, "proxy-policy.json"),
		envUpstream:   "/var/run/docker.sock.host",
		envAPIURL:     "http://host.docker.internal:8080",
		envToken:      "tok-1",
		envCID:        "cid-1",
		envChannelID:  "ch-1",
	}
}

func (s *AppSuite) TestRunHappyPathCancelViaContext() {
	env := s.minimalEnv()
	a, _, chmodCalls := s.baseApp(env, s.minimalPolicyJSON())

	// Pre-cancel the notifyContext so serve returns immediately.
	a.notifyContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}

	var out bytes.Buffer
	require.NoError(s.T(), a.run(&out))
	require.Equal(s.T(), 1, *chmodCalls)
	require.Contains(s.T(), out.String(), "loop-dockerproxy started")
}

func (s *AppSuite) TestRunMissingPolicyFileEnvError() {
	a, _, _ := s.baseApp(s.minimalEnv(), nil)
	a.getenv = func(k string) string {
		if k == envPolicyFile {
			return ""
		}
		return s.minimalEnv()[k]
	}
	err := a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), envPolicyFile)
}

func (s *AppSuite) TestRunMissingAPIURLError() {
	env := s.minimalEnv()
	env[envAPIURL] = ""
	a, _, _ := s.baseApp(env, s.minimalPolicyJSON())
	err := a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), envAPIURL)
}

func (s *AppSuite) TestRunMissingTokenError() {
	env := s.minimalEnv()
	env[envToken] = ""
	a, _, _ := s.baseApp(env, s.minimalPolicyJSON())
	err := a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), envToken)
}

func (s *AppSuite) TestRunDefaultSocketAndUpstreamApplied() {
	env := s.minimalEnv()
	env[envSocket] = ""
	env[envUpstream] = ""

	a, _, chmodCalls := s.baseApp(env, s.minimalPolicyJSON())
	// chmod should now be invoked against defaultSocket, not env[envSocket].
	a.chmod = func(path string, mode os.FileMode) error {
		require.Equal(s.T(), defaultSocket, path)
		require.Equal(s.T(), os.FileMode(0o666), mode)
		*chmodCalls++
		return nil
	}
	// listenUnix should also be invoked with defaultSocket.
	called := false
	a.listenUnix = func(path string) (net.Listener, error) {
		called = true
		require.Equal(s.T(), defaultSocket, path)
		return newStubListener(), nil
	}
	a.notifyContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}

	require.NoError(s.T(), a.run(io.Discard))
	require.True(s.T(), called)
	require.Equal(s.T(), 1, *chmodCalls)
}

func (s *AppSuite) TestRunPolicyReadError() {
	a, _, _ := s.baseApp(s.minimalEnv(), nil)
	a.readFile = func(_ string) ([]byte, error) {
		return nil, errors.New("boom")
	}
	err := a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "read policy")
	require.Contains(s.T(), err.Error(), "boom")
}

func (s *AppSuite) TestRunPolicyParseError() {
	a, _, _ := s.baseApp(s.minimalEnv(), []byte("not-json"))
	err := a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parse policy")
}

func (s *AppSuite) TestRunCompilePolicyError() {
	// Compile rejects invalid regex in path rules.
	cfg := config.DockerProxyConfig{
		DefaultDecision: types.DecisionDeny,
		HTTPRules: []types.HTTPServiceRule{
			{Methods: []string{"GET"}, Paths: []string{"("}, Decision: types.DecisionAllow},
		},
	}
	b, err := json.Marshal(cfg)
	require.NoError(s.T(), err)

	a, _, _ := s.baseApp(s.minimalEnv(), b)
	err = a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "compile policy")
}

func (s *AppSuite) TestRunListenError() {
	a, _, _ := s.baseApp(s.minimalEnv(), s.minimalPolicyJSON())
	a.listenUnix = func(_ string) (net.Listener, error) {
		return nil, errors.New("bind refused")
	}
	err := a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listen unix")
	require.Contains(s.T(), err.Error(), "bind refused")
}

func (s *AppSuite) TestRunChmodErrorClosesListener() {
	ln := newStubListener()
	env := s.minimalEnv()
	a, _, _ := s.baseApp(env, s.minimalPolicyJSON())
	a.listenUnix = func(_ string) (net.Listener, error) { return ln, nil }
	a.chmod = func(_ string, _ os.FileMode) error {
		return errors.New("eperm")
	}
	err := a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "chmod")
	// Listener must be closed on chmod failure so bind is released.
	select {
	case <-ln.closed:
	default:
		s.T().Fatal("listener was not closed on chmod error")
	}
}

func (s *AppSuite) TestRunMainReturnsZeroOnSuccess() {
	env := s.minimalEnv()
	a, _, _ := s.baseApp(env, s.minimalPolicyJSON())
	a.notifyContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}
	code := runMain(io.Discard, io.Discard, a)
	require.Equal(s.T(), 0, code)
}

func (s *AppSuite) TestRunMainReturnsOneAndLogsOnError() {
	env := s.minimalEnv()
	env[envPolicyFile] = ""
	a, _, _ := s.baseApp(env, nil)
	var errOut bytes.Buffer
	code := runMain(io.Discard, &errOut, a)
	require.Equal(s.T(), 1, code)
	require.Contains(s.T(), errOut.String(), "loop-dockerproxy:")
	require.Contains(s.T(), errOut.String(), envPolicyFile)
}

// TestRunNewServerErrorReturned forces NewServer to fail by clearing the
// CID env var; the app must surface "build server:" to the caller.
func (s *AppSuite) TestRunNewServerErrorReturned() {
	env := s.minimalEnv()
	env[envCID] = ""
	a, _, _ := s.baseApp(env, s.minimalPolicyJSON())
	err := a.run(io.Discard)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "build server:")
}

// TestRunExportedEntrypoint covers the exported Run() which wraps
// runMain + newApp(). No env → policy-file required → exit 1.
func (s *AppSuite) TestRunExportedEntrypoint() {
	s.T().Setenv(envPolicyFile, "")
	code := Run(io.Discard, io.Discard)
	require.Equal(s.T(), 1, code)
}

func (s *AppSuite) TestDefaultListenUnixRoundTrip() {
	path := shortSockPath(s.T(), "a.sock")

	ln, err := defaultListenUnix(path)
	require.NoError(s.T(), err)
	defer ln.Close()

	// Dial and immediately close to prove the listener accepts.
	c, err := net.Dial("unix", path)
	require.NoError(s.T(), err)
	c.Close()
}

func (s *AppSuite) TestDefaultServeShutsDownOnContextCancel() {
	path := shortSockPath(s.T(), "d.sock")
	ln, err := defaultListenUnix(path)
	require.NoError(s.T(), err)
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	done := make(chan error, 1)
	go func() { done <- defaultServe(ctx, ln, handler) }()

	// Give Serve a tick to install, then cancel.
	cancel()
	select {
	case err := <-done:
		require.NoError(s.T(), err)
	case <-context.Background().Done():
		s.T().Fatal("serve did not return")
	}
}

// TestDefaultServeReturnsServeError covers the "err := <-errCh" case — when
// httpd.Serve returns a non-ErrServerClosed error (e.g. listener closed
// externally) before the context is cancelled.
func (s *AppSuite) TestDefaultServeReturnsServeError() {
	path := shortSockPath(s.T(), "bad.sock")
	ln, err := defaultListenUnix(path)
	require.NoError(s.T(), err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})

	done := make(chan error, 1)
	go func() { done <- defaultServe(ctx, ln, handler) }()

	// Close the listener; httpd.Serve returns net.ErrClosed (not
	// http.ErrServerClosed) which flows to errCh before ctx is done.
	require.NoError(s.T(), ln.Close())
	got := <-done
	require.Error(s.T(), got)
	require.ErrorIs(s.T(), got, net.ErrClosed)
}

func (s *AppSuite) TestDefaultServeProxiesHTTPBeforeCancel() {
	path := shortSockPath(s.T(), "e.sock")
	ln, err := defaultListenUnix(path)
	require.NoError(s.T(), err)
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "path="+r.URL.Path)
	})

	done := make(chan error, 1)
	go func() { done <- defaultServe(ctx, ln, handler) }()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
	}
	resp, err := client.Get("http://unix/hello")
	require.NoError(s.T(), err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(s.T(), "path=/hello", string(body))

	cancel()
	require.NoError(s.T(), <-done)
}

// TestDefaultNewApproverReturnsHTTPApprover ensures the production wire uses
// the real httpapprover.New. We hit a stub server with a token assertion to
// prove the returned Approver speaks the expected wire protocol.
func (s *AppSuite) TestDefaultNewApproverReturnsHTTPApprover() {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"decision":"allow","actor":"u"}`)
	}))
	defer srv.Close()

	approver := defaultNewApprover(srv.URL, "tok-xyz")
	require.NotNil(s.T(), approver)

	out := approver.Request(context.Background(), "ch", agentgate.ApprovalRequest{Kind: "k", Target: "t"})
	require.Equal(s.T(), types.DecisionAllow, out.Decision)
	require.True(s.T(), strings.HasPrefix(gotAuth, "Bearer tok-xyz"))
}

func (s *AppSuite) TestNewAppWiresAllFields() {
	a := newApp()
	require.NotNil(s.T(), a.getenv)
	require.NotNil(s.T(), a.readFile)
	require.NotNil(s.T(), a.removeAll)
	require.NotNil(s.T(), a.listenUnix)
	require.NotNil(s.T(), a.chmod)
	require.NotNil(s.T(), a.serve)
	require.NotNil(s.T(), a.notifyContext)
	require.NotNil(s.T(), a.newApprover)
	require.NotNil(s.T(), a.evalSymlinks)
	require.NotNil(s.T(), a.lookupVolume)
}

// TestRunStampsEvalSymlinksOnPolicy proves the SymlinkResolver wired on the
// app reaches every source_path_in body-rule check by the time serve runs.
// Without this stamp, the bypass closed by symlink resolution would silently
// regress — a body rule check would be evaluated against the literal source.
func (s *AppSuite) TestRunStampsEvalSymlinksOnPolicy() {
	cfg := config.DockerProxyConfig{
		DefaultDecision: types.DecisionAllow,
		BodyRules: []types.BodyRule{{
			AppliesTo:    "POST ^/containers/create$",
			MaxBodyBytes: 64 * 1024,
			JSONChecks: []types.JSONCheck{
				{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/$`}},
			},
			Decision: types.DecisionDeny,
		}},
	}
	policyJSON, err := json.Marshal(cfg)
	require.NoError(s.T(), err)

	env := s.minimalEnv()
	a, _, _ := s.baseApp(env, policyJSON)

	resolverCalls := 0
	a.evalSymlinks = func(p string) (string, error) {
		resolverCalls++
		return p, nil
	}

	// Capture the handler the serve func receives so we can drive a body-rule
	// match through it after run() returns.
	var captured http.Handler
	a.serve = func(ctx context.Context, _ net.Listener, h http.Handler) error {
		captured = h
		<-ctx.Done()
		return nil
	}
	a.notifyContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, func() {}
	}

	require.NoError(s.T(), a.run(io.Discard))
	require.NotNil(s.T(), captured)

	// Drive a docker-create body through the captured handler. The body's
	// Binds source is "/workdir/link" — no literal match for `^/$`. With the
	// resolver wired (returning the input unchanged), the request must NOT
	// fire the deny rule, but the resolver MUST be invoked.
	body := bytes.NewBufferString(`{"Binds":["/workdir/link:/host"]}`)
	req := httptest.NewRequest(http.MethodPost, "/containers/create", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	captured.ServeHTTP(rec, req)
	require.Greater(s.T(), resolverCalls, 0, "evalSymlinks must be invoked for source_path_in body rule")
}

func (s *AppSuite) TestDefaultNotifyContextReturnsUsableContext() {
	ctx, stop := defaultNotifyContext(context.Background())
	defer stop()
	require.NotNil(s.T(), ctx)
	require.NoError(s.T(), ctx.Err())
}

// nestedApp stubs an app for a run with the nested socket enabled. Each
// listenUnix call gets its own stubListener, recorded by path; serve
// records the handler per listener and returns nestedServeErr for the
// nested one.
type nestedApp struct {
	*app
	mu        sync.Mutex
	listeners map[string]*stubListener
	handlers  map[string]http.Handler
	served    chan string
}

func (s *AppSuite) nestedApp(env map[string]string, lookup func() (string, error), listenErr map[string]error, nestedServeErr error) *nestedApp {
	a, _, _ := s.baseApp(env, s.minimalPolicyJSON())
	n := &nestedApp{
		app:       a,
		listeners: map[string]*stubListener{},
		handlers:  map[string]http.Handler{},
		served:    make(chan string, 2),
	}
	a.chmod = func(_ string, mode os.FileMode) error {
		require.Equal(s.T(), os.FileMode(0o666), mode)
		return nil
	}
	a.evalSymlinks = noSymlinks
	a.lookupVolume = func(_ context.Context, upstream, cid, dir string) (string, error) {
		require.Equal(s.T(), env[envUpstream], upstream)
		require.Equal(s.T(), env[envCID], cid)
		require.Equal(s.T(), env[envNestedDir], dir)
		return lookup()
	}
	a.listenUnix = func(path string) (net.Listener, error) {
		if err := listenErr[path]; err != nil {
			return nil, err
		}
		ln := newStubListener()
		n.mu.Lock()
		n.listeners[path] = ln
		n.mu.Unlock()
		return ln, nil
	}
	a.serve = func(ctx context.Context, ln net.Listener, h http.Handler) error {
		n.mu.Lock()
		var path string
		for p, l := range n.listeners {
			if l == ln {
				path = p
			}
		}
		n.handlers[path] = h
		n.mu.Unlock()
		n.served <- path
		if path == "/run/loop-dproxy/docker.sock" {
			return nestedServeErr
		}
		// Wait for the nested serve so the test sees both.
		<-ctx.Done()
		return nil
	}
	return n
}

func (s *AppSuite) nestedEnv() map[string]string {
	env := s.minimalEnv()
	env[envNestedDir] = "/run/loop-dproxy"
	return env
}

func (s *AppSuite) TestRunServesNestedSocket() {
	for _, tc := range []struct {
		name     string
		serveErr error
		wantLog  string
	}{
		{name: "clean", wantLog: "nested_volume=vol-1"},
		{name: "nested serve fails", serveErr: errors.New("accept broke"), wantLog: "nested docker socket stopped"},
	} {
		s.Run(tc.name, func() {
			n := s.nestedApp(s.nestedEnv(), func() (string, error) { return "vol-1", nil }, nil, tc.serveErr)
			ctx, cancel := context.WithCancel(context.Background())
			n.notifyContext = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
			var out syncBuffer
			done := make(chan error, 1)
			go func() { done <- n.run(&out) }()
			got := map[string]bool{<-n.served: true, <-n.served: true}
			require.Equal(s.T(), map[string]bool{"/var/run/docker.sock": true, "/run/loop-dproxy/docker.sock": true}, got)
			require.Eventually(s.T(), func() bool { return strings.Contains(out.String(), tc.wantLog) }, time.Second, 5*time.Millisecond)
			cancel()
			require.NoError(s.T(), <-done)

			n.mu.Lock()
			defer n.mu.Unlock()
			srv := n.handlers["/run/loop-dproxy/docker.sock"].(*Server)
			require.Same(s.T(), srv, n.handlers["/var/run/docker.sock"])
			require.Equal(s.T(), "vol-1", srv.cfg.NestedVolume)
			require.NotNil(s.T(), srv.cfg.EvalSymlinks)
		})
	}
}

func (s *AppSuite) TestRunNestedUnavailableStillServes() {
	for _, tc := range []struct {
		name      string
		lookup    func() (string, error)
		listenErr map[string]error
		wantLog   string
	}{
		{
			name:    "lookup fails",
			lookup:  func() (string, error) { return "", errors.New("no such container") },
			wantLog: "no such container",
		},
		{
			name:      "nested listen fails",
			lookup:    func() (string, error) { return "vol-1", nil },
			listenErr: map[string]error{"/run/loop-dproxy/docker.sock": errors.New("read-only fs")},
			wantLog:   "read-only fs",
		},
	} {
		s.Run(tc.name, func() {
			n := s.nestedApp(s.nestedEnv(), tc.lookup, tc.listenErr, nil)
			n.notifyContext = func(parent context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(parent)
				cancel()
				return ctx, func() {}
			}
			var out bytes.Buffer
			require.NoError(s.T(), n.run(&out))
			require.Contains(s.T(), out.String(), "socket mounts will be rejected")
			require.Contains(s.T(), out.String(), tc.wantLog)
			require.Equal(s.T(), "/var/run/docker.sock", <-n.served)
			require.Len(s.T(), n.served, 0)
			require.Empty(s.T(), n.handlers["/var/run/docker.sock"].(*Server).cfg.NestedVolume)
		})
	}
}

// When setup fails after the nested socket is up, run closes it.
func (s *AppSuite) TestRunClosesNestedListenerOnError() {
	for _, tc := range []struct {
		name    string
		env     func(map[string]string)
		listen  map[string]error
		wantErr string
	}{
		{name: "build server", env: func(e map[string]string) { e[envCID] = "" }, wantErr: "build server:"},
		{name: "main listen", listen: map[string]error{"/var/run/docker.sock": errors.New("busy")}, wantErr: "busy"},
	} {
		s.Run(tc.name, func() {
			env := s.nestedEnv()
			if tc.env != nil {
				tc.env(env)
			}
			n := s.nestedApp(env, func() (string, error) { return "vol-1", nil }, tc.listen, nil)
			err := n.run(io.Discard)
			require.ErrorContains(s.T(), err, tc.wantErr)
			select {
			case <-n.listeners["/run/loop-dproxy/docker.sock"].closed:
			default:
				s.T().Fatal("nested listener not closed")
			}
		})
	}
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of two serve
// goroutines' loggers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (s *AppSuite) TestReadOnlyDirs() {
	require.Nil(s.T(), readOnlyDirs(""))
	require.Equal(s.T(), []string{"/a/.loop", "/b/.loop"}, readOnlyDirs("/a/.loop/::rel:/b/./.loop"))
}
