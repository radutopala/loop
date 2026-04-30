package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

func (s *ServerSuite) TestHandleTimelineNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/timeline", srv.handleTimeline)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/timeline", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestHandleTimelineSuccessMessagesOnly() {
	now := time.Now().UTC()
	rows := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "hello", IsBot: false, CreatedAt: now, Kind: db.MessageKindMessage, ChainPosition: 2},
		{ID: 9, ChannelID: "ch-1", MsgID: "m9", AuthorID: "bot", AuthorName: "Bot", Content: "hi", IsBot: true, CreatedAt: now, Kind: db.MessageKindMessage, ChainPosition: 1},
	}
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(0), 51).Return(rows, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp timelineResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Items, 2)
	require.Equal(s.T(), "message", resp.Items[0].Kind)
	require.Equal(s.T(), int64(2), resp.Items[0].Position)
	require.Equal(s.T(), int64(10), resp.Items[0].ID)
	require.NotNil(s.T(), resp.Items[0].Data)
	require.Equal(s.T(), "hello", resp.Items[0].Data.Content)
	require.Equal(s.T(), int64(1), resp.Items[1].Position)
	require.Nil(s.T(), resp.NextCursor)
}

func (s *ServerSuite) TestHandleTimelineWithCursor() {
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(5), int64(99), 51).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline?cursor_position=5&cursor_id=99", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp timelineResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp.Items)
	require.Nil(s.T(), resp.NextCursor)
}

func (s *ServerSuite) TestHandleTimelineInvalidCursorPosition() {
	rec := s.testRequest("GET", "/api/channels/ch-1/timeline?cursor_position=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleTimelineInvalidCursorID() {
	rec := s.testRequest("GET", "/api/channels/ch-1/timeline?cursor_id=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleTimelineInvalidLimit() {
	rec := s.testRequest("GET", "/api/channels/ch-1/timeline?limit=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// cursor_position=0 is meaningful: it pages into legacy rows that all sit
// at chain_position=0. Combined with cursor_id, the backend selects rows
// older than the given id within the legacy band.
func (s *ServerSuite) TestHandleTimelineCursorPositionZeroIsValid() {
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(42), 51).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline?cursor_position=0&cursor_id=42", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestHandleTimelineCursorRejectsNegative() {
	rec := s.testRequest("GET", "/api/channels/ch-1/timeline?cursor_position=-1", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/timeline?cursor_id=-1", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleTimelineLimitCap() {
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(0), 201).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline?limit=500", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestHandleTimelineStoreError() {
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(0), 51).Return(nil, errors.New("db boom"))

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestHandleTimelinePagination() {
	now := time.Now().UTC()
	// Return limit+1 rows (3 with limit=2) to trigger pagination.
	rows := []*db.Message{
		{ID: 30, ChannelID: "ch-1", Content: "third", CreatedAt: now, Kind: db.MessageKindMessage, ChainPosition: 3},
		{ID: 20, ChannelID: "ch-1", Content: "second", CreatedAt: now, Kind: db.MessageKindMessage, ChainPosition: 2},
		{ID: 10, ChannelID: "ch-1", Content: "first", CreatedAt: now, Kind: db.MessageKindMessage, ChainPosition: 1},
	}
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(0), 3).Return(rows, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline?limit=2", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp timelineResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Items, 2)
	require.NotNil(s.T(), resp.NextCursor)
	require.Equal(s.T(), int64(2), resp.NextCursor.Position)
	require.Equal(s.T(), int64(20), resp.NextCursor.ID)
}

func (s *ServerSuite) TestHandleTimelineRendersInlineEventContent() {
	now := time.Now().UTC()
	rows := []*db.Message{
		{ID: 30, ChannelID: "ch-1", IsBot: true, CreatedAt: now, Kind: db.MessageKindToolResult, ChainPosition: 3, ToolUseID: "toolu_1", Content: "file contents", IsError: false},
		{ID: 20, ChannelID: "ch-1", IsBot: true, CreatedAt: now, Kind: db.MessageKindToolUse, ChainPosition: 2, ToolUseID: "toolu_1", ToolName: "Read", Content: `{"path":"/x"}`},
		{ID: 10, ChannelID: "ch-1", IsBot: true, CreatedAt: now, Kind: db.MessageKindThinking, ChainPosition: 1, Content: "deep thoughts"},
	}
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(0), 51).Return(rows, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp timelineResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Items, 3)

	require.Equal(s.T(), "tool_result", resp.Items[0].Kind)
	require.Equal(s.T(), "file contents", resp.Items[0].Text)
	require.Equal(s.T(), "toolu_1", resp.Items[0].ToolUseID)
	require.False(s.T(), resp.Items[0].IsError)

	require.Equal(s.T(), "tool_use", resp.Items[1].Kind)
	require.Equal(s.T(), "Read", resp.Items[1].ToolName)
	require.Contains(s.T(), resp.Items[1].ToolInput, "/x")
	require.Equal(s.T(), "toolu_1", resp.Items[1].ToolUseID)

	require.Equal(s.T(), "thinking", resp.Items[2].Kind)
	require.Equal(s.T(), "deep thoughts", resp.Items[2].Text)
}

func (s *ServerSuite) TestHandleTimelineToolResultIsErrorPropagates() {
	now := time.Now().UTC()
	rows := []*db.Message{
		{ID: 10, ChannelID: "ch-1", IsBot: true, CreatedAt: now, Kind: db.MessageKindToolResult, ChainPosition: 1, ToolUseID: "toolu_e", Content: "boom", IsError: true},
	}
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(0), 51).Return(rows, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp timelineResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Items, 1)
	require.True(s.T(), resp.Items[0].IsError)
	require.Equal(s.T(), "boom", resp.Items[0].Text)
}

func (s *ServerSuite) TestHandleTimelineCapsOversizeContent() {
	huge := strings.Repeat("x", timelineInlineCap+128)
	now := time.Now().UTC()
	rows := []*db.Message{
		{ID: 10, ChannelID: "ch-1", IsBot: true, CreatedAt: now, Kind: db.MessageKindThinking, ChainPosition: 1, Content: huge},
	}
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(0), 51).Return(rows, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp timelineResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Items, 1)
	require.Equal(s.T(), timelineInlineCap, len(resp.Items[0].Text))
	require.True(s.T(), resp.Items[0].Truncated)
}

func (s *ServerSuite) TestHandleTimelineMixedLegacyAndBackfilled() {
	now := time.Now().UTC()
	rows := []*db.Message{
		// backfilled rows
		{ID: 30, ChannelID: "ch-1", AuthorName: "alice", Content: "user msg", CreatedAt: now, Kind: db.MessageKindMessage, ChainPosition: 2},
		{ID: 25, ChannelID: "ch-1", IsBot: true, CreatedAt: now, Kind: db.MessageKindThinking, ChainPosition: 1, Content: "thinking it"},
		// legacy rows (chain_position = 0)
		{ID: 5, ChannelID: "ch-1", AuthorName: "alice", Content: "old msg", CreatedAt: now, Kind: db.MessageKindMessage, ChainPosition: 0},
	}
	s.store.On("GetTimeline", mock.Anything, "ch-1", int64(0), int64(0), 51).Return(rows, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/timeline", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp timelineResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Items, 3)
	require.Equal(s.T(), "message", resp.Items[0].Kind)
	require.Equal(s.T(), "user msg", resp.Items[0].Data.Content)
	require.Equal(s.T(), "thinking", resp.Items[1].Kind)
	require.Equal(s.T(), "thinking it", resp.Items[1].Text)
	require.Equal(s.T(), "message", resp.Items[2].Kind)
	require.Equal(s.T(), int64(0), resp.Items[2].Position)
	require.Equal(s.T(), "old msg", resp.Items[2].Data.Content)
}

func (s *ServerSuite) TestBuildTimelineItemUnknownKind() {
	m := &db.Message{ID: 1, ChannelID: "ch-1", Kind: db.MessageKind("future_unknown_kind"), ChainPosition: 7}
	item := buildTimelineItem(m)
	require.Equal(s.T(), "future_unknown_kind", item.Kind)
	require.Equal(s.T(), int64(7), item.Position)
	require.Equal(s.T(), int64(1), item.ID)
	require.Nil(s.T(), item.Data)
}

func (s *ServerSuite) TestBuildTimelineItemEmptyKindFallsBackToMessage() {
	m := &db.Message{ID: 9, ChannelID: "ch-1", Content: "legacy", Kind: ""}
	item := buildTimelineItem(m)
	require.Equal(s.T(), "message", item.Kind)
	require.NotNil(s.T(), item.Data)
	require.Equal(s.T(), "legacy", item.Data.Content)
}

// silence unused-import lint for testing
var _ = testing.T{}
