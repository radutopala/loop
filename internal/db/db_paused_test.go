package db

import (
	"context"
	"errors"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func (s *StoreSuite) TestUpsertPausedChannel() {
	s.mock.ExpectExec(`INSERT INTO paused_channels`).
		WithArgs("ch-1", PausedKindAsk, "plan", `{"questions":[]}`, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.store.UpsertPausedChannel(context.Background(), &PausedChannel{
		ChannelID: "ch-1", Kind: PausedKindAsk, Mode: "plan", Data: `{"questions":[]}`,
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestUpsertPausedChannelError() {
	s.mock.ExpectExec(`INSERT INTO paused_channels`).
		WillReturnError(errors.New("disk full"))

	err := s.store.UpsertPausedChannel(context.Background(), &PausedChannel{ChannelID: "ch-1", Kind: PausedKindPlan})
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestDeletePausedChannel() {
	s.mock.ExpectExec(`DELETE FROM paused_channels WHERE channel_id = \? AND kind = \?`).
		WithArgs("ch-1", PausedKindAsk).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(s.T(), s.store.DeletePausedChannel(context.Background(), "ch-1", PausedKindAsk))
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListPausedChannels() {
	rows := sqlmock.NewRows([]string{"channel_id", "kind", "mode", "data"}).
		AddRow("ch-1", PausedKindAsk, "plan", `{"questions":[]}`).
		AddRow("ch-2", PausedKindPlan, "", `{"plan":"# P"}`)
	s.mock.ExpectQuery(`SELECT channel_id, kind, mode, data FROM paused_channels`).
		WillReturnRows(rows)

	out, err := s.store.ListPausedChannels(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), out, 2)
	require.Equal(s.T(), &PausedChannel{ChannelID: "ch-1", Kind: PausedKindAsk, Mode: "plan", Data: `{"questions":[]}`}, out[0])
	require.Equal(s.T(), &PausedChannel{ChannelID: "ch-2", Kind: PausedKindPlan, Data: `{"plan":"# P"}`}, out[1])
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListPausedChannelsQueryError() {
	s.mock.ExpectQuery(`SELECT channel_id, kind, mode, data FROM paused_channels`).
		WillReturnError(errors.New("db closed"))

	_, err := s.store.ListPausedChannels(context.Background())
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListPausedChannelsScanError() {
	rows := sqlmock.NewRows([]string{"channel_id", "kind", "mode", "data"}).
		AddRow(nil, nil, nil, nil)
	s.mock.ExpectQuery(`SELECT channel_id, kind, mode, data FROM paused_channels`).
		WillReturnRows(rows)

	_, err := s.store.ListPausedChannels(context.Background())
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestListPausedChannelsRowsError() {
	rows := sqlmock.NewRows([]string{"channel_id", "kind", "mode", "data"}).
		AddRow("ch-1", PausedKindAsk, "", "{}").
		RowError(0, errors.New("row torn"))
	s.mock.ExpectQuery(`SELECT channel_id, kind, mode, data FROM paused_channels`).
		WillReturnRows(rows)

	_, err := s.store.ListPausedChannels(context.Background())
	require.Error(s.T(), err)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
