package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/filewatch"
)

type FileWatchHandlerSuite struct {
	suite.Suite
	store *MockChannelLister
	srv   *Server
	dir   string
	ts    *httptest.Server
}

func TestFileWatchHandlerSuite(t *testing.T) { suite.Run(t, new(FileWatchHandlerSuite)) }

func (s *FileWatchHandlerSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.store = new(MockChannelLister)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: s.dir}, nil).Maybe()
	s.srv = untrustedProjectLoads(NewServer(nil, nil, nil, s.store, nil, testLogger()))
	s.srv.configs.load = func() (*config.Config, error) { return &config.Config{}, nil }
	s.srv.fileWatchDebounce = 10 * time.Millisecond
	mux := http.NewServeMux()
	s.srv.registerFileRoutes(mux)
	s.ts = httptest.NewServer(mux)
}

func (s *FileWatchHandlerSuite) TearDownTest() { s.ts.Close() }

func (s *FileWatchHandlerSuite) dial() *websocket.Conn {
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.ts.URL, "http")+"/api/channels/ch-1/files/watch", nil)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = conn.Close() })
	return conn
}

// next reads the next change, nudging the disk with poke while it waits:
// the watch message is applied asynchronously, so an early change can be
// missed.
func (s *FileWatchHandlerSuite) next(conn *websocket.Conn, poke func()) fileWatchChange {
	got := make(chan fileWatchChange, 1)
	go func() {
		var c fileWatchChange
		if conn.ReadJSON(&c) == nil {
			got <- c
		}
	}()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case c := <-got:
			return c
		case <-tick.C:
			poke()
		case <-deadline:
			s.FailNow("no change reported")
		}
	}
}

func (s *FileWatchHandlerSuite) TestNotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	srv.registerFileRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/ch-1/files/watch", nil))
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *FileWatchHandlerSuite) TestNotAWebSocket() {
	res, err := http.Get(s.ts.URL + "/api/channels/ch-1/files/watch")
	require.NoError(s.T(), err)
	_ = res.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, res.StatusCode)
}

func (s *FileWatchHandlerSuite) TestWatcherFails() {
	s.srv.newFileWatcher = func() (filewatch.Watcher, error) { return nil, errors.New("too many open files") }
	var c fileWatchChange
	require.NoError(s.T(), s.dial().ReadJSON(&c))
	require.Equal(s.T(), fileWatchChange{Type: "error", Message: "too many open files"}, c)
}

func (s *FileWatchHandlerSuite) TestReportsFileAndDirChanges() {
	file := filepath.Join(s.dir, "a.txt")
	require.NoError(s.T(), os.WriteFile(file, []byte("1"), 0o644))
	require.NoError(s.T(), os.Mkdir(filepath.Join(s.dir, "sub"), 0o755))

	conn := s.dial()
	require.NoError(s.T(), conn.WriteMessage(websocket.TextMessage, []byte("not json")))
	require.NoError(s.T(), conn.WriteJSON(map[string]string{"type": "other"}))
	require.NoError(s.T(), conn.WriteJSON(fileWatchRequest{
		Type: fileWatchMsgWatch,
		Files: []fileWatchRef{
			{Root: 0, Path: "a.txt"},
			{Root: 0, Path: "../outside.txt"}, // escapes the root: left out
			{Root: 7, Path: "b.txt"},          // no such root
			{Root: 7, Path: "c.txt"},
		},
		Dirs: []fileWatchRef{{Root: 0, Path: ""}},
	}))

	n := 0
	c := s.next(conn, func() {
		n++
		_ = os.WriteFile(file, []byte(strings.Repeat("x", n)), 0o644)
	})
	require.Equal(s.T(), fileWatchChange{Type: filewatch.KindFile, Root: 0, Path: "a.txt"}, c)

	// Only the watched dirs report new entries: sub isn't watched.
	c = s.next(conn, func() {
		n++
		_ = os.WriteFile(filepath.Join(s.dir, "sub", "n.txt"), nil, 0o644)
		_ = os.WriteFile(filepath.Join(s.dir, "new"+strings.Repeat("x", n)+".txt"), nil, 0o644)
	})
	for c.Type == filewatch.KindFile {
		c = s.next(conn, func() {})
	}
	require.Equal(s.T(), fileWatchChange{Type: filewatch.KindDir, Root: 0, Path: ""}, c)
}

func (s *FileWatchHandlerSuite) TestParseFileWatchKey() {
	for _, ref := range []fileWatchRef{{0, ""}, {2, "a/b.go"}} {
		require.Equal(s.T(), ref, parseFileWatchKey(fileWatchKey(ref)))
	}
}
