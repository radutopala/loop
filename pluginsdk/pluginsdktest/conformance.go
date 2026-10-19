package pluginsdktest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/pluginsdk"
)

// Timeouts Loop gives the plugin's answers.
const (
	ActionTimeout  = 10 * time.Second
	ToolTimeout    = 60 * time.Second
	RunDoneTimeout = 10 * time.Second
	XTimeout       = 30 * time.Second
	SwitchTimeout  = 10 * time.Second
)

// Plugin calls a plugin the way Loop does.
type Plugin struct {
	// URL is where the plugin serves, such as an httptest.Server's URL.
	URL string
	// Secret is what Loop sends as the bearer token.
	Secret string
	// HTTP sends the requests; nil is http.DefaultClient.
	HTTP *http.Client
}

// Response is a plugin's answer.
type Response struct {
	Status      int
	ContentType string
	Body        []byte
}

// Do sends method and path to the plugin with body as JSON, unless it's nil
// or already bytes, and the secret as auth.
func (p *Plugin) Do(ctx context.Context, method, path string, body any, timeout time.Duration) (*Response, error) {
	return p.do(ctx, method, path, body, "Bearer "+p.Secret, timeout)
}

func (p *Plugin) do(ctx context.Context, method, path string, body any, auth string, timeout time.Duration) (*Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var r io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("encoding the body: %w", err)
		}
		r, contentType = bytes.NewReader(data), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, p.URL+path, r)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: data}, nil
}

// Health calls GET /healthz.
func (p *Plugin) Health(ctx context.Context) (*Response, error) {
	return p.Do(ctx, http.MethodGet, "/healthz", nil, ActionTimeout)
}

// Action calls POST /actions/{action}.
func (p *Plugin) Action(ctx context.Context, action string, req pluginsdk.ActionRequest) (*Response, error) {
	return p.Do(ctx, http.MethodPost, "/actions/"+url.PathEscape(action), req, ActionTimeout)
}

// Tool calls POST /tools/{tool}.
func (p *Plugin) Tool(ctx context.Context, tool string, req pluginsdk.ToolRequest) (*Response, error) {
	return p.Do(ctx, http.MethodPost, "/tools/"+url.PathEscape(tool), req, ToolTimeout)
}

// RunDone calls POST /runs/{run_id}/done.
func (p *Plugin) RunDone(ctx context.Context, runID string, req pluginsdk.RunDoneRequest) (*Response, error) {
	return p.Do(ctx, http.MethodPost, "/runs/"+url.PathEscape(runID)+"/done", req, RunDoneTimeout)
}

// Switch calls POST /switches/{switch}.
func (p *Plugin) Switch(ctx context.Context, id string, req pluginsdk.SwitchRequest) (*Response, error) {
	return p.Do(ctx, http.MethodPost, "/switches/"+url.PathEscape(id), req, SwitchTimeout)
}

// X calls the plugin's own API at /x/{path}, as its UI does through Loop.
func (p *Plugin) X(ctx context.Context, method, path string, body any) (*Response, error) {
	return p.Do(ctx, method, "/x/"+path, body, XTimeout)
}

// CheckOptions tunes Check.
type CheckOptions struct {
	// Loop is the FakeLoop the plugin calls, whose runs Check ends.
	Loop *FakeLoop
	// Settle, when set, waits for the work the plugin's actions started in
	// the background, such as Server.Wait, before Check ends their runs.
	Settle func()
}

// Check drives the plugin p, described by m, through the Loop → plugin
// contract: its health check and version, the secret, every action with
// each trigger, every tool as an agent and a workflow call it, every
// composer switch turned on and off, and the end
// of each agent run it started on opts.Loop. A plugin's
// tests call it with the plugin serving against a FakeLoop.
func Check(t *testing.T, p *Plugin, m *pluginsdk.Manifest, opts CheckOptions) {
	t.Helper()
	ctx := context.Background()

	t.Run("health", func(t *testing.T) {
		resp, err := p.Health(ctx)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.Status, "GET /healthz answers 200")
		var h pluginsdk.Health
		require.NoError(t, json.Unmarshal(resp.Body, &h), "GET /healthz answers JSON")
		require.Equal(t, m.API, h.API, "GET /healthz reports the manifest's api")
		require.Equal(t, m.Version, h.Version, "GET /healthz reports the manifest's version")
	})

	t.Run("secret", func(t *testing.T) {
		for _, auth := range []string{"", "Bearer wrong", "Bearer " + p.Secret + "x"} {
			resp, err := p.do(ctx, http.MethodGet, "/healthz", nil, auth, ActionTimeout)
			require.NoError(t, err)
			require.Equal(t, http.StatusUnauthorized, resp.Status, "a call with auth %q is refused", auth)
		}
	})

	t.Run("actions", func(t *testing.T) {
		for _, a := range m.Actions {
			for _, req := range []pluginsdk.ActionRequest{{Trigger: pluginsdk.TriggerManual}, {Trigger: pluginsdk.TriggerSchedule, TaskID: 1}, {Trigger: pluginsdk.TriggerWorkflow}} {
				resp, err := p.Action(ctx, a.ID, req)
				require.NoError(t, err, "action %s answers within %s", a.ID, ActionTimeout)
				require.Equal(t, http.StatusAccepted, resp.Status, "action %s answers 202: %s", a.ID, resp.Body)
			}
		}
	})

	t.Run("tools", func(t *testing.T) {
		calls := []pluginsdk.ToolRequest{
			{RunID: "conformance", Agent: "conformance", ThreadID: "conformance", Input: json.RawMessage(`{}`)},
			{RunID: "workflow:conformance:node", Input: json.RawMessage(`{}`)},
		}
		for _, tool := range m.Tools {
			for _, req := range calls {
				resp, err := p.Tool(ctx, tool.Name, req)
				require.NoError(t, err, "tool %s answers within %s", tool.Name, ToolTimeout)
				require.Equal(t, http.StatusOK, resp.Status, "tool %s answers 200, with is_error for a call it can't take: %s", tool.Name, resp.Body)
				var res pluginsdk.ToolResult
				require.NoError(t, json.Unmarshal(resp.Body, &res), "tool %s answers a tool result", tool.Name)
			}
		}
	})

	t.Run("switches", func(t *testing.T) {
		if m.UI == nil {
			return
		}
		for _, sw := range m.UI.ComposerSwitches {
			for _, on := range []bool{true, false} {
				resp, err := p.Switch(ctx, sw.ID, pluginsdk.SwitchRequest{On: on})
				require.NoError(t, err, "switch %s answers within %s", sw.ID, SwitchTimeout)
				require.Equal(t, http.StatusOK, resp.Status, "switch %s answers 200 when turned on=%t: %s", sw.ID, on, resp.Body)
				var state pluginsdk.SwitchState
				require.NoError(t, json.Unmarshal(resp.Body, &state), "switch %s answers its state", sw.ID)
				require.Contains(t, switchStatuses, state.Status, "switch %s answers a known status", sw.ID)
				require.LessOrEqual(t, utf8.RuneCountInString(state.Title), 200, "switch %s answers a title of at most 200 characters", sw.ID)
			}
		}
	})

	t.Run("runs", func(t *testing.T) {
		if opts.Loop == nil {
			t.Skip("no FakeLoop to see the plugin's runs")
		}
		if opts.Settle != nil {
			opts.Settle()
		}
		for i, run := range opts.Loop.Runs() {
			req := pluginsdk.RunDoneRequest{Status: pluginsdk.RunStatusDone}
			if i%2 == 1 {
				req = pluginsdk.RunDoneRequest{Status: pluginsdk.RunStatusFailed, Error: "conformance"}
			}
			resp, err := p.RunDone(ctx, run.RunID, req)
			require.NoError(t, err)
			require.Equal(t, 2, resp.Status/100, "run %s's end is taken: %s", run.RunID, resp.Body)
		}
	})
}
