package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

type MockThreadCreator struct {
	mock.Mock
}

func (m *MockThreadCreator) CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error) {
	args := m.Called(ctx, channelID, name, mentionUserID, message)
	return args.String(0), args.Error(1)
}

func (m *MockThreadCreator) DeleteThread(ctx context.Context, threadID string) error {
	return m.Called(ctx, threadID).Error(0)
}

type ThreadServiceSuite struct {
	suite.Suite
	store     *testutil.MockStore
	creator   *MockThreadCreator
	svc       ThreadEnsurer
	threadSvc *threadService
	ctx       context.Context
}

func TestThreadServiceSuite(t *testing.T) {
	suite.Run(t, new(ThreadServiceSuite))
}

func (s *ThreadServiceSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.creator = new(MockThreadCreator)
	s.ctx = context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewThreadService(s.store, s.creator, logger)
	s.threadSvc = svc.(*threadService)
	s.svc = svc
}

func (s *ThreadServiceSuite) TestCreateThreadSuccess() {
	parentPerms := types.Permissions{
		Owners:  types.RoleGrant{Users: []string{"owner1"}},
		Members: types.RoleGrant{Users: []string{"member1"}},
	}
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1", DirPath: "/work", Platform: types.PlatformDiscord, SessionID: "sess-1", Permissions: parentPerms}, nil)
	s.creator.On("CreateThread", s.ctx, "ch-1", "my-thread", "user-42", "Do the task").
		Return("thread-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-1" && ch.GuildID == "guild-1" &&
			ch.Name == "my-thread" && ch.ParentID == "ch-1" &&
			ch.DirPath == "/work" && ch.Platform == types.PlatformDiscord && ch.SessionID == "sess-1" &&
			len(ch.Permissions.Owners.Users) == 1 && ch.Permissions.Owners.Users[0] == "owner1" &&
			len(ch.Permissions.Members.Users) == 1 && ch.Permissions.Members.Users[0] == "member1" &&
			ch.Active
	})).Return(nil)

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread", "user-42", "Do the task")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ThreadServiceSuite) TestCreateThreadSuccessWithMessage() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1", DirPath: "/work", SessionID: "sess-1"}, nil)
	s.creator.On("CreateThread", s.ctx, "ch-1", "my-thread", "", "Do the task").
		Return("thread-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-1" && ch.GuildID == "guild-1" &&
			ch.Name == "my-thread" && ch.ParentID == "ch-1"
	})).Return(nil)

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread", "", "Do the task")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-1", threadID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ThreadServiceSuite) TestCreateThreadParentLookupError() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(nil, errors.New("db error"))

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread", "", "Do the task")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up parent channel")
	require.Empty(s.T(), threadID)
}

func (s *ThreadServiceSuite) TestCreateThreadParentNotFound() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(nil, nil)

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread", "", "Do the task")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parent channel ch-1 not found")
	require.Empty(s.T(), threadID)
}

func (s *ThreadServiceSuite) TestCreateThreadDiscordError() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1"}, nil)
	s.creator.On("CreateThread", s.ctx, "ch-1", "my-thread", "", "Do the task").
		Return("", errors.New("discord error"))

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread", "", "Do the task")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating thread")
	require.Empty(s.T(), threadID)
}

func (s *ThreadServiceSuite) TestCreateThreadUpsertError() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1"}, nil)
	s.creator.On("CreateThread", s.ctx, "ch-1", "my-thread", "", "Do the task").
		Return("thread-1", nil)
	s.store.On("UpsertChannel", s.ctx, mock.Anything).
		Return(errors.New("upsert error"))

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread", "", "Do the task")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "storing thread mapping")
	require.Empty(s.T(), threadID)
}

func (s *ThreadServiceSuite) TestCreateThreadFromThreadResolvesParent() {
	// First GetChannel returns a thread (has ParentID).
	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-1", GuildID: "guild-1"}, nil)
	// Second GetChannel resolves to the real parent.
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1", DirPath: "/work", Platform: types.PlatformDiscord, SessionID: "sess-1"}, nil)
	s.creator.On("CreateThread", s.ctx, "ch-1", "new-thread", "user-1", "Do the task").
		Return("thread-2", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-2" && ch.ParentID == "ch-1" && ch.GuildID == "guild-1"
	})).Return(nil)

	threadID, err := s.svc.CreateThread(s.ctx, "thread-1", "new-thread", "user-1", "Do the task")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "thread-2", threadID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ThreadServiceSuite) TestCreateThreadFromThreadResolveError() {
	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-1"}, nil)
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(nil, errors.New("db error"))

	threadID, err := s.svc.CreateThread(s.ctx, "thread-1", "new-thread", "", "Do the task")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up resolved parent channel")
	require.Empty(s.T(), threadID)
}

func (s *ThreadServiceSuite) TestCreateThreadFromThreadResolvedParentNotFound() {
	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-1"}, nil)
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(nil, nil)

	threadID, err := s.svc.CreateThread(s.ctx, "thread-1", "new-thread", "", "Do the task")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolved parent channel ch-1 not found")
	require.Empty(s.T(), threadID)
}

// --- DeleteThread tests ---

func (s *ThreadServiceSuite) TestDeleteThreadSuccess() {
	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-1"}, nil)
	s.creator.On("DeleteThread", s.ctx, "thread-1").Return(nil)
	s.store.On("DeleteChannel", s.ctx, "thread-1").Return(nil)

	err := s.svc.DeleteThread(s.ctx, "thread-1")
	require.NoError(s.T(), err)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ThreadServiceSuite) TestDeleteThreadLookupError() {
	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(nil, errors.New("db error"))

	err := s.svc.DeleteThread(s.ctx, "thread-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up thread")
}

func (s *ThreadServiceSuite) TestDeleteThreadNotFound() {
	s.store.On("GetChannel", s.ctx, "thread-1").Return(nil, nil)

	err := s.svc.DeleteThread(s.ctx, "thread-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "thread thread-1 not found")
}

func (s *ThreadServiceSuite) TestDeleteThreadNotAThread() {
	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", ParentID: ""}, nil)

	err := s.svc.DeleteThread(s.ctx, "ch-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "is not a thread")
}

func (s *ThreadServiceSuite) TestDeleteThreadDiscordError() {
	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-1"}, nil)
	s.creator.On("DeleteThread", s.ctx, "thread-1").
		Return(errors.New("discord error"))

	err := s.svc.DeleteThread(s.ctx, "thread-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "deleting thread")
}

func (s *ThreadServiceSuite) TestDeleteThreadDBError() {
	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-1"}, nil)
	s.creator.On("DeleteThread", s.ctx, "thread-1").Return(nil)
	s.store.On("DeleteChannel", s.ctx, "thread-1").
		Return(errors.New("db error"))

	err := s.svc.DeleteThread(s.ctx, "thread-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "deleting thread from db")
}

func (s *ThreadServiceSuite) TestCreateThreadCreatorReturnsEmptyID() {
	s.threadSvc.generateThreadID = func() string { return "fallback-id" }

	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "guild-1", DirPath: "/work", Platform: types.PlatformLocal}, nil)
	// Creator returns empty string (like LocalBot).
	s.creator.On("CreateThread", s.ctx, "ch-1", "my-thread", "desktop", "").
		Return("", nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "fallback-id" && ch.ParentID == "ch-1" && ch.Name == "my-thread"
	})).Return(nil)

	threadID, err := s.svc.CreateThread(s.ctx, "ch-1", "my-thread", "desktop", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "fallback-id", threadID)
	s.store.AssertExpectations(s.T())
	s.creator.AssertExpectations(s.T())
}

func (s *ThreadServiceSuite) TestCreateThreadLocalPlatformNilCreator() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewThreadService(s.store, nil, logger)
	svc.(*threadService).generateThreadID = func() string { return "local-thread-abc" }

	s.store.On("GetChannel", s.ctx, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", GuildID: "", DirPath: "/work", Platform: types.PlatformLocal}, nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "local-thread-abc" && ch.ParentID == "ch-1" && ch.Name == "my-thread"
	})).Return(nil)

	threadID, err := svc.CreateThread(s.ctx, "ch-1", "my-thread", "", "Do the task")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "local-thread-abc", threadID)
	s.store.AssertExpectations(s.T())
}

func (s *ThreadServiceSuite) TestDeleteThreadLocalPlatformNilCreator() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewThreadService(s.store, nil, logger)

	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-1"}, nil)
	s.store.On("DeleteChannel", s.ctx, "thread-1").Return(nil)

	err := svc.DeleteThread(s.ctx, "thread-1")
	require.NoError(s.T(), err)
	s.store.AssertExpectations(s.T())
}

func (s *ThreadServiceSuite) TestDeleteThreadMCPConfigErrorLogsWarning() {
	s.threadSvc.removeMCPConfig = func(string, string) error { return errors.New("rm error") }

	s.store.On("GetChannel", s.ctx, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-1", DirPath: "/work"}, nil)
	s.creator.On("DeleteThread", s.ctx, "thread-1").Return(nil)
	s.store.On("DeleteChannel", s.ctx, "thread-1").Return(nil)

	err := s.svc.DeleteThread(s.ctx, "thread-1")
	require.NoError(s.T(), err)
	s.store.AssertExpectations(s.T())
}

func TestGenerateThreadIDDefault(t *testing.T) {
	store := new(testutil.MockStore)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewThreadService(store, nil, logger)
	got := svc.(*threadService).generateThreadID()
	require.Len(t, got, 12) // 6 bytes = 12 hex chars
}
