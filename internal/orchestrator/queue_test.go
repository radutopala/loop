package orchestrator

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type QueueSuite struct {
	suite.Suite
	queue *ChannelQueue
}

func TestQueueSuite(t *testing.T) {
	suite.Run(t, new(QueueSuite))
}

func (s *QueueSuite) SetupTest() {
	s.queue = NewChannelQueue()
}

func (s *QueueSuite) TestNewChannelQueue() {
	require.NotNil(s.T(), s.queue)
	require.NotNil(s.T(), s.queue.channels)
	require.Empty(s.T(), s.queue.channels)
}

func (s *QueueSuite) TestAcquireRelease() {
	s.queue.Acquire("ch1")
	// Should be acquired; channel map should have an entry
	s.queue.mu.Lock()
	_, ok := s.queue.channels["ch1"]
	s.queue.mu.Unlock()
	require.True(s.T(), ok)

	s.queue.Release("ch1")
}

func (s *QueueSuite) TestAcquireBlocksSecondCaller() {
	s.queue.Acquire("ch1")

	acquired := make(chan struct{})
	go func() {
		s.queue.Acquire("ch1")
		close(acquired)
	}()

	// Second acquire should block
	select {
	case <-acquired:
		s.T().Fatal("second acquire should block")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}

	s.queue.Release("ch1")

	// Now second acquire should succeed
	select {
	case <-acquired:
		// Expected
	case <-time.After(time.Second):
		s.T().Fatal("second acquire should have succeeded after release")
	}

	s.queue.Release("ch1")
}

func (s *QueueSuite) TestDifferentChannelsIndependent() {
	s.queue.Acquire("ch1")

	acquired := make(chan struct{})
	go func() {
		s.queue.Acquire("ch2")
		close(acquired)
	}()

	select {
	case <-acquired:
		// Expected: different channels are independent
	case <-time.After(time.Second):
		s.T().Fatal("different channels should not block each other")
	}

	s.queue.Release("ch1")
	s.queue.Release("ch2")
}

func (s *QueueSuite) TestReleaseNonExistentChannel() {
	// Should not panic
	s.queue.Release("nonexistent")
}

func (s *QueueSuite) TestConcurrentAccess() {
	const numGoroutines = 10
	var counter atomic.Int64
	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {
			s.queue.Acquire("ch1")
			defer s.queue.Release("ch1")
			// Increment and verify mutual exclusion
			val := counter.Add(1)
			require.Equal(s.T(), int64(1), val)
			time.Sleep(time.Millisecond)
			counter.Add(-1)
		})
	}

	wg.Wait()
}
