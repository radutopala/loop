// Package proxy calls into plugin containers: the Loop → plugin half of the
// contract. Every call has a timeout and reads a capped answer, so a slow or
// misbehaving plugin can't hold the daemon up.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/plugin/runtime"
)

const (
	// ActionTimeout caps an action call; a plugin answers it at once and
	// carries on in the background.
	ActionTimeout = 10 * time.Second
	// maxErrorBody caps how much of a failed answer goes into the error.
	maxErrorBody = 4 << 10
)

// Trigger is what started an action.
type Trigger string

const (
	TriggerManual   Trigger = "manual"
	TriggerSchedule Trigger = "schedule"
)

// ActionRequest is the body of POST /actions/{action}.
type ActionRequest struct {
	Trigger Trigger `json:"trigger"`
	TaskID  int64   `json:"task_id,omitempty"`
}

// Doer sends HTTP requests.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client calls plugin containers.
type Client struct {
	http Doer
}

// NewClient returns a Client sending its requests with doer.
func NewClient(doer Doer) *Client {
	return &Client{http: doer}
}

// Action asks the plugin at ep to run action. The plugin accepts it with
// any 2xx; it reports how the work went through events, later.
func (c *Client) Action(ctx context.Context, ep runtime.Endpoint, action string, req ActionRequest) error {
	ctx, cancel := context.WithTimeout(ctx, ActionTimeout)
	defer cancel()
	// ActionRequest always encodes.
	body, _ := json.Marshal(req)
	return c.post(ctx, ep, "/actions/"+url.PathEscape(action), body)
}

func (c *Client) post(ctx context.Context, ep runtime.Endpoint, path string, body []byte) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building the plugin request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+ep.Secret)
	resp, err := c.http.Do(r)
	if err != nil {
		return fmt.Errorf("calling the plugin: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("the plugin answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	return nil
}
