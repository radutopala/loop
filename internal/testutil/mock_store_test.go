package testutil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

func TestMockStoreGetMessagesCursor(t *testing.T) {
	m := new(MockStore)
	msgs := []*db.Message{{ID: 1}}
	m.On("GetMessagesCursor", context.Background(), "ch1", int64(0), 10).Return(msgs, nil)

	result, err := m.GetMessagesCursor(context.Background(), "ch1", 0, 10)
	require.NoError(t, err)
	require.Equal(t, msgs, result)
	m.AssertExpectations(t)
}

func TestMockStoreGetMessagesCursorNil(t *testing.T) {
	m := new(MockStore)
	m.On("GetMessagesCursor", context.Background(), "ch1", int64(0), 10).Return(nil, nil)

	result, err := m.GetMessagesCursor(context.Background(), "ch1", 0, 10)
	require.NoError(t, err)
	require.Nil(t, result)
	m.AssertExpectations(t)
}
