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

// ListPluginRuns returns the runs not yet ended, oldest first, each with
// whether its message still waits to run.
func (s *SQLiteStore) ListPluginRuns(ctx context.Context) ([]*PluginRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pluginRunColumns+`, EXISTS (
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
			&r.ThreadID, &r.ChannelID, &r.CreatedAt, &r.Queued); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}
