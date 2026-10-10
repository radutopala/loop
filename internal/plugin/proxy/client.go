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
	// ToolTimeout caps a tool call, which an agent waits on.
	ToolTimeout = 60 * time.Second
	// maxToolResult caps a tool call's answer.
	maxToolResult = 1 << 20
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

// ToolRequest is the body of POST /tools/{tool}: a call from agent, in the
// run runID in the hidden thread threadID.
type ToolRequest struct {
	RunID    string          `json:"run_id"`
	Agent    string          `json:"agent"`
	ThreadID string          `json:"thread_id"`
	Input    json.RawMessage `json:"input"`
}

// ToolResult is the plugin's answer to a tool call, which the agent sees as
// the tool's result.
type ToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
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
	return c.post(ctx, ep, "/actions/"+url.PathEscape(action), body, nil)
}

// Tool calls tool on the plugin at ep and returns its answer.
func (c *Client) Tool(ctx context.Context, ep runtime.Endpoint, tool string, req ToolRequest) (*ToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, ToolTimeout)
	defer cancel()
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encoding the tool call: %w", err)
	}
	var res ToolResult
	if err := c.post(ctx, ep, "/tools/"+url.PathEscape(tool), body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// post sends body to path on the plugin at ep and decodes its answer into
// out, when out is set.
func (c *Client) post(ctx context.Context, ep runtime.Endpoint, path string, body []byte, out any) error {
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
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxToolResult)).Decode(out); err != nil {
		return fmt.Errorf("reading the plugin's answer: %w", err)
	}
	return nil
}
