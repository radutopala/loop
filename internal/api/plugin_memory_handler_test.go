package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/testutil"
)

const echoMemoryDir = "/home/u/.claude/projects/-src-app/memory"

// pluginMemoryRoutes registers the memory routes for echo, allowed
// memory.write in ch-1 at dir, and returns the system they write through.
func (s *ServerSuite) pluginMemoryRoutes(dir string) *testutil.MockSystem {
	s.pluginAPIRoutes(plugin.CapMemoryWrite, dir)
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	s.T().Cleanup(func() { sys.AssertExpectations(s.T()) })
	return sys
}

func (s *ServerSuite) TestPutPluginMemory() {
	tests := []struct {
		name     string
		path     string
		dir      string
		body     string
		setup    func(sys *testutil.MockSystem)
		wantCode int
		wantBody string
	}{
		{name: "writes", path: "quiz-notes", dir: "/src/app", body: `{"content":"# Notes"}`, setup: func(sys *testutil.MockSystem) {
			sys.On("UserHomeDir").Return("/home/u", nil).Once()
			sys.On("MkdirAll", echoMemoryDir, os.FileMode(0o700)).Return(nil).Once()
			sys.On("WriteFile", echoMemoryDir+"/plugin-echo-quiz-notes.md", []byte("# Notes"), os.FileMode(0o644)).Return(nil).Once()
		}, wantCode: http.StatusNoContent},
		{name: "bad name", path: "Quiz", dir: "/src/app", body: `{}`, wantCode: http.StatusBadRequest, wantBody: "lowercase"},
		{name: "name too long", path: strings.Repeat("a", 65), dir: "/src/app", body: `{}`, wantCode: http.StatusBadRequest},
		{name: "channel without a dir", path: "notes", body: `{}`, wantCode: http.StatusConflict, wantBody: "no dir"},
		{name: "no home", path: "notes", dir: "/src/app", body: `{}`, setup: func(sys *testutil.MockSystem) {
			sys.On("UserHomeDir").Return("", errors.New("no home")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "getting home dir: no home"},
		{name: "bad body", path: "notes", dir: "/src/app", body: `{`, setup: func(sys *testutil.MockSystem) {
			sys.On("UserHomeDir").Return("/home/u", nil).Once()
		}, wantCode: http.StatusBadRequest},
		{name: "too big", path: "notes", dir: "/src/app", body: `{"content":"` + strings.Repeat("a", pluginMemoryMax) + `"}`, setup: func(sys *testutil.MockSystem) {
			sys.On("UserHomeDir").Return("/home/u", nil).Once()
		}, wantCode: http.StatusBadRequest},
		{name: "mkdir fails", path: "notes", dir: "/src/app", body: `{"content":"x"}`, setup: func(sys *testutil.MockSystem) {
			sys.On("UserHomeDir").Return("/home/u", nil).Once()
			sys.On("MkdirAll", echoMemoryDir, os.FileMode(0o700)).Return(errors.New("ro")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "creating the memory dir failed"},
		{name: "write fails", path: "notes", dir: "/src/app", body: `{"content":"x"}`, setup: func(sys *testutil.MockSystem) {
			sys.On("UserHomeDir").Return("/home/u", nil).Once()
			sys.On("MkdirAll", echoMemoryDir, os.FileMode(0o700)).Return(nil).Once()
			sys.On("WriteFile", echoMemoryDir+"/plugin-echo-notes.md", []byte("x"), os.FileMode(0o644)).Return(errors.New("full")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "writing the memory file failed"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			sys := s.pluginMemoryRoutes(tt.dir)
			if tt.setup != nil {
				tt.setup(sys)
			}
			rec := s.requestAs(echoPlugin, "PUT", "/api/plugin/v1/memory/"+tt.path, tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

func (s *ServerSuite) TestDeletePluginMemory() {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "removes", wantCode: http.StatusNoContent},
		{name: "already gone", err: fs.ErrNotExist, wantCode: http.StatusNoContent},
		{name: "remove fails", err: errors.New("busy"), wantCode: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			sys := s.pluginMemoryRoutes("/src/app")
			sys.On("UserHomeDir").Return("/home/u", nil).Once()
			sys.On("Remove", echoMemoryDir+"/plugin-echo-notes.md").Return(tt.err).Once()
			rec := s.requestAs(echoPlugin, "DELETE", "/api/plugin/v1/memory/notes", "")
			require.Equal(s.T(), tt.wantCode, rec.Code)
		})
	}
}

func (s *ServerSuite) TestPluginMemoryNeedsTheCapability() {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("PUT /api/plugin/v1/memory/{name}", s.srv.handlePutPluginMemory)
	pm.On("Allows", mock.Anything, "echo", plugin.CapMemoryWrite).Return(install.ErrNotApproved).Once()
	rec := s.requestAs(echoPlugin, "PUT", "/api/plugin/v1/memory/notes", `{"content":"x"}`)
	require.Equal(s.T(), http.StatusForbidden, rec.Code)
}
