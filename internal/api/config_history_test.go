package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/testutil"
)

// homeSystem is the real filesystem with the home dir set, for the
// global config routes that find it from the home dir.
type homeSystem struct {
	osutil.RealSystem
	home string
}

func (h homeSystem) UserHomeDir() (string, error) { return h.home, nil }

// useConfigHistory records the config history in a real SQLite, on the
// real filesystem with the loop dir in a temp dir, and returns the global
// config's path.
func (s *ServerSuite) useConfigHistory() string {
	s.srv.sys = osutil.RealSystem{}
	s.srv.loopDir = filepath.Join(s.T().TempDir(), ".loop")
	store, err := db.NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = store.Close() })
	s.srv.history.store = store
	require.NoError(s.T(), os.MkdirAll(s.srv.loopDir, 0o755))
	return filepath.Join(s.srv.loopDir, "config.json")
}

func (s *ServerSuite) configHistory(url string) configHistoryResponse {
	rec := s.testRequest("GET", url, "")
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
	var resp configHistoryResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func (s *ServerSuite) configRevisionAt(id int64) configRevisionResponse {
	rec := s.testRequest("GET", "/api/config/history/"+strconv.FormatInt(id, 10), "")
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
	var resp configRevisionResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func historySources(h configHistoryResponse) []string {
	var out []string
	for _, r := range h.Revisions {
		out = append(out, r.Source)
	}
	return out
}

// TestGlobalConfigHistory saves, edits outside Loop and restores the global
// config, and reads its history back.
func (s *ServerSuite) TestGlobalConfigHistory() {
	path := s.useConfigHistory()
	require.Empty(s.T(), s.configHistory("/api/config/history").Revisions)

	s.srv.sys = homeSystem{home: filepath.Dir(s.srv.loopDir)}
	rec := s.testRequest("PUT", "/api/config", `{"content":"{\"a\": 1}"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	saved, err := os.ReadFile(path)
	require.NoError(s.T(), err)

	// An edit made outside Loop shows up on the next scan, and only once.
	require.NoError(s.T(), os.WriteFile(path, append(saved, []byte("// note\n")...), 0o644))
	s.store.On("ListChannels", mock.Anything).Return(nil, nil)
	s.srv.scanConfigHistory(context.Background())
	s.srv.scanConfigHistory(context.Background())

	h := s.configHistory("/api/config/history")
	require.Equal(s.T(), path, h.Path)
	require.Equal(s.T(), []string{db.ConfigSourceExternal, "settings"}, historySources(h))
	require.Equal(s.T(), [2]int{1, 0}, [2]int{h.Revisions[0].Added, h.Revisions[0].Removed})
	require.Equal(s.T(), lineCount(saved), h.Revisions[1].Added, "the first revision is all added")

	first := s.configRevisionAt(h.Revisions[1].ID)
	require.Equal(s.T(), string(saved), first.Content)
	require.Contains(s.T(), first.Diff, "--- /dev/null")
	latest := s.configRevisionAt(h.Revisions[0].ID)
	require.Contains(s.T(), latest.Diff, "+// note")

	// Compared against a newer revision, the diff goes from that one.
	rec = s.testRequest("GET", "/api/config/history/"+strconv.FormatInt(first.ID, 10)+"?against="+strconv.FormatInt(latest.ID, 10), "")
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
	var compared configRevisionResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &compared))
	require.Equal(s.T(), first.ID, compared.ID)
	require.Contains(s.T(), compared.Diff, "--- "+path)
	require.Contains(s.T(), compared.Diff, "-// note")

	rec = s.testRequest("POST", "/api/config/history/"+strconv.FormatInt(first.ID, 10)+"/restore", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	restored, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), saved, restored)
	h = s.configHistory("/api/config/history")
	require.Equal(s.T(), "restore:"+strconv.FormatInt(first.ID, 10), h.Revisions[0].Source)
}

// lineCount counts content's lines.
func lineCount(content []byte) int {
	n := 0
	for _, c := range content {
		if c == '\n' {
			n++
		}
	}
	return n
}

// TestProjectConfigHistory checks a project config's first content is
// recorded as initial, Loop's edits by their source, and that restoring a
// trusted project config keeps it trusted.
func (s *ServerSuite) TestProjectConfigHistory() {
	dir := s.trustProject(`{"mounts": ["/a:/a"]}`)
	s.useConfigHistory()
	st := s.projectTrustStatus()
	rec := s.testRequest("POST", "/api/config/project/trust?channel_id=ch-1", `{"hash":"`+st.Hash+`"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())

	rec = s.testRequest("PUT", "/api/config/project?channel_id=ch-1", `{"content":"{\"mounts\": [\"/b:/b\"]}"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	require.True(s.T(), s.projectTrustStatus().Trusted)

	h := s.configHistory("/api/config/project/history?channel_id=ch-1")
	require.Equal(s.T(), filepath.Join(dir, ".loop", "config.json"), h.Path)
	require.Equal(s.T(), []string{"settings", db.ConfigSourceInitial}, historySources(h))

	initial := h.Revisions[1].ID
	rec = s.testRequest("POST", "/api/config/history/"+strconv.FormatInt(initial, 10)+"/restore", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	data, err := os.ReadFile(h.Path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), `{"mounts": ["/a:/a"]}`, string(data))
	require.True(s.T(), s.projectTrustStatus().Trusted, "an owner restore keeps the config trusted")
	require.Len(s.T(), s.configHistory("/api/config/project/history?channel_id=ch-1").Revisions, 3)
}

// TestRestoreGlobalConfigSkipsTrust restores the global config without
// trust bookkeeping, which is only for project configs.
func (s *ServerSuite) TestRestoreGlobalConfigSkipsTrust() {
	path := s.useConfigHistory()
	trust := new(mockProjectTrust)
	s.srv.projectTrust = trust
	ok, err := s.srv.history.store.InsertConfigRevision(context.Background(), &db.ConfigRevision{Path: path, Content: "{}\n", Hash: "h"}, configHistoryKeep)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)

	rec := s.testRequest("POST", "/api/config/history/1/restore", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	data, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "{}\n", string(data))
	trust.AssertNotCalled(s.T(), "Keep", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestScanConfigHistoryProjects() {
	s.useConfigHistory()
	s.srv.loopDir = s.T().TempDir()
	shared := s.T().TempDir()
	s.writeProject(shared, "{}\n")
	s.writeProject(filepath.Join(s.srv.loopDir, "ch-3", "work"), "{\"x\": 1}\n")
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-1"}, {ChannelID: "ch-2"}, {ChannelID: "ch-3"}, {ChannelID: "gone"},
	}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: shared}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-2").Return(&db.Channel{ChannelID: "ch-2", DirPath: shared}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-3").Return(&db.Channel{ChannelID: "ch-3"}, nil)
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)

	s.srv.scanConfigHistory(context.Background())

	for _, path := range []string{
		filepath.Join(shared, ".loop", "config.json"),
		filepath.Join(s.srv.loopDir, "ch-3", "work", ".loop", "config.json"),
	} {
		revs, err := s.srv.history.store.ListConfigRevisions(context.Background(), path)
		require.NoError(s.T(), err)
		require.Len(s.T(), revs, 1, path)
		require.Equal(s.T(), db.ConfigSourceInitial, revs[0].Source)
	}
}

func (s *ServerSuite) TestScanConfigHistoryErrors() {
	store := new(testutil.MockStore)
	s.srv.history.store = store
	s.srv.loopDir = ""
	s.store.On("ListChannels", mock.Anything).Return(nil, errors.New("boom"))
	s.srv.scanConfigHistory(context.Background())

	s.srv.store = nil
	s.srv.scanConfigHistory(context.Background())
	store.AssertNotCalled(s.T(), "InsertConfigRevision", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestRecordConfig() {
	path := "/p/.loop/config.json"
	s.Run("no store", func() {
		s.srv.history.store = nil
		s.srv.recordConfig(path, "settings")
	})
	s.Run("read error", func() {
		store := new(testutil.MockStore)
		s.srv.history = configHistory{store: store}
		sys := new(testutil.MockSystem)
		sys.On("ReadFile", path).Return(nil, errors.New("denied")).Once()
		sys.On("ReadFile", path).Return(nil, os.ErrNotExist).Once()
		s.srv.sys = sys
		s.srv.recordConfig(path, "settings")
		s.srv.recordConfig(path, "settings")
		store.AssertNotCalled(s.T(), "InsertConfigRevision", mock.Anything, mock.Anything, mock.Anything)
	})
	s.Run("insert error then cached", func() {
		store := new(testutil.MockStore)
		s.srv.history = configHistory{store: store}
		sys := new(testutil.MockSystem)
		sys.On("ReadFile", path).Return([]byte("{}"), nil)
		s.srv.sys = sys
		store.On("InsertConfigRevision", mock.Anything, mock.Anything, configHistoryKeep).Return(false, errors.New("boom")).Once()
		store.On("InsertConfigRevision", mock.Anything, mock.MatchedBy(func(r *db.ConfigRevision) bool {
			return r.Path == path && r.Content == "{}" && r.Source == "learn"
		}), configHistoryKeep).Return(false, nil).Once()
		s.srv.recordConfig(path, "settings") // not cached: retried below
		s.srv.recordConfig(path, "learn")
		s.srv.recordConfig(path, "learn") // cached: no insert
		store.AssertExpectations(s.T())
	})
}

func (s *ServerSuite) TestRunConfigHistory() {
	s.srv.history.store = nil
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.srv.RunConfigHistory(ctx, time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
}

func (s *ServerSuite) TestConfigHistoryNotConfigured() {
	for _, req := range [][2]string{
		{"GET", "/api/config/history"},
		{"GET", "/api/config/project/history?channel_id=ch-1"},
		{"GET", "/api/config/history/1"},
		{"POST", "/api/config/history/1/restore"},
	} {
		rec := s.testRequest(req[0], req[1], "")
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code, req[1])
	}
}

func (s *ServerSuite) TestConfigHistoryErrors() {
	boom := errors.New("boom")
	rev := &db.ConfigRevision{ID: 1, Path: "/home/testuser/.loop/config.json", Content: "{}"}
	cases := []struct {
		name   string
		method string
		url    string
		setup  func(store *testutil.MockStore)
		want   int
	}{
		{"loop dir", "GET", "/api/config/history", func(*testutil.MockStore) {
			s.srv.loopDir = ""
		}, http.StatusInternalServerError},
		{"project channel", "GET", "/api/config/project/history", nil, http.StatusBadRequest},
		{"list", "GET", "/api/config/history", func(store *testutil.MockStore) {
			store.On("ListConfigRevisions", mock.Anything, rev.Path).Return(nil, boom)
		}, http.StatusInternalServerError},
		{"bad id", "GET", "/api/config/history/x", nil, http.StatusBadRequest},
		{"get", "GET", "/api/config/history/1", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(nil, boom)
		}, http.StatusInternalServerError},
		{"missing", "GET", "/api/config/history/1", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(nil, nil)
		}, http.StatusNotFound},
		{"revision list", "GET", "/api/config/history/1", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(rev, nil)
			store.On("ListConfigRevisions", mock.Anything, rev.Path).Return(nil, boom)
		}, http.StatusInternalServerError},
		{"bad against", "GET", "/api/config/history/1?against=x", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(rev, nil)
		}, http.StatusBadRequest},
		{"against missing", "GET", "/api/config/history/1?against=2", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(rev, nil)
			store.On("GetConfigRevision", mock.Anything, int64(2)).Return(nil, nil)
		}, http.StatusNotFound},
		{"against another file", "GET", "/api/config/history/1?against=2", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(rev, nil)
			store.On("GetConfigRevision", mock.Anything, int64(2)).Return(&db.ConfigRevision{ID: 2, Path: "/work/.loop/config.json"}, nil)
		}, http.StatusBadRequest},
		{"restore missing", "POST", "/api/config/history/1/restore", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(nil, nil)
		}, http.StatusNotFound},
		{"restore loop dir", "POST", "/api/config/history/1/restore", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(rev, nil)
			s.srv.loopDir = ""
		}, http.StatusInternalServerError},
		{"restore not a config", "POST", "/api/config/history/1/restore", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(&db.ConfigRevision{ID: 1, Path: "/etc/config.json"}, nil)
		}, http.StatusBadRequest},
		{"restore mkdir", "POST", "/api/config/history/1/restore", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(rev, nil)
			sys := new(testutil.MockSystem)
			sys.On("MkdirAll", mock.Anything, mock.Anything).Return(boom)
			s.srv.sys = sys
		}, http.StatusInternalServerError},
		{"restore write", "POST", "/api/config/history/1/restore", func(store *testutil.MockStore) {
			store.On("GetConfigRevision", mock.Anything, int64(1)).Return(rev, nil)
			sys := new(testutil.MockSystem)
			sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
			sys.On("ReadFile", rev.Path).Return(nil, os.ErrNotExist)
			sys.On("WriteFile", rev.Path, mock.Anything, mock.Anything).Return(boom)
			s.srv.sys = sys
		}, http.StatusInternalServerError},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			store := new(testutil.MockStore)
			s.srv.history = configHistory{store: store}
			s.srv.sys = s.sys
			s.srv.loopDir = "/home/testuser/.loop"
			if c.setup != nil {
				c.setup(store)
			}
			rec := s.testRequest(c.method, c.url, "")
			require.Equal(s.T(), c.want, rec.Code, rec.Body.String())
		})
	}
}
