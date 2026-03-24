package browser

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sync"
)

// CaptureClient is the subset of CDPClient needed by CaptureState.
type CaptureClient interface {
	EnableConsoleCapture(ctx context.Context, ch chan<- ConsoleMessage) error
	EnableNetworkCapture(ctx context.Context, ch chan<- NetworkRequest) error
}

// CaptureState tracks console and network capture buffers.
// Safe for concurrent use. Fields are exported for cross-package test access.
type CaptureState struct {
	ConsoleMu   sync.Mutex
	ConsoleMsgs []ConsoleMessage

	NetworkMu   sync.Mutex
	NetworkReqs []NetworkRequest

	Started    bool
	lastClient CaptureClient // tracks last wired client to avoid duplicate rewiring
}

// Enable wires up console and network capture on the given CDP client.
// No-op if already started or if client is nil.
func (cs *CaptureState) Enable(ctx context.Context, client CaptureClient) {
	if cs.Started || client == nil || reflect.ValueOf(client).IsNil() {
		return
	}
	cs.Started = true
	cs.wireCapture(ctx, client)
}

// Rewire enables capture on a new CDP client (e.g. after a tab switch)
// without clearing the existing buffers. No-op if client is the same as the
// last wired client. Old listeners on previous targets continue running,
// so events from all visited tabs accumulate.
func (cs *CaptureState) Rewire(ctx context.Context, client CaptureClient) {
	if client == nil || reflect.ValueOf(client).IsNil() {
		return
	}
	if client == cs.lastClient {
		return
	}
	cs.wireCapture(ctx, client)
}

// wireCapture sets up console and network event listeners on the given client,
// feeding captured events into the shared buffers.
func (cs *CaptureState) wireCapture(ctx context.Context, client CaptureClient) {
	cs.lastClient = client
	consoleCh := make(chan ConsoleMessage, 100)
	if err := client.EnableConsoleCapture(ctx, consoleCh); err == nil {
		go func() {
			for msg := range consoleCh {
				cs.ConsoleMu.Lock()
				cs.ConsoleMsgs = append(cs.ConsoleMsgs, msg)
				cs.ConsoleMu.Unlock()
			}
		}()
	}

	networkCh := make(chan NetworkRequest, 100)
	if err := client.EnableNetworkCapture(ctx, networkCh); err == nil {
		go func() {
			for req := range networkCh {
				cs.NetworkMu.Lock()
				cs.NetworkReqs = append(cs.NetworkReqs, req)
				cs.NetworkMu.Unlock()
			}
		}()
	}
}

// ReadConsole returns captured console messages with optional filtering.
func (cs *CaptureState) ReadConsole(pattern string, onlyErrors bool, limit int, clear bool) (string, error) {
	if limit <= 0 {
		limit = 100
	}

	var re *regexp.Regexp
	if pattern != "" {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("invalid regex pattern: %v", err)
		}
	}

	cs.ConsoleMu.Lock()
	msgs := make([]ConsoleMessage, len(cs.ConsoleMsgs))
	copy(msgs, cs.ConsoleMsgs)
	if clear {
		cs.ConsoleMsgs = nil
	}
	cs.ConsoleMu.Unlock()

	var filtered []ConsoleMessage
	for _, msg := range msgs {
		if onlyErrors && msg.Level != "error" {
			continue
		}
		if re != nil && !re.MatchString(msg.Text) {
			continue
		}
		filtered = append(filtered, msg)
	}

	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	if len(filtered) == 0 {
		return "No console messages", nil
	}

	result := fmt.Sprintf("%d console message(s):\n", len(filtered))
	for _, msg := range filtered {
		result += fmt.Sprintf("[%s] %s: %s\n", msg.Time.Format("15:04:05"), msg.Level, msg.Text)
	}
	return result, nil
}

// ReadNetwork returns captured network requests with optional filtering.
func (cs *CaptureState) ReadNetwork(pattern string, limit int, clear bool) (string, error) {
	if limit <= 0 {
		limit = 50
	}

	var re *regexp.Regexp
	if pattern != "" {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("invalid regex pattern: %v", err)
		}
	}

	cs.NetworkMu.Lock()
	reqs := make([]NetworkRequest, len(cs.NetworkReqs))
	copy(reqs, cs.NetworkReqs)
	if clear {
		cs.NetworkReqs = nil
	}
	cs.NetworkMu.Unlock()

	var filtered []NetworkRequest
	for _, req := range reqs {
		if re != nil && !re.MatchString(req.URL) {
			continue
		}
		filtered = append(filtered, req)
	}

	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	if len(filtered) == 0 {
		return "No network requests", nil
	}

	result := fmt.Sprintf("%d network request(s):\n", len(filtered))
	for _, req := range filtered {
		result += fmt.Sprintf("[%s] %s %s — %d %s\n", req.Time.Format("15:04:05"), req.Method, req.URL, req.Status, req.StatusText)
	}
	return result, nil
}
