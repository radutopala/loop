package terminal

import (
	"sync"
)

// RingBuffer is a fixed-size circular byte buffer that is safe for
// concurrent use. When full, new writes overwrite the oldest data.
type RingBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	w    int  // next write position
	full bool // whether the buffer has wrapped
}

// NewRingBuffer creates a ring buffer with the given capacity in bytes.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

// Write appends data to the ring buffer, overwriting oldest data if full.
func (r *RingBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}

	if n >= r.size {
		// Data larger than buffer — keep only the last r.size bytes.
		copy(r.buf, p[n-r.size:])
		r.w = 0
		r.full = true
		return n, nil
	}

	end := r.w + n
	if end <= r.size {
		copy(r.buf[r.w:], p)
	} else {
		first := r.size - r.w
		copy(r.buf[r.w:], p[:first])
		copy(r.buf, p[first:])
	}

	if end > r.size || (r.full) {
		r.full = true
	}
	r.w = end % r.size
	if !r.full && r.w == 0 {
		r.full = true
	}

	return n, nil
}

// Bytes returns a copy of the buffered data in chronological order.
func (r *RingBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		out := make([]byte, r.w)
		copy(out, r.buf[:r.w])
		return out
	}

	out := make([]byte, r.size)
	copy(out, r.buf[r.w:])
	copy(out[r.size-r.w:], r.buf[:r.w])
	return out
}

// Len returns the number of bytes currently stored.
func (r *RingBuffer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		return r.size
	}
	return r.w
}

// Reset clears the buffer.
func (r *RingBuffer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.w = 0
	r.full = false
}
