package api

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

type MockCreator struct {
	mock.Mock
}

func (m *MockCreator) CreateChannel(ctx context.Context, guildID, name string) (string, error) {
	args := m.Called(ctx, guildID, name)
	return args.String(0), args.Error(1)
}

func (m *MockCreator) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	return m.Called(ctx, channelID, userID).Error(0)
}

func (m *MockCreator) GetOwnerUserID(ctx context.Context) (string, error) {
	args := m.Called(ctx)
	return args.String(0), args.Error(1)
}

func (m *MockCreator) SetChannelTopic(ctx context.Context, channelID, topic string) error {
	return m.Called(ctx, channelID, topic).Error(0)
}

// --- Test Suite ---

type ChannelServiceSuite struct {
	suite.Suite
	store   *testutil.MockStore
	creator *MockCreator
	svc     ChannelEnsurer
	ctx     context.Context
}

func TestChannelServiceSuite(t *testing.T) {
	suite.Run(t, new(ChannelServiceSuite))
}

func (s *ChannelServiceSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.creator = new(MockCreator)
	s.ctx = context.Background()
	s.svc = NewChannelService(s.store, s.creator, "guild-1", types.PlatformDiscord)
	randSuffix = func() string { return "ab12" }
}

func (s *ChannelServiceSuite) TestEnsureChannelExisting() {
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformDiscord).
		Return(&db.Channel{ChannelID: "existing-ch-1", DirPath: "/home/user/dev/loop"}, nil)

	channelID, err := s.svc.EnsureChannel(s.ctx, "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing-ch-1", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertNotCalled(s.T(), "CreateChannel", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ChannelServiceSuite) TestEnsureChannelCreatesNew() {
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformDiscord).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "guild-1", "loop-ab12").
		Return("new-ch-1", nil)
	s.creator.On("SetChannelTopic", s.ctx, "new-ch-1", "/home/user/dev/loop").Return(nil)
	s.creator.On("GetOwnerUserID", s.ctx).Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-1" && ch.GuildID == "guild-1" &&
			ch.Name == "loop-ab12" && ch.DirPath == "/home/user/dev/loop" &&
			ch.Platform == types.PlatformDiscord && ch.Active
	})).Return(nil)

	channelID, err := s.svc.EnsureChannel(s.ctx, "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestEnsureChannelCreatesNewWithOwnerInvite() {
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformDiscord).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "guild-1", "loop-ab12").
		Return("new-ch-1", nil)
	s.creator.On("SetChannelTopic", s.ctx, "new-ch-1", "/home/user/dev/loop").Return(nil)
	s.creator.On("GetOwnerUserID", s.ctx).Return("U-OWNER", nil)
	s.creator.On("InviteUserToChannel", s.ctx, "new-ch-1", "U-OWNER").Return(nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-1"
	})).Return(nil)

	channelID, err := s.svc.EnsureChannel(s.ctx, "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.creator.AssertCalled(s.T(), "InviteUserToChannel", s.ctx, "new-ch-1", "U-OWNER")
}

func (s *ChannelServiceSuite) TestEnsureChannelLookupError() {
	s.store.On("GetChannelByDirPath", s.ctx, "/path", types.PlatformDiscord).
		Return(nil, errors.New("db error"))

	channelID, err := s.svc.EnsureChannel(s.ctx, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up channel by dir path")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestEnsureChannelCreatorError() {
	s.store.On("GetChannelByDirPath", s.ctx, "/path", types.PlatformDiscord).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "guild-1", "path-ab12").
		Return("", errors.New("discord error"))

	channelID, err := s.svc.EnsureChannel(s.ctx, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating channel")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestEnsureChannelUpsertError() {
	s.store.On("GetChannelByDirPath", s.ctx, "/path", types.PlatformDiscord).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "guild-1", "path-ab12").
		Return("ch-1", nil)
	s.creator.On("SetChannelTopic", s.ctx, "ch-1", "/path").Return(nil)
	s.creator.On("GetOwnerUserID", s.ctx).Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).
		Return(errors.New("upsert error"))

	channelID, err := s.svc.EnsureChannel(s.ctx, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "storing channel mapping")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestCreateChannelSuccess() {
	s.creator.On("CreateChannel", s.ctx, "guild-1", "trial").
		Return("new-ch-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-1" && ch.GuildID == "guild-1" &&
			ch.Name == "trial" && ch.DirPath == "" &&
			ch.Platform == types.PlatformDiscord && ch.Active
	})).Return(nil)

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestCreateChannelWithAuthorInvite() {
	s.creator.On("CreateChannel", s.ctx, "guild-1", "trial").
		Return("new-ch-1", nil)
	s.creator.On("InviteUserToChannel", s.ctx, "new-ch-1", "user-42").
		Return(nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-1" && ch.Name == "trial"
	})).Return(nil)

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "user-42")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestCreateChannelInviteError() {
	s.creator.On("CreateChannel", s.ctx, "guild-1", "trial").
		Return("new-ch-1", nil)
	s.creator.On("InviteUserToChannel", s.ctx, "new-ch-1", "user-42").
		Return(errors.New("invite failed"))

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "user-42")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "inviting user to channel")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestCreateChannelCreatorError() {
	s.creator.On("CreateChannel", s.ctx, "guild-1", "trial").
		Return("", errors.New("platform error"))

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating channel")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestCreateChannelUpsertError() {
	s.creator.On("CreateChannel", s.ctx, "guild-1", "trial").
		Return("ch-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).
		Return(errors.New("upsert error"))

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "storing channel mapping")
	require.Empty(s.T(), channelID)
}
