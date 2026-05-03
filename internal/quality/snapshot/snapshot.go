package snapshot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/radutopala/loop/internal/quality/metrics"
)

// ErrNotFound is returned by Get and GetLatest when no row matches. The
// HTTP handler maps this to a 404 (or empty-state response) and the
// panel uses it as the trigger to render the "Scan now" placeholder.
var ErrNotFound = errors.New("quality snapshot not found")

// Snapshot is the persisted record returned to consumers. MetricBreakdown
// is the raw JSON payload the panel renders without further unmarshalling
// — round-tripping into typed metric Detail values would require a
// per-metric registry the panel doesn't need. TileData carries the
// per-file deficit projection for the treemap.
type Snapshot struct {
	ChannelID       string
	Branch          string
	ScannedAt       time.Time
	Value           int
	GeoMean         float64
	MetricBreakdown json.RawMessage
	TileData        json.RawMessage
}

// Store is the persistence contract. Implementations must be safe for
// concurrent use — multiple writers may race UPSERTs from concurrent
// "Scan now" buttons, the live-rescan agentgate hook, and CLI runs.
type Store interface {
	// Save UPSERTs the row keyed by (channelID, branch). scannedAt is
	// taken from the caller (rather than CURRENT_TIMESTAMP) so tests
	// and offline runs can pin deterministic timestamps.
	Save(ctx context.Context, channelID, branch string, sig metrics.Signal, scannedAt time.Time) error

	// Get returns the snapshot for an exact (channelID, branch) pair
	// or ErrNotFound. Used by the panel when the current branch matches.
	Get(ctx context.Context, channelID, branch string) (*Snapshot, error)

	// GetLatest returns the most recent snapshot for the channel across
	// any branch — backs the "snapshot taken on <old_branch>" banner
	// when the user has just switched branches and hasn't rescanned.
	GetLatest(ctx context.Context, channelID string) (*Snapshot, error)

	// DeleteForChannel removes every snapshot owned by the channel.
	// Called from the channel-deletion path to keep the table from
	// outliving its rows.
	DeleteForChannel(ctx context.Context, channelID string) error
}

// SQLStore is the database-backed Store. The caller owns the *sql.DB —
// matching the rest of the project's persistence layers (memory store,
// scheduler store), which all open the DB in cmd/loop/serve and pass
// the handle in.
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore wraps db in a Store. db must already have the
// quality_snapshots migration applied (handled by db.NewSQLiteStore).
func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db}
}

// Save UPSERTs the row using SQLite's ON CONFLICT clause. The unique
// constraint on (channel_id, branch_name) makes the conflict path
// deterministic — no need for explicit transactions.
func (s *SQLStore) Save(ctx context.Context, channelID, branch string, sig metrics.Signal, scannedAt time.Time) error {
	raw, err := json.Marshal(sig.Metrics)
	if err != nil {
		return fmt.Errorf("marshal metrics breakdown: %w", err)
	}
	tiles := sig.Tiles
	if tiles == nil {
		tiles = []metrics.FileTile{}
	}
	tilesRaw, err := json.Marshal(tiles)
	if err != nil {
		return fmt.Errorf("marshal tile data: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO quality_snapshots
			(channel_id, branch_name, scanned_at, signal_value, geo_mean, metric_breakdown_json, tile_data_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id, branch_name) DO UPDATE SET
			scanned_at = excluded.scanned_at,
			signal_value = excluded.signal_value,
			geo_mean = excluded.geo_mean,
			metric_breakdown_json = excluded.metric_breakdown_json,
			tile_data_json = excluded.tile_data_json
	`, channelID, branch, scannedAt.UTC(), sig.Value, sig.GeoMean, string(raw), string(tilesRaw))
	if err != nil {
		return fmt.Errorf("upsert quality snapshot: %w", err)
	}
	return nil
}

// Get returns the snapshot for an exact branch or ErrNotFound.
func (s *SQLStore) Get(ctx context.Context, channelID, branch string) (*Snapshot, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT channel_id, branch_name, scanned_at, signal_value, geo_mean, metric_breakdown_json, tile_data_json
		  FROM quality_snapshots
		 WHERE channel_id = ? AND branch_name = ?
	`, channelID, branch)
	return scanRow(row)
}

// GetLatest returns the most-recently-scanned row for the channel.
func (s *SQLStore) GetLatest(ctx context.Context, channelID string) (*Snapshot, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT channel_id, branch_name, scanned_at, signal_value, geo_mean, metric_breakdown_json, tile_data_json
		  FROM quality_snapshots
		 WHERE channel_id = ?
		 ORDER BY scanned_at DESC
		 LIMIT 1
	`, channelID)
	return scanRow(row)
}

// DeleteForChannel removes all rows for a channel. No-op if none exist.
func (s *SQLStore) DeleteForChannel(ctx context.Context, channelID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM quality_snapshots WHERE channel_id = ?`, channelID,
	); err != nil {
		return fmt.Errorf("delete quality snapshots for channel: %w", err)
	}
	return nil
}

func scanRow(row *sql.Row) (*Snapshot, error) {
	var snap Snapshot
	var raw, tiles string
	if err := row.Scan(
		&snap.ChannelID,
		&snap.Branch,
		&snap.ScannedAt,
		&snap.Value,
		&snap.GeoMean,
		&raw,
		&tiles,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan quality snapshot row: %w", err)
	}
	snap.MetricBreakdown = json.RawMessage(raw)
	snap.TileData = json.RawMessage(tiles)
	return &snap, nil
}
