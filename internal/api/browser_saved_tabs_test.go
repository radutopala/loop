package api

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

// savedTabsFile is where ch's saved tabs live under loopDir.
func savedTabsFile(loopDir, ch string) string {
	return filepath.Join(loopDir, "browser-tabs", ch+".json")
}

func (s *BrowserHandlerSuite) TestSaveTabs() {
	tabs := []browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A"},
		{TargetID: "t2", URL: "https://b.com/x?y=1"},
		{TargetID: "t3", URL: "about:blank"},
		{TargetID: "t4", URL: ""},
		{TargetID: "stray", URL: "https://not-ours.com"},
	}
	tests := []struct {
		name     string
		noDir    bool
		dead     bool
		tabs     []browser.TabInfo
		listErr  error
		prepare  func(loopDir string)
		wantFile string // "" when no file should be left
	}{
		{name: "saved, tracked tabs only, blank ones left out", tabs: tabs,
			wantFile: `[{"url":"https://a.com","title":"A","active":true},{"url":"https://b.com/x?y=1"}]`},
		{name: "no loop dir", noDir: true, tabs: tabs},
		{name: "dead browser", dead: true, tabs: tabs},
		{name: "listing fails", listErr: errTestAPI},
		{name: "only blank tabs remove an older list", tabs: tabs[2:4], prepare: func(loopDir string) {
			require.NoError(s.T(), os.MkdirAll(filepath.Join(loopDir, "browser-tabs"), 0o700))
			require.NoError(s.T(), os.WriteFile(savedTabsFile(loopDir, "ch-1"), []byte(`[{"url":"https://old.com"}]`), 0o600))
		}},
		{name: "dir can't be made", tabs: tabs, prepare: func(loopDir string) {
			require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "browser-tabs"), nil, 0o600))
		}},
		{name: "file can't be written", tabs: tabs, prepare: func(loopDir string) {
			require.NoError(s.T(), os.MkdirAll(savedTabsFile(loopDir, "ch-1"), 0o700))
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			loopDir := s.T().TempDir()
			if !tc.noDir {
				s.srv.browser.loopDir = loopDir
			}
			if tc.prepare != nil {
				tc.prepare(loopDir)
			}
			client := new(mockCDPSession)
			client.On("TargetID").Return("t1").Maybe()
			client.On("ListTabs", mock.Anything).Return(tc.tabs, tc.listErr).Maybe()
			mgr := s.reuseTestManager("ch-1", client)
			for _, id := range []string{"t2", "t3", "t4"} {
				mgr.TrackTab(id)
			}
			client.dead = tc.dead
			// A host browser is the user's own; its tabs are not ours to keep.
			s.srv.browser.cdpManagers["ch-1|host"] = mgr

			s.browserMgr.On("Cleanup", mock.Anything).Return()
			s.srv.browser.cleanup(context.Background())

			data, err := os.ReadFile(savedTabsFile(loopDir, "ch-1"))
			if tc.wantFile == "" {
				if err == nil {
					s.Failf("unexpected file", "saved tabs left behind: %s", data)
				}
				return
			}
			require.NoError(s.T(), err)
			require.JSONEq(s.T(), tc.wantFile, string(data))
			entries, err := os.ReadDir(filepath.Join(loopDir, "browser-tabs"))
			require.NoError(s.T(), err)
			require.Len(s.T(), entries, 1, "only the docker browser's tabs are saved")
		})
	}
}

func (s *BrowserHandlerSuite) TestTakeSavedTabs() {
	tests := []struct {
		name    string
		prepare func(path string)
		want    []savedTab
	}{
		{name: "nothing saved"},
		{name: "saved", prepare: func(path string) {
			require.NoError(s.T(), os.WriteFile(path, []byte(`[{"url":"https://a.com","active":true}]`), 0o600))
		}, want: []savedTab{{URL: "https://a.com", Active: true}}},
		{name: "unreadable", prepare: func(path string) {
			require.NoError(s.T(), os.Mkdir(path, 0o700))
		}},
		{name: "not JSON", prepare: func(path string) {
			require.NoError(s.T(), os.WriteFile(path, []byte(`{`), 0o600))
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.srv.browser.loopDir = s.T().TempDir()
			path := savedTabsFile(s.srv.browser.loopDir, "ch-1")
			require.NoError(s.T(), os.MkdirAll(filepath.Dir(path), 0o700))
			if tc.prepare != nil {
				tc.prepare(path)
			}
			require.Equal(s.T(), tc.want, s.srv.browser.takeSavedTabs("ch-1"))
			if tc.want != nil {
				require.Nil(s.T(), s.srv.browser.takeSavedTabs("ch-1"), "saved tabs are opened once")
			}
		})
	}
}

func (s *BrowserHandlerSuite) TestTakeSavedTabsNoLoopDir() {
	require.Nil(s.T(), s.srv.browser.takeSavedTabs("ch-1"))
}

func (s *BrowserHandlerSuite) TestSavedTabsPathNeedsAFileName() {
	s.srv.browser.loopDir = s.T().TempDir()
	require.Empty(s.T(), s.srv.browser.savedTabsPath("!!!"), "a channel id with no letters or digits leaves no file name")
	require.Equal(s.T(), savedTabsFile(s.srv.browser.loopDir, "ch-1"), s.srv.browser.savedTabsPath("ch-1"))
}

// restoreSetup gives ch-1 a connected manager on t1 and saved, as the last
// daemon would have left them.
func (s *BrowserHandlerSuite) restoreSetup(saved string) (*browser.CDPManager, *mockCDPSession) {
	s.srv.browser.loopDir = s.T().TempDir()
	s.srv.browser.carriesOwnerToken = func(u string) bool { return strings.Contains(u, "token=") }
	if saved != "" {
		path := savedTabsFile(s.srv.browser.loopDir, "ch-1")
		require.NoError(s.T(), os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(s.T(), os.WriteFile(path, []byte(saved), 0o600))
	}
	client := new(mockCDPSession)
	client.On("TargetID").Return("t1").Maybe()
	return s.reuseTestManager("ch-1", client), client
}

func (s *BrowserHandlerSuite) TestRestoreTabsNothingSaved() {
	mgr, client := s.restoreSetup("")
	require.Same(s.T(), client, s.srv.browser.restoreTabs(context.Background(), "ch-1", mgr, client))
	client.AssertNotCalled(s.T(), "ListTabs", mock.Anything)
}

func (s *BrowserHandlerSuite) TestRestoreTabsLeavesTheBrowserAlone() {
	tests := []struct {
		name    string
		tabs    []browser.TabInfo
		listErr error
	}{
		{name: "listing fails", listErr: errTestAPI},
		{name: "the browser kept its tabs", tabs: []browser.TabInfo{{TargetID: "t1", URL: "about:blank"}, {TargetID: "t9", URL: "https://kept.com"}}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			mgr, client := s.restoreSetup(`[{"url":"https://a.com"}]`)
			client.On("ListTabs", mock.Anything).Return(tc.tabs, tc.listErr)

			require.Same(s.T(), client, s.srv.browser.restoreTabs(context.Background(), "ch-1", mgr, client))
			client.AssertNotCalled(s.T(), "Navigate", mock.Anything, mock.Anything)
			require.Nil(s.T(), s.srv.browser.takeSavedTabs("ch-1"), "the list is used up either way")
		})
	}
}

// The saved tabs open again, the first in the tab the browser came up with,
// and the one that was active comes back to the front with a client on it.
func (s *BrowserHandlerSuite) TestRestoreTabs() {
	mgr, client := s.restoreSetup(`[
		{"url":"https://a.com"},
		{"url":"https://loop.local/?token=secret"},
		{"url":"https://b.com","title":"B","active":true},
		{"url":"https://c.com"}
	]`)
	onT2 := new(mockCDPSession)
	onT2.On("TargetID").Return("t2").Maybe()
	client.attachFn = func(targetID string) (browser.CDPSession, error) {
		require.Equal(s.T(), "t2", targetID)
		return onT2, nil
	}
	client.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "t1", URL: "about:blank"}}, nil).Once()
	client.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "t1", URL: "https://a.com"}, {TargetID: "t2", URL: "https://b.com"}}, nil)
	client.On("Navigate", mock.MatchedBy(func(ctx context.Context) bool {
		_, ok := ctx.Deadline()
		return ok
	}), "https://a.com").Return(context.DeadlineExceeded) // still loading: fine
	client.On("NewTab", mock.Anything, "https://b.com").Return("t2", nil)
	client.On("NewTab", mock.Anything, "https://c.com").Return("", errTestAPI)
	client.On("SwitchTab", mock.Anything, "t2").Return(nil)

	got := s.srv.browser.restoreTabs(context.Background(), "ch-1", mgr, client)

	require.Same(s.T(), onT2, got)
	require.Equal(s.T(), "t2", mgr.ActiveTargetID())
	require.True(s.T(), mgr.IsTrackedTab("t2"))
	client.AssertNotCalled(s.T(), "NewTab", mock.Anything, "https://loop.local/?token=secret")
	client.AssertExpectations(s.T())
}

// With none marked active the first tab is, and a failed load or a refused
// switch still leaves a working client.
func (s *BrowserHandlerSuite) TestRestoreTabsFirstTabFailures() {
	mgr, client := s.restoreSetup(`[{"url":"https://a.com"}]`)
	client.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "t1", URL: "about:blank"}}, nil)
	client.On("Navigate", mock.Anything, "https://a.com").Return(errTestAPI)
	client.On("SwitchTab", mock.Anything, "t1").Return(errTestAPI)

	require.Same(s.T(), client, s.srv.browser.restoreTabs(context.Background(), "ch-1", mgr, client))
	require.Equal(s.T(), "t1", mgr.ActiveTargetID())
	client.AssertExpectations(s.T())
}

func (s *BrowserHandlerSuite) TestRestoreTabsAllSkipped() {
	mgr, client := s.restoreSetup(`[{"url":"https://loop.local/?token=secret"}]`)
	client.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "t1", URL: "about:blank"}}, nil)

	require.Same(s.T(), client, s.srv.browser.restoreTabs(context.Background(), "ch-1", mgr, client))
	client.AssertNotCalled(s.T(), "SwitchTab", mock.Anything, mock.Anything)
}

// Attaching to the restored tab fails: the client the browser came up with
// still works, so it is what comes back.
func (s *BrowserHandlerSuite) TestRestoreTabsSyncFails() {
	mgr, client := s.restoreSetup(`[{"url":"https://a.com"},{"url":"https://b.com","active":true}]`)
	client.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "t1", URL: "about:blank"}}, nil).Once()
	// Every tab is gone by the time it attaches, and no new one opens.
	client.On("ListTabs", mock.Anything).Return([]browser.TabInfo{}, nil)
	client.On("Navigate", mock.Anything, "https://a.com").Return(nil)
	client.On("NewTab", mock.Anything, "https://b.com").Return("t2", nil)
	client.On("NewTab", mock.Anything, "about:blank").Return("", errors.New("browser gone"))
	client.On("SwitchTab", mock.Anything, "t2").Return(nil)

	require.Same(s.T(), client, s.srv.browser.restoreTabs(context.Background(), "ch-1", mgr, client))
	client.AssertExpectations(s.T())
}

// Without a manager there is nothing to narrow by, so every tab shows.
func (s *BrowserHandlerSuite) TestAgentTabsNoManager() {
	tabs := []browser.TabInfo{{TargetID: "t1"}, {TargetID: "t2"}}
	require.Equal(s.T(), tabs, agentTabs(nil, tabs))
}

// The pane is often what connects first after a restart, so it reopens the
// saved tabs as the agent's tools would.
func (s *BrowserHandlerSuite) TestHandleStartRestoresTabs() {
	s.srv.browser.loopDir = s.T().TempDir()
	path := savedTabsFile(s.srv.browser.loopDir, "ch-1")
	require.NoError(s.T(), os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(s.T(), os.WriteFile(path, []byte(`[{"url":"https://a.com","active":true}]`), 0o600))

	client := new(mockCDPSession)
	client.On("TargetID").Return("test-target").Maybe()
	client.On("SwitchTarget", mock.Anything).Return(nil).Maybe()
	client.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "test-target", URL: "about:blank"}}, nil)
	client.On("Navigate", mock.Anything, "https://a.com").Return(nil)
	client.On("SwitchTab", mock.Anything, "test-target").Return(nil)
	client.On("Close").Return().Maybe()
	client.On("StopScreencast").Return().Maybe()
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")

	mgr := newTestCDPManager()
	browser.SetCDPFactoryForTest(mgr, func(context.Context, string, *slog.Logger, ...browser.CDPOption) (browser.CDPSession, error) {
		return client, nil
	})
	s.srv.browser.cdpManagersMu.Lock()
	s.srv.browser.cdpManagers = map[string]*browser.CDPManager{"ch-1|docker": mgr}
	s.srv.browser.cdpManagersMu.Unlock()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	require.Equal(s.T(), bwsRespStarted, s.readResp(ws).Type)

	client.AssertCalled(s.T(), "Navigate", mock.Anything, "https://a.com")
	_, err := os.Stat(path)
	require.ErrorIs(s.T(), err, os.ErrNotExist)
}
