package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

type MockStoreSuite struct {
	suite.Suite
	store *MockStore
	ctx   context.Context
}

func TestMockStoreSuite(t *testing.T) {
	suite.Run(t, new(MockStoreSuite))
}

func (s *MockStoreSuite) SetupTest() {
	s.store = new(MockStore)
	s.ctx = context.Background()
}

func (s *MockStoreSuite) TestUpsertChannel() {
	ch := &db.Channel{ChannelID: "ch1"}
	s.store.On("UpsertChannel", s.ctx, ch).Return(nil)
	require.NoError(s.T(), s.store.UpsertChannel(s.ctx, ch))
	s.store.AssertExpectations(s.T())
}

func (s *MockStoreSuite) TestGetChannel() {
	ch := &db.Channel{ChannelID: "ch1"}
	s.store.On("GetChannel", s.ctx, "ch1").Return(ch, nil)
	got, err := s.store.GetChannel(s.ctx, "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), ch, got)
	s.store.AssertExpectations(s.T())
}

func (s *MockStoreSuite) TestGetChannelNil() {
	s.store.On("GetChannel", s.ctx, "missing").Return(nil, nil)
	got, err := s.store.GetChannel(s.ctx, "missing")
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestGetChannelByDirPath() {
	ch := &db.Channel{ChannelID: "ch1"}
	s.store.On("GetChannelByDirPath", s.ctx, "/dir", types.Platform("discord")).Return(ch, nil)
	got, err := s.store.GetChannelByDirPath(s.ctx, "/dir", types.Platform("discord"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), ch, got)
}

func (s *MockStoreSuite) TestGetChannelByDirPathNil() {
	s.store.On("GetChannelByDirPath", s.ctx, "/dir", types.Platform("discord")).Return(nil, nil)
	got, err := s.store.GetChannelByDirPath(s.ctx, "/dir", types.Platform("discord"))
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestIsChannelActive() {
	s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
	active, err := s.store.IsChannelActive(s.ctx, "ch1")
	require.NoError(s.T(), err)
	require.True(s.T(), active)
}

func (s *MockStoreSuite) TestUpdateSessionID() {
	s.store.On("UpdateSessionID", s.ctx, "ch1", "sess1").Return(nil)
	require.NoError(s.T(), s.store.UpdateSessionID(s.ctx, "ch1", "sess1"))
}

func (s *MockStoreSuite) TestInsertMessage() {
	msg := &db.Message{ChannelID: "ch1"}
	s.store.On("InsertMessage", s.ctx, msg).Return(nil)
	require.NoError(s.T(), s.store.InsertMessage(s.ctx, msg))
}

func (s *MockStoreSuite) TestGetUnprocessedMessages() {
	msgs := []*db.Message{{ChannelID: "ch1"}}
	s.store.On("GetUnprocessedMessages", s.ctx, "ch1").Return(msgs, nil)
	got, err := s.store.GetUnprocessedMessages(s.ctx, "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), msgs, got)
}

func (s *MockStoreSuite) TestGetUnprocessedMessagesNil() {
	s.store.On("GetUnprocessedMessages", s.ctx, "ch1").Return(nil, nil)
	got, err := s.store.GetUnprocessedMessages(s.ctx, "ch1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestMarkMessagesProcessed() {
	s.store.On("MarkMessagesProcessed", s.ctx, []int64{1, 2}).Return(nil)
	require.NoError(s.T(), s.store.MarkMessagesProcessed(s.ctx, []int64{1, 2}))
}

func (s *MockStoreSuite) TestGetRecentMessages() {
	msgs := []*db.Message{{ChannelID: "ch1"}}
	s.store.On("GetRecentMessages", s.ctx, "ch1", 10).Return(msgs, nil)
	got, err := s.store.GetRecentMessages(s.ctx, "ch1", 10)
	require.NoError(s.T(), err)
	require.Equal(s.T(), msgs, got)
}

func (s *MockStoreSuite) TestGetRecentMessagesNil() {
	s.store.On("GetRecentMessages", s.ctx, "ch1", 10).Return(nil, nil)
	got, err := s.store.GetRecentMessages(s.ctx, "ch1", 10)
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestCreateScheduledTask() {
	task := &db.ScheduledTask{ChannelID: "ch1"}
	s.store.On("CreateScheduledTask", s.ctx, task).Return(int64(42), nil)
	id, err := s.store.CreateScheduledTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(42), id)
}

func (s *MockStoreSuite) TestGetDueTasks() {
	now := time.Now()
	tasks := []*db.ScheduledTask{{ID: 1}}
	s.store.On("GetDueTasks", s.ctx, now).Return(tasks, nil)
	got, err := s.store.GetDueTasks(s.ctx, now)
	require.NoError(s.T(), err)
	require.Equal(s.T(), tasks, got)
}

func (s *MockStoreSuite) TestGetDueTasksNil() {
	now := time.Now()
	s.store.On("GetDueTasks", s.ctx, now).Return(nil, nil)
	got, err := s.store.GetDueTasks(s.ctx, now)
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestUpdateScheduledTask() {
	task := &db.ScheduledTask{ID: 1}
	s.store.On("UpdateScheduledTask", s.ctx, task).Return(nil)
	require.NoError(s.T(), s.store.UpdateScheduledTask(s.ctx, task))
}

func (s *MockStoreSuite) TestDeleteScheduledTask() {
	s.store.On("DeleteScheduledTask", s.ctx, int64(1)).Return(nil)
	require.NoError(s.T(), s.store.DeleteScheduledTask(s.ctx, int64(1)))
}

func (s *MockStoreSuite) TestListScheduledTasks() {
	tasks := []*db.ScheduledTask{{ID: 1}}
	s.store.On("ListScheduledTasks", s.ctx, "ch1").Return(tasks, nil)
	got, err := s.store.ListScheduledTasks(s.ctx, "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), tasks, got)
}

func (s *MockStoreSuite) TestListScheduledTasksNil() {
	s.store.On("ListScheduledTasks", s.ctx, "ch1").Return(nil, nil)
	got, err := s.store.ListScheduledTasks(s.ctx, "ch1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestUpdateScheduledTaskEnabled() {
	s.store.On("UpdateScheduledTaskEnabled", s.ctx, int64(1), true).Return(nil)
	require.NoError(s.T(), s.store.UpdateScheduledTaskEnabled(s.ctx, int64(1), true))
}

func (s *MockStoreSuite) TestGetScheduledTask() {
	task := &db.ScheduledTask{ID: 1}
	s.store.On("GetScheduledTask", s.ctx, int64(1)).Return(task, nil)
	got, err := s.store.GetScheduledTask(s.ctx, int64(1))
	require.NoError(s.T(), err)
	require.Equal(s.T(), task, got)
}

func (s *MockStoreSuite) TestGetScheduledTaskNil() {
	s.store.On("GetScheduledTask", s.ctx, int64(1)).Return(nil, nil)
	got, err := s.store.GetScheduledTask(s.ctx, int64(1))
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestInsertTaskRunLog() {
	trl := &db.TaskRunLog{TaskID: 1}
	s.store.On("InsertTaskRunLog", s.ctx, trl).Return(int64(10), nil)
	id, err := s.store.InsertTaskRunLog(s.ctx, trl)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(10), id)
}

func (s *MockStoreSuite) TestUpdateTaskRunLog() {
	trl := &db.TaskRunLog{ID: 10}
	s.store.On("UpdateTaskRunLog", s.ctx, trl).Return(nil)
	require.NoError(s.T(), s.store.UpdateTaskRunLog(s.ctx, trl))
}

func (s *MockStoreSuite) TestDeleteChannel() {
	s.store.On("DeleteChannel", s.ctx, "ch1").Return(nil)
	require.NoError(s.T(), s.store.DeleteChannel(s.ctx, "ch1"))
}

func (s *MockStoreSuite) TestDeleteChannelsByParentID() {
	s.store.On("DeleteChannelsByParentID", s.ctx, "parent1").Return(nil)
	require.NoError(s.T(), s.store.DeleteChannelsByParentID(s.ctx, "parent1"))
}

func (s *MockStoreSuite) TestClose() {
	s.store.On("Close").Return(nil)
	require.NoError(s.T(), s.store.Close())
}

func (s *MockStoreSuite) TestGetScheduledTaskByTemplateName() {
	task := &db.ScheduledTask{ID: 1, TemplateName: "daily"}
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily").Return(task, nil)
	got, err := s.store.GetScheduledTaskByTemplateName(s.ctx, "ch1", "daily")
	require.NoError(s.T(), err)
	require.Equal(s.T(), task, got)
}

func (s *MockStoreSuite) TestGetScheduledTaskByTemplateNameNil() {
	s.store.On("GetScheduledTaskByTemplateName", s.ctx, "ch1", "daily").Return(nil, nil)
	got, err := s.store.GetScheduledTaskByTemplateName(s.ctx, "ch1", "daily")
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestListChannels() {
	channels := []*db.Channel{{ChannelID: "ch1"}}
	s.store.On("ListChannels", s.ctx).Return(channels, nil)
	got, err := s.store.ListChannels(s.ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), channels, got)
}

func (s *MockStoreSuite) TestListChannelsNil() {
	s.store.On("ListChannels", s.ctx).Return(nil, nil)
	got, err := s.store.ListChannels(s.ctx)
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockStoreSuite) TestUpsertMemoryFile() {
	file := &db.MemoryFile{FilePath: "test.md"}
	s.store.On("UpsertMemoryFile", s.ctx, file).Return(nil)
	require.NoError(s.T(), s.store.UpsertMemoryFile(s.ctx, file))
}

func (s *MockStoreSuite) TestGetMemoryFilesByDirPath() {
	files := []*db.MemoryFile{{FilePath: "test.md"}}
	s.store.On("GetMemoryFilesByDirPath", s.ctx, "/dir").Return(files, nil)
	got, err := s.store.GetMemoryFilesByDirPath(s.ctx, "/dir")
	require.NoError(s.T(), err)
	require.Equal(s.T(), files, got)
}

func (s *MockStoreSuite) TestGetMemoryFileHash() {
	s.store.On("GetMemoryFileHash", s.ctx, "test.md", "/dir").Return("abc123", nil)
	hash, err := s.store.GetMemoryFileHash(s.ctx, "test.md", "/dir")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "abc123", hash)
}

func (s *MockStoreSuite) TestDeleteMemoryFile() {
	s.store.On("DeleteMemoryFile", s.ctx, "test.md", "/dir").Return(nil)
	require.NoError(s.T(), s.store.DeleteMemoryFile(s.ctx, "test.md", "/dir"))
}

func (s *MockStoreSuite) TestUpdateChannelPermissions() {
	perms := db.ChannelPermissions{
		Owners: db.ChannelRoleGrant{Users: []string{"u1"}},
	}
	s.store.On("UpdateChannelPermissions", s.ctx, "ch1", perms).Return(nil)
	require.NoError(s.T(), s.store.UpdateChannelPermissions(s.ctx, "ch1", perms))
}
