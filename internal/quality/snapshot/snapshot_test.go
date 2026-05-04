package snapshot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SnapshotSuite struct {
	suite.Suite
	db    *sql.DB
	mock  sqlmock.Sqlmock
	store *SQLStore
	now   time.Time
}

func TestSnapshotSuite(t *testing.T) {
	suite.Run(t, new(SnapshotSuite))
}

func (s *SnapshotSuite) SetupTest() {
	db, mock, err := sqlmock.New()
	require.NoError(s.T(), err)
	s.db = db
	s.mock = mock
	s.store = NewSQLStore(db)
	s.now = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
}

func (s *SnapshotSuite) TearDownTest() {
	s.db.Close()
}

// --- Save ---

func (s *SnapshotSuite) TestSaveUpsertsRow() {
	sig := metrics.Signal{
		Value:   8500,
		GeoMean: 0.85,
		Metrics: []metrics.Result{{Name: "modularity", Score: 0.9}},
		Tiles:   []metrics.FileTile{{Path: "a/x.go", LOC: 10, Deficit: 0.5, MetricDeficits: map[string]float64{"modularity": 0.5}, TopReason: "modularity"}},
	}
	s.mock.ExpectExec("INSERT INTO quality_snapshots").
		WithArgs("ch1", "main", s.now, 8500, 0.85, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(s.T(), s.store.Save(context.Background(), "ch1", "main", sig, s.now))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *SnapshotSuite) TestSaveConvertsTimestampToUTC() {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(s.T(), err)
	local := time.Date(2026, 5, 1, 8, 0, 0, 0, loc)
	expectedUTC := local.UTC()

	s.mock.ExpectExec("INSERT INTO quality_snapshots").
		WithArgs("ch1", "main", expectedUTC, 0, 0.0, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(s.T(), s.store.Save(context.Background(), "ch1", "main", metrics.Signal{}, local))
}

func (s *SnapshotSuite) TestSaveMarshalErrorPropagates() {
	// A function value isn't JSON-encodable; surfaces a marshal error.
	sig := metrics.Signal{Metrics: []metrics.Result{{Detail: func() {}}}}
	err := s.store.Save(context.Background(), "ch1", "main", sig, s.now)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "marshal metrics breakdown")
}

func (s *SnapshotSuite) TestSaveDBErrorPropagates() {
	s.mock.ExpectExec("INSERT INTO quality_snapshots").
		WithArgs("ch1", "main", s.now, 0, 0.0, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("disk full"))

	err := s.store.Save(context.Background(), "ch1", "main", metrics.Signal{}, s.now)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "upsert quality snapshot")
}

func (s *SnapshotSuite) TestSaveTilesMarshalErrorPropagates() {
	// json.Marshal rejects NaN values; a per-metric deficit that ended
	// up NaN (e.g. from a divide-by-zero slip) propagates as an error
	// rather than silently writing a malformed JSON blob.
	sig := metrics.Signal{
		Tiles: []metrics.FileTile{{
			Path:           "a/x.go",
			MetricDeficits: map[string]float64{"modularity": math.NaN()},
		}},
	}
	err := s.store.Save(context.Background(), "ch1", "main", sig, s.now)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "marshal tile data")
}

// --- Get ---

func (s *SnapshotSuite) TestGetHit() {
	rows := sqlmock.NewRows([]string{"channel_id", "branch_name", "scanned_at", "signal_value", "previous_signal_value", "geo_mean", "metric_breakdown_json", "tile_data_json"}).
		AddRow("ch1", "main", s.now, 8500, 8200, 0.85, `[{"Name":"modularity","Score":0.9}]`, `[{"path":"a/x.go","loc":10,"deficit":0.5,"metric_deficits":{"modularity":0.5},"top_reason":"modularity"}]`)
	s.mock.ExpectQuery("SELECT channel_id, branch_name, scanned_at, signal_value, previous_signal_value, geo_mean, metric_breakdown_json, tile_data_json").
		WithArgs("ch1", "main").
		WillReturnRows(rows)

	snap, err := s.store.Get(context.Background(), "ch1", "main")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ch1", snap.ChannelID)
	require.Equal(s.T(), "main", snap.Branch)
	require.Equal(s.T(), 8500, snap.Value)
	require.Equal(s.T(), 8200, snap.PreviousValue)
	require.InDelta(s.T(), 0.85, snap.GeoMean, 1e-9)
	require.JSONEq(s.T(), `[{"Name":"modularity","Score":0.9}]`, string(snap.MetricBreakdown))
	require.Contains(s.T(), string(snap.TileData), `"a/x.go"`)
}

func (s *SnapshotSuite) TestGetMissReturnsErrNotFound() {
	s.mock.ExpectQuery("SELECT channel_id, branch_name, scanned_at, signal_value, previous_signal_value, geo_mean, metric_breakdown_json, tile_data_json").
		WithArgs("ch1", "feature/x").
		WillReturnError(sql.ErrNoRows)

	snap, err := s.store.Get(context.Background(), "ch1", "feature/x")
	require.Nil(s.T(), snap)
	require.True(s.T(), errors.Is(err, ErrNotFound))
}

func (s *SnapshotSuite) TestGetUnexpectedErrorPropagates() {
	s.mock.ExpectQuery("SELECT channel_id, branch_name, scanned_at, signal_value, previous_signal_value, geo_mean, metric_breakdown_json, tile_data_json").
		WithArgs("ch1", "main").
		WillReturnError(fmt.Errorf("boom"))

	_, err := s.store.Get(context.Background(), "ch1", "main")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "scan quality snapshot row")
}

func (s *SnapshotSuite) TestGetFirstScanHasSentinelPreviousValue() {
	// First scan: previous_signal_value sits at the column default (-1).
	// Consumers use this to decide whether to render a delta chip.
	rows := sqlmock.NewRows([]string{"channel_id", "branch_name", "scanned_at", "signal_value", "previous_signal_value", "geo_mean", "metric_breakdown_json", "tile_data_json"}).
		AddRow("ch1", "main", s.now, 8500, NoPreviousValue, 0.85, `[]`, `[]`)
	s.mock.ExpectQuery("SELECT channel_id, branch_name, scanned_at, signal_value, previous_signal_value, geo_mean, metric_breakdown_json, tile_data_json").
		WithArgs("ch1", "main").
		WillReturnRows(rows)

	snap, err := s.store.Get(context.Background(), "ch1", "main")
	require.NoError(s.T(), err)
	require.Equal(s.T(), NoPreviousValue, snap.PreviousValue)
}

// --- GetLatest ---

func (s *SnapshotSuite) TestGetLatest() {
	rows := sqlmock.NewRows([]string{"channel_id", "branch_name", "scanned_at", "signal_value", "previous_signal_value", "geo_mean", "metric_breakdown_json", "tile_data_json"}).
		AddRow("ch1", "feature/x", s.now, 7200, 7100, 0.72, `[]`, `[]`)
	s.mock.ExpectQuery("SELECT channel_id, branch_name, scanned_at, signal_value, previous_signal_value, geo_mean, metric_breakdown_json, tile_data_json").
		WithArgs("ch1").
		WillReturnRows(rows)

	snap, err := s.store.GetLatest(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "feature/x", snap.Branch)
	require.Equal(s.T(), 7200, snap.Value)
	require.Equal(s.T(), 7100, snap.PreviousValue)
}

func (s *SnapshotSuite) TestGetLatestMissReturnsErrNotFound() {
	s.mock.ExpectQuery("SELECT channel_id, branch_name, scanned_at, signal_value, previous_signal_value, geo_mean, metric_breakdown_json, tile_data_json").
		WithArgs("ch1").
		WillReturnError(sql.ErrNoRows)

	_, err := s.store.GetLatest(context.Background(), "ch1")
	require.True(s.T(), errors.Is(err, ErrNotFound))
}

// --- DeleteForChannel ---

func (s *SnapshotSuite) TestDeleteForChannel() {
	s.mock.ExpectExec("DELETE FROM quality_snapshots WHERE channel_id = ?").
		WithArgs("ch1").
		WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(s.T(), s.store.DeleteForChannel(context.Background(), "ch1"))
}

func (s *SnapshotSuite) TestDeleteForChannelErrorPropagates() {
	s.mock.ExpectExec("DELETE FROM quality_snapshots WHERE channel_id = ?").
		WithArgs("ch1").
		WillReturnError(fmt.Errorf("locked"))

	err := s.store.DeleteForChannel(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "delete quality snapshots for channel")
}
