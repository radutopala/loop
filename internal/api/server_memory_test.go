package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/memory"
)

// --- MemorySearch tests ---

func (s *ServerSuite) TestMemorySearchSuccess() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	indexer.On("Search", mock.Anything, "/tmp/memory", "docker tips", 3).
		Return([]memory.SearchResult{
			{FilePath: "/tmp/memory/MEMORY.md", Content: "Tips", Score: 0.95},
		}, nil)

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"docker tips","top_k":3,"dir_path":"/tmp/memory"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp memorySearchResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Results, 1)
	require.Equal(s.T(), "/tmp/memory/MEMORY.md", resp.Results[0].FilePath)
	require.InDelta(s.T(), 0.95, float64(resp.Results[0].Score), 0.001)
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchNotConfigured() {
	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","dir_path":"/tmp/memory"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestMemorySearchValidation() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	tests := []struct {
		name string
		body string
	}{
		{"EmptyQuery", `{"query":"","dir_path":"/tmp/memory"}`},
		{"EmptyDirPathAndChannelID", `{"query":"test"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			rec := s.testRequest("POST", "/api/memory/search", tt.body)
			require.Equal(s.T(), http.StatusBadRequest, rec.Code)
		})
	}
}

func (s *ServerSuite) TestMemorySearchByChannelID() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	indexer.On("Search", mock.Anything, "/home/user/project", "docker tips", 5).
		Return([]memory.SearchResult{
			{FilePath: "/tmp/mem/MEMORY.md", Content: "Tips", Score: 0.9},
		}, nil)

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"docker tips","top_k":5,"channel_id":"ch-1"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp memorySearchResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Results, 1)
	s.store.AssertExpectations(s.T())
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchByChannelIDNotFound() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-unknown").
		Return(nil, nil)

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","channel_id":"ch-unknown"}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchError() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	indexer.On("Search", mock.Anything, "/tmp/memory", "test", 0).
		Return(nil, errors.New("search failed"))

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","dir_path":"/tmp/memory"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	indexer.AssertExpectations(s.T())
}

// --- MemoryIndex tests ---

func (s *ServerSuite) TestMemoryIndexSuccess() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	indexer.On("Index", mock.Anything, "/tmp/memory").Return(15, nil)

	rec := s.testRequest("POST", "/api/memory/index", `{"dir_path":"/tmp/memory"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp memoryIndexResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), 15, resp.Count)
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemoryIndexNotConfigured() {
	rec := s.testRequest("POST", "/api/memory/index", `{"dir_path":"/tmp/memory"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestMemoryIndexEmptyDirPathAndChannelID() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	rec := s.testRequest("POST", "/api/memory/index", `{}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestMemoryIndexByChannelID() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	indexer.On("Index", mock.Anything, "/home/user/project").Return(10, nil)

	rec := s.testRequest("POST", "/api/memory/index", `{"channel_id":"ch-1"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp memoryIndexResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), 10, resp.Count)
	s.store.AssertExpectations(s.T())
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemoryIndexError() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	indexer.On("Index", mock.Anything, "/tmp/memory").Return(0, errors.New("index failed"))

	rec := s.testRequest("POST", "/api/memory/index", `{"dir_path":"/tmp/memory"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchByChannelIDLookupError() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return(nil, errors.New("db error"))

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","channel_id":"ch-err"}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchByChannelIDEmptyDirPath() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-nodir").
		Return(&db.Channel{ChannelID: "ch-nodir", DirPath: ""}, nil)

	// Without loopDir set, should return error.
	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","channel_id":"ch-nodir"}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchByChannelIDEmptyDirPathFallback() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)
	s.srv.SetLoopDir("/home/test/.loop")

	s.store.On("GetChannel", mock.Anything, "ch-nodir").
		Return(&db.Channel{ChannelID: "ch-nodir", DirPath: ""}, nil)
	indexer.On("Search", mock.Anything, "/home/test/.loop/ch-nodir/work", "test", 0).
		Return([]memory.SearchResult{}, nil)

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","channel_id":"ch-nodir"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	indexer.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())

	// Clean up loopDir for other tests.
	s.srv.SetLoopDir("")
}

func (s *ServerSuite) TestMemorySearchByChannelIDNilStore() {
	srv := nilServer()
	indexer := new(MockMemoryIndexer)
	srv.SetMemoryIndexer(indexer)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/memory/search", srv.handleMemorySearch)

	req := httptest.NewRequest("POST", "/api/memory/search", bytes.NewBufferString(`{"query":"test","channel_id":"ch-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- SetMemoryIndexer ---

func (s *ServerSuite) TestSetMemoryIndexer() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)
	require.NotNil(s.T(), s.srv.memoryIndexer)
}
