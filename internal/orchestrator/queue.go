package orchestrator

import "sync"

// ChannelQueue provides per-channel concurrency control, ensuring only one
// agent container runs per channel at a time.
type ChannelQueue struct {
	mu       sync.Mutex
	channels map[string]chan struct{}
}

// NewChannelQueue creates a new ChannelQueue.
func NewChannelQueue() *ChannelQueue {
	return &ChannelQueue{
		channels: make(map[string]chan struct{}),
	}
}

// Acquire blocks until the channel slot is available, then acquires it.
func (q *ChannelQueue) Acquire(channelID string) {
	q.mu.Lock()
	ch, ok := q.channels[channelID]
	if !ok {
		ch = make(chan struct{}, 1)
		q.channels[channelID] = ch
	}
	q.mu.Unlock()

	ch <- struct{}{}
}

// Release releases the channel slot so the next request can proceed.
func (q *ChannelQueue) Release(channelID string) {
	q.mu.Lock()
	ch, ok := q.channels[channelID]
	q.mu.Unlock()

	if ok {
		<-ch
	}
}
