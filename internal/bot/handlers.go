package bot

import "sync"

// RegisterHandler appends a handler to the slice under the given mutex.
func RegisterHandler[T any](mu *sync.RWMutex, handlers *[]T, handler T) {
	mu.Lock()
	defer mu.Unlock()
	*handlers = append(*handlers, handler)
}

// CopyHandlers returns a snapshot of the handler slice under a read lock.
func CopyHandlers[T any](mu *sync.RWMutex, handlers []T) []T {
	mu.RLock()
	defer mu.RUnlock()
	cp := make([]T, len(handlers))
	copy(cp, handlers)
	return cp
}
