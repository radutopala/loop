package pluginsdktest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/pluginsdk"
)

type ConformanceSuite struct {
	suite.Suite
	ctx context.Context
}

func TestConformanceSuite(t *testing.T) {
	suite.Run(t, new(ConformanceSuite))
}

func (s *ConformanceSuite) SetupTest() {
	s.ctx = context.Background()
}

// serve starts h and returns a Plugin calling it.
func (s *ConformanceSuite) serve(h http.Handler) *Plugin {
	srv := httptest.NewServer(h)
	s.T().Cleanup(srv.Close)
	return &Plugin{URL: srv.URL, Secret: "sekret"}
}

// TestCheck runs Check against a plugin built on pluginsdk.Server that
// starts two agent runs from its action.
func (s *ConformanceSuite) TestCheck() {
	m := &pluginsdk.Manifest{
		ID: "echo", Version: "0.1.0", API: pluginsdk.APIVersion,
		Capabilities: []string{pluginsdk.CapAgentReadOnly},
		Agents:       []pluginsdk.Agent{{ID: "say", ReadOnly: true}},
		Tools:        []pluginsdk.Tool{{Name: "echo"}},
		Actions:      []pluginsdk.Action{{ID: "ping"}},
		UI:           &pluginsdk.UI{ComposerSwitches: []pluginsdk.ComposerSwitch{{ID: "mode", Label: "Mode", Icon: "ui/mode.svg"}}},
	}
	loop := NewFakeLoop(s.T(), Config{Manifest: m})
	var turned []bool
	api := loop.Client()
	ended := map[string]string{}
	srv := &pluginsdk.Server{
		Secret:  "sekret",
		Version: m.Version,
		Actions: map[string]pluginsdk.ActionFunc{
			"ping": func(ctx context.Context, _ pluginsdk.ActionRequest) error {
				_, err := api.StartRun(ctx, pluginsdk.RunRequest{Agent: "say", Prompt: "hi"})
				return err
			},
		},
		Tools: map[string]pluginsdk.ToolFunc{
			"echo": func(_ context.Context, req pluginsdk.ToolRequest) (pluginsdk.ToolResult, error) {
				return pluginsdk.ToolResult{Content: string(req.Input)}, nil
			},
		},
		RunDone: func(_ context.Context, runID string, req pluginsdk.RunDoneRequest) error {
			ended[runID] = req.Status
			return nil
		},
		Switches: map[string]pluginsdk.SwitchFunc{
			"mode": func(_ context.Context, req pluginsdk.SwitchRequest) (pluginsdk.SwitchState, error) {
				turned = append(turned, req.On)
				return pluginsdk.SwitchState{On: req.On, Status: pluginsdk.SwitchOK}, nil
			},
		},
	}
	s.T().Cleanup(srv.Close)
	p := s.serve(srv.Handler())

	Check(s.T(), p, m, CheckOptions{Loop: loop, Settle: srv.Wait})
	require.Len(s.T(), loop.Runs(), 3, "the action ran for each trigger")
	require.Equal(s.T(), map[string]string{"run-1": pluginsdk.RunStatusDone, "run-2": pluginsdk.RunStatusFailed, "run-3": pluginsdk.RunStatusDone}, ended)
	require.Equal(s.T(), []bool{true, false}, turned, "the switch was turned on and off")

	// Without a FakeLoop or Settle the runs check is skipped.
	Check(s.T(), p, m, CheckOptions{})
	Check(s.T(), p, m, CheckOptions{Loop: NewFakeLoop(s.T(), Config{})})
	noUI := *m
	noUI.UI = nil
	Check(s.T(), p, &noUI, CheckOptions{})
}

func (s *ConformanceSuite) TestCalls() {
	var got struct {
		method, path, auth, contentType string
		body                            []byte
	}
	p := s.serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.auth, got.contentType = r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		got.body, _ = io.ReadAll(r.Body)
		pluginsdk.WriteJSON(w, http.StatusTeapot, map[string]bool{"ok": true})
	}))
	p.HTTP = &http.Client{}

	tests := []struct {
		name        string
		call        func() (*Response, error)
		method      string
		path        string
		body        string
		contentType string
	}{
		{"health", func() (*Response, error) { return p.Health(s.ctx) }, http.MethodGet, "/healthz", "", ""},
		{"action", func() (*Response, error) {
			return p.Action(s.ctx, "a b", pluginsdk.ActionRequest{Trigger: pluginsdk.TriggerSchedule, TaskID: 3})
		}, http.MethodPost, "/actions/a%20b", `{"trigger":"schedule","task_id":3}`, "application/json"},
		{"tool", func() (*Response, error) {
			return p.Tool(s.ctx, "echo", pluginsdk.ToolRequest{RunID: "r", Input: json.RawMessage(`{}`)})
		}, http.MethodPost, "/tools/echo", `{"run_id":"r","agent":"","thread_id":"","input":{}}`, "application/json"},
		{"run done", func() (*Response, error) {
			return p.RunDone(s.ctx, "r1", pluginsdk.RunDoneRequest{Status: pluginsdk.RunStatusDone})
		}, http.MethodPost, "/runs/r1/done", `{"status":"done"}`, "application/json"},
		{"switch", func() (*Response, error) {
			return p.Switch(s.ctx, "mode", pluginsdk.SwitchRequest{On: true, ThreadID: "th1"})
		}, http.MethodPost, "/switches/mode", `{"on":true,"thread_id":"th1"}`, "application/json"},
		{"x with bytes", func() (*Response, error) { return p.X(s.ctx, http.MethodPut, "quizzes/1", []byte("raw")) }, http.MethodPut, "/x/quizzes/1", "raw", ""},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			resp, err := tt.call()
			require.NoError(s.T(), err)
			require.Equal(s.T(), &Response{Status: http.StatusTeapot, ContentType: "application/json", Body: []byte("{\"ok\":true}\n")}, resp)
			require.Equal(s.T(), tt.method, got.method)
			require.Equal(s.T(), tt.path, got.path)
			require.Equal(s.T(), "Bearer sekret", got.auth)
			require.Equal(s.T(), tt.contentType, got.contentType)
			switch {
			case tt.body == "":
				require.Empty(s.T(), got.body)
			case tt.contentType == "":
				require.Equal(s.T(), tt.body, string(got.body))
			default:
				require.JSONEq(s.T(), tt.body, string(got.body))
			}
		})
	}

	_, err := p.do(s.ctx, http.MethodGet, "/healthz", nil, "", ActionTimeout)
	require.NoError(s.T(), err)
	require.Empty(s.T(), got.auth, "an empty auth sends no header")
}

func (s *ConformanceSuite) TestCallErrors() {
	short := s.serve(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("ab"))
	}))
	down := s.serve(http.NotFoundHandler())
	down.URL = "http://127.0.0.1:1"

	tests := []struct {
		name    string
		call    func() (*Response, error)
		wantErr string
	}{
		{"body that isn't JSON", func() (*Response, error) { return short.X(s.ctx, http.MethodPost, "a", func() {}) }, "encoding the body"},
		{"bad URL", func() (*Response, error) { return (&Plugin{URL: "http://\x7f"}).Health(s.ctx) }, "invalid control character"},
		{"plugin down", func() (*Response, error) { return down.Health(s.ctx) }, "connection refused"},
		{"cut answer", func() (*Response, error) { return short.Health(s.ctx) }, "unexpected EOF"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := tt.call()
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}
}
