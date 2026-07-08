package container

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type OOMWatcherSuite struct {
	suite.Suite
}

func TestOOMWatcherSuite(t *testing.T) {
	suite.Run(t, new(OOMWatcherSuite))
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (s *OOMWatcherSuite) TestHandleEventNotifiesForLabeledContainer() {
	var gotChannel, gotContent string
	notified := make(chan struct{}, 1)
	watcher := NewOOMWatcher(nil, func(_ context.Context, channelID, content string) {
		gotChannel = channelID
		gotContent = content
		notified <- struct{}{}
	}, discardLogger())

	watcher.handleEvent(context.Background(), events.Message{
		Actor: events.Actor{
			ID: "container-123",
			Attributes: map[string]string{
				ChannelLabelKey: "ch-1",
				"name":          "loop-agent-ch-1",
			},
		},
	})

	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		s.T().Fatal("notify was not called")
	}
	require.Equal(s.T(), "ch-1", gotChannel)
	require.Contains(s.T(), gotContent, "loop-agent-ch-1")
	require.Contains(s.T(), gotContent, "memory limit")
	require.Contains(s.T(), gotContent, "claude --continue")
}

func (s *OOMWatcherSuite) TestHandleEventFallsBackToContainerIDWhenNameMissing() {
	var gotContent string
	watcher := NewOOMWatcher(nil, func(_ context.Context, _, content string) {
		gotContent = content
	}, discardLogger())

	watcher.handleEvent(context.Background(), events.Message{
		Actor: events.Actor{
			ID:         "container-123",
			Attributes: map[string]string{ChannelLabelKey: "ch-1"},
		},
	})

	require.Contains(s.T(), gotContent, "container-123")
}

func (s *OOMWatcherSuite) TestHandleEventSkipsWithoutChannelLabel() {
	watcher := NewOOMWatcher(nil, func(_ context.Context, _, _ string) {
		s.T().Fatal("notify should not be called without a channel label")
	}, discardLogger())

	watcher.handleEvent(context.Background(), events.Message{
		Actor: events.Actor{ID: "container-123", Attributes: map[string]string{}},
	})
}

func (s *OOMWatcherSuite) TestHandleEventDebouncesRepeatEvents() {
	calls := 0
	watcher := NewOOMWatcher(nil, func(_ context.Context, _, _ string) {
		calls++
	}, discardLogger())
	fakeNow := time.Now()
	watcher.now = func() time.Time { return fakeNow }

	msg := events.Message{Actor: events.Actor{ID: "container-123", Attributes: map[string]string{ChannelLabelKey: "ch-1"}}}
	watcher.handleEvent(context.Background(), msg)
	watcher.handleEvent(context.Background(), msg)
	require.Equal(s.T(), 1, calls)

	fakeNow = fakeNow.Add(oomDebounceWindow + time.Second)
	watcher.handleEvent(context.Background(), msg)
	require.Equal(s.T(), 2, calls)
}

func (s *OOMWatcherSuite) TestRunOnceProcessesEventsUntilChannelCloses() {
	msgCh := make(chan events.Message, 1)
	errCh := make(chan error)
	msgCh <- events.Message{Actor: events.Actor{ID: "c1", Attributes: map[string]string{ChannelLabelKey: "ch-1"}}}
	close(msgCh)

	notified := make(chan struct{}, 1)
	watcher := NewOOMWatcher(func(_ context.Context) (<-chan events.Message, <-chan error) {
		return msgCh, errCh
	}, func(_ context.Context, _, _ string) {
		notified <- struct{}{}
	}, discardLogger())

	watcher.runOnce(context.Background())

	select {
	case <-notified:
	default:
		s.T().Fatal("expected notify to have been called before msgCh closed")
	}
}

func (s *OOMWatcherSuite) TestRunOnceReturnsOnError() {
	msgCh := make(chan events.Message)
	errCh := make(chan error, 1)
	errCh <- errors.New("stream broke")

	watcher := NewOOMWatcher(func(_ context.Context) (<-chan events.Message, <-chan error) {
		return msgCh, errCh
	}, func(_ context.Context, _, _ string) {}, discardLogger())

	done := make(chan struct{})
	go func() {
		watcher.runOnce(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("runOnce did not return after error")
	}
}

func (s *OOMWatcherSuite) TestRunOnceReturnsWhenContextCanceled() {
	msgCh := make(chan events.Message)
	errCh := make(chan error)
	watcher := NewOOMWatcher(func(_ context.Context) (<-chan events.Message, <-chan error) {
		return msgCh, errCh
	}, func(_ context.Context, _, _ string) {}, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		watcher.runOnce(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("runOnce did not return after context cancel")
	}
}

func (s *OOMWatcherSuite) TestRunOnceIgnoresClosedErrChAndWaitsForMsg() {
	msgCh := make(chan events.Message, 1)
	errCh := make(chan error)
	close(errCh)
	msgCh <- events.Message{Actor: events.Actor{ID: "c1", Attributes: map[string]string{ChannelLabelKey: "ch-1"}}}

	notified := make(chan struct{}, 1)
	watcher := NewOOMWatcher(func(_ context.Context) (<-chan events.Message, <-chan error) {
		return msgCh, errCh
	}, func(_ context.Context, _, _ string) {
		notified <- struct{}{}
	}, discardLogger())

	done := make(chan struct{})
	go func() {
		watcher.runOnce(context.Background())
		close(done)
	}()

	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		s.T().Fatal("expected notify to fire despite closed errCh")
	}
	close(msgCh)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("runOnce did not return after msgCh closed")
	}
}

func (s *OOMWatcherSuite) TestRunStopsWhenContextCanceled() {
	msgCh := make(chan events.Message)
	errCh := make(chan error)
	watcher := NewOOMWatcher(func(_ context.Context) (<-chan events.Message, <-chan error) {
		return msgCh, errCh
	}, func(_ context.Context, _, _ string) {}, discardLogger())
	watcher.sleep = func(time.Duration) {}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Run did not return after context cancel")
	}
}

func (s *OOMWatcherSuite) TestRunResubscribesAfterStreamCloses() {
	callCount := 0
	msgCh := make(chan events.Message)
	errCh := make(chan error)
	close(msgCh)

	ctx, cancel := context.WithCancel(context.Background())
	watcher := NewOOMWatcher(func(_ context.Context) (<-chan events.Message, <-chan error) {
		callCount++
		if callCount >= 2 {
			cancel()
		}
		return msgCh, errCh
	}, func(_ context.Context, _, _ string) {}, discardLogger())

	slept := make(chan struct{}, 1)
	watcher.sleep = func(time.Duration) {
		select {
		case slept <- struct{}{}:
		default:
		}
	}

	done := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Run did not return after resubscribing")
	}
	require.GreaterOrEqual(s.T(), callCount, 2)
}
