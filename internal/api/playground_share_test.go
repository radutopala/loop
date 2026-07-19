package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
)

// fakeTunnel is a TunnelManager double that records calls without spawning
// cloudflared.
type fakeTunnel struct {
	mu         sync.Mutex
	url        string
	running    bool
	startErr   error
	startCalls int
	stopCalls  int
}

func (f *fakeTunnel) Start(_ context.Context, _ int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	if f.startErr != nil {
		return "", f.startErr
	}
	f.running = true
	if f.url == "" {
		f.url = "https://fake-tunnel.trycloudflare.com"
	}
	return f.url, nil
}

func (f *fakeTunnel) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	f.running = false
}

func (f *fakeTunnel) PublicURL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.url
}

func (f *fakeTunnel) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// enableShare sets up a playground dir + enabled config + fake tunnel.
func (s *ServerSuite) enableShare() (dir string, ft *fakeTunnel) {
	dir = s.setPlaygroundDir()
	// Seed a playground so its dir resolves and has files to serve.
	pgDir := filepath.Join(dir, "playground", "demo")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "index.html"), []byte("<h1>Demo</h1>"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "script.js"), []byte("console.log(1)"), 0o644))
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{PlaygroundShare: config.PlaygroundShareConfig{Enabled: true}}, nil
	}
	ft = &fakeTunnel{}
	s.srv.SetTunnelManager(ft)
	return dir, ft
}

// --- token store ---

func (s *ServerSuite) TestShareTokenOpaqueAndStable() {
	st := newShareStore()
	t1 := st.add("demo", "global", "", "/abs/demo")
	require.Len(s.T(), t1, 32)
	require.NotContains(s.T(), t1, "demo")
	// Re-adding the same dir returns the same token.
	t2 := st.add("demo", "global", "", "/abs/demo")
	require.Equal(s.T(), t1, t2)
	require.Equal(s.T(), 1, st.count())
	// A different dir gets a different token.
	t3 := st.add("other", "global", "", "/abs/other")
	require.NotEqual(s.T(), t1, t3)
	require.Equal(s.T(), 2, st.count())
}

func (s *ServerSuite) TestShareIdempotentByDirAcrossChannels() {
	// The same project playground shared from two different channels/threads
	// that resolve to the same dir must collapse to one token — not two.
	st := newShareStore()
	t1 := st.add("foo", "project", "chanA", "/proj/.loop/playground/foo")
	t2 := st.add("foo", "project", "chanB", "/proj/.loop/playground/foo")
	require.Equal(s.T(), t1, t2)
	require.Equal(s.T(), 1, st.count())
	// A worktree thread resolves to a distinct dir → separate share.
	t3 := st.add("foo", "project", "chanC", "/proj/.worktrees/x/.loop/playground/foo")
	require.NotEqual(s.T(), t1, t3)
	require.Equal(s.T(), 2, st.count())
}

func (s *ServerSuite) TestShareStoreLookupAndRemove() {
	st := newShareStore()
	tok := st.add("demo", "global", "", "/abs/demo")
	e, ok := st.lookup(tok)
	require.True(s.T(), ok)
	require.Equal(s.T(), "/abs/demo", e.AbsDir)
	_, ok = st.lookup("nope")
	require.False(s.T(), ok)
	byDir, ok := st.lookupByDir("/abs/demo")
	require.True(s.T(), ok)
	require.Equal(s.T(), tok, byDir.Token)
	_, ok = st.lookupByDir("/abs/missing")
	require.False(s.T(), ok)
	require.True(s.T(), st.removeByDir("/abs/demo"))
	require.False(s.T(), st.removeByDir("/abs/demo"))
	require.Equal(s.T(), 0, st.count())
}

// --- endpoints ---

func (s *ServerSuite) TestPlaygroundShareDisabled() {
	s.setPlaygroundDir()
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{PlaygroundShare: config.PlaygroundShareConfig{Enabled: false}}, nil
	}
	rec := s.testRequest("PUT", "/api/playground/share?name=demo", "")
	require.Equal(s.T(), http.StatusForbidden, rec.Code)
}

func (s *ServerSuite) TestPlaygroundShareSuccess() {
	s.enableShare()
	s.srv.SetEventsHub(NewEventsHub(testLogger()))
	rec := s.testRequest("PUT", "/api/playground/share?name=demo", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp map[string]string
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(s.T(), resp["url"], "https://fake-tunnel.trycloudflare.com/p/")
	require.Len(s.T(), resp["token"], 32)
	require.Equal(s.T(), 1, s.srv.playground.shares.count())
}

func (s *ServerSuite) TestPlaygroundShareMissingName() {
	s.enableShare()
	rec := s.testRequest("PUT", "/api/playground/share", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlaygroundShareTunnelError() {
	_, ft := s.enableShare()
	ft.startErr = context.DeadlineExceeded
	rec := s.testRequest("PUT", "/api/playground/share?name=demo", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	// Failed tunnel start must not leave a dangling share.
	require.Equal(s.T(), 0, s.srv.playground.shares.count())
}

func (s *ServerSuite) TestPlaygroundUnshareStopsTunnel() {
	_, ft := s.enableShare()
	s.testRequest("PUT", "/api/playground/share?name=demo", "")
	require.True(s.T(), ft.Running())

	rec := s.testRequest("DELETE", "/api/playground/share?name=demo", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.Equal(s.T(), 0, s.srv.playground.shares.count())
	require.False(s.T(), ft.Running())
	require.Equal(s.T(), 1, ft.stopCalls)
}

func (s *ServerSuite) TestPlaygroundUnshareMissingName() {
	s.enableShare()
	rec := s.testRequest("DELETE", "/api/playground/share", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlaygroundShareParallelReusesTunnel() {
	dir, ft := s.enableShare()
	// Seed a second playground.
	pg2 := filepath.Join(dir, "playground", "demo2")
	require.NoError(s.T(), os.MkdirAll(pg2, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pg2, "index.html"), []byte("<h1>2</h1>"), 0o644))

	s.testRequest("PUT", "/api/playground/share?name=demo", "")
	s.testRequest("PUT", "/api/playground/share?name=demo2", "")
	require.Equal(s.T(), 2, s.srv.playground.shares.count())
	// One tunnel serves both — Start called per share but the fake stays up;
	// crucially only one listener exists and Stop hasn't fired.
	require.True(s.T(), ft.Running())

	// Removing one leaves the tunnel up.
	s.testRequest("DELETE", "/api/playground/share?name=demo", "")
	require.True(s.T(), ft.Running())
	require.Equal(s.T(), 0, ft.stopCalls)
	// Removing the last stops it.
	s.testRequest("DELETE", "/api/playground/share?name=demo2", "")
	require.False(s.T(), ft.Running())
	require.Equal(s.T(), 1, ft.stopCalls)
}

func (s *ServerSuite) TestPlaygroundShareList() {
	s.enableShare()
	s.testRequest("PUT", "/api/playground/share?name=demo", "")
	rec := s.testRequest("GET", "/api/playground/share", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp map[string][]map[string]string
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(s.T(), resp["shares"], 1)
	require.Equal(s.T(), "demo", resp["shares"][0]["name"])
	require.Contains(s.T(), resp["shares"][0]["url"], "/p/")
}

func (s *ServerSuite) TestShareListURLNoTunnel() {
	// A lingering share with no tunnel manager yields an empty URL.
	s.setPlaygroundDir()
	s.srv.playground.tunnel = nil
	s.srv.playground.shares.add("demo", "global", "", "/abs/demo")
	rec := s.testRequest("GET", "/api/playground/share", "")
	var resp map[string][]map[string]string
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "", resp["shares"][0]["url"])
}

func (s *ServerSuite) TestShareListURLTunnelDown() {
	// Tunnel present but not started (empty PublicURL) → empty URL.
	s.setPlaygroundDir()
	s.srv.SetTunnelManager(&fakeTunnel{})
	s.srv.playground.shares.add("demo", "global", "", "/abs/demo")
	rec := s.testRequest("GET", "/api/playground/share", "")
	var resp map[string][]map[string]string
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), "", resp["shares"][0]["url"])
}

func (s *ServerSuite) TestPlaygroundShareStatusShared() {
	s.enableShare()
	s.testRequest("PUT", "/api/playground/share?name=demo", "")
	rec := s.testRequest("GET", "/api/playground/share?name=demo", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), true, resp["shared"])
	require.Contains(s.T(), resp["url"], "/p/")
}

func (s *ServerSuite) TestPlaygroundShareStatusNotShared() {
	s.enableShare()
	rec := s.testRequest("GET", "/api/playground/share?name=demo", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), false, resp["shared"])
	require.Equal(s.T(), "", resp["url"])
}

func (s *ServerSuite) TestPlaygroundShareStatusBadName() {
	s.enableShare()
	rec := s.testRequest("GET", "/api/playground/share?name=..%2fetc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlaygroundUnshareBadName() {
	s.enableShare()
	rec := s.testRequest("DELETE", "/api/playground/share?name=..%2fetc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- serving via /p/{token} ---

func (s *ServerSuite) TestSharedPlaygroundServe() {
	s.enableShare()
	rec := s.testRequest("PUT", "/api/playground/share?name=demo", "")
	var resp map[string]string
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	token := resp["token"]

	// Serve index via the shared handler directly (the ephemeral mux routes
	// /p/{token} to it).
	req := httptest.NewRequest("GET", "/p/"+token, nil)
	req.SetPathValue("token", token)
	rec2 := httptest.NewRecorder()
	s.srv.playground.handleSharedPlaygroundServe(rec2, req)
	require.Equal(s.T(), http.StatusOK, rec2.Code)
	require.Contains(s.T(), rec2.Body.String(), "<h1>Demo</h1>")
	require.Contains(s.T(), rec2.Body.String(), `<base href="/p/`+token+`/">`)
}

func (s *ServerSuite) TestSharedPlaygroundServeUnknownToken() {
	s.enableShare()
	req := httptest.NewRequest("GET", "/p/bad", nil)
	req.SetPathValue("token", "bad")
	rec := httptest.NewRecorder()
	s.srv.playground.handleSharedPlaygroundServe(rec, req)
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestSharedPlaygroundServeFile() {
	s.enableShare()
	rec := s.testRequest("PUT", "/api/playground/share?name=demo", "")
	var resp map[string]string
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	token := resp["token"]

	req := httptest.NewRequest("GET", "/p/"+token+"/script.js", nil)
	req.SetPathValue("token", token)
	req.SetPathValue("path", "script.js")
	rec2 := httptest.NewRecorder()
	s.srv.playground.handleSharedPlaygroundServeFile(rec2, req)
	require.Equal(s.T(), http.StatusOK, rec2.Code)
	require.Contains(s.T(), rec2.Body.String(), "console.log(1)")
}

func (s *ServerSuite) TestSharedPlaygroundServeFileUnknownToken() {
	s.enableShare()
	req := httptest.NewRequest("GET", "/p/bad/x.js", nil)
	req.SetPathValue("token", "bad")
	req.SetPathValue("path", "x.js")
	rec := httptest.NewRecorder()
	s.srv.playground.handleSharedPlaygroundServeFile(rec, req)
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

// --- shutdown teardown ---

func (s *ServerSuite) TestStopTearsDownShareInfra() {
	_, ft := s.enableShare()
	s.testRequest("PUT", "/api/playground/share?name=demo", "")
	require.True(s.T(), ft.Running())
	_ = s.srv.Stop(context.Background())
	require.False(s.T(), ft.Running())
	require.GreaterOrEqual(s.T(), ft.stopCalls, 1)
}

// --- config-off default via real config ---

func (s *ServerSuite) TestPlaygroundShareEnabledDefaultsFalse() {
	s.srv.configs.load = func() (*config.Config, error) { return &config.Config{}, nil }
	require.False(s.T(), s.srv.playground.playgroundShareEnabled())
}

func (s *ServerSuite) TestPlaygroundShareEnabledConfigError() {
	s.srv.configs.load = func() (*config.Config, error) { return nil, os.ErrNotExist }
	require.False(s.T(), s.srv.playground.playgroundShareEnabled())
}

func (s *ServerSuite) TestPlaygroundShareEnabledNilLoaderFallsBackToConfigLoad() {
	// nil loadConfig falls back to the package-level config.Load. Point HOME at
	// an empty temp dir so config.Load reads a non-existent ~/.loop/config.json
	// (→ error → disabled), making the branch deterministic regardless of the
	// developer's real config.
	s.T().Setenv("HOME", s.T().TempDir())
	s.srv.configs.load = nil
	require.False(s.T(), s.srv.playground.playgroundShareEnabled())
}

func (s *ServerSuite) TestPlaygroundShareInvalidName() {
	s.enableShare()
	// A name failing the playground-name regex → resolve error → 400.
	rec := s.testRequest("PUT", "/api/playground/share?name=..%2fetc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureShareInfraNilTunnelManager() {
	s.setPlaygroundDir()
	s.srv.SetTunnelManager(nil)
	_, err := s.srv.playground.ensureShareInfra(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "tunnel manager not configured")
	// The ephemeral listener that got opened before the tunnel check must be
	// cleaned up so it doesn't leak across tests.
	s.srv.playground.stopShareInfra()
}

func (s *ServerSuite) TestEnsureShareInfraListenError() {
	s.setPlaygroundDir()
	s.srv.SetTunnelManager(&fakeTunnel{})
	s.srv.playground.listenTCP = func(string) (net.Listener, error) { return nil, os.ErrPermission }
	_, err := s.srv.playground.ensureShareInfra(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting playground listener")
}

func (s *ServerSuite) TestNoStoreHeader() {
	h := noStore(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/p/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(s.T(), "no-store", rec.Header().Get("Cache-Control"))
}
