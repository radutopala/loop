package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

func TestStoreAgentEventNilStore(t *testing.T) {
	require.NotPanics(t, func() {
		storeAgentEvent(context.Background(), nil, "ch1", &db.Message{Kind: db.MessageKindThinking}, nil)
	})
}

func TestStoreAgentEventGetChannelError(t *testing.T) {
	store := &testutil.MockStore{}
	store.On("GetChannel", mock.Anything, "ch1").Return((*db.Channel)(nil), errors.New("boom"))

	var captured []any
	logFn := func(msg string, args ...any) {
		captured = append(captured, msg)
		captured = append(captured, args...)
	}

	storeAgentEvent(context.Background(), store, "ch1", &db.Message{Kind: db.MessageKindThinking}, logFn)
	require.Contains(t, captured, "looking up channel for agent event")
	store.AssertExpectations(t)
}

func TestStoreAgentEventInsertError(t *testing.T) {
	store := &testutil.MockStore{}
	store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ID: 9, ChannelID: "ch1"}, nil)
	store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(errors.New("insert failed"))

	var msg string
	logFn := func(s string, _ ...any) { msg = s }

	storeAgentEvent(context.Background(), store, "ch1", &db.Message{Kind: db.MessageKindToolUse}, logFn)
	require.Equal(t, "inserting agent event", msg)
	store.AssertExpectations(t)
}
