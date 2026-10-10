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
	"path"
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
	// RunDoneTimeout caps telling a plugin one of its agent runs ended.
	RunDoneTimeout = 10 * time.Second
	// XTimeout caps a call to the plugin's own API for its UI.
	XTimeout = 30 * time.Second
	// maxToolResult caps a tool call's answer.
	maxToolResult = 1 << 20
	// MaxXBody caps the bodies of calls to the plugin's own API, both ways.
	MaxXBody = 8 << 20
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

// Run statuses sent to POST /runs/{run_id}/done.
const (
	RunStatusDone   = "done"
	RunStatusFailed = "failed"
)

// RunDoneRequest is the body of POST /runs/{run_id}/done: how an agent run
// the plugin started ended.
type RunDoneRequest struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// XRequest is a call from the plugin's UI to its own API, /x/{path}.
type XRequest struct {
	Method      string
	Path        string
	RawQuery    string
	ContentType string
	Body        []byte
}

// XResponse is the plugin's answer to an XRequest.
type XResponse struct {
	Status      int
	ContentType string
	Body        []byte
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

// RunDone tells the plugin at ep that its agent run runID ended.
func (c *Client) RunDone(ctx context.Context, ep runtime.Endpoint, runID string, req RunDoneRequest) error {
	ctx, cancel := context.WithTimeout(ctx, RunDoneTimeout)
	defer cancel()
	// RunDoneRequest always encodes.
	body, _ := json.Marshal(req)
	return c.post(ctx, ep, "/runs/"+url.PathEscape(runID)+"/done", body, nil)
}

// X sends req to /x/{path} on the plugin at ep and returns its answer,
// whatever the status. The path is cleaned, so it can't leave /x.
func (c *Client) X(ctx context.Context, ep runtime.Endpoint, req XRequest) (*XResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, XTimeout)
	defer cancel()
	u := ep.URL + "/x" + path.Clean("/"+req.Path)
	if req.RawQuery != "" {
		u += "?" + req.RawQuery
	}
	r, err := http.NewRequestWithContext(ctx, req.Method, u, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("building the plugin request: %w", err)
	}
	if req.ContentType != "" {
		r.Header.Set("Content-Type", req.ContentType)
	}
	r.Header.Set("Authorization", "Bearer "+ep.Secret)
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("calling the plugin: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxXBody+1))
	if err != nil {
		return nil, fmt.Errorf("reading the plugin's answer: %w", err)
	}
	if len(body) > MaxXBody {
		return nil, fmt.Errorf("the plugin's answer is over %d MB", MaxXBody>>20)
	}
	return &XResponse{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: body}, nil
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
