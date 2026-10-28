package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/prwatch"
	"github.com/radutopala/loop/internal/testutil"
)

type mockMessageHandler struct {
	mock.Mock
	api.IncomingMessageHandler
}

func (m *mockMessageHandler) HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string) {
	m.Called(ctx, channelID, authorID, content, mode)
}

type PRWatcherSuite struct {
	suite.Suite
	logger *slog.Logger
	store  *db.SQLiteStore
	srv    *api.Server
	hub    *api.EventsHub
}

func TestPRWatcherSuite(t *testing.T) {
	suite.Run(t, new(PRWatcherSuite))
}

func (s *PRWatcherSuite) SetupTest() {
	s.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := db.NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = store.Close() })
	s.store = store
	s.srv = api.NewServer(nil, nil, nil, store, nil, s.logger)
	s.hub = api.NewEventsHub(s.logger)
}

func (s *PRWatcherSuite) TestStartPRWatcher() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startPRWatcher(ctx, s.srv, s.store, s.hub, &mockMessageHandler{}, nil, s.logger)
}

func (s *PRWatcherSuite) TestStartPRWatcherWithoutPRState() {
	// A store without CI state starts nothing: no call reaches it.
	startPRWatcher(context.Background(), s.srv, new(testutil.MockStore), s.hub, nil, nil, s.logger)
}

func (s *PRWatcherSuite) TestPRDeps() {
	msgs := new(mockMessageHandler)
	msgs.On("HandleIncomingMessage", mock.Anything, "th-1", prwatch.AuthorID, "fix it", "").Once()
	d := prDeps(s.srv, s.store, s.store, s.hub, msgs, nil, s.logger)
	d.Enqueue(context.Background(), "th-1", "fix it")
	msgs.AssertExpectations(s.T())

	var seen []api.Event
	s.hub.Observe(func(e api.Event) { seen = append(seen, e) })
	d.Notice(context.Background(), "th-1", "stopped")
	require.Len(s.T(), seen, 1)
	require.Equal(s.T(), api.EventMessageCreated, seen[0].Type)
	require.Equal(s.T(), "th-1", seen[0].ChannelID)
}
