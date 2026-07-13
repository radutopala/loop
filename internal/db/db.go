package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/radutopala/loop/internal/types"
)

// Store defines all database operations.
type Store interface {
	UpsertChannel(ctx context.Context, ch *Channel) error
	GetChannel(ctx context.Context, channelID string) (*Channel, error)
	GetChannelByDirPath(ctx context.Context, dirPath string, platform types.Platform) (*Channel, error)
	GetChannelsByDirPath(ctx context.Context, dirPath string) ([]*Channel, error)
	IsChannelActive(ctx context.Context, channelID string) (bool, error)
	UpdateSessionID(ctx context.Context, channelID string, sessionID string) error
	UpdateChannelPermissions(ctx context.Context, channelID string, perms types.Permissions) error
	UpdateChannelLocked(ctx context.Context, channelID string, locked bool) error
	UpdateChannelName(ctx context.Context, channelID, name string) error
	UpdateChannelDirPath(ctx context.Context, channelID, dirPath string) error
	DeleteChannel(ctx context.Context, channelID string) error
	DeleteChannelsByParentID(ctx context.Context, parentID string) error
	ListChannelIDsByParentID(ctx context.Context, parentID string) ([]string, error)
	UpsertPausedChannel(ctx context.Context, p *PausedChannel) error
	DeletePausedChannel(ctx context.Context, channelID, kind string) error
	ListPausedChannels(ctx context.Context) ([]*PausedChannel, error)
	InsertMessage(ctx context.Context, msg *Message) error
	MarkMessagesProcessed(ctx context.Context, ids []int64) error
	DeleteQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error)
	ClaimNextPending(ctx context.Context, channelID string) (*Message, error)
	ReleaseRunningMessage(ctx context.Context, id int64, processed bool) error
	ResetStaleRunningMessages(ctx context.Context) ([]StaleRunningMessage, error)
	MaxQueuedPriority(ctx context.Context, channelID string) (int, error)
	ListPendingChannels(ctx context.Context) ([]string, error)
	GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*Message, error)
	ListUserMessageContents(ctx context.Context, channelID string, limit int) ([]string, error)
	ListQueuedUserMessages(ctx context.Context, channelID string) ([]*Message, error)
	ReorderQueuedMessages(ctx context.Context, channelID string, orderedMsgIDs []string) error
	GetMessagesCursor(ctx context.Context, channelID string, cursor int64, limit int) ([]*Message, error)
	SearchMessages(ctx context.Context, query string, limit int) ([]*Message, error)
	GetMessagesAround(ctx context.Context, channelID string, messageID int64, limit int) ([]*Message, error)
	InsertAgentEvent(ctx context.Context, evt *Message) error
	GetTimeline(ctx context.Context, channelID string, cursorPosition, cursorID int64, limit int) ([]*Message, error)
	CreateScheduledTask(ctx context.Context, task *ScheduledTask) (int64, error)
	GetDueTasks(ctx context.Context, now time.Time) ([]*ScheduledTask, error)
	UpdateScheduledTask(ctx context.Context, task *ScheduledTask) error
	DeleteScheduledTask(ctx context.Context, id int64) error
	ListScheduledTasks(ctx context.Context, channelID string) ([]*ScheduledTask, error)
	ListAllScheduledTasks(ctx context.Context) ([]*ScheduledTask, error)
	UpdateScheduledTaskEnabled(ctx context.Context, id int64, enabled bool) error
	UpdateScheduledTaskThreadID(ctx context.Context, id int64, threadID string) error
	LinkTaskThread(ctx context.Context, ch *Channel, taskID int64, threadID string) error
	UpdateScheduledTaskOriginBranch(ctx context.Context, id int64, branch string) error
	ClaimScheduledTaskRunning(ctx context.Context, id int64) (bool, error)
	ReleaseScheduledTaskRunning(ctx context.Context, id int64) error
	ResetStaleRunningTasks(ctx context.Context) (int64, error)
	GetScheduledTask(ctx context.Context, id int64) (*ScheduledTask, error)
	GetScheduledTaskByTemplateName(ctx context.Context, channelID, templateName string) (*ScheduledTask, error)
	ListChannels(ctx context.Context) ([]*Channel, error)
	InsertTaskRunLog(ctx context.Context, log *TaskRunLog) (int64, error)
	UpdateTaskRunLog(ctx context.Context, log *TaskRunLog) error
	ListTaskRunLogs(ctx context.Context, taskID int64, limit int) ([]*TaskRunLog, error)
	UpsertMemoryFile(ctx context.Context, file *MemoryFile) error
	GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*MemoryFile, error)
	GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error)
	DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error
	ListDistinctMemoryFilePaths(ctx context.Context, dirPath string) ([]MemoryFileInfo, error)
	CreateWorkflowRunWithNodes(ctx context.Context, run *WorkflowRun, nodeIDs []string) error
	GetWorkflowRun(ctx context.Context, id string) (*WorkflowRun, error)
	UpdateWorkflowRun(ctx context.Context, run *WorkflowRun) error
	MarkRunFailedWithStaleNodes(ctx context.Context, runID, errorText, nodeErrorText string, finishedAt time.Time) error
	ListWorkflowRuns(ctx context.Context, channelID string, limit, offset int) ([]*WorkflowRun, error)
	ListWorkflowRunsByStatus(ctx context.Context, statuses []WorkflowRunStatus) ([]*WorkflowRun, error)
	UpsertNodeRun(ctx context.Context, nr *NodeRun) error
	ListNodeRuns(ctx context.Context, runID string) ([]*NodeRun, error)
	UpdateNodeHeartbeat(ctx context.Context, runID, nodeID string, iteration int) error
	DeleteWorkflowRun(ctx context.Context, id string) error
	Close() error
}

// dbConn abstracts the database methods used by SQLiteStore so that reads and
// writes can be routed to separate connections.
type dbConn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Close() error
}

// splitDB routes reads to a pooled reader and writes to a single-connection
// writer. SQLite WAL mode supports concurrent readers with one writer.
type splitDB struct {
	writer *sql.DB
	reader *sql.DB
}

func (s *splitDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.writer.ExecContext(ctx, query, args...)
}

func (s *splitDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.reader.QueryContext(ctx, query, args...)
}

func (s *splitDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.reader.QueryRowContext(ctx, query, args...)
}

func (s *splitDB) Close() error {
	err1 := s.writer.Close()
	err2 := s.reader.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db      dbConn
	writer  *sql.DB
	nowFunc func() time.Time
}

// NewSQLiteStore opens a SQLite database and returns a new SQLiteStore.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	return newSQLiteStoreWith(sql.Open, dsn)
}

func newSQLiteStoreWith(openFunc func(string, string) (*sql.DB, error), dsn string) (*SQLiteStore, error) {
	writer, err := openFunc("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// Single writer connection: serializes writes and ensures PRAGMAs stick.
	writer.SetMaxOpenConns(1)

	if err := initDB(writer); err != nil {
		writer.Close()
		return nil, err
	}

	reader, err := openFunc("sqlite", dsn)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("opening reader database: %w", err)
	}

	db := &splitDB{writer: writer, reader: reader}
	return &SQLiteStore{db: db, writer: writer, nowFunc: func() time.Time { return time.Now().UTC() }}, nil
}

// WriterDB returns the underlying writer connection. This is used by
// auxiliary migration runners (e.g. internal/fsmigrate) that track applied
// versions in the same SQLite database. Returns nil for stores constructed
// via NewSQLiteStoreFromDB without a *sql.DB writer.
func (s *SQLiteStore) WriterDB() *sql.DB { return s.writer }

// initDB configures pragmas and runs migrations on an open database connection.
func initDB(sqlDB *sql.DB) error {
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("enabling WAL mode: %w", err)
	}

	if _, err := sqlDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("setting busy timeout: %w", err)
	}

	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enabling foreign keys: %w", err)
	}

	// synchronous=NORMAL is the SQLite-recommended setting under WAL: durability
	// still survives process crashes; only OS-level power loss between the WAL
	// flush and disk fsync risks data loss. Cuts write latency materially.
	if _, err := sqlDB.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		return fmt.Errorf("setting synchronous mode: %w", err)
	}

	// cache_size negative = KB; -32768 = 32 MB page cache (default ~2 MB) keeps
	// more of the working set hot for read-heavy timeline / channel listings.
	if _, err := sqlDB.Exec("PRAGMA cache_size=-32768"); err != nil {
		return fmt.Errorf("setting cache size: %w", err)
	}

	if err := RunMigrations(context.Background(), sqlDB); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	return nil
}

// NewSQLiteStoreFromDB creates a SQLiteStore from an existing *sql.DB connection.
func NewSQLiteStoreFromDB(sqlDB *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: sqlDB, writer: sqlDB, nowFunc: func() time.Time { return time.Now().UTC() }}
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// withTx runs fn in a write transaction on the writer connection, committing
// on success and rolling back on error. fn must use the provided tx for all
// statements so they share atomicity with the commit.
func (s *SQLiteStore) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Column lists for SELECT queries.
const (
	messageColumns = `id, chat_id, channel_id, msg_id, author_id, author_name, content, is_bot, is_processed, is_triggered, is_running, priority, mode, created_at, kind, chain_position, tool_use_id, tool_name, is_error, trigger_msg_id`
	taskColumns    = `id, channel_id, guild_id, schedule, type, prompt, enabled, next_run_at, created_at, updated_at, template_name, auto_delete_sec, thread_id, worktree, origin_branch, update_before_run, running, workflow_name, workflow_inputs`
)

// helpers

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanChannelFrom(scanner rowScanner) (*Channel, error) {
	ch := &Channel{}
	var active, worktree, locked int
	var permJSON string
	if err := scanner.Scan(&ch.ID, &ch.ChannelID, &ch.GuildID, &ch.Name, &ch.DirPath,
		&ch.ParentID, &ch.Platform, &active, &ch.SessionID, &permJSON, &worktree, &ch.BaseBranch, &locked, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
		return nil, err
	}
	ch.Active = active == 1
	ch.Worktree = worktree == 1
	ch.Locked = locked == 1
	if permJSON != "" {
		_ = json.Unmarshal([]byte(permJSON), &ch.Permissions)
	}
	return ch, nil
}

func scanChannel(row *sql.Row) (*Channel, error) {
	return scanChannelFrom(row)
}

func scanChannels(rows *sql.Rows) ([]*Channel, error) {
	var channels []*Channel
	for rows.Next() {
		ch, err := scanChannelFrom(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func scanMessages(rows *sql.Rows) ([]*Message, error) {
	var msgs []*Message
	for rows.Next() {
		msg, err := scanMessageRow(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func scanMessageRow(scanner rowScanner) (*Message, error) {
	msg := &Message{}
	var isBot, isProcessed, isTriggered, isRunning, isError int
	var kind string
	if err := scanner.Scan(
		&msg.ID, &msg.ChatID, &msg.ChannelID, &msg.MsgID,
		&msg.AuthorID, &msg.AuthorName, &msg.Content,
		&isBot, &isProcessed, &isTriggered, &isRunning, &msg.Priority, &msg.Mode,
		&msg.CreatedAt,
		&kind, &msg.ChainPosition,
		&msg.ToolUseID, &msg.ToolName, &isError,
		&msg.TriggerMsgID,
	); err != nil {
		return nil, err
	}
	msg.IsBot = isBot == 1
	msg.IsProcessed = isProcessed == 1
	msg.IsTriggered = isTriggered == 1
	msg.IsRunning = isRunning == 1
	msg.IsError = isError == 1
	msg.Kind = MessageKind(kind)
	return msg, nil
}

func scanScheduledTasks(rows *sql.Rows) ([]*ScheduledTask, error) {
	var tasks []*ScheduledTask
	for rows.Next() {
		task := &ScheduledTask{}
		var enabled, worktree, updateBeforeRun, running int
		var taskType string
		if err := rows.Scan(
			&task.ID, &task.ChannelID, &task.GuildID, &task.Schedule,
			&taskType, &task.Prompt, &enabled, &task.NextRunAt,
			&task.CreatedAt, &task.UpdatedAt, &task.TemplateName, &task.AutoDeleteSec, &task.ThreadID, &worktree,
			&task.OriginBranch, &updateBeforeRun, &running,
			&task.WorkflowName, &task.WorkflowInputs,
		); err != nil {
			return nil, err
		}
		task.Type = TaskType(taskType)
		task.Enabled = enabled == 1
		task.Worktree = worktree == 1
		task.UpdateBeforeRun = updateBeforeRun == 1
		task.Running = running == 1
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}
