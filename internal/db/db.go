package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
	DeleteChannel(ctx context.Context, channelID string) error
	DeleteChannelsByParentID(ctx context.Context, parentID string) error
	ListChannelIDsByParentID(ctx context.Context, parentID string) ([]string, error)
	InsertMessage(ctx context.Context, msg *Message) error
	MarkMessagesProcessed(ctx context.Context, ids []int64) error
	DeleteQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error)
	ClaimNextPending(ctx context.Context, channelID string) (*Message, error)
	ReleaseRunningMessage(ctx context.Context, id int64, processed bool) error
	ResetStaleRunningMessages(ctx context.Context) ([]StaleRunningMessage, error)
	MaxQueuedPriority(ctx context.Context, channelID string) (int, error)
	ListPendingChannels(ctx context.Context) ([]string, error)
	GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*Message, error)
	ListQueuedUserMessages(ctx context.Context, channelID string) ([]*Message, error)
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
	UpdateNodeHeartbeat(ctx context.Context, runID, nodeID string) error
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

func (s *SQLiteStore) UpsertChannel(ctx context.Context, ch *Channel) error {
	var permStr string
	if !ch.Permissions.IsEmpty() {
		data, _ := json.Marshal(ch.Permissions) // Permissions is always serializable
		permStr = string(data)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (channel_id, guild_id, name, dir_path, parent_id, platform, session_id, permissions, active, worktree, locked, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
		   guild_id = excluded.guild_id,
		   name = excluded.name,
		   dir_path = CASE WHEN excluded.dir_path != '' THEN excluded.dir_path ELSE channels.dir_path END,
		   parent_id = excluded.parent_id,
		   platform = CASE WHEN excluded.platform != '' THEN excluded.platform ELSE channels.platform END,
		   session_id = CASE WHEN excluded.session_id != '' THEN excluded.session_id ELSE channels.session_id END,
		   permissions = CASE WHEN excluded.permissions != '' THEN excluded.permissions ELSE channels.permissions END,
		   active = excluded.active,
		   worktree = excluded.worktree,
		   updated_at = excluded.updated_at`,
		ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, ch.ParentID, ch.Platform, ch.SessionID, permStr, boolToInt(ch.Active), boolToInt(ch.Worktree), boolToInt(ch.Locked), s.nowFunc(),
	)
	return err
}

func (s *SQLiteStore) GetChannel(ctx context.Context, channelID string) (*Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, worktree, locked, created_at, updated_at FROM channels WHERE channel_id = ?`,
		channelID,
	)
	ch, err := scanChannel(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return ch, err
}

func (s *SQLiteStore) GetChannelByDirPath(ctx context.Context, dirPath string, platform types.Platform) (*Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, worktree, locked, created_at, updated_at
		 FROM channels WHERE dir_path = ? AND platform = ? AND parent_id = ''`,
		dirPath, platform,
	)
	ch, err := scanChannel(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return ch, err
}

func (s *SQLiteStore) GetChannelsByDirPath(ctx context.Context, dirPath string) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, worktree, locked, created_at, updated_at
		 FROM channels WHERE dir_path = ? AND parent_id = ''`,
		dirPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChannels(rows)
}

func (s *SQLiteStore) IsChannelActive(ctx context.Context, channelID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM channels WHERE channel_id = ? AND active = 1`,
		channelID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *SQLiteStore) UpdateSessionID(ctx context.Context, channelID string, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET session_id = ?, updated_at = ? WHERE channel_id = ?`,
		sessionID, s.nowFunc(), channelID,
	)
	return err
}

func (s *SQLiteStore) UpdateChannelPermissions(ctx context.Context, channelID string, perms types.Permissions) error {
	data, _ := json.Marshal(perms) // Permissions is always serializable
	permStr := string(data)
	now := s.nowFunc()
	// Update the channel and propagate to all child threads
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET permissions = ?, updated_at = ? WHERE channel_id = ? OR parent_id = ?`,
		permStr, now, channelID, channelID,
	)
	return err
}

// UpdateChannelLocked flips the locked flag on a single channel/thread row.
// The flag is intentionally not propagated to children: a parent channel's
// lock state is independent from its threads'.
func (s *SQLiteStore) UpdateChannelLocked(ctx context.Context, channelID string, locked bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET locked = ?, updated_at = ? WHERE channel_id = ?`,
		boolToInt(locked), s.nowFunc(), channelID,
	)
	return err
}

func (s *SQLiteStore) DeleteChannel(ctx context.Context, channelID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE channel_id = ?`, channelID); err != nil {
			return fmt.Errorf("deleting messages for channel: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM quality_snapshots WHERE channel_id = ?`, channelID,
		); err != nil {
			return fmt.Errorf("deleting quality snapshots for channel: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE channel_id = ?`, channelID); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) DeleteChannelsByParentID(ctx context.Context, parentID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM messages WHERE channel_id IN (SELECT channel_id FROM channels WHERE parent_id = ?)`, parentID); err != nil {
			return fmt.Errorf("deleting messages for child channels: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM quality_snapshots WHERE channel_id IN (SELECT channel_id FROM channels WHERE parent_id = ?)`, parentID,
		); err != nil {
			return fmt.Errorf("deleting quality snapshots for child channels: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE parent_id = ?`, parentID); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) ListChannelIDsByParentID(ctx context.Context, parentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id FROM channels WHERE parent_id = ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *SQLiteStore) ListChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, worktree, locked, created_at, updated_at
		 FROM channels ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChannels(rows)
}

func (s *SQLiteStore) InsertMessage(ctx context.Context, msg *Message) error {
	// chain_position is assigned atomically as MAX+1 over the channel so the
	// row sorts after every prior chat-or-event row. Single-writer SQLite
	// serialises Exec calls, so this subselect can't race itself.
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (chat_id, channel_id, msg_id, author_id, author_name, content, is_bot, is_processed, is_triggered, priority, mode, trigger_msg_id, created_at, chain_position)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT MAX(chain_position) FROM messages WHERE channel_id = ?), 0) + 1)`,
		msg.ChatID, msg.ChannelID, msg.MsgID, msg.AuthorID, msg.AuthorName, msg.Content,
		boolToInt(msg.IsBot), boolToInt(msg.IsProcessed), boolToInt(msg.IsTriggered),
		msg.Priority, msg.Mode, msg.TriggerMsgID, msg.CreatedAt, msg.ChannelID,
	)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	msg.ID = id
	if err := s.db.QueryRowContext(ctx, `SELECT chain_position FROM messages WHERE id = ?`, id).Scan(&msg.ChainPosition); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) MarkMessagesProcessed(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET is_processed = 1 WHERE id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	return err
}

// ClaimNextPending atomically picks the highest-priority pending row for a channel
// and marks it is_running=1 in a single transaction. Eligibility: is_processed=0,
// is_triggered=1, is_running=0, kind='message'. Order: priority DESC, id ASC.
// Returns nil with no error when the channel has nothing to process.
func (s *SQLiteStore) ClaimNextPending(ctx context.Context, channelID string) (*Message, error) {
	var msg *Message
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx,
			`SELECT `+messageColumns+` FROM messages
			 WHERE channel_id = ? AND is_processed = 0 AND is_triggered = 1
			   AND is_running = 0 AND kind = 'message'
			 ORDER BY priority DESC, id ASC LIMIT 1`,
			channelID,
		)
		m, err := scanMessageRow(row)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET is_running = 1 WHERE id = ?`, m.ID); err != nil {
			return err
		}
		m.IsRunning = true
		msg = m
		return nil
	})
	return msg, err
}

// ReleaseRunningMessage clears the is_running flag on a row. When processed=true
// also marks the row is_processed=1 — the normal completion path. processed=false
// leaves the row eligible for re-claim (used when the agent cannot be invoked,
// e.g. row picked up before channel is registered).
func (s *SQLiteStore) ReleaseRunningMessage(ctx context.Context, id int64, processed bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET is_running = 0, is_processed = CASE WHEN ? = 1 THEN 1 ELSE is_processed END WHERE id = ?`,
		boolToInt(processed), id,
	)
	return err
}

// ResetStaleRunningMessages clears is_running=1 left over from a previous daemon
// process (the agent run cannot survive a restart) and marks those rows
// is_processed=1 so chat history doesn't keep showing them as "processing".
// Returns (channel_id, msg_id) pairs for cleared rows so the caller can
// broadcast per-channel messages.processed events. Safe to call at daemon startup.
func (s *SQLiteStore) ResetStaleRunningMessages(ctx context.Context) ([]StaleRunningMessage, error) {
	var records []StaleRunningMessage
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT channel_id, msg_id FROM messages WHERE is_running = 1`,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var rec StaleRunningMessage
			if err := rows.Scan(&rec.ChannelID, &rec.MsgID); err != nil {
				rows.Close()
				return err
			}
			records = append(records, rec)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if _, err := tx.ExecContext(ctx,
			`UPDATE messages SET is_running = 0, is_processed = 1 WHERE is_running = 1`,
		); err != nil {
			return err
		}
		return nil
	})
	return records, err
}

// MaxQueuedPriority returns the highest priority among eligible-but-not-running
// rows for a channel. Used by the interrupt branch to insert a higher-priority
// row that will be claimed ahead of everything else queued.
func (s *SQLiteStore) MaxQueuedPriority(ctx context.Context, channelID string) (int, error) {
	var prio sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(priority) FROM messages
		 WHERE channel_id = ? AND is_processed = 0 AND is_triggered = 1 AND kind = 'message'`,
		channelID,
	).Scan(&prio)
	if err != nil {
		return 0, err
	}
	if !prio.Valid {
		return 0, nil
	}
	return int(prio.Int64), nil
}

// ListPendingChannels returns the set of channel_ids that have at least one
// eligible (is_triggered=1, is_processed=0, is_running=0) pending message row.
// Used at daemon startup to wake processors for channels with queued work.
func (s *SQLiteStore) ListPendingChannels(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT channel_id FROM messages
		 WHERE is_processed = 0 AND is_triggered = 1 AND is_running = 0 AND kind = 'message'`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteQueuedMessage removes a waiting (not-yet-processed, non-bot) user message
// from the queue. Returns true when a row was deleted, false when no matching row
// exists (already processed, wrong channel, bot message, or never existed).
func (s *SQLiteStore) DeleteQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM messages WHERE channel_id = ? AND msg_id = ? AND is_bot = 0 AND is_processed = 0 AND kind = 'message'`,
		channelID, msgID,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *SQLiteStore) GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' ORDER BY created_at DESC LIMIT ?`,
		channelID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// ListQueuedUserMessages returns every user message on the channel that is
// still unprocessed, ordered by the same (priority DESC, id ASC) rule the
// processor uses to pick the next row. This is the canonical queue: the FE
// should render it directly rather than filtering its paginated subset, which
// can include stale unprocessed orphans from crashes that the in-memory
// processor will never run.
func (s *SQLiteStore) ListQueuedUserMessages(ctx context.Context, channelID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' AND is_bot = 0 AND is_processed = 0 ORDER BY priority DESC, id ASC`,
		channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) GetMessagesCursor(ctx context.Context, channelID string, cursor int64, limit int) ([]*Message, error) {
	var rows *sql.Rows
	var err error
	if cursor > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' AND id < ? ORDER BY id DESC LIMIT ?`,
			channelID, cursor, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' ORDER BY id DESC LIMIT ?`,
			channelID, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) SearchMessages(ctx context.Context, query string, limit int) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE kind = 'message' AND content LIKE ? ORDER BY created_at DESC LIMIT ?`,
		"%"+query+"%", limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) GetMessagesAround(ctx context.Context, channelID string, messageID int64, limit int) ([]*Message, error) {
	half := limit / 2
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM (
		   SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' AND id < ? ORDER BY id DESC LIMIT ?
		 ) UNION ALL
		 SELECT `+messageColumns+` FROM (
		   SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' AND id >= ? ORDER BY id ASC LIMIT ?
		 ) ORDER BY id ASC`,
		channelID, messageID, half,
		channelID, messageID, limit-half,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// InsertAgentEvent inserts a new agent-event row (kind != "message") into the
// messages table. Caller must populate Kind plus the kind-specific payload
// fields (Content for thinking/tool_result, ToolName/Content for tool_use,
// IsError for tool_result, etc.). chain_position is assigned atomically as
// MAX+1 over the channel so the row sorts after every prior chat-or-event row
// in the same channel. MsgID defaults to a synthetic id; AuthorName defaults
// to "agent". The single-writer SQLite connection serialises Exec calls so
// the MAX+1 subselect cannot race itself.
func (s *SQLiteStore) InsertAgentEvent(ctx context.Context, evt *Message) error {
	if evt.MsgID == "" {
		evt.MsgID = fmt.Sprintf("evt-%d-%s", s.nowFunc().UnixNano(), evt.ToolUseID)
	}
	if evt.AuthorName == "" {
		evt.AuthorName = "agent"
	}
	evt.IsBot = true
	evt.IsProcessed = true
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (chat_id, channel_id, msg_id, author_id, author_name, content, is_bot, is_processed, trigger_msg_id, created_at,
		                       kind, chain_position, tool_use_id, tool_name, is_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		         COALESCE((SELECT MAX(chain_position) FROM messages WHERE channel_id = ?), 0) + 1,
		         ?, ?, ?)`,
		evt.ChatID, evt.ChannelID, evt.MsgID, evt.AuthorID, evt.AuthorName, evt.Content,
		boolToInt(evt.IsBot), boolToInt(evt.IsProcessed), evt.TriggerMsgID, evt.CreatedAt,
		string(evt.Kind),
		evt.ChannelID,
		evt.ToolUseID, evt.ToolName, boolToInt(evt.IsError),
	)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	evt.ID = id
	if err := s.db.QueryRowContext(ctx, `SELECT chain_position FROM messages WHERE id = ?`, id).Scan(&evt.ChainPosition); err != nil {
		return err
	}
	return nil
}

// GetTimeline returns a page of timeline rows for a channel — both real messages
// and agent events — ordered by (chain_position DESC, id DESC). Legacy rows
// (chain_position=0) sort by id, matching today's chat-list behaviour.
//
// Cursor semantics: pass cursorPosition=0 + cursorID=0 for the first page; for
// subsequent pages, pass the (chain_position, id) of the last item from the
// previous page so the next page picks up strictly older rows.
func (s *SQLiteStore) GetTimeline(ctx context.Context, channelID string, cursorPosition, cursorID int64, limit int) ([]*Message, error) {
	var rows *sql.Rows
	var err error
	if cursorPosition > 0 || cursorID > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+messageColumns+` FROM messages
			 WHERE channel_id = ?
			   AND (chain_position < ? OR (chain_position = ? AND id < ?))
			 ORDER BY chain_position DESC, id DESC LIMIT ?`,
			channelID, cursorPosition, cursorPosition, cursorID, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+messageColumns+` FROM messages
			 WHERE channel_id = ?
			 ORDER BY chain_position DESC, id DESC LIMIT ?`,
			channelID, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) CreateScheduledTask(ctx context.Context, task *ScheduledTask) (int64, error) {
	now := s.nowFunc()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_tasks (channel_id, guild_id, schedule, type, prompt, enabled, next_run_at, created_at, updated_at, template_name, auto_delete_sec, worktree, origin_branch, update_before_run, workflow_name, workflow_inputs)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ChannelID, task.GuildID, task.Schedule, string(task.Type), task.Prompt, boolToInt(task.Enabled), task.NextRunAt, now, now, task.TemplateName, task.AutoDeleteSec, boolToInt(task.Worktree), task.OriginBranch, boolToInt(task.UpdateBeforeRun), task.WorkflowName, task.WorkflowInputs,
	)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	task.ID = id
	return id, nil
}

func (s *SQLiteStore) GetDueTasks(ctx context.Context, now time.Time) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE enabled = 1 AND running = 0 AND next_run_at <= ?`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledTasks(rows)
}

func (s *SQLiteStore) UpdateScheduledTask(ctx context.Context, task *ScheduledTask) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET schedule = ?, type = ?, prompt = ?, enabled = ?, next_run_at = ?, updated_at = ?, auto_delete_sec = ?, thread_id = ?, worktree = ?, origin_branch = ?, update_before_run = ?, running = ?, workflow_name = ?, workflow_inputs = ? WHERE id = ?`,
		task.Schedule, string(task.Type), task.Prompt, boolToInt(task.Enabled), task.NextRunAt, s.nowFunc(), task.AutoDeleteSec, task.ThreadID, boolToInt(task.Worktree), task.OriginBranch, boolToInt(task.UpdateBeforeRun), boolToInt(task.Running), task.WorkflowName, task.WorkflowInputs, task.ID,
	)
	return err
}

func (s *SQLiteStore) UpdateScheduledTaskThreadID(ctx context.Context, id int64, threadID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET thread_id = ?, updated_at = ? WHERE id = ?`,
		threadID, s.nowFunc(), id,
	)
	return err
}

// LinkTaskThread atomically registers a thread channel and stamps its ID on
// a recurring scheduled task. Used the first time a task creates a thread so
// a crash mid-update can never leave the scheduled task referencing a thread
// channel that doesn't exist (or vice versa: a thread row no task points to,
// causing the next run to spawn another thread and leak the old row).
func (s *SQLiteStore) LinkTaskThread(ctx context.Context, ch *Channel, taskID int64, threadID string) error {
	var permStr string
	if !ch.Permissions.IsEmpty() {
		data, _ := json.Marshal(ch.Permissions)
		permStr = string(data)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO channels (channel_id, guild_id, name, dir_path, parent_id, platform, session_id, permissions, active, worktree, locked, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(channel_id) DO UPDATE SET
			   guild_id = excluded.guild_id,
			   name = excluded.name,
			   dir_path = CASE WHEN excluded.dir_path != '' THEN excluded.dir_path ELSE channels.dir_path END,
			   parent_id = excluded.parent_id,
			   platform = CASE WHEN excluded.platform != '' THEN excluded.platform ELSE channels.platform END,
			   session_id = CASE WHEN excluded.session_id != '' THEN excluded.session_id ELSE channels.session_id END,
			   permissions = CASE WHEN excluded.permissions != '' THEN excluded.permissions ELSE channels.permissions END,
			   active = excluded.active,
			   worktree = excluded.worktree,
			   updated_at = excluded.updated_at`,
			ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, ch.ParentID, ch.Platform, ch.SessionID, permStr, boolToInt(ch.Active), boolToInt(ch.Worktree), boolToInt(ch.Locked), s.nowFunc(),
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE scheduled_tasks SET thread_id = ?, updated_at = ? WHERE id = ?`,
			threadID, s.nowFunc(), taskID,
		)
		return err
	})
}

func (s *SQLiteStore) UpdateScheduledTaskOriginBranch(ctx context.Context, id int64, branch string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET origin_branch = ?, updated_at = ? WHERE id = ?`,
		branch, s.nowFunc(), id,
	)
	return err
}

func (s *SQLiteStore) ClaimScheduledTaskRunning(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET running = 1, updated_at = ? WHERE id = ? AND running = 0`,
		s.nowFunc(), id,
	)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *SQLiteStore) ReleaseScheduledTaskRunning(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET running = 0, updated_at = ? WHERE id = ?`,
		s.nowFunc(), id,
	)
	return err
}

// ResetStaleRunningTasks clears `running=1` on any scheduled_tasks row and
// finalizes any orphaned `task_run_logs` row whose run never completed (e.g.
// because the daemon was killed before its defer-based release could fire).
// Returns the number of tasks whose running flag was cleared. Safe to call
// at daemon startup — by that point no task is genuinely in flight, since
// task execution is owned by this same process.
func (s *SQLiteStore) ResetStaleRunningTasks(ctx context.Context) (int64, error) {
	var reset int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		now := s.nowFunc()
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_run_logs
			    SET status = 'failed',
			        finished_at = ?,
			        error_text = CASE WHEN error_text = '' THEN 'daemon restarted while running' ELSE error_text END
			  WHERE status = 'running' AND finished_at IS NULL`,
			now,
		); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE scheduled_tasks SET running = 0, updated_at = ? WHERE running = 1`,
			now,
		)
		if err != nil {
			return err
		}
		reset, err = result.RowsAffected()
		return err
	})
	return reset, err
}

func (s *SQLiteStore) DeleteScheduledTask(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_run_logs WHERE task_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id = ?`, id); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) ListScheduledTasks(ctx context.Context, channelID string) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE channel_id = ? AND (type != 'once' OR next_run_at > ?) ORDER BY next_run_at ASC`,
		channelID, s.nowFunc(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledTasks(rows)
}

func (s *SQLiteStore) ListAllScheduledTasks(ctx context.Context) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE (type != 'once' OR next_run_at > ?) ORDER BY next_run_at ASC`,
		s.nowFunc(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduledTasks(rows)
}

func (s *SQLiteStore) UpdateScheduledTaskEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolToInt(enabled), s.nowFunc(), id,
	)
	return err
}

func (s *SQLiteStore) GetScheduledTask(ctx context.Context, id int64) (*ScheduledTask, error) {
	task := &ScheduledTask{}
	var enabled, worktree, updateBeforeRun, running int
	var taskType string
	err := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE id = ?`,
		id,
	).Scan(&task.ID, &task.ChannelID, &task.GuildID, &task.Schedule,
		&taskType, &task.Prompt, &enabled, &task.NextRunAt,
		&task.CreatedAt, &task.UpdatedAt, &task.TemplateName, &task.AutoDeleteSec, &task.ThreadID, &worktree,
		&task.OriginBranch, &updateBeforeRun, &running,
		&task.WorkflowName, &task.WorkflowInputs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	task.Type = TaskType(taskType)
	task.Enabled = enabled == 1
	task.Worktree = worktree == 1
	task.UpdateBeforeRun = updateBeforeRun == 1
	task.Running = running == 1
	return task, nil
}

func (s *SQLiteStore) GetScheduledTaskByTemplateName(ctx context.Context, channelID, templateName string) (*ScheduledTask, error) {
	task := &ScheduledTask{}
	var enabled, worktree, updateBeforeRun, running int
	var taskType string
	err := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM scheduled_tasks WHERE channel_id = ? AND template_name = ?`,
		channelID, templateName,
	).Scan(&task.ID, &task.ChannelID, &task.GuildID, &task.Schedule,
		&taskType, &task.Prompt, &enabled, &task.NextRunAt,
		&task.CreatedAt, &task.UpdatedAt, &task.TemplateName, &task.AutoDeleteSec, &task.ThreadID, &worktree,
		&task.OriginBranch, &updateBeforeRun, &running,
		&task.WorkflowName, &task.WorkflowInputs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	task.Type = TaskType(taskType)
	task.Enabled = enabled == 1
	task.Worktree = worktree == 1
	task.UpdateBeforeRun = updateBeforeRun == 1
	task.Running = running == 1
	return task, nil
}

func (s *SQLiteStore) InsertTaskRunLog(ctx context.Context, trl *TaskRunLog) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO task_run_logs (task_id, status, response_text, error_text, started_at)
		 VALUES (?, ?, ?, ?, ?)`,
		trl.TaskID, string(trl.Status), trl.ResponseText, trl.ErrorText, trl.StartedAt,
	)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	trl.ID = id
	return id, nil
}

func (s *SQLiteStore) UpdateTaskRunLog(ctx context.Context, trl *TaskRunLog) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE task_run_logs SET status = ?, response_text = ?, error_text = ?, finished_at = ? WHERE id = ?`,
		string(trl.Status), trl.ResponseText, trl.ErrorText, trl.FinishedAt, trl.ID,
	)
	return err
}

func (s *SQLiteStore) ListTaskRunLogs(ctx context.Context, taskID int64, limit int) ([]*TaskRunLog, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, status, response_text, error_text, started_at, finished_at
		 FROM task_run_logs WHERE task_id = ? ORDER BY started_at DESC LIMIT ?`,
		taskID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []*TaskRunLog
	for rows.Next() {
		trl := &TaskRunLog{}
		if err := rows.Scan(&trl.ID, &trl.TaskID, &trl.Status, &trl.ResponseText, &trl.ErrorText, &trl.StartedAt, &trl.FinishedAt); err != nil {
			return nil, err
		}
		logs = append(logs, trl)
	}
	return logs, rows.Err()
}

func (s *SQLiteStore) UpsertMemoryFile(ctx context.Context, file *MemoryFile) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_files (file_path, chunk_index, content, content_hash, embedding, dimensions, dir_path, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(file_path, chunk_index, dir_path) DO UPDATE SET
		   content = excluded.content,
		   content_hash = excluded.content_hash,
		   embedding = excluded.embedding,
		   dimensions = excluded.dimensions,
		   updated_at = excluded.updated_at`,
		file.FilePath, file.ChunkIndex, file.Content, file.ContentHash, file.Embedding, file.Dimensions, file.DirPath, s.nowFunc(),
	)
	return err
}

func (s *SQLiteStore) GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*MemoryFile, error) { //nolint:dupl
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, file_path, chunk_index, content, content_hash, embedding, dimensions, dir_path, updated_at
		 FROM memory_files WHERE (dir_path = ? OR dir_path = '') AND dimensions > 0`,
		dirPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []*MemoryFile
	for rows.Next() {
		f := &MemoryFile{}
		if err := rows.Scan(&f.ID, &f.FilePath, &f.ChunkIndex, &f.Content, &f.ContentHash, &f.Embedding, &f.Dimensions, &f.DirPath, &f.UpdatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (s *SQLiteStore) GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash FROM memory_files WHERE file_path = ? AND dir_path = ? AND chunk_index = 0`,
		filePath, dirPath,
	).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

func (s *SQLiteStore) DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memory_files WHERE file_path = ? AND dir_path = ?`, filePath, dirPath)
	return err
}

func (s *SQLiteStore) ListDistinctMemoryFilePaths(ctx context.Context, dirPath string) ([]MemoryFileInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT file_path, dir_path FROM memory_files WHERE (dir_path = ? OR dir_path = '') ORDER BY file_path ASC`,
		dirPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []MemoryFileInfo
	for rows.Next() {
		var f MemoryFileInfo
		if err := rows.Scan(&f.FilePath, &f.DirPath); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// CreateWorkflowRunWithNodes inserts the workflow run and seeds initial pending
// node runs in a single transaction so a partial failure can never leave a run
// without its node rows.
func (s *SQLiteStore) CreateWorkflowRunWithNodes(ctx context.Context, run *WorkflowRun, nodeIDs []string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflow_runs (id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, run.WorkflowName, run.ChannelID, run.DirPath, run.WorktreePath, string(run.Status), run.Inputs, run.PausedNodeID, run.ErrorText, run.WorkflowDef, run.StartedAt,
		); err != nil {
			return err
		}
		for _, nodeID := range nodeIDs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO workflow_node_runs (run_id, node_id, status, output, error_text, attempt, started_at, finished_at, last_heartbeat_at)
				 VALUES (?, ?, ?, '', '', 0, NULL, NULL, NULL)`,
				run.ID, nodeID, string(NodeRunStatusPending),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLiteStore) GetWorkflowRun(ctx context.Context, id string) (*WorkflowRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at, finished_at
		 FROM workflow_runs WHERE id = ?`, id,
	)
	run := &WorkflowRun{}
	err := row.Scan(&run.ID, &run.WorkflowName, &run.ChannelID, &run.DirPath, &run.WorktreePath, &run.Status, &run.Inputs, &run.PausedNodeID, &run.ErrorText, &run.WorkflowDef, &run.StartedAt, &run.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return run, err
}

func (s *SQLiteStore) UpdateWorkflowRun(ctx context.Context, run *WorkflowRun) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE workflow_runs SET status = ?, paused_node_id = ?, error_text = ?, finished_at = ? WHERE id = ?`,
		string(run.Status), run.PausedNodeID, run.ErrorText, run.FinishedAt, run.ID,
	)
	return err
}

// MarkRunFailedWithStaleNodes marks a running workflow as failed and bulk-updates
// all of its still-pending/running node rows to failed in a single transaction.
// Used by recovery on startup so a crash mid-update can never leave a failed run
// with zombie pending/running nodes that the next restart would re-execute.
func (s *SQLiteStore) MarkRunFailedWithStaleNodes(ctx context.Context, runID, errorText, nodeErrorText string, finishedAt time.Time) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflow_runs SET status = ?, error_text = ?, finished_at = ? WHERE id = ?`,
			string(WorkflowRunStatusFailed), errorText, finishedAt, runID,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflow_node_runs SET status = ?, error_text = ?, finished_at = ?
			 WHERE run_id = ? AND status IN (?, ?)`,
			string(NodeRunStatusFailed), nodeErrorText, finishedAt, runID,
			string(NodeRunStatusPending), string(NodeRunStatusRunning),
		); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) ListWorkflowRuns(ctx context.Context, channelID string, limit, offset int) ([]*WorkflowRun, error) {
	if offset < 0 {
		offset = 0
	}
	var rows *sql.Rows
	var err error
	if channelID != "" {
		// Include runs whose channel is the requested channel OR a child
		// channel of it. Two sources of "child":
		//   1. channels.parent_id == channelID — real persisted threads.
		//   2. scheduled_tasks.thread_id where task.channel_id == channelID
		//      — ghost threads (e.g. local-platform tasks) where the MCP
		//      server's channelID is the thread_id but no channel row exists.
		// Scheduled tasks that fire inside such a thread store the workflow
		// run under the thread's channel ID; the user expects to see those
		// when viewing the parent channel.
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at, finished_at
			 FROM workflow_runs
			 WHERE channel_id = ?
			    OR channel_id IN (SELECT channel_id FROM channels WHERE parent_id = ?)
			    OR channel_id IN (SELECT thread_id FROM scheduled_tasks WHERE channel_id = ? AND thread_id != '')
			 ORDER BY started_at DESC LIMIT ? OFFSET ?`, channelID, channelID, channelID, limit, offset)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at, finished_at
			 FROM workflow_runs ORDER BY started_at DESC LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*WorkflowRun
	for rows.Next() {
		run := &WorkflowRun{}
		if err := rows.Scan(&run.ID, &run.WorkflowName, &run.ChannelID, &run.DirPath, &run.WorktreePath, &run.Status, &run.Inputs, &run.PausedNodeID, &run.ErrorText, &run.WorkflowDef, &run.StartedAt, &run.FinishedAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) ListWorkflowRunsByStatus(ctx context.Context, statuses []WorkflowRunStatus) ([]*WorkflowRun, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, st := range statuses {
		placeholders[i] = "?"
		args[i] = string(st)
	}
	query := `SELECT id, workflow_name, channel_id, dir_path, worktree_path, status, inputs, paused_node_id, error_text, workflow_def, started_at, finished_at
		 FROM workflow_runs WHERE status IN (` + strings.Join(placeholders, ",") + `) ORDER BY started_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*WorkflowRun
	for rows.Next() {
		run := &WorkflowRun{}
		if err := rows.Scan(&run.ID, &run.WorkflowName, &run.ChannelID, &run.DirPath, &run.WorktreePath, &run.Status, &run.Inputs, &run.PausedNodeID, &run.ErrorText, &run.WorkflowDef, &run.StartedAt, &run.FinishedAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) UpsertNodeRun(ctx context.Context, nr *NodeRun) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO workflow_node_runs (run_id, node_id, status, output, error_text, attempt, started_at, finished_at, last_heartbeat_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, node_id) DO UPDATE SET
		   status = excluded.status,
		   output = excluded.output,
		   error_text = excluded.error_text,
		   attempt = excluded.attempt,
		   started_at = COALESCE(excluded.started_at, workflow_node_runs.started_at),
		   finished_at = excluded.finished_at,
		   last_heartbeat_at = COALESCE(excluded.last_heartbeat_at, workflow_node_runs.last_heartbeat_at)`,
		nr.RunID, nr.NodeID, string(nr.Status), nr.Output, nr.ErrorText, nr.Attempt, nr.StartedAt, nr.FinishedAt, nr.LastHeartbeatAt,
	)
	return err
}

func (s *SQLiteStore) ListNodeRuns(ctx context.Context, runID string) ([]*NodeRun, error) { //nolint:dupl
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, node_id, status, output, error_text, attempt, started_at, finished_at, last_heartbeat_at
		 FROM workflow_node_runs WHERE run_id = ? ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodeRuns []*NodeRun
	for rows.Next() {
		nr := &NodeRun{}
		if err := rows.Scan(&nr.ID, &nr.RunID, &nr.NodeID, &nr.Status, &nr.Output, &nr.ErrorText, &nr.Attempt, &nr.StartedAt, &nr.FinishedAt, &nr.LastHeartbeatAt); err != nil {
			return nil, err
		}
		nodeRuns = append(nodeRuns, nr)
	}
	return nodeRuns, rows.Err()
}

func (s *SQLiteStore) UpdateNodeHeartbeat(ctx context.Context, runID, nodeID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE workflow_node_runs SET last_heartbeat_at = ? WHERE run_id = ? AND node_id = ?`,
		s.nowFunc(), runID, nodeID,
	)
	return err
}

func (s *SQLiteStore) DeleteWorkflowRun(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workflow_node_runs WHERE run_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workflow_runs WHERE id = ?`, id); err != nil {
			return err
		}
		return nil
	})
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
		&ch.ParentID, &ch.Platform, &active, &ch.SessionID, &permJSON, &worktree, &locked, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
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
