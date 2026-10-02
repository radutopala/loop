package testutil

import (
	"context"
	"database/sql"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// MockStore implements the db.Store interface for testing.
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

func (m *MockStore) GetChannelsByDirPath(ctx context.Context, dirPath string) ([]*db.Channel, error) {
	args := m.Called(ctx, dirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (m *MockStore) IsChannelActive(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) UpdateSessionID(ctx context.Context, channelID string, sessionID string) error {
	return m.Called(ctx, channelID, sessionID).Error(0)
}

func (m *MockStore) MarkSessionForkPending(ctx context.Context, channelID string, sessionID string) (bool, error) {
	args := m.Called(ctx, channelID, sessionID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) MarkSessionForkPendingAt(ctx context.Context, channelID, sessionID, resumeAt string) (bool, error) {
	args := m.Called(ctx, channelID, sessionID, resumeAt)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) ForkResumeAt(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
}

func (m *MockStore) SessionInUse(ctx context.Context, sessionID, exceptChannelID string) (bool, error) {
	args := m.Called(ctx, sessionID, exceptChannelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) UpdateChannelAgentOverrides(ctx context.Context, channelID, model, effort string) error {
	return m.Called(ctx, channelID, model, effort).Error(0)
}

func (m *MockStore) UpdateChannelLocked(ctx context.Context, channelID string, locked bool) error {
	return m.Called(ctx, channelID, locked).Error(0)
}

func (m *MockStore) UpdateChannelTicketURL(ctx context.Context, channelID, ticketURL string) error {
	args := m.Called(ctx, channelID, ticketURL)
	return args.Error(0)
}

func (m *MockStore) UpdateChannelDescription(ctx context.Context, channelID, description string) error {
	args := m.Called(ctx, channelID, description)
	return args.Error(0)
}

func (m *MockStore) UpdateChannelName(ctx context.Context, channelID, name string) error {
	return m.Called(ctx, channelID, name).Error(0)
}

func (m *MockStore) UpsertPausedChannel(ctx context.Context, p *db.PausedChannel) error {
	return m.Called(ctx, p).Error(0)
}

func (m *MockStore) DeletePausedChannel(ctx context.Context, channelID, kind string) error {
	return m.Called(ctx, channelID, kind).Error(0)
}

func (m *MockStore) ListPausedChannels(ctx context.Context) ([]*db.PausedChannel, error) {
	args := m.Called(ctx)
	if v := args.Get(0); v != nil {
		return v.([]*db.PausedChannel), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockStore) ListUserMessageContents(ctx context.Context, channelID string, limit int) ([]string, error) {
	args := m.Called(ctx, channelID, limit)
	if v := args.Get(0); v != nil {
		return v.([]string), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockStore) InsertMessage(ctx context.Context, msg *db.Message) error {
	return m.Called(ctx, msg).Error(0)
}

func (m *MockStore) MarkMessagesProcessed(ctx context.Context, ids []int64) error {
	return m.Called(ctx, ids).Error(0)
}

func (m *MockStore) DeleteQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error) {
	args := m.Called(ctx, channelID, msgID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) SteerQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error) {
	args := m.Called(ctx, channelID, msgID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) HoldQueuedMessage(ctx context.Context, channelID, msgID string, until int64) (bool, error) {
	args := m.Called(ctx, channelID, msgID, until)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) ReleaseQueuedHold(ctx context.Context, channelID, msgID string) (bool, error) {
	args := m.Called(ctx, channelID, msgID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) UpdateQueuedMessage(ctx context.Context, channelID, msgID, content string) (bool, error) {
	args := m.Called(ctx, channelID, msgID, content)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) ClaimNextPending(ctx context.Context, channelID string) (*db.Message, error) {
	args := m.Called(ctx, channelID)
	if fn, ok := args.Get(0).(func(context.Context, string) *db.Message); ok {
		return fn(ctx, channelID), args.Error(1)
	}
	msg, _ := args.Get(0).(*db.Message)
	return msg, args.Error(1)
}

func (m *MockStore) ChannelsWithDueDelayedMessages(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockStore) ReleaseRunningMessage(ctx context.Context, id int64, processed bool) error {
	return m.Called(ctx, id, processed).Error(0)
}

func (m *MockStore) RunningMessageID(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
}

func (m *MockStore) ResetStaleRunningMessages(ctx context.Context) ([]db.StaleRunningMessage, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]db.StaleRunningMessage), args.Error(1)
}

func (m *MockStore) MaxQueuedPriority(ctx context.Context, channelID string) (int, error) {
	args := m.Called(ctx, channelID)
	return args.Int(0), args.Error(1)
}

func (m *MockStore) ListPendingChannels(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockStore) GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, channelID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) ListQueuedUserMessages(ctx context.Context, channelID string) ([]*db.Message, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) ReorderQueuedMessages(ctx context.Context, channelID string, orderedMsgIDs []string) error {
	return m.Called(ctx, channelID, orderedMsgIDs).Error(0)
}

func (m *MockStore) GetMessagesCursor(ctx context.Context, channelID string, cursor int64, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, channelID, cursor, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) SearchChannelMessages(ctx context.Context, channelID, query string, limit int) ([]int64, error) {
	args := m.Called(ctx, channelID, query, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]int64), args.Error(1)
}

func (m *MockStore) SearchMessages(ctx context.Context, query string, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, query, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) GetMessagesAround(ctx context.Context, channelID string, messageID int64, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, channelID, messageID, limit)
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

func (m *MockStore) ListAllScheduledTasks(ctx context.Context) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) UpdateScheduledTaskEnabled(ctx context.Context, id int64, enabled bool) error {
	return m.Called(ctx, id, enabled).Error(0)
}

func (m *MockStore) UpdateScheduledTaskThreadID(ctx context.Context, id int64, threadID string) error {
	return m.Called(ctx, id, threadID).Error(0)
}

func (m *MockStore) LinkTaskThread(ctx context.Context, ch *db.Channel, taskID int64, threadID string) error {
	return m.Called(ctx, ch, taskID, threadID).Error(0)
}

func (m *MockStore) UpdateScheduledTaskOriginBranch(ctx context.Context, id int64, branch string) error {
	return m.Called(ctx, id, branch).Error(0)
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

func (m *MockStore) ListTaskRunLogs(ctx context.Context, taskID int64, limit int) ([]*db.TaskRunLog, error) {
	args := m.Called(ctx, taskID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.TaskRunLog), args.Error(1)
}

func (m *MockStore) DeleteChannel(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *MockStore) DeleteChannelsByParentID(ctx context.Context, parentID string) error {
	return m.Called(ctx, parentID).Error(0)
}

func (m *MockStore) ListChannelIDsByParentID(ctx context.Context, parentID string) ([]string, error) {
	args := m.Called(ctx, parentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockStore) Close() error {
	return m.Called().Error(0)
}

// WriterDB satisfies the optional `interface{ WriterDB() *sql.DB }` that
// serve() type-asserts on to wire fs migrations and the quality engine.
// Returns whatever the test set via .On("WriterDB"). Tests typically pass
// `(*sql.DB)(nil)` since the mock never needs a real handle.
func (m *MockStore) WriterDB() *sql.DB {
	return m.Called().Get(0).(*sql.DB)
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

func (m *MockStore) ChannelActivity(ctx context.Context) (map[string]time.Time, error) {
	args := m.Called(ctx)
	return args.Get(0).(map[string]time.Time), args.Error(1)
}

func (m *MockStore) UpsertMemoryFile(ctx context.Context, file *db.MemoryFile) error {
	return m.Called(ctx, file).Error(0)
}

func (m *MockStore) GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*db.MemoryFile, error) {
	args := m.Called(ctx, dirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.MemoryFile), args.Error(1)
}

func (m *MockStore) GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error) {
	args := m.Called(ctx, filePath, dirPath)
	return args.String(0), args.Error(1)
}

func (m *MockStore) DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error {
	return m.Called(ctx, filePath, dirPath).Error(0)
}

func (m *MockStore) ListDistinctMemoryFilePaths(ctx context.Context, dirPath string) ([]db.MemoryFileInfo, error) {
	args := m.Called(ctx, dirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]db.MemoryFileInfo), args.Error(1)
}

func (m *MockStore) ClaimScheduledTaskRunning(ctx context.Context, id int64) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) ReleaseScheduledTaskRunning(ctx context.Context, id int64) error {
	return m.Called(ctx, id).Error(0)
}

func (m *MockStore) ResetStaleRunningTasks(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStore) UpdateChannelPermissions(ctx context.Context, channelID string, perms types.Permissions) error {
	return m.Called(ctx, channelID, perms).Error(0)
}

func (m *MockStore) CreateWorkflowRunWithNodes(ctx context.Context, run *db.WorkflowRun, nodeIDs []string) error {
	return m.Called(ctx, run, nodeIDs).Error(0)
}

func (m *MockStore) MarkRunFailedWithStaleNodes(ctx context.Context, runID, errorText, nodeErrorText string, finishedAt time.Time) error {
	return m.Called(ctx, runID, errorText, nodeErrorText, finishedAt).Error(0)
}

func (m *MockStore) GetWorkflowRun(ctx context.Context, id string) (*db.WorkflowRun, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	// Return a shallow copy so concurrent callers don't race on the same pointer.
	orig := args.Get(0).(*db.WorkflowRun)
	cp := *orig
	return &cp, args.Error(1)
}

func (m *MockStore) UpdateWorkflowRun(ctx context.Context, run *db.WorkflowRun) error {
	return m.Called(ctx, run).Error(0)
}

func (m *MockStore) ListWorkflowRuns(ctx context.Context, channelID string, limit, offset int) ([]*db.WorkflowRun, error) {
	args := m.Called(ctx, channelID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.WorkflowRun), args.Error(1)
}

func (m *MockStore) ListWorkflowRunsByStatus(ctx context.Context, statuses []db.WorkflowRunStatus) ([]*db.WorkflowRun, error) {
	args := m.Called(ctx, statuses)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.WorkflowRun), args.Error(1)
}

func (m *MockStore) UpsertNodeRun(ctx context.Context, nr *db.NodeRun) error {
	return m.Called(ctx, nr).Error(0)
}

func (m *MockStore) ListNodeRuns(ctx context.Context, runID string) ([]*db.NodeRun, error) {
	args := m.Called(ctx, runID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.NodeRun), args.Error(1)
}

func (m *MockStore) UpdateNodeHeartbeat(ctx context.Context, runID, nodeID string, iteration int) error {
	return m.Called(ctx, runID, nodeID, iteration).Error(0)
}

func (m *MockStore) DeleteWorkflowRun(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *MockStore) InsertAgentEvent(ctx context.Context, evt *db.Message) error {
	return m.Called(ctx, evt).Error(0)
}

func (m *MockStore) GetTimeline(ctx context.Context, channelID string, cursorPosition, cursorID int64, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, channelID, cursorPosition, cursorID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) UpdateChannelLearnOverride(ctx context.Context, channelID, value string) error {
	return m.Called(ctx, channelID, value).Error(0)
}

func (m *MockStore) GetHiddenThread(ctx context.Context, parentID, kind string) (*db.Channel, error) {
	args := m.Called(ctx, parentID, kind)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *MockStore) ListHiddenThreads(ctx context.Context, parentID string) ([]*db.Channel, error) {
	args := m.Called(ctx, parentID)
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (m *MockStore) InsertHiddenThread(ctx context.Context, ch *db.Channel) error {
	return m.Called(ctx, ch).Error(0)
}

func (m *MockStore) FileLearnProposals(ctx context.Context, channelID string, proposals []*db.LearnProposal, withdraw []db.LearnWithdrawal) ([]*db.LearnProposal, error) {
	args := m.Called(ctx, channelID, proposals, withdraw)
	v, _ := args.Get(0).([]*db.LearnProposal)
	return v, args.Error(1)
}

func (m *MockStore) ListLearnProposals(ctx context.Context, channelID string) ([]*db.LearnProposal, error) {
	args := m.Called(ctx, channelID)
	v, _ := args.Get(0).([]*db.LearnProposal)
	return v, args.Error(1)
}

func (m *MockStore) GetLearnProposal(ctx context.Context, id int64) (*db.LearnProposal, error) {
	args := m.Called(ctx, id)
	v, _ := args.Get(0).(*db.LearnProposal)
	return v, args.Error(1)
}

func (m *MockStore) ClaimLearnProposal(ctx context.Context, id int64) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) SetLearnProposalStatus(ctx context.Context, id int64, status, errText string) error {
	return m.Called(ctx, id, status, errText).Error(0)
}

func (m *MockStore) UpdateChannelExplainOverride(ctx context.Context, channelID, value string) error {
	return m.Called(ctx, channelID, value).Error(0)
}

func (m *MockStore) GetChatMessage(ctx context.Context, channelID, msgID string) (*db.Message, error) {
	args := m.Called(ctx, channelID, msgID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Message), args.Error(1)
}

func (m *MockStore) LastBotMessage(ctx context.Context, channelID, triggerMsgID string) (*db.Message, error) {
	args := m.Called(ctx, channelID, triggerMsgID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Message), args.Error(1)
}

func (m *MockStore) FirstForkableReply(ctx context.Context, channelID, triggerMsgID string) (*db.Message, error) {
	args := m.Called(ctx, channelID, triggerMsgID)
	msg, _ := args.Get(0).(*db.Message)
	return msg, args.Error(1)
}

func (m *MockStore) SetPromptTranscriptRef(ctx context.Context, channelID, msgID, sessionID, uuid string) error {
	return m.Called(ctx, channelID, msgID, sessionID, uuid).Error(0)
}

func (m *MockStore) QueueExplanation(ctx context.Context, e *db.Explanation) (*db.Explanation, bool, error) {
	args := m.Called(ctx, e)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*db.Explanation), args.Bool(1), args.Error(2)
}

func (m *MockStore) GetExplanation(ctx context.Context, channelID, messageID string) (*db.Explanation, error) {
	args := m.Called(ctx, channelID, messageID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Explanation), args.Error(1)
}

func (m *MockStore) GetExplanationByTrigger(ctx context.Context, explainChannelID, triggerMsgID string) (*db.Explanation, error) {
	args := m.Called(ctx, explainChannelID, triggerMsgID)
	e, _ := args.Get(0).(*db.Explanation)
	return e, args.Error(1)
}

func (m *MockStore) UpdateExplanation(ctx context.Context, id int64, status, content, errText string) error {
	return m.Called(ctx, id, status, content, errText).Error(0)
}

func (m *MockStore) ListExplanations(ctx context.Context, channelID string) ([]*db.Explanation, error) {
	args := m.Called(ctx, channelID)
	return args.Get(0).([]*db.Explanation), args.Error(1)
}

func (m *MockStore) FailInterruptedExplanations(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStore) InsertLearnPass(ctx context.Context, p *db.LearnPass) (*db.LearnPass, error) {
	args := m.Called(ctx, p)
	v, _ := args.Get(0).(*db.LearnPass)
	return v, args.Error(1)
}

func (m *MockStore) GetLearnPassByTrigger(ctx context.Context, learnChannelID, triggerMsgID string) (*db.LearnPass, error) {
	args := m.Called(ctx, learnChannelID, triggerMsgID)
	v, _ := args.Get(0).(*db.LearnPass)
	return v, args.Error(1)
}

func (m *MockStore) UpdateLearnPass(ctx context.Context, id int64, status, errText string) error {
	return m.Called(ctx, id, status, errText).Error(0)
}

func (m *MockStore) ListLearnPasses(ctx context.Context, channelID string) ([]*db.LearnPass, error) {
	args := m.Called(ctx, channelID)
	v, _ := args.Get(0).([]*db.LearnPass)
	return v, args.Error(1)
}

func (m *MockStore) LatestLearnPass(ctx context.Context, learnChannelID string) (*db.LearnPass, error) {
	args := m.Called(ctx, learnChannelID)
	v, _ := args.Get(0).(*db.LearnPass)
	return v, args.Error(1)
}

func (m *MockStore) ActiveLearnPass(ctx context.Context, channelID, messageID string) (*db.LearnPass, error) {
	args := m.Called(ctx, channelID, messageID)
	v, _ := args.Get(0).(*db.LearnPass)
	return v, args.Error(1)
}

func (m *MockStore) LearnPassRunning(ctx context.Context, learnChannelID string) (bool, error) {
	args := m.Called(ctx, learnChannelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) FailInterruptedLearnPasses(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
