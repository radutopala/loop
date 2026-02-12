package api

import (
	"context"
	"errors"
	"testing"

	"github.com/radutopala/loop/internal/db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MockThreadCreator struct {
	mock.Mock
}

func (m *MockThreadCreator) CreateThread(ctx context.Context, channelID, name string) (string, error) {
	args := m.Called(ctx, channelID, name)
	return args.String(0), args.Error(1)
}

type ThreadServiceSuite struct {
	suite.Suite
	store   *MockStore
	creator *MockThreadCreator
	svc     ThreadEnsurer
	ctx     context.Context
}

func TestThreadServiceSuite(t *testing.T) {
	suite.Run(t, new(ThreadServiceSuite))
}

func (s *ThreadServiceSuite) SetupTest() {
	s.store = new(MockStore)
	s.creator = new(MockThreadCreator)
	s.ctx = context.Background()
	s.svc = NewThreadService(s.store, s.creator)
}

func (s *ThreadServiceSuite) TestCreateThreadSuccess() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1", DirPath: "/work", SessionID: "sess-1"}, nil)
	s.creator.On("CreateThread", s.ctx, "ch-1", "my-thread").
		Return("thread-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-1" && ch.GuildID == "guild-1" &&
			ch.Name == "my-thread" && ch.ParentID == "ch-1" &&
			ch.DirPath == "/work" && ch.SessionID == "sess-1" && ch.Active
	})).Return(nil)

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ThreadServiceSuite) TestCreateThreadParentLookupError() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(nil, errors.New("db error"))

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up parent channel")
	require.Empty(s.T(), threadID)
}

func (s *ThreadServiceSuite) TestCreateThreadParentNotFound() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(nil, nil)

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parent channel ch-1 not found")
	require.Empty(s.T(), threadID)
}

func (s *ThreadServiceSuite) TestCreateThreadDiscordError() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1"}, nil)
	s.creator.On("CreateThread", s.ctx, "ch-1", "my-thread").
		Return("", errors.New("discord error"))

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating discord thread")
	require.Empty(s.T(), threadID)
}

func (s *ThreadServiceSuite) TestCreateThreadUpsertError() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1"}, nil)
	s.creator.On("CreateThread", s.ctx, "ch-1", "my-thread").
		Return("thread-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).
		Return(errors.New("upsert error"))

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "storing thread mapping")
	require.Empty(s.T(), threadID)
}
