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
		storeAgentEvent(context.Background(), nil, 9, "ch1", &db.Message{Kind: db.MessageKindThinking}, nil)
	})
}

func TestStoreAgentEventZeroChatIDSkips(t *testing.T) {
	store := &testutil.MockStore{}
	// No expectations: chatID==0 must short-circuit before any store call.
	storeAgentEvent(context.Background(), store, 0, "ch1", &db.Message{Kind: db.MessageKindThinking}, nil)
	store.AssertExpectations(t)
}

func TestStoreAgentEventInsertError(t *testing.T) {
	store := &testutil.MockStore{}
	store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(errors.New("insert failed"))

	var msg string
	logFn := func(s string, _ ...any) { msg = s }

	storeAgentEvent(context.Background(), store, 9, "ch1", &db.Message{Kind: db.MessageKindToolUse}, logFn)
	require.Equal(t, "inserting agent event", msg)
	store.AssertExpectations(t)
}
