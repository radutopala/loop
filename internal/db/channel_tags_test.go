package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/types"
)

const (
	qChannelRowID  = `SELECT id FROM channels WHERE channel_id = \?`
	qInsertTag     = `INSERT INTO tags \(name\) VALUES \(\?\) ON CONFLICT\(name\) DO NOTHING`
	qTagChannel    = `INSERT OR IGNORE INTO channel_tags \(channel_id, tag_id\) SELECT \?, id FROM tags WHERE name = \?`
	qUntagChannel  = `DELETE FROM channel_tags WHERE channel_id = \? AND tag_id IN \(SELECT id FROM tags WHERE name = \?\)`
	qReadTags      = `SELECT json_group_array\(t.name\) FROM channel_tags ct JOIN tags t ON t.id = ct.tag_id WHERE ct.channel_id = \?`
	qPruneTags     = `DELETE FROM tags WHERE id NOT IN \(SELECT tag_id FROM channel_tags\)`
	qTouchChannel  = `UPDATE channels SET updated_at = \? WHERE id = \?`
	qListTagCounts = `SELECT t.name, COUNT\(\*\) FROM tags t JOIN channel_tags ct`
)

func (s *StoreSuite) TestUpdateChannelTags() {
	ok := sqlmock.NewResult(0, 1)
	tooMany := make([]string, types.MaxTags+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf(`"t%02d"`, i)
	}

	tests := []struct {
		name   string
		add    []string
		remove []string
		setup  func()
		want   []string
		err    error
		errMsg string
	}{
		{
			name: "adds and removes, a tag in both removed",
			add:  []string{"web", "x"}, remove: []string{"x", "old"},
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				s.mock.ExpectExec(qInsertTag).WithArgs("web").WillReturnResult(ok)
				s.mock.ExpectExec(qTagChannel).WithArgs(int64(7), "web").WillReturnResult(ok)
				s.mock.ExpectExec(qUntagChannel).WithArgs(int64(7), "x").WillReturnResult(ok)
				s.mock.ExpectExec(qUntagChannel).WithArgs(int64(7), "old").WillReturnResult(ok)
				s.mock.ExpectQuery(qReadTags).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"t"}).AddRow(`["web","api"]`))
				s.mock.ExpectExec(qPruneTags).WillReturnResult(ok)
				s.mock.ExpectExec(qTouchChannel).WithArgs(sqlmock.AnyArg(), int64(7)).WillReturnResult(ok)
				s.mock.ExpectCommit()
			},
			want: []string{"api", "web"},
		},
		{
			name: "no channel",
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnError(sql.ErrNoRows)
				s.mock.ExpectRollback()
			},
			err: sql.ErrNoRows,
		},
		{
			name: "insert tag fails", add: []string{"web"},
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				s.mock.ExpectExec(qInsertTag).WillReturnError(sql.ErrConnDone)
				s.mock.ExpectRollback()
			},
			err: sql.ErrConnDone, errMsg: "adding tag web",
		},
		{
			name: "tag channel fails", add: []string{"web"},
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				s.mock.ExpectExec(qInsertTag).WillReturnResult(ok)
				s.mock.ExpectExec(qTagChannel).WillReturnError(sql.ErrConnDone)
				s.mock.ExpectRollback()
			},
			err: sql.ErrConnDone, errMsg: "tagging web",
		},
		{
			name: "untag fails", remove: []string{"web"},
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				s.mock.ExpectExec(qUntagChannel).WillReturnError(sql.ErrConnDone)
				s.mock.ExpectRollback()
			},
			err: sql.ErrConnDone, errMsg: "untagging web",
		},
		{
			name: "read fails",
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				s.mock.ExpectQuery(qReadTags).WillReturnError(sql.ErrConnDone)
				s.mock.ExpectRollback()
			},
			err: sql.ErrConnDone, errMsg: "reading tags",
		},
		{
			name: "too many",
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				s.mock.ExpectQuery(qReadTags).WillReturnRows(sqlmock.NewRows([]string{"t"}).AddRow("[" + strings.Join(tooMany, ",") + "]"))
				s.mock.ExpectRollback()
			},
			err: types.ErrTooManyTags,
		},
		{
			name: "prune fails",
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				s.mock.ExpectQuery(qReadTags).WillReturnRows(sqlmock.NewRows([]string{"t"}).AddRow(`[]`))
				s.mock.ExpectExec(qPruneTags).WillReturnError(sql.ErrConnDone)
				s.mock.ExpectRollback()
			},
			err: sql.ErrConnDone, errMsg: "pruning tags",
		},
		{
			name: "touch fails",
			setup: func() {
				s.mock.ExpectBegin()
				s.mock.ExpectQuery(qChannelRowID).WithArgs("ch1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
				s.mock.ExpectQuery(qReadTags).WillReturnRows(sqlmock.NewRows([]string{"t"}).AddRow(`[]`))
				s.mock.ExpectExec(qPruneTags).WillReturnResult(ok)
				s.mock.ExpectExec(qTouchChannel).WillReturnError(sql.ErrConnDone)
				s.mock.ExpectRollback()
			},
			err: sql.ErrConnDone, errMsg: "touching channel",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			tt.setup()
			got, err := s.store.UpdateChannelTags(context.Background(), "ch1", tt.add, tt.remove)
			if tt.err != nil {
				require.ErrorIs(s.T(), err, tt.err)
				if tt.errMsg != "" {
					require.ErrorContains(s.T(), err, tt.errMsg)
				}
				require.Nil(s.T(), got)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.want, got)
			}
			require.NoError(s.T(), s.mock.ExpectationsWereMet())
		})
	}
}

func (s *StoreSuite) TestListChannelTags() {
	s.Run("lists", func() {
		s.mock.ExpectQuery(qListTagCounts).WillReturnRows(sqlmock.NewRows([]string{"name", "count"}).AddRow("api", 2).AddRow("web", 1))
		got, err := s.store.ListChannelTags(context.Background())
		require.NoError(s.T(), err)
		require.Equal(s.T(), []TagCount{{Tag: "api", Count: 2}, {Tag: "web", Count: 1}}, got)
	})
	s.Run("empty", func() {
		s.mock.ExpectQuery(qListTagCounts).WillReturnRows(sqlmock.NewRows([]string{"name", "count"}))
		got, err := s.store.ListChannelTags(context.Background())
		require.NoError(s.T(), err)
		require.Equal(s.T(), []TagCount{}, got)
	})
	s.Run("query fails", func() {
		s.mock.ExpectQuery(qListTagCounts).WillReturnError(sql.ErrConnDone)
		_, err := s.store.ListChannelTags(context.Background())
		require.ErrorIs(s.T(), err, sql.ErrConnDone)
	})
	s.Run("scan fails", func() {
		s.mock.ExpectQuery(qListTagCounts).WillReturnRows(sqlmock.NewRows([]string{"name", "count"}).AddRow("api", "x"))
		_, err := s.store.ListChannelTags(context.Background())
		require.Error(s.T(), err)
	})
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

// Tags are shared rows: two channels carrying one tag count it twice, a
// re-sync upsert keeps them, deleting a channel drops its links, and a tag
// nobody carries any more is pruned.
func (s *IntegrationSuite) TestChannelTagsRoundTrip() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()

	for _, id := range []string{"c1", "c2"} {
		require.NoError(s.T(), store.UpsertChannel(ctx, &Channel{ChannelID: id, Name: id, DirPath: "/tmp/" + id, Active: true}))
	}
	got, err := store.UpdateChannelTags(ctx, "c1", []string{"web", "api"}, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"api", "web"}, got)
	_, err = store.UpdateChannelTags(ctx, "c2", []string{"web"}, nil)
	require.NoError(s.T(), err)

	// The re-sync upsert keeps the channel's row id, so its tags stay.
	require.NoError(s.T(), store.UpsertChannel(ctx, &Channel{ChannelID: "c1", Name: "renamed", DirPath: "/tmp/c1", Active: true}))
	ch, err := store.GetChannel(ctx, "c1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"api", "web"}, ch.Tags)
	all, err := store.ListChannels(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), all, 2)

	counts, err := store.ListChannelTags(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []TagCount{{Tag: "api", Count: 1}, {Tag: "web", Count: 2}}, counts)

	got, err = store.UpdateChannelTags(ctx, "c1", nil, []string{"api"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"web"}, got)
	require.NoError(s.T(), store.DeleteChannel(ctx, "c2"))
	counts, err = store.ListChannelTags(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []TagCount{{Tag: "web", Count: 1}}, counts)

	_, err = store.UpdateChannelTags(ctx, "gone", []string{"x"}, nil)
	require.True(s.T(), errors.Is(err, sql.ErrNoRows))

	many := make([]string, types.MaxTags+1)
	for i := range many {
		many[i] = fmt.Sprintf("t%02d", i)
	}
	_, err = store.UpdateChannelTags(ctx, "c1", many, nil)
	require.ErrorIs(s.T(), err, types.ErrTooManyTags)
	ch, err = store.GetChannel(ctx, "c1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"web"}, ch.Tags, "a refused edit rolls back")

	ch, err = store.GetChannel(ctx, "c2")
	require.NoError(s.T(), err)
	require.Nil(s.T(), ch)
}
