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

func (m *MockCreator) CreateChannel(ctx context.Context, name string) (string, error) {
	args := m.Called(ctx, name)
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
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{types.PlatformLocal: s.creator}, "/tmp/test-loop")
	svc.(*channelService).randSuffix = func() string { return "ab12" }
	s.svc = svc
}

func (s *ChannelServiceSuite) TestEnsureChannelExisting() {
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformLocal).
		Return(&db.Channel{ChannelID: "existing-ch-1", DirPath: "/home/user/dev/loop"}, nil)

	channelID, err := s.svc.EnsureChannel(s.ctx, "/home/user/dev/loop", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing-ch-1", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertNotCalled(s.T(), "CreateChannel", mock.Anything, mock.Anything)
}

func (s *ChannelServiceSuite) TestEnsureChannelCreatesNew() {
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformLocal).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "loop-ab12").
		Return("new-ch-1", nil)
	s.creator.On("SetChannelTopic", s.ctx, "new-ch-1", "/home/user/dev/loop").Return(nil)
	s.creator.On("GetOwnerUserID", s.ctx).Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-1" &&
			ch.Name == "loop-ab12" && ch.DirPath == "/home/user/dev/loop" &&
			ch.Platform == types.PlatformLocal && ch.Active
	})).Return(nil)

	channelID, err := s.svc.EnsureChannel(s.ctx, "/home/user/dev/loop", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestEnsureChannelCreatesNewWithOwnerInvite() {
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformLocal).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "loop-ab12").
		Return("new-ch-1", nil)
	s.creator.On("SetChannelTopic", s.ctx, "new-ch-1", "/home/user/dev/loop").Return(nil)
	s.creator.On("GetOwnerUserID", s.ctx).Return("U-OWNER", nil)
	s.creator.On("InviteUserToChannel", s.ctx, "new-ch-1", "U-OWNER").Return(nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-1"
	})).Return(nil)

	channelID, err := s.svc.EnsureChannel(s.ctx, "/home/user/dev/loop", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.creator.AssertCalled(s.T(), "InviteUserToChannel", s.ctx, "new-ch-1", "U-OWNER")
}

func (s *ChannelServiceSuite) TestEnsureChannelSanitizesDots() {
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/my.dotted.project", types.PlatformLocal).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "my-dotted-project-ab12").
		Return("new-ch-2", nil)
	s.creator.On("SetChannelTopic", s.ctx, "new-ch-2", "/home/user/dev/my.dotted.project").Return(nil)
	s.creator.On("GetOwnerUserID", s.ctx).Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-2" && ch.Name == "my-dotted-project-ab12" &&
			ch.DirPath == "/home/user/dev/my.dotted.project"
	})).Return(nil)

	channelID, err := s.svc.EnsureChannel(s.ctx, "/home/user/dev/my.dotted.project", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-2", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestSanitizeChannelName() {
	tests := []struct {
		input    string
		expected string
	}{
		{"my.dotted.project", "my-dotted-project"},
		{"My Project", "my-project"},
		{"UPPER_case", "upper_case"},
		{"--leading--", "leading"},
		{"normal", "normal"},
		{"a.b.c", "a-b-c"},
		{"...", "project"},
	}
	for _, tt := range tests {
		s.Run(tt.input, func() {
			require.Equal(s.T(), tt.expected, sanitizeChannelName(tt.input))
		})
	}
}

func (s *ChannelServiceSuite) TestEnsureChannelLookupError() {
	s.store.On("GetChannelByDirPath", s.ctx, "/path", types.PlatformLocal).
		Return(nil, errors.New("db error"))

	channelID, err := s.svc.EnsureChannel(s.ctx, "/path", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up channel by dir path")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestEnsureChannelCreatorError() {
	s.store.On("GetChannelByDirPath", s.ctx, "/path", types.PlatformLocal).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "path-ab12").
		Return("", errors.New("discord error"))

	channelID, err := s.svc.EnsureChannel(s.ctx, "/path", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating channel")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestEnsureChannelUpsertError() {
	s.store.On("GetChannelByDirPath", s.ctx, "/path", types.PlatformLocal).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "path-ab12").
		Return("ch-1", nil)
	s.creator.On("SetChannelTopic", s.ctx, "ch-1", "/path").Return(nil)
	s.creator.On("GetOwnerUserID", s.ctx).Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).
		Return(errors.New("upsert error"))

	channelID, err := s.svc.EnsureChannel(s.ctx, "/path", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "storing channel mapping")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestCreateChannelSuccess() {
	s.creator.On("CreateChannel", s.ctx, "trial").
		Return("new-ch-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-1" &&
			ch.Name == "trial" && ch.DirPath == "/tmp/test-loop/new-ch-1/work" &&
			ch.Platform == types.PlatformLocal && ch.Active
	})).Return(nil)

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "", "", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestCreateChannelWithAuthorInvite() {
	s.creator.On("CreateChannel", s.ctx, "trial").
		Return("new-ch-1", nil)
	s.creator.On("InviteUserToChannel", s.ctx, "new-ch-1", "user-42").
		Return(nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-ch-1" && ch.Name == "trial"
	})).Return(nil)

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "user-42", "", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-ch-1", channelID)
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestCreateChannelInviteError() {
	s.creator.On("CreateChannel", s.ctx, "trial").
		Return("new-ch-1", nil)
	s.creator.On("InviteUserToChannel", s.ctx, "new-ch-1", "user-42").
		Return(errors.New("invite failed"))

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "user-42", "", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "inviting user to channel")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestCreateChannelCreatorError() {
	s.creator.On("CreateChannel", s.ctx, "trial").
		Return("", errors.New("platform error"))

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "", "", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating channel")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestCreateChannelUpsertError() {
	s.creator.On("CreateChannel", s.ctx, "trial").
		Return("ch-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).
		Return(errors.New("upsert error"))

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "", "", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "storing channel mapping")
	require.Empty(s.T(), channelID)
}

func (s *ChannelServiceSuite) TestCreateChannelCreatorReturnsEmptyID() {
	s.creator.On("CreateChannel", s.ctx, "trial").
		Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ab12ab12ab12" && ch.Name == "trial" && ch.Active
	})).Return(nil)

	channelID, err := s.svc.CreateChannel(s.ctx, "trial", "", "", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ab12ab12ab12", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestEnsureChannelCreatorReturnsEmptyID() {
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformLocal).
		Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "loop-ab12").
		Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "ab12ab12ab12" && ch.Name == "loop-ab12" &&
			ch.DirPath == "/home/user/dev/loop" && ch.Active
	})).Return(nil)

	channelID, err := s.svc.EnsureChannel(s.ctx, "/home/user/dev/loop", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ab12ab12ab12", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestCreateChannelLocalPlatformNilCreator() {
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{}, "/tmp/test-loop")

	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.Name == "my-project" && ch.Platform == types.PlatformLocal && ch.Active && ch.ChannelID != ""
	})).Return(nil)

	channelID, err := svc.CreateChannel(s.ctx, "my-project", "", "", "local")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), channelID)
	s.store.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestEnsureChannelLocalPlatformNilCreator() {
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{}, "/tmp/test-loop")
	svc.(*channelService).randSuffix = func() string { return "ab12" }

	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformLocal).
		Return(nil, nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.Name == "loop-ab12" && ch.DirPath == "/home/user/dev/loop" &&
			ch.Platform == types.PlatformLocal && ch.Active && ch.ChannelID != ""
	})).Return(nil)

	channelID, err := svc.EnsureChannel(s.ctx, "/home/user/dev/loop", "local")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), channelID)
	s.store.AssertExpectations(s.T())
}

func (s *ChannelServiceSuite) TestCreateChannelRoutesToSourcePlatform() {
	discordCreator := new(MockCreator)
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{
		types.PlatformLocal:   s.creator,
		types.PlatformDiscord: discordCreator,
	}, "/tmp/test-loop")

	// Source channel is a Discord channel — platform is overridden from source.
	s.store.On("GetChannel", s.ctx, "discord-ch-1").
		Return(&db.Channel{ChannelID: "discord-ch-1", Platform: types.PlatformDiscord, GuildID: "guild-1"}, nil)
	discordCreator.On("CreateChannel", s.ctx, "my-channel").
		Return("new-discord-ch", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-discord-ch" && ch.Name == "my-channel" &&
			ch.Platform == types.PlatformDiscord && ch.GuildID == "guild-1" && ch.Active
	})).Return(nil)

	channelID, err := svc.CreateChannel(s.ctx, "my-channel", "", "discord-ch-1", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-discord-ch", channelID)
	s.store.AssertExpectations(s.T())
	discordCreator.AssertExpectations(s.T())
	s.creator.AssertNotCalled(s.T(), "CreateChannel", mock.Anything, mock.Anything)
}

func (s *ChannelServiceSuite) TestCreateChannelFallsBackWhenSourceNotFound() {
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{
		types.PlatformLocal: s.creator,
	}, "/tmp/test-loop")

	// Source channel not found — falls back to explicit platform.
	s.store.On("GetChannel", s.ctx, "unknown-ch").Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "my-channel").
		Return("new-local-ch", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-local-ch" && ch.Platform == types.PlatformLocal
	})).Return(nil)

	channelID, err := svc.CreateChannel(s.ctx, "my-channel", "", "unknown-ch", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-local-ch", channelID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

// --- EnsureChannelAllPlatforms ---

func (s *ChannelServiceSuite) TestEnsureChannelAllPlatformsCreatesNew() {
	discordCreator := new(MockCreator)
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{
		types.PlatformLocal:   s.creator,
		types.PlatformDiscord: discordCreator,
	}, "/tmp/test-loop")
	svc.(*channelService).randSuffix = func() string { return "ab12" }

	// No existing channels.
	s.store.On("GetChannelsByDirPath", s.ctx, "/home/user/dev/loop").Return([]*db.Channel{}, nil)

	// EnsureChannel calls for each platform.
	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformLocal).Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, "loop-ab12").Return("local-ch", nil)
	s.creator.On("SetChannelTopic", s.ctx, "local-ch", "/home/user/dev/loop").Return(nil)
	s.creator.On("GetOwnerUserID", s.ctx).Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.Platform == types.PlatformLocal && ch.ChannelID == "local-ch"
	})).Return(nil)

	s.store.On("GetChannelByDirPath", s.ctx, "/home/user/dev/loop", types.PlatformDiscord).Return(nil, nil)
	discordCreator.On("CreateChannel", s.ctx, "loop-ab12").Return("discord-ch", nil)
	discordCreator.On("SetChannelTopic", s.ctx, "discord-ch", "/home/user/dev/loop").Return(nil)
	discordCreator.On("GetOwnerUserID", s.ctx).Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.Platform == types.PlatformDiscord && ch.ChannelID == "discord-ch"
	})).Return(nil)

	results, err := svc.EnsureChannelAllPlatforms(s.ctx, "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 2)
	for _, r := range results {
		require.True(s.T(), r.Created)
	}
}

func (s *ChannelServiceSuite) TestEnsureChannelAllPlatformsSkipsExisting() {
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{
		types.PlatformLocal: s.creator,
	}, "/tmp/test-loop")

	// Channel already exists for local platform.
	s.store.On("GetChannelsByDirPath", s.ctx, "/home/user/dev/loop").Return([]*db.Channel{
		{ChannelID: "existing-ch", Platform: types.PlatformLocal},
	}, nil)

	results, err := svc.EnsureChannelAllPlatforms(s.ctx, "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1)
	require.False(s.T(), results[0].Created)
	require.Equal(s.T(), "existing-ch", results[0].ChannelID)
	s.creator.AssertNotCalled(s.T(), "CreateChannel", mock.Anything, mock.Anything)
}

func (s *ChannelServiceSuite) TestEnsureChannelAllPlatformsLookupError() {
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{
		types.PlatformLocal: s.creator,
	}, "/tmp/test-loop")

	s.store.On("GetChannelsByDirPath", s.ctx, "/path").Return(nil, errors.New("db error"))

	results, err := svc.EnsureChannelAllPlatforms(s.ctx, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up channels by dir path")
	require.Nil(s.T(), results)
}

func (s *ChannelServiceSuite) TestEnsureChannelAllPlatformsCreateError() {
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{
		types.PlatformLocal: s.creator,
	}, "/tmp/test-loop")

	// No existing channels.
	s.store.On("GetChannelsByDirPath", s.ctx, "/path").Return([]*db.Channel{}, nil)
	// EnsureChannel fails.
	s.store.On("GetChannelByDirPath", s.ctx, "/path", types.PlatformLocal).Return(nil, nil)
	s.creator.On("CreateChannel", s.ctx, mock.Anything).Return("", errors.New("create failed"))

	results, err := svc.EnsureChannelAllPlatforms(s.ctx, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "ensuring channel for platform local")
	require.Nil(s.T(), results)
}

func (s *ChannelServiceSuite) TestEnsureChannelWithExplicitPlatform() {
	discordCreator := new(MockCreator)
	svc := NewChannelService(s.store, map[types.Platform]ChannelCreator{
		types.PlatformLocal:   s.creator,
		types.PlatformDiscord: discordCreator,
	}, "/tmp/test-loop")
	svc.(*channelService).randSuffix = func() string { return "ab12" }

	s.store.On("GetChannelByDirPath", s.ctx, "/path", types.PlatformDiscord).Return(nil, nil)
	discordCreator.On("CreateChannel", s.ctx, "path-ab12").Return("discord-ch", nil)
	discordCreator.On("SetChannelTopic", s.ctx, "discord-ch", "/path").Return(nil)
	discordCreator.On("GetOwnerUserID", s.ctx).Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.Platform == types.PlatformDiscord && ch.ChannelID == "discord-ch"
	})).Return(nil)

	channelID, err := svc.EnsureChannel(s.ctx, "/path", "discord")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "discord-ch", channelID)
	discordCreator.AssertExpectations(s.T())
	s.creator.AssertNotCalled(s.T(), "CreateChannel", mock.Anything, mock.Anything)
}

func TestRandSuffixDefault(t *testing.T) {
	store := new(testutil.MockStore)
	svc := NewChannelService(store, nil, "/tmp/test-loop")
	got := svc.(*channelService).randSuffix()
	require.Len(t, got, 4) // 2 bytes = 4 hex chars
}
