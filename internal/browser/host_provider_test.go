package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HostProviderSuite struct {
	suite.Suite
	provider *HostProvider
	server   *httptest.Server
}

func TestHostProviderSuite(t *testing.T) {
	suite.Run(t, new(HostProviderSuite))
}

func (s *HostProviderSuite) SetupTest() {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"Browser":"Chrome/120"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	port := s.server.Listener.Addr().(*net.TCPAddr).Port
	s.provider = NewHostProvider(port, nil)
	s.provider.timeNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	// Disable DevToolsActivePort discovery in tests by default.
	s.provider.readFile = func(_ string) ([]byte, error) { return nil, os.ErrNotExist }
}

func (s *HostProviderSuite) TearDownTest() {
	s.server.Close()
}

func (s *HostProviderSuite) TestEnsureBrowserSuccess() {
	err := s.provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.NoError(s.T(), err)

	// Second call should be a no-op.
	err = s.provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.NoError(s.T(), err)
}

func (s *HostProviderSuite) TestEnsureBrowserUnreachable() {
	provider := NewHostProvider(19999, nil)
	provider.readFile = func(_ string) ([]byte, error) { return nil, os.ErrNotExist }
	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "host Chrome not reachable")
	require.Contains(s.T(), err.Error(), "chrome://inspect/#remote-debugging")
}

func (s *HostProviderSuite) TestEnsureBrowserViaDevToolsActivePort() {
	port := s.server.Listener.Addr().(*net.TCPAddr).Port
	provider := NewHostProvider(0, nil)
	provider.timeNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte(fmt.Sprintf("%d\n/devtools/browser/abc-123\n", port)), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), port, provider.port)
}

func (s *HostProviderSuite) TestEnsureBrowserDevToolsActivePortInvalid() {
	provider := NewHostProvider(19999, nil)
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte("notaport\n/devtools/browser/abc\n"), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
}

func (s *HostProviderSuite) TestEnsureBrowserDevToolsActivePortTooFewLines() {
	provider := NewHostProvider(19999, nil)
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte("9222\n"), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
}

func (s *HostProviderSuite) TestEnsureBrowserDevToolsActivePortEmptyWSPath() {
	provider := NewHostProvider(19999, nil)
	provider.readFile = func(_ string) ([]byte, error) {
		// Three lines: port, empty ws path, extra line.
		// TrimSpace preserves internal newlines, Split produces ["9222", "", "extra"].
		return []byte("9222\n\nextra"), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
}

func (s *HostProviderSuite) TestEnsureBrowserDevToolsActivePortNoHomeDir() {
	provider := NewHostProvider(19999, nil)
	provider.readFile = func(_ string) ([]byte, error) { return nil, os.ErrNotExist }
	provider.userHomeDir = func() (string, error) { return "", fmt.Errorf("no home") }

	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
}

func (s *HostProviderSuite) TestGetCDPEndpointWithDevToolsActivePort() {
	port := s.server.Listener.Addr().(*net.TCPAddr).Port
	s.provider.readFile = func(_ string) ([]byte, error) {
		return []byte(fmt.Sprintf("%d\n/devtools/browser/abc-123\n", port)), nil
	}
	s.provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	endpoint := s.provider.GetCDPEndpoint("ch-1")
	require.Equal(s.T(), fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser/abc-123", port), endpoint)
}

func (s *HostProviderSuite) TestGetCDPEndpointFallback() {
	port := s.server.Listener.Addr().(*net.TCPAddr).Port
	endpoint := s.provider.GetCDPEndpoint("ch-1")
	require.Equal(s.T(), fmt.Sprintf("ws://127.0.0.1:%d", port), endpoint)
}

func (s *HostProviderSuite) TestGetContainerID() {
	id, ok := s.provider.GetContainerID("ch-1")
	require.Empty(s.T(), id)
	require.False(s.T(), ok)
}

func (s *HostProviderSuite) TestIsHostMode() {
	require.True(s.T(), s.provider.IsHostMode())
}

func (s *HostProviderSuite) TestIsRunning() {
	require.True(s.T(), s.provider.IsRunning(context.Background(), "ch-1"))
}

func (s *HostProviderSuite) TestIsRunningUnreachable() {
	provider := NewHostProvider(19999, nil)
	provider.readFile = func(_ string) ([]byte, error) { return nil, os.ErrNotExist }
	require.False(s.T(), provider.IsRunning(context.Background(), "ch-1"))
}

func (s *HostProviderSuite) TestIsRunningViaDevToolsActivePort() {
	port := s.server.Listener.Addr().(*net.TCPAddr).Port
	provider := NewHostProvider(0, nil)
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte(fmt.Sprintf("%d\n/devtools/browser/abc\n", port)), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }
	require.True(s.T(), provider.IsRunning(context.Background(), "ch-1"))
}

func (s *HostProviderSuite) TestStopBrowser() {
	require.NoError(s.T(), s.provider.EnsureBrowser(context.Background(), "ch-1", ""))
	containerID, err := s.provider.StopBrowser(context.Background(), "ch-1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), containerID)

	s.provider.mu.Lock()
	_, ok := s.provider.sessions["ch-1"]
	s.provider.mu.Unlock()
	require.False(s.T(), ok)
}

func (s *HostProviderSuite) TestStopBrowserNotExist() {
	containerID, err := s.provider.StopBrowser(context.Background(), "nonexistent")
	require.NoError(s.T(), err)
	require.Empty(s.T(), containerID)
}

func (s *HostProviderSuite) TestIsChromeReachableUnreachablePort() {
	require.False(s.T(), isChromeReachable("127.0.0.1:19999"))
}

func (s *HostProviderSuite) TestIsChromeReachableEmptyHostPort() {
	require.False(s.T(), isChromeReachable(""))
}

func (s *HostProviderSuite) TestChromeUserDataDirDarwin() {
	dir := chromeUserDataDirForOS(func() (string, error) { return "/Users/test", nil }, "darwin")
	require.Contains(s.T(), dir, "Google/Chrome")
}

func (s *HostProviderSuite) TestChromeUserDataDirNoHome() {
	dir := chromeUserDataDir(func() (string, error) { return "", fmt.Errorf("no home") })
	require.Empty(s.T(), dir)
}

func (s *HostProviderSuite) TestChromeUserDataDirLinux() {
	dir := chromeUserDataDirForOS(func() (string, error) { return "/home/user", nil }, "linux")
	require.Contains(s.T(), dir, ".config/google-chrome")
}

func (s *HostProviderSuite) TestChromeUserDataDirWindows() {
	dir := chromeUserDataDirForOS(func() (string, error) { return `C:\Users\test`, nil }, "windows")
	require.Contains(s.T(), dir, "User Data")
}

func (s *HostProviderSuite) TestChromeUserDataDirUnknownOS() {
	dir := chromeUserDataDirForOS(func() (string, error) { return "/home/user", nil }, "freebsd")
	require.Empty(s.T(), dir)
}

func (s *HostProviderSuite) TestChromeUserDataDirForOSNoHome() {
	dir := chromeUserDataDirForOS(func() (string, error) { return "", fmt.Errorf("no home") }, "linux")
	require.Empty(s.T(), dir)
}

func (s *HostProviderSuite) TestEnsureBrowserDevToolsActivePortUnreachable() {
	// DevToolsActivePort file exists and is valid, but port is not reachable.
	provider := NewHostProvider(19999, slog.Default())
	provider.timeNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte("19998\n/devtools/browser/abc-123\n"), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "host Chrome not reachable")
}

func (s *HostProviderSuite) TestEnsureBrowserDevToolsActivePortUnreachableNilLogger() {
	// Same but with nil logger — covers the nil logger guard.
	provider := NewHostProvider(19999, nil)
	provider.timeNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte("19998\n/devtools/browser/abc-123\n"), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
}

func (s *HostProviderSuite) TestDevToolsActivePortInvalidPortTooHigh() {
	provider := NewHostProvider(19999, nil)
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte("99999\n/devtools/browser/abc\n"), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	_, _, err := provider.devToolsActivePort()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid port")
}

func (s *HostProviderSuite) TestDevToolsActivePortZeroPort() {
	provider := NewHostProvider(19999, nil)
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte("0\n/devtools/browser/abc\n"), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	_, _, err := provider.devToolsActivePort()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid port")
}

func (s *HostProviderSuite) TestEnsureBrowserWithLogger() {
	port := s.server.Listener.Addr().(*net.TCPAddr).Port
	provider := NewHostProvider(0, slog.Default())
	provider.timeNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	provider.readFile = func(_ string) ([]byte, error) {
		return []byte(fmt.Sprintf("%d\n/devtools/browser/abc-123\n", port)), nil
	}
	provider.userHomeDir = func() (string, error) { return "/tmp", nil }

	err := provider.EnsureBrowser(context.Background(), "ch-1", "")
	require.NoError(s.T(), err)
}

func (s *HostProviderSuite) TestDiscoverWSEndpointUsesOsDefaults() {
	// DiscoverWSEndpoint calls parseDevToolsActivePort with os.ReadFile/os.UserHomeDir.
	// On CI/test environments the file won't exist, so we expect an error.
	_, err := DiscoverWSEndpoint()
	// Either it works (Chrome is running) or fails with a descriptive error.
	if err != nil {
		require.Contains(s.T(), err.Error(), "DevToolsActivePort")
	}
}

func (s *HostProviderSuite) TestDiscoverWSEndpointError() {
	// Temporarily rename DevToolsActivePort so DiscoverWSEndpoint hits the error branch.
	home, err := os.UserHomeDir()
	require.NoError(s.T(), err)

	dataDir := chromeUserDataDirForOS(func() (string, error) { return home, nil }, runtime.GOOS)
	if dataDir == "" {
		s.T().Skip("unsupported OS for this test")
	}

	portFile := filepath.Join(dataDir, "DevToolsActivePort")
	backupFile := portFile + ".test-backup"

	// Rename existing file if present; restore after the test.
	renamed := false
	if _, statErr := os.Stat(portFile); statErr == nil {
		require.NoError(s.T(), os.Rename(portFile, backupFile))
		renamed = true
		defer func() { _ = os.Rename(backupFile, portFile) }()
	}
	_ = renamed

	_, discoverErr := DiscoverWSEndpoint()
	require.Error(s.T(), discoverErr)
}

func (s *HostProviderSuite) TestDiscoverWSEndpointSuccess() {
	// Use a temp dir as fake home so we don't need write access to the real
	// Chrome data directory (which may not be writable in CI/sandbox).
	fakeHome := s.T().TempDir()
	dataDir := chromeUserDataDirForOS(func() (string, error) { return fakeHome, nil }, runtime.GOOS)
	require.NotEmpty(s.T(), dataDir, "unsupported OS for this test")

	ln, listenErr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), listenErr)
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	portFile := filepath.Join(dataDir, "DevToolsActivePort")
	require.NoError(s.T(), os.MkdirAll(dataDir, 0o755))
	require.NoError(s.T(), os.WriteFile(
		portFile,
		[]byte(fmt.Sprintf("%d\n/devtools/browser/test-guid\n", port)),
		0o644,
	))

	fakeHomeDir := func() (string, error) { return fakeHome, nil }

	wsEndpoint, _, parseErr := parseDevToolsActivePort(os.ReadFile, fakeHomeDir)
	require.NoError(s.T(), parseErr)
	require.Equal(s.T(), fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser/test-guid", port), wsEndpoint)

	// Also exercise discoverWSEndpoint to cover the public wrapper's success path.
	ws2, discoverErr := discoverWSEndpoint(os.ReadFile, fakeHomeDir)
	require.NoError(s.T(), discoverErr)
	require.Equal(s.T(), wsEndpoint, ws2)
}
