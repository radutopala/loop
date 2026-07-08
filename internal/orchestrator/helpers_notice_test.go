package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/testutil"
)

type StoreSystemNoticeSuite struct {
	suite.Suite
}

func TestStoreSystemNoticeSuite(t *testing.T) {
	suite.Run(t, new(StoreSystemNoticeSuite))
}

func (s *StoreSystemNoticeSuite) TestStoresAndBroadcasts() {
	store := new(testutil.MockStore)
	eb := new(MockEventBroadcaster)
	ctx := context.Background()

	store.On("GetChannel", ctx, "ch-1").Return(&db.Channel{ID: 1, ChannelID: "ch-1"}, nil)
	store.On("InsertMessage", ctx, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.ChannelID == "ch-1" && msg.Content == "oom notice" && msg.IsBot && msg.TriggerMsgID == ""
	})).Return(nil)
	eb.On("BroadcastMessageCreated", "ch-1", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == "oom notice" && d.IsBot
	})).Return()

	StoreSystemNotice(ctx, store, eb, "ch-1", "oom notice")

	store.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *StoreSystemNoticeSuite) TestNilStoreAndBroadcasterDoNotPanic() {
	require.NotPanics(s.T(), func() {
		StoreSystemNotice(context.Background(), nil, nil, "ch-1", "oom notice")
	})
}

func (s *StoreSystemNoticeSuite) TestUnknownChannelSkipsInsertButStillBroadcasts() {
	store := new(testutil.MockStore)
	eb := new(MockEventBroadcaster)
	ctx := context.Background()

	store.On("GetChannel", ctx, "ch-missing").Return(nil, nil)
	eb.On("BroadcastMessageCreated", "ch-missing", mock.Anything).Return()

	StoreSystemNotice(ctx, store, eb, "ch-missing", "oom notice")

	store.AssertExpectations(s.T())
	store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
	eb.AssertExpectations(s.T())
}
