package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// failingFileSystem fails stats of a file named base after statOK of them
// succeed, or reads of it.
type failingFileSystem struct {
	serverSystem
	base             string
	failStat, failRd bool
	statOK           int
}

func (f *failingFileSystem) Stat(name string) (os.FileInfo, error) {
	if f.failStat && filepath.Base(name) == f.base {
		if f.statOK == 0 {
			return nil, os.ErrPermission
		}
		f.statOK--
	}
	return f.serverSystem.Stat(name)
}

func (f *failingFileSystem) ReadFileIn(root, name string) ([]byte, error) {
	if f.failRd && filepath.Base(name) == f.base {
		return nil, os.ErrPermission
	}
	return f.serverSystem.ReadFileIn(root, name)
}

// walkthroughDirs makes a project dir and a global extra dir with files in
// them, and a channel ch-1 (with a side chat side-1) on the project.
func (s *ServerSuite) walkthroughDirs() (dir, extra string) {
	dir, extra = s.T().TempDir(), s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(1)\n}\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "bin"), []byte("a\x00b\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "long.txt"), []byte(strings.Repeat("é", 3000)+"\n"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(extra, "lib.go"), []byte("package lib\n"), 0o644))
	s.srv.configs.load = func() (*config.Config, error) { return &config.Config{ExtraDirs: []string{extra}}, nil }
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "side-1").Return(&db.Channel{ChannelID: "side-1", DirPath: dir, Kind: db.ChannelKindSide, ParentID: "ch-1"}, nil).Maybe()
	return dir, extra
}

func (s *ServerSuite) TestCreateWalkthrough() {
	_, extra := s.walkthroughDirs()
	want := []db.WalkthroughStep{
		{Path: "main.go", StartLine: 3, EndLine: 4, Anchor: "func main() {\n\tprintln(1)", Title: "Entry", Body: "Starts here."},
		{Path: filepath.Join(extra, "lib.go"), StartLine: 1, EndLine: 1, Anchor: "package lib", Body: "The lib."},
		{Path: "long.txt", StartLine: 1, EndLine: 1, Anchor: strings.Repeat("é", 2000), Body: "Cut at a whole character."},
	}
	s.store.On("CreateWalkthrough", mock.Anything, &db.Walkthrough{ChannelID: "ch-1", Title: "Tour", Steps: want}).
		Return(&db.Walkthrough{ID: 7, ChannelID: "ch-1", Title: "Tour", Steps: want, StepCount: 3}, nil)

	body := `{"title":" Tour ","steps":[
		{"path":"main.go","start_line":3,"end_line":4,"title":" Entry ","body":" Starts here. "},
		{"path":"lib.go","start_line":1,"body":"The lib."},
		{"path":"long.txt","start_line":1,"body":"Cut at a whole character."}]}`
	w := s.explainRequest("POST", "side-1/walkthroughs", body)
	require.Equal(s.T(), http.StatusCreated, w.Code, w.Body.String())
	var got db.Walkthrough
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(s.T(), int64(7), got.ID)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateWalkthroughErrors() {
	step := func(fields string) string {
		return `{"title":"T","steps":[{` + fields + `}]}`
	}
	tests := []struct {
		name, channel, body, want string
		code                      int
		sys                       func() serverSystem
	}{
		{name: "bad json", body: `{`, code: http.StatusBadRequest},
		{name: "no channel", channel: "gone", body: `{}`, code: http.StatusNotFound},
		{name: "no title", body: `{"title":" "}`, code: http.StatusBadRequest, want: "title is required"},
		{name: "long title", body: `{"title":"` + strings.Repeat("x", 201) + `"}`, code: http.StatusBadRequest, want: "title is longer"},
		{name: "no steps", body: `{"title":"T"}`, code: http.StatusBadRequest, want: "1 to 50 steps"},
		{name: "too many steps", body: `{"title":"T","steps":[` + strings.TrimSuffix(strings.Repeat(`{},`, 51), ",") + `]}`, code: http.StatusBadRequest, want: "1 to 50 steps"},
		{name: "no body", body: step(`"path":"main.go","start_line":1`), code: http.StatusBadRequest, want: "step 1: body is required"},
		{name: "long body", body: step(`"path":"main.go","start_line":1,"body":"` + strings.Repeat("x", 8001) + `"`), code: http.StatusBadRequest, want: "body is longer"},
		{name: "long step title", body: step(`"path":"main.go","start_line":1,"body":"b","title":"` + strings.Repeat("x", 201) + `"`), code: http.StatusBadRequest, want: "title is longer"},
		{name: "no start", body: step(`"path":"main.go","body":"b"`), code: http.StatusBadRequest, want: "aren't a range"},
		{name: "end before start", body: step(`"path":"main.go","start_line":3,"end_line":2,"body":"b"`), code: http.StatusBadRequest, want: "aren't a range"},
		{name: "no file", body: step(`"path":"nope.go","start_line":1,"body":"b"`), code: http.StatusBadRequest, want: `no file "nope.go"`},
		{name: "past the end", body: step(`"path":"main.go","start_line":5,"end_line":6,"body":"b"`), code: http.StatusBadRequest, want: "has 5 lines, not 6"},
		{name: "binary", body: step(`"path":"bin","start_line":1,"body":"b"`), code: http.StatusBadRequest, want: "binary file"},
		{
			name: "stat fails", body: step(`"path":"main.go","start_line":1,"body":"b"`), code: http.StatusBadRequest, want: "too large",
			sys: func() serverSystem {
				return &failingFileSystem{serverSystem: s.srv.sys, base: "main.go", failStat: true, statOK: 1}
			},
		},
		{
			name: "read fails", body: step(`"path":"main.go","start_line":1,"body":"b"`), code: http.StatusBadRequest, want: "reading main.go",
			sys: func() serverSystem {
				return &failingFileSystem{serverSystem: s.srv.sys, base: "main.go", failRd: true}
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.walkthroughDirs()
			s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
			if tc.sys != nil {
				s.srv.sys = tc.sys()
			}
			w := s.explainRequest("POST", cmp.Or(tc.channel, "ch-1")+"/walkthroughs", tc.body)
			require.Equal(s.T(), tc.code, w.Code, w.Body.String())
			require.Contains(s.T(), w.Body.String(), tc.want)
		})
	}
}

func (s *ServerSuite) TestCreateWalkthroughLookupErrors() {
	boom := errors.New("boom")
	body := `{"title":"T","steps":[{"path":"main.go","start_line":1,"body":"b"}]}`
	s.Run("group", func() {
		s.SetupTest()
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Once()
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, boom).Once()
		require.Equal(s.T(), http.StatusInternalServerError, s.explainRequest("POST", "ch-1/walkthroughs", body).Code)
	})
	s.Run("dirs", func() {
		s.SetupTest()
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Twice()
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, boom).Once()
		require.Equal(s.T(), http.StatusInternalServerError, s.explainRequest("POST", "ch-1/walkthroughs", body).Code)
	})
	s.Run("store", func() {
		s.SetupTest()
		s.walkthroughDirs()
		s.store.On("CreateWalkthrough", mock.Anything, mock.Anything).Return(nil, boom)
		require.Equal(s.T(), http.StatusInternalServerError, s.explainRequest("POST", "ch-1/walkthroughs", body).Code)
	})
}

func (s *ServerSuite) TestListWalkthroughs() {
	s.walkthroughDirs()
	s.store.On("GetChannel", mock.Anything, "ch-2").Return(&db.Channel{ChannelID: "ch-2"}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-3").Return(&db.Channel{ChannelID: "ch-3"}, nil)
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	s.store.On("ListWalkthroughs", mock.Anything, "ch-1").Return([]*db.Walkthrough{{ID: 2, ChannelID: "ch-1", Title: "T", StepCount: 3}}, nil)
	s.store.On("ListWalkthroughs", mock.Anything, "ch-2").Return(nil, nil)
	s.store.On("ListWalkthroughs", mock.Anything, "ch-3").Return(nil, errors.New("boom"))

	w := s.explainRequest("GET", "side-1/walkthroughs", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var list []*db.Walkthrough
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(s.T(), list, 1)
	require.Equal(s.T(), 3, list[0].StepCount)

	w = s.explainRequest("GET", "ch-2/walkthroughs", "")
	require.JSONEq(s.T(), `[]`, w.Body.String())
	require.Equal(s.T(), http.StatusInternalServerError, s.explainRequest("GET", "ch-3/walkthroughs", "").Code)
	require.Equal(s.T(), http.StatusNotFound, s.explainRequest("GET", "gone/walkthroughs", "").Code)
}

func (s *ServerSuite) TestGetWalkthrough() {
	s.walkthroughDirs()
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	s.store.On("GetWalkthrough", mock.Anything, int64(1)).Return(&db.Walkthrough{ID: 1, ChannelID: "ch-1", Title: "T"}, nil)
	s.store.On("GetWalkthrough", mock.Anything, int64(2)).Return(&db.Walkthrough{ID: 2, ChannelID: "other"}, nil)
	s.store.On("GetWalkthrough", mock.Anything, int64(3)).Return(nil, nil)
	s.store.On("GetWalkthrough", mock.Anything, int64(4)).Return(nil, errors.New("boom"))

	tests := []struct {
		path string
		code int
	}{
		{"side-1/walkthroughs/1", http.StatusOK},
		{"ch-1/walkthroughs/2", http.StatusNotFound},
		{"ch-1/walkthroughs/3", http.StatusNotFound},
		{"ch-1/walkthroughs/4", http.StatusInternalServerError},
		{"ch-1/walkthroughs/x", http.StatusBadRequest},
		{"gone/walkthroughs/1", http.StatusNotFound},
	}
	for _, tc := range tests {
		s.Run(tc.path, func() {
			w := s.explainRequest("GET", tc.path, "")
			require.Equal(s.T(), tc.code, w.Code, w.Body.String())
		})
	}
	w := s.explainRequest("GET", "ch-1/walkthroughs/1", "")
	require.Contains(s.T(), w.Body.String(), `"title":"T"`)
}

func (s *ServerSuite) TestDeleteWalkthrough() {
	s.walkthroughDirs()
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	s.store.On("DeleteWalkthrough", mock.Anything, "ch-1", int64(1)).Return(true, nil)
	s.store.On("DeleteWalkthrough", mock.Anything, "ch-1", int64(2)).Return(false, nil)
	s.store.On("DeleteWalkthrough", mock.Anything, "ch-1", int64(3)).Return(false, errors.New("boom"))

	tests := []struct {
		path string
		code int
	}{
		{"side-1/walkthroughs/1", http.StatusNoContent},
		{"ch-1/walkthroughs/2", http.StatusNotFound},
		{"ch-1/walkthroughs/3", http.StatusInternalServerError},
		{"ch-1/walkthroughs/x", http.StatusBadRequest},
		{"gone/walkthroughs/1", http.StatusNotFound},
	}
	for _, tc := range tests {
		s.Run(tc.path, func() {
			require.Equal(s.T(), tc.code, s.explainRequest("DELETE", tc.path, "").Code)
		})
	}
}
