package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/browser"
)

type BrowserProfileResetSuite struct {
	suite.Suite
	srv        *Server
	browserMgr *mockBrowserProvider
	ts         *httptest.Server
}

func TestBrowserProfileResetSuite(t *testing.T) {
	suite.Run(t, new(BrowserProfileResetSuite))
}

func (s *BrowserProfileResetSuite) SetupTest() {
	s.srv = nilServer()
	s.browserMgr = new(mockBrowserProvider)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/profile/reset", s.srv.browser.handleBrowserProfileReset)
	s.ts = httptest.NewServer(mux)
}

func (s *BrowserProfileResetSuite) TearDownTest() {
	s.ts.Close()
}

func (s *BrowserProfileResetSuite) post(body string) *http.Response {
	resp, err := http.Post(s.ts.URL+"/api/browser/profile/reset", "application/json", strings.NewReader(body))
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (s *BrowserProfileResetSuite) TestInvalidJSON() {
	require.Equal(s.T(), http.StatusBadRequest, s.post("not json").StatusCode)
}

func (s *BrowserProfileResetSuite) TestMissingChannelID() {
	require.Equal(s.T(), http.StatusBadRequest, s.post(`{}`).StatusCode)
}

func (s *BrowserProfileResetSuite) TestDockerProviderNotConfigured() {
	require.Equal(s.T(), http.StatusServiceUnavailable, s.post(`{"channel_id":"ch-1"}`).StatusCode)
}

// Host mode drives the user's own Chrome against their own profile — wiping it
// is never ours to do, so the request is refused rather than silently ignored.
func (s *BrowserProfileResetSuite) TestHostModeConflict() {
	s.srv.browser.setProviders(s.browserMgr, new(mockHostBrowserProvider))
	s.srv.browser.modeMu.Lock()
	s.srv.browser.activeMode["ch-1"] = "host"
	s.srv.browser.modeMu.Unlock()

	require.Equal(s.T(), http.StatusConflict, s.post(`{"channel_id":"ch-1"}`).StatusCode)
	s.browserMgr.AssertNotCalled(s.T(), "RemoveProfile", mock.Anything, mock.Anything)
}

func (s *BrowserProfileResetSuite) TestResetTearsDownAndRemovesVolume() {
	reg := &mockContainerManager{}
	reg.On("RemoveContainer", mock.Anything, "chrome-c1").Return(nil)
	s.srv.SetContainerRegistry(reg)
	s.srv.browser.setProviders(s.browserMgr, s.srv.browser.hostProvider)

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("chrome-c1", nil)
	s.browserMgr.On("RemoveProfile", mock.Anything, "ch-1").Return(nil)

	s.srv.browser.capturesMu.Lock()
	s.srv.browser.captures["ch-1"] = &browser.CaptureState{}
	s.srv.browser.capturesMu.Unlock()

	require.Equal(s.T(), http.StatusNoContent, s.post(`{"channel_id":"ch-1"}`).StatusCode)

	s.browserMgr.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())

	s.srv.browser.capturesMu.Lock()
	_, ok := s.srv.browser.captures["ch-1"]
	s.srv.browser.capturesMu.Unlock()
	require.False(s.T(), ok, "capture state should be dropped with the profile")
}

// A container that will not go away is logged, not fatal: the volume removal is
// forced, and reporting a failure here would leave the user with no way out.
func (s *BrowserProfileResetSuite) TestContainerRemoveErrorIsLogged() {
	reg := &mockContainerManager{}
	reg.On("RemoveContainer", mock.Anything, "chrome-c1").Return(errors.New("still running"))
	s.srv.SetContainerRegistry(reg)
	s.srv.browser.setProviders(s.browserMgr, s.srv.browser.hostProvider)

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("chrome-c1", nil)
	s.browserMgr.On("RemoveProfile", mock.Anything, "ch-1").Return(nil)

	require.Equal(s.T(), http.StatusNoContent, s.post(`{"channel_id":"ch-1"}`).StatusCode)
	s.browserMgr.AssertExpectations(s.T())
}

// No sidecar running: nothing to remove, but the volume still must go.
func (s *BrowserProfileResetSuite) TestResetWithNoContainer() {
	s.srv.browser.setProviders(s.browserMgr, s.srv.browser.hostProvider)
	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("", nil)
	s.browserMgr.On("RemoveProfile", mock.Anything, "ch-1").Return(nil)

	require.Equal(s.T(), http.StatusNoContent, s.post(`{"channel_id":"ch-1"}`).StatusCode)
	s.browserMgr.AssertExpectations(s.T())
}

func (s *BrowserProfileResetSuite) TestRemoveProfileError() {
	s.srv.browser.setProviders(s.browserMgr, s.srv.browser.hostProvider)
	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("", nil)
	s.browserMgr.On("RemoveProfile", mock.Anything, "ch-1").Return(errors.New("volume in use"))

	resp := s.post(`{"channel_id":"ch-1"}`)
	require.Equal(s.T(), http.StatusInternalServerError, resp.StatusCode)
}

// The live CDP connection points at a container that is about to be destroyed,
// so it is dropped before teardown rather than left to the idle monitor.
func (s *BrowserProfileResetSuite) TestCloseCDPManagerDropsEntry() {
	s.srv.browser.cdpManagersMu.Lock()
	s.srv.browser.cdpManagers["ch-1|docker"] = browser.NewCDPManager("ws://127.0.0.1:1", browser.CDPManagerConfig{}, testLogger())
	s.srv.browser.cdpManagersMu.Unlock()

	s.srv.browser.closeCDPManager("ch-1", "docker")

	s.srv.browser.cdpManagersMu.Lock()
	_, ok := s.srv.browser.cdpManagers["ch-1|docker"]
	s.srv.browser.cdpManagersMu.Unlock()
	require.False(s.T(), ok)

	// Idempotent — a second call on an absent key is a no-op.
	s.srv.browser.closeCDPManager("ch-1", "docker")
}
