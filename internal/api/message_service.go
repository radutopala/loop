package api

import (
	"context"
	"time"

	"github.com/radutopala/loop/internal/db"
)

// MessageSender can send messages to channels or threads.
type MessageSender interface {
	PostMessage(ctx context.Context, channelID, content string) error
}

// ChannelLister can list, look up, and delete channels and their messages from the database.
type ChannelLister interface {
	ListChannels(ctx context.Context) ([]*db.Channel, error)
	ChannelActivity(ctx context.Context) (map[string]time.Time, error)
	GetChannel(ctx context.Context, channelID string) (*db.Channel, error)
	UpsertChannel(ctx context.Context, ch *db.Channel) error
	GetMessagesCursor(ctx context.Context, channelID string, cursor int64, limit int) ([]*db.Message, error)
	ListUserMessageContents(ctx context.Context, channelID string, limit int) ([]string, error)
	ListQueuedUserMessages(ctx context.Context, channelID string) ([]*db.Message, error)
	SearchMessages(ctx context.Context, query string, limit int) ([]*db.Message, error)
	SearchChannelMessages(ctx context.Context, channelID, query string, limit int) ([]int64, error)
	GetMessagesAround(ctx context.Context, channelID string, messageID int64, limit int) ([]*db.Message, error)
	GetTimeline(ctx context.Context, channelID string, cursorPosition, cursorID int64, limit int) ([]*db.Message, error)
	UpdateSessionID(ctx context.Context, channelID string, sessionID string) error
	MarkSessionForkPending(ctx context.Context, channelID string, sessionID string) (bool, error)
	MarkSessionForkPendingAt(ctx context.Context, channelID, sessionID, resumeAt string) (bool, error)
	GetChatMessage(ctx context.Context, channelID, msgID string) (*db.Message, error)
	FirstForkableReply(ctx context.Context, channelID, triggerMsgID string) (*db.Message, error)
	UpdateChannelAgentOverrides(ctx context.Context, channelID, model, effort string) error
	UpdateChannelLocked(ctx context.Context, channelID string, locked bool) error
	UpdateChannelName(ctx context.Context, channelID, name string) error
	UpdateChannelDescription(ctx context.Context, channelID, description string) error
	UpdateChannelTicketURL(ctx context.Context, channelID, ticketURL string) error
	DeleteChannel(ctx context.Context, channelID string) error
	DeleteChannelsByParentID(ctx context.Context, parentID string) error
	ListChannelIDsByParentID(ctx context.Context, parentID string) ([]string, error)
	ListDistinctMemoryFilePaths(ctx context.Context, dirPath string) ([]db.MemoryFileInfo, error)
	InsertMessage(ctx context.Context, msg *db.Message) error
	RunningMessageID(ctx context.Context, channelID string) (string, error)
	DeleteQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error)
	SteerQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error)
	HoldQueuedMessage(ctx context.Context, channelID, msgID string, until int64) (bool, error)
	ReleaseQueuedHold(ctx context.Context, channelID, msgID string) (bool, error)
	UpdateQueuedMessage(ctx context.Context, channelID, msgID, content string) (bool, error)
	ReorderQueuedMessages(ctx context.Context, channelID string, orderedMsgIDs []string) error
	MaxQueuedPriority(ctx context.Context, channelID string) (int, error)
	ListTaskRunLogs(ctx context.Context, taskID int64, limit int) ([]*db.TaskRunLog, error)
	ListAllScheduledTasks(ctx context.Context) ([]*db.ScheduledTask, error)
	UpdateChannelLearnOverride(ctx context.Context, channelID, value string) error
	GetHiddenThread(ctx context.Context, parentID, kind string) (*db.Channel, error)
	ListHiddenThreads(ctx context.Context, parentID string) ([]*db.Channel, error)
	UpdateChannelExplainOverride(ctx context.Context, channelID, value string) error
	ListExplanations(ctx context.Context, channelID string) ([]*db.Explanation, error)
	FileLearnProposals(ctx context.Context, channelID string, proposals []*db.LearnProposal, withdraw []db.LearnWithdrawal) ([]*db.LearnProposal, error)
	ListLearnProposals(ctx context.Context, channelID string) ([]*db.LearnProposal, error)
	GetLearnProposal(ctx context.Context, id int64) (*db.LearnProposal, error)
	ClaimLearnProposal(ctx context.Context, id int64) (bool, error)
	SetLearnProposalStatus(ctx context.Context, id int64, status, errText string) error
	ListLearnPasses(ctx context.Context, channelID string) ([]*db.LearnPass, error)
	LatestLearnPass(ctx context.Context, learnChannelID string) (*db.LearnPass, error)
	LearnPassRunning(ctx context.Context, learnChannelID string) (bool, error)
}
