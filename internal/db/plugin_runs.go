package db

import (
	"context"
	"time"
)

// PluginRun is an agent run a plugin asked for, kept from when it's queued
// until it ends, so one a restart interrupts can resume or fail.
type PluginRun struct {
	RunID    string
	PluginID string
	Agent    string
	ReadOnly bool
	Prompt   string
	Model    string
	Effort   string
	// Tools is the JSON of the plugin's tools the agent gets.
	Tools string
	// ThreadID is the plugin's hidden thread the run is queued in,
	// ChannelID the channel it's a thread of.
	ThreadID  string
	ChannelID string
	CreatedAt time.Time
	// Done marks a run that ended well as Loop stopped, before its plugin
	// was told.
	Done bool
	// Queued reports, on a listed run, whether its message still waits to
	// run in the thread.
	Queued bool
}

const pluginRunColumns = `run_id, plugin_id, agent, read_only, prompt, model, effort, tools, thread_id, channel_id, created_at`

// InsertPluginRun stores r.
func (s *SQLiteStore) InsertPluginRun(ctx context.Context, r *PluginRun) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plugin_runs (`+pluginRunColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RunID, r.PluginID, r.Agent, r.ReadOnly, r.Prompt, r.Model, r.Effort, r.Tools, r.ThreadID, r.ChannelID, s.nowFunc(),
	)
	return err
}

// DeletePluginRun removes the run runID, once it has ended.
func (s *SQLiteStore) DeletePluginRun(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugin_runs WHERE run_id = ?`, runID)
	return err
}

// CountPluginRunsSince counts the agent runs plugin pluginID started since
// since, in all its hidden threads: their messages not from the agent (a
// plugin thread takes no other).
func (s *SQLiteStore) CountPluginRunsSince(ctx context.Context, pluginID string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages m JOIN channels c ON c.channel_id = m.channel_id
		 WHERE c.kind = ? AND m.kind = 'message' AND m.is_bot = 0 AND m.created_at >= ?`,
		PluginKind(pluginID), since.UTC(),
	).Scan(&n)
	return n, err
}

// MarkPluginRunDone marks the run runID as ended well.
func (s *SQLiteStore) MarkPluginRunDone(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE plugin_runs SET done = 1 WHERE run_id = ?`, runID)
	return err
}

// ListPluginRuns returns the runs not yet ended, oldest first, each with
// whether its message still waits to run.
func (s *SQLiteStore) ListPluginRuns(ctx context.Context) ([]*PluginRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pluginRunColumns+`, done, EXISTS (
		   SELECT 1 FROM messages m WHERE m.channel_id = plugin_runs.thread_id
		     AND m.msg_id = plugin_runs.run_id AND m.kind = 'message' AND m.is_processed = 0)
		 FROM plugin_runs ORDER BY created_at, run_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PluginRun
	for rows.Next() {
		var r PluginRun
		if err := rows.Scan(&r.RunID, &r.PluginID, &r.Agent, &r.ReadOnly, &r.Prompt, &r.Model, &r.Effort, &r.Tools,
			&r.ThreadID, &r.ChannelID, &r.CreatedAt, &r.Done, &r.Queued); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}
