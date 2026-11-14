package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/container"
)

func (s *BrowserHandlerSuite) TestWithBrowserDownloads() {
	srv := NewServer(nil, nil, nil, nil, nil, testLogger(), WithBrowserDownloads("/home/u/.loop"))
	require.Equal(s.T(), "/home/u/.loop", srv.browser.loopDir)
}

func (s *BrowserHandlerSuite) TestGetOrCreateCDPManagerDownloadDir() {
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	hostProvider := new(mockHostBrowserProvider)
	hostProvider.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9223")

	// Off until the daemon turns downloads on.
	require.Empty(s.T(), s.srv.browser.getOrCreateCDPManager("ch-1", "docker", s.browserMgr).DownloadDir())
	s.srv.browser.closeCDPManager("ch-1", "docker")

	s.srv.browser.setLoopDir(s.T().TempDir())
	require.Equal(s.T(), browser.ChromeDownloadsDir, s.srv.browser.getOrCreateCDPManager("ch-1", "docker", s.browserMgr).DownloadDir())
	// The user's own Chrome keeps its own download settings.
	require.Empty(s.T(), s.srv.browser.getOrCreateCDPManager("ch-1", "host", hostProvider).DownloadDir())
}

func (s *BrowserHandlerSuite) TestListDownloads() {
	loopDir := s.T().TempDir()
	dir := container.DownloadsDir(loopDir, "ch-1")

	tests := []struct {
		name    string
		loopDir string
		mode    string
		files   map[string]string
		subdir  bool
		result  string
		err     string
	}{
		{name: "host mode", loopDir: loopDir, mode: "host", err: "in host mode downloads go wherever your own Chrome saves them"},
		{name: "off", err: "downloads are off for this browser"},
		{name: "no dir yet", loopDir: loopDir, result: "No downloads yet. The browser saves them to " + dir},
		{
			// Newest first.
			name:    "files",
			loopDir: loopDir,
			files:   map[string]string{"a.pdf": "pdf!", "b.zip.crdownload": "zi"},
			subdir:  true,
			result: "Downloads in " + dir + ":\n" +
				"- " + filepath.Join(dir, "b.zip") + " (still downloading, 2 bytes so far)\n" +
				"- " + filepath.Join(dir, "a.pdf") + " (4 bytes)\n",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.srv.browser.setLoopDir(tt.loopDir)
			if tt.mode != "" {
				s.srv.browser.activeMode["ch-1"] = tt.mode
			}
			if tt.files != nil || tt.subdir {
				require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, "nested"), 0o755))
			}
			for name, body := range tt.files {
				require.NoError(s.T(), os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
			}
			if tt.files != nil {
				old := time.Now().Add(-time.Hour)
				require.NoError(s.T(), os.Chtimes(filepath.Join(dir, "a.pdf"), old, old))
			}

			// Listing needs no browser, so none is started.
			w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "list_downloads"})
			var resp browserActionResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(s.T(), tt.result, resp.Result)
			require.Contains(s.T(), resp.Error, tt.err)
			s.browserMgr.AssertNotCalled(s.T(), "EnsureBrowser")
		})
	}
}

func (s *BrowserHandlerSuite) TestListDownloadsReadError() {
	loopDir := s.T().TempDir()
	// A file where the dir should be fails the read with something other than
	// "not exist".
	require.NoError(s.T(), os.MkdirAll(filepath.Join(loopDir, "downloads"), 0o755))
	require.NoError(s.T(), os.WriteFile(container.DownloadsDir(loopDir, "ch-1"), nil, 0o644))
	s.srv.browser.setLoopDir(loopDir)

	resp := s.srv.browser.listDownloads("ch-1")
	require.Contains(s.T(), resp.Error, "reading downloads:")
}

func (s *BrowserHandlerSuite) TestListDownloadsRefused() {
	tests := []struct {
		name      string
		channelID string
		setup     func(loopDir string)
		err       string
	}{
		{name: "no name left", channelID: "!!!", err: `channel "!!!" has no downloads dir`},
		{
			// The dir is a link out of the downloads dir.
			name:      "link out",
			channelID: "ch-1",
			setup: func(loopDir string) {
				require.NoError(s.T(), os.MkdirAll(filepath.Join(loopDir, "downloads"), 0o755))
				require.NoError(s.T(), os.Symlink(s.T().TempDir(), container.DownloadsDir(loopDir, "ch-1")))
			},
			err: "reading downloads:",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			loopDir := s.T().TempDir()
			if tt.setup != nil {
				tt.setup(loopDir)
			}
			s.srv.browser.setLoopDir(loopDir)
			resp := s.srv.browser.listDownloads(tt.channelID)
			require.Empty(s.T(), resp.Result)
			require.Contains(s.T(), resp.Error, tt.err)
		})
	}
}

func (s *BrowserHandlerSuite) TestWSListDownloads() {
	loopDir := s.T().TempDir()
	dir := container.DownloadsDir(loopDir, "ch-1")
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))
	old, recent := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Truncate(time.Second)
	for name, at := range map[string]time.Time{"a.pdf": old, "b.zip.crdownload": recent} {
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644))
		require.NoError(s.T(), os.Chtimes(filepath.Join(dir, name), at, at))
	}

	tests := []struct {
		name      string
		loopDir   string
		channelID string
		want      browserWSResponse
	}{
		{name: "no channel", loopDir: loopDir, want: browserWSResponse{Type: bwsRespError, Message: "channel_id required"}},
		{name: "off", channelID: "ch-1", want: browserWSResponse{Type: bwsRespError, Message: "downloads are off for this browser"}},
		{name: "empty", loopDir: loopDir, channelID: "ch-2", want: browserWSResponse{Type: bwsRespDownloads, DownloadsDir: container.DownloadsDir(loopDir, "ch-2")}},
		{
			name: "files", loopDir: loopDir, channelID: "ch-1",
			want: browserWSResponse{Type: bwsRespDownloads, DownloadsDir: dir, Files: []browserDownloadFile{
				{Name: "b.zip", Path: filepath.Join(dir, "b.zip"), Size: 4, ModTime: recent, Partial: true},
				{Name: "a.pdf", Path: filepath.Join(dir, "a.pdf"), Size: 4, ModTime: old},
			}},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.srv.browser.setLoopDir(tt.loopDir)
			ws, ts := s.dialBrowserWS()
			defer ts.Close()
			defer ws.Close()

			require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgDownloads, ChannelID: tt.channelID}))
			resp := s.readResp(ws)
			for i := range resp.Files {
				resp.Files[i].ModTime = resp.Files[i].ModTime.Local()
			}
			require.Equal(s.T(), tt.want, resp)
			// Listing needs no browser, so none is started.
			s.browserMgr.AssertNotCalled(s.T(), "EnsureBrowser")
		})
	}
}
