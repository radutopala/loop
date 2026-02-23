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
	IsChannelActive(ctx context.Context, channelID string) (bool, error)
	UpdateSessionID(ctx context.Context, channelID string, sessionID string) error
	UpdateChannelPermissions(ctx context.Context, channelID string, perms ChannelPermissions) error
	DeleteChannel(ctx context.Context, channelID string) error
	DeleteChannelsByParentID(ctx context.Context, parentID string) error
	InsertMessage(ctx context.Context, msg *Message) error
	GetUnprocessedMessages(ctx context.Context, channelID string) ([]*Message, error)
	MarkMessagesProcessed(ctx context.Context, ids []int64) error
	GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*Message, error)
	CreateScheduledTask(ctx context.Context, task *ScheduledTask) (int64, error)
	GetDueTasks(ctx context.Context, now time.Time) ([]*ScheduledTask, error)
	UpdateScheduledTask(ctx context.Context, task *ScheduledTask) error
	DeleteScheduledTask(ctx context.Context, id int64) error
	ListScheduledTasks(ctx context.Context, channelID string) ([]*ScheduledTask, error)
	UpdateScheduledTaskEnabled(ctx context.Context, id int64, enabled bool) error
	GetScheduledTask(ctx context.Context, id int64) (*ScheduledTask, error)
	GetScheduledTaskByTemplateName(ctx context.Context, channelID, templateName string) (*ScheduledTask, error)
	ListChannels(ctx context.Context) ([]*Channel, error)
	InsertTaskRunLog(ctx context.Context, log *TaskRunLog) (int64, error)
	UpdateTaskRunLog(ctx context.Context, log *TaskRunLog) error
	UpsertMemoryFile(ctx context.Context, file *MemoryFile) error
	GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*MemoryFile, error)
	GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error)
	DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error
	Close() error
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// sqlOpenFunc is a package-level variable to allow testing sql.Open failures.
var sqlOpenFunc = sql.Open

// NewSQLiteStore opens a SQLite database and returns a new SQLiteStore.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	sqlDB, err := sqlOpenFunc("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := initDB(sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return &SQLiteStore{db: sqlDB}, nil
}

// initDB configures pragmas and runs migrations on an open database connection.
func initDB(sqlDB *sql.DB) error {
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("enabling WAL mode: %w", err)
	}

	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enabling foreign keys: %w", err)
	}

	if err := RunMigrations(context.Background(), sqlDB); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	return nil
}

// NewSQLiteStoreFromDB creates a SQLiteStore from an existing *sql.DB connection.
func NewSQLiteStoreFromDB(sqlDB *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: sqlDB}
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) UpsertChannel(ctx context.Context, ch *Channel) error {
	var permStr string
	if !ch.Permissions.IsEmpty() {
		data, _ := json.Marshal(ch.Permissions) // ChannelPermissions is always serializable
		permStr = string(data)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (channel_id, guild_id, name, dir_path, parent_id, platform, session_id, permissions, active, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
		   guild_id = excluded.guild_id,
		   name = excluded.name,
		   dir_path = excluded.dir_path,
		   parent_id = excluded.parent_id,
		   platform = CASE WHEN excluded.platform != '' THEN excluded.platform ELSE channels.platform END,
		   session_id = CASE WHEN excluded.session_id != '' THEN excluded.session_id ELSE channels.session_id END,
		   permissions = CASE WHEN excluded.permissions != '' THEN excluded.permissions ELSE channels.permissions END,
		   active = excluded.active,
		   updated_at = excluded.updated_at`,
		ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, ch.ParentID, ch.Platform, ch.SessionID, permStr, boolToInt(ch.Active), time.Now().UTC(),
	)
	return err
}

func (s *SQLiteStore) GetChannel(ctx context.Context, channelID string) (*Channel, error) {
	ch := &Channel{}
	var active int
	var permJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, created_at, updated_at FROM channels WHERE channel_id = ?`,
		channelID,
	).Scan(&ch.ID, &ch.ChannelID, &ch.GuildID, &ch.Name, &ch.DirPath, &ch.ParentID, &ch.Platform, &active, &ch.SessionID, &permJSON, &ch.CreatedAt, &ch.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ch.Active = active == 1
	if permJSON != "" {
		_ = json.Unmarshal([]byte(permJSON), &ch.Permissions)
	}
	return ch, nil
}

func (s *SQLiteStore) GetChannelByDirPath(ctx context.Context, dirPath string, platform types.Platform) (*Channel, error) {
	ch := &Channel{}
	var active int
	var permJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, created_at, updated_at FROM channels WHERE dir_path = ? AND platform = ?`,
		dirPath, platform,
	).Scan(&ch.ID, &ch.ChannelID, &ch.GuildID, &ch.Name, &ch.DirPath, &ch.ParentID, &ch.Platform, &active, &ch.SessionID, &permJSON, &ch.CreatedAt, &ch.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ch.Active = active == 1
	if permJSON != "" {
		_ = json.Unmarshal([]byte(permJSON), &ch.Permissions)
	}
	return ch, nil
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
		sessionID, time.Now().UTC(), channelID,
	)
	return err
}

func (s *SQLiteStore) UpdateChannelPermissions(ctx context.Context, channelID string, perms ChannelPermissions) error {
	data, _ := json.Marshal(perms) // ChannelPermissions is always serializable
	permStr := string(data)
	now := time.Now().UTC()
	// Update the channel and propagate to all child threads
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET permissions = ?, updated_at = ? WHERE channel_id = ? OR parent_id = ?`,
		permStr, now, channelID, channelID,
	)
	return err
}

func (s *SQLiteStore) DeleteChannel(ctx context.Context, channelID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE channel_id = ?`, channelID)
	if err != nil {
		return fmt.Errorf("deleting messages for channel: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM channels WHERE channel_id = ?`, channelID)
	return err
}

func (s *SQLiteStore) DeleteChannelsByParentID(ctx context.Context, parentID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM messages WHERE channel_id IN (SELECT channel_id FROM channels WHERE parent_id = ?)`, parentID)
	if err != nil {
		return fmt.Errorf("deleting messages for child channels: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM channels WHERE parent_id = ?`, parentID)
	return err
}

func (s *SQLiteStore) ListChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, created_at, updated_at
		 FROM channels ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []*Channel
	for rows.Next() {
		ch := &Channel{}
		var active int
		var permJSON string
		if err := rows.Scan(&ch.ID, &ch.ChannelID, &ch.GuildID, &ch.Name, &ch.DirPath,
			&ch.ParentID, &ch.Platform, &active, &ch.SessionID, &permJSON, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, err
		}
		ch.Active = active == 1
		if permJSON != "" {
			_ = json.Unmarshal([]byte(permJSON), &ch.Permissions)
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (s *SQLiteStore) InsertMessage(ctx context.Context, msg *Message) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (chat_id, channel_id, msg_id, author_id, author_name, content, is_bot, is_processed, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ChatID, msg.ChannelID, msg.MsgID, msg.AuthorID, msg.AuthorName, msg.Content, boolToInt(msg.IsBot), boolToInt(msg.IsProcessed), msg.CreatedAt,
	)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	msg.ID = id
	return nil
}

func (s *SQLiteStore) GetUnprocessedMessages(ctx context.Context, channelID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, channel_id, msg_id, author_id, author_name, content, is_bot, is_processed, created_at
		 FROM messages WHERE channel_id = ? AND is_processed = 0 ORDER BY created_at ASC`,
		channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) MarkMessagesProcessed(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `UPDATE messages SET is_processed = 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, channel_id, msg_id, author_id, author_name, content, is_bot, is_processed, created_at
		 FROM messages WHERE channel_id = ? ORDER BY created_at DESC LIMIT ?`,
		channelID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) CreateScheduledTask(ctx context.Context, task *ScheduledTask) (int64, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_tasks (channel_id, guild_id, schedule, type, prompt, enabled, next_run_at, created_at, updated_at, template_name, auto_delete_sec)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ChannelID, task.GuildID, task.Schedule, string(task.Type), task.Prompt, boolToInt(task.Enabled), task.NextRunAt, now, now, task.TemplateName, task.AutoDeleteSec,
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
		`SELECT id, channel_id, guild_id, schedule, type, prompt, enabled, next_run_at, created_at, updated_at, template_name, auto_delete_sec
		 FROM scheduled_tasks WHERE enabled = 1 AND next_run_at <= ?`,
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
		`UPDATE scheduled_tasks SET schedule = ?, type = ?, prompt = ?, enabled = ?, next_run_at = ?, updated_at = ?, auto_delete_sec = ? WHERE id = ?`,
		task.Schedule, string(task.Type), task.Prompt, boolToInt(task.Enabled), task.NextRunAt, time.Now().UTC(), task.AutoDeleteSec, task.ID,
	)
	return err
}

func (s *SQLiteStore) DeleteScheduledTask(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM task_run_logs WHERE task_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) ListScheduledTasks(ctx context.Context, channelID string) ([]*ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, guild_id, schedule, type, prompt, enabled, next_run_at, created_at, updated_at, template_name, auto_delete_sec
		 FROM scheduled_tasks WHERE channel_id = ? AND (type != 'once' OR next_run_at > ?) ORDER BY next_run_at ASC`,
		channelID, time.Now().UTC(),
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
		boolToInt(enabled), time.Now().UTC(), id,
	)
	return err
}

func (s *SQLiteStore) GetScheduledTask(ctx context.Context, id int64) (*ScheduledTask, error) {
	task := &ScheduledTask{}
	var enabled int
	var taskType string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, guild_id, schedule, type, prompt, enabled, next_run_at, created_at, updated_at, template_name, auto_delete_sec
		 FROM scheduled_tasks WHERE id = ?`,
		id,
	).Scan(&task.ID, &task.ChannelID, &task.GuildID, &task.Schedule,
		&taskType, &task.Prompt, &enabled, &task.NextRunAt,
		&task.CreatedAt, &task.UpdatedAt, &task.TemplateName, &task.AutoDeleteSec)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	task.Type = TaskType(taskType)
	task.Enabled = enabled == 1
	return task, nil
}

func (s *SQLiteStore) GetScheduledTaskByTemplateName(ctx context.Context, channelID, templateName string) (*ScheduledTask, error) {
	task := &ScheduledTask{}
	var enabled int
	var taskType string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, guild_id, schedule, type, prompt, enabled, next_run_at, created_at, updated_at, template_name, auto_delete_sec
		 FROM scheduled_tasks WHERE channel_id = ? AND template_name = ?`,
		channelID, templateName,
	).Scan(&task.ID, &task.ChannelID, &task.GuildID, &task.Schedule,
		&taskType, &task.Prompt, &enabled, &task.NextRunAt,
		&task.CreatedAt, &task.UpdatedAt, &task.TemplateName, &task.AutoDeleteSec)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	task.Type = TaskType(taskType)
	task.Enabled = enabled == 1
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
		file.FilePath, file.ChunkIndex, file.Content, file.ContentHash, file.Embedding, file.Dimensions, file.DirPath, time.Now().UTC(),
	)
	return err
}

func (s *SQLiteStore) GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*MemoryFile, error) {
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

// helpers

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanMessages(rows *sql.Rows) ([]*Message, error) {
	var msgs []*Message
	for rows.Next() {
		msg := &Message{}
		var isBot, isProcessed int
		if err := rows.Scan(
			&msg.ID, &msg.ChatID, &msg.ChannelID, &msg.MsgID,
			&msg.AuthorID, &msg.AuthorName, &msg.Content,
			&isBot, &isProcessed, &msg.CreatedAt,
		); err != nil {
			return nil, err
		}
		msg.IsBot = isBot == 1
		msg.IsProcessed = isProcessed == 1
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

func scanScheduledTasks(rows *sql.Rows) ([]*ScheduledTask, error) {
	var tasks []*ScheduledTask
	for rows.Next() {
		task := &ScheduledTask{}
		var enabled int
		var taskType string
		if err := rows.Scan(
			&task.ID, &task.ChannelID, &task.GuildID, &task.Schedule,
			&taskType, &task.Prompt, &enabled, &task.NextRunAt,
			&task.CreatedAt, &task.UpdatedAt, &task.TemplateName, &task.AutoDeleteSec,
		); err != nil {
			return nil, err
		}
		task.Type = TaskType(taskType)
		task.Enabled = enabled == 1
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}
