package terminal

import (
	"log/slog"
	"sync"
)

// outputPump is a per-client buffered pipeline between the session's readLoop
// and its consumer (typically the WS write goroutine). It absorbs bursts of
// PTY output without dropping bytes: readLoop calls push (non-blocking,
// appends to an internal slice), and a drain goroutine forwards items to a
// public buffered channel that the consumer reads. This decouples the speed
// of the docker exec stream from the speed of the consumer.
//
// A soft byte cap (maxBytes) bounds memory. When the queued backlog exceeds
// the cap, the oldest entries are evicted with a "slow consumer" warning,
// matching the prior contract for genuinely stuck clients while tolerating
// transient bursts that previously truncated tails of command output.
type outputPump struct {
	out      chan []byte
	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once

	mu    sync.Mutex
	queue [][]byte
	bytes int

	logger    *slog.Logger
	sessionID string
	maxBytes  int
}

// newOutputPump constructs a pump and starts its drain goroutine. Callers
// must invoke close exactly once when the client detaches or the session
// stops.
func newOutputPump(logger *slog.Logger, sessionID string, maxBytes int) *outputPump {
	p := &outputPump{
		out:       make(chan []byte, clientChannelBuffer),
		wake:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
		logger:    logger,
		sessionID: sessionID,
		maxBytes:  maxBytes,
	}
	go p.drain()
	return p
}

// push enqueues data for the consumer. It is non-blocking: even when the
// consumer is stalled, push returns immediately. If the queued backlog
// exceeds maxBytes, the oldest entries are evicted to keep memory bounded
// and a warning is logged. push always keeps the most recent entry so
// callers that send a final burst observe at least the tail.
func (p *outputPump) push(data []byte) {
	p.mu.Lock()
	p.queue = append(p.queue, data)
	p.bytes += len(data)
	for p.bytes > p.maxBytes && len(p.queue) > 1 {
		evicted := p.queue[0]
		p.queue = p.queue[1:]
		p.bytes -= len(evicted)
		p.logger.Warn("slow consumer, dropped output", "session_id", p.sessionID, "bytes", len(evicted))
	}
	p.mu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// drain forwards queued items to the public channel one at a time. When stop
// fires, drain exits and closes the public channel so readers observe the
// detach as a normal channel close. Items already delivered into the
// buffered public channel remain readable.
func (p *outputPump) drain() {
	defer close(p.out)
	for {
		p.mu.Lock()
		var data []byte
		if len(p.queue) > 0 {
			data = p.queue[0]
			p.queue = p.queue[1:]
			p.bytes -= len(data)
		}
		p.mu.Unlock()

		if data == nil {
			select {
			case <-p.wake:
				continue
			case <-p.stop:
				return
			}
		}

		select {
		case p.out <- data:
		case <-p.stop:
			return
		}
	}
}

// close signals the drain goroutine to exit. Safe to call multiple times.
func (p *outputPump) close() {
	p.stopOnce.Do(func() { close(p.stop) })
}
