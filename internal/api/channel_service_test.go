package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MockStore struct {
	mock.Mock
}

func (m *MockStore) UpsertChannel(ctx context.Context, ch *db.Channel) error {
	return m.Called(ctx, ch).Error(0)
}

func (m *MockStore) GetChannel(ctx context.Context, channelID string) (*db.Channel, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *MockStore) GetChannelByDirPath(ctx context.Context, dirPath string, platform types.Platform) (*db.Channel, error) {
	args := m.Called(ctx, dirPath, platform)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *MockStore) IsChannelActive(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) UpdateSessionID(ctx context.Context, channelID string, sessionID string) error {
	return m.Called(ctx, channelID, sessionID).Error(0)
}

func (m *MockStore) InsertMessage(ctx context.Context, msg *db.Message) error {
	return m.Called(ctx, msg).Error(0)
}

func (m *MockStore) GetUnprocessedMessages(ctx context.Context, channelID string) ([]*db.Message, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) MarkMessagesProcessed(ctx context.Context, ids []int64) error {
	return m.Called(ctx, ids).Error(0)
}

func (m *MockStore) GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, channelID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) CreateScheduledTask(ctx context.Context, task *db.ScheduledTask) (int64, error) {
	args := m.Called(ctx, task)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStore) GetDueTasks(ctx context.Context, now time.Time) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) UpdateScheduledTask(ctx context.Context, task *db.ScheduledTask) error {
	return m.Called(ctx, task).Error(0)
}

func (m *MockStore) DeleteScheduledTask(ctx context.Context, id int64) error {
	return m.Called(ctx, id).Error(0)
}

func (m *MockStore) ListScheduledTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) UpdateScheduledTaskEnabled(ctx context.Context, id int64, enabled bool) error {
	return m.Called(ctx, id, enabled).Error(0)
}

func (m *MockStore) GetScheduledTask(ctx context.Context, id int64) (*db.ScheduledTask, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) InsertTaskRunLog(ctx context.Context, trl *db.TaskRunLog) (int64, error) {
	args := m.Called(ctx, trl)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStore) UpdateTaskRunLog(ctx context.Context, trl *db.TaskRunLog) error {
	return m.Called(ctx, trl).Error(0)
}

func (m *MockStore) DeleteChannel(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *MockStore) DeleteChannelsByParentID(ctx context.Context, parentID string) error {
	return m.Called(ctx, parentID).Error(0)
}

func (m *MockStore) Close() error {
	return m.Called().Error(0)
}

func (m *MockStore) GetScheduledTaskByTemplateName(ctx context.Context, channelID, templateName string) (*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID, templateName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) ListChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (m *MockStore) UpsertMemoryFile(ctx context.Context, file *db.MemoryFile) error {
	return m.Called(ctx, file).Error(0)
}

func (m *MockStore) GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*db.MemoryFile, error) {
	args := m.Called(ctx, dirPath)
	return args.Get(0).([]*db.MemoryFile), args.Error(1)
}

func (m *MockStore) GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error) {
	args := m.Called(ctx, filePath, dirPath)
	return args.String(0), args.Error(1)
}

func (m *MockStore) DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error {
	return m.Called(ctx, filePath, dirPath).Error(0)
}

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
	store   *MockStore
	creator *MockCreator
	svc     ChannelEnsurer
	ctx     context.Context
}

func TestChannelServiceSuite(t *testing.T) {
	suite.Run(t, new(ChannelServiceSuite))
}

func (s *ChannelServiceSuite) SetupTest() {
	s.store = new(MockStore)
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
