package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/plugin/runtime"
)

type ClientSuite struct {
	suite.Suite
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}

type failingDoer struct{}

func (failingDoer) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("refused")
}

func (s *ClientSuite) TestAction() {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "accepted", status: http.StatusAccepted},
		{name: "ok with a body", status: http.StatusOK, body: `{"ok":true}`},
		{name: "refused", status: http.StatusConflict, body: "already running\n", wantErr: "the plugin answered 409 Conflict: already running"},
		{name: "error body capped", status: http.StatusInternalServerError, body: strings.Repeat("x", maxErrorBody+10), wantErr: "the plugin answered 500 Internal Server Error: " + strings.Repeat("x", maxErrorBody)},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var got ActionRequest
			var method, path, auth, ctype string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path = r.Method, r.URL.EscapedPath()
				auth, ctype = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
				_ = json.NewDecoder(r.Body).Decode(&got)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			err := NewClient(srv.Client()).Action(context.Background(), runtime.Endpoint{URL: srv.URL, Secret: "sec"}, "daily run", ActionRequest{Trigger: TriggerSchedule, TaskID: 7})
			require.Equal(s.T(), http.MethodPost, method)
			require.Equal(s.T(), "/actions/daily%20run", path)
			require.Equal(s.T(), "Bearer sec", auth)
			require.Equal(s.T(), "application/json", ctype)
			require.Equal(s.T(), ActionRequest{Trigger: TriggerSchedule, TaskID: 7}, got)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
		})
	}
}

func (s *ClientSuite) TestActionTimesOut() {
	// The plugin doesn't answer until the test is done; the caller's
	// deadline ends the call first.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := NewClient(srv.Client()).Action(ctx, runtime.Endpoint{URL: srv.URL}, "ping", ActionRequest{Trigger: TriggerManual})
	require.ErrorIs(s.T(), err, context.DeadlineExceeded)
}

func (s *ClientSuite) TestActionFailures() {
	c := NewClient(failingDoer{})
	err := c.Action(context.Background(), runtime.Endpoint{URL: "http://plugin"}, "ping", ActionRequest{})
	require.EqualError(s.T(), err, "calling the plugin: refused")

	err = c.Action(context.Background(), runtime.Endpoint{URL: "http://plugin\x7f"}, "ping", ActionRequest{})
	require.ErrorContains(s.T(), err, "building the plugin request")
}

func (s *ClientSuite) TestTool() {
	tests := []struct {
		name    string
		status  int
		body    string
		want    *ToolResult
		wantErr string
	}{
		{name: "result", status: http.StatusOK, body: `{"content":"hi","is_error":false}`, want: &ToolResult{Content: "hi"}},
		{name: "tool error", status: http.StatusOK, body: `{"content":"bad input","is_error":true}`, want: &ToolResult{Content: "bad input", IsError: true}},
		{name: "refused", status: http.StatusNotFound, body: "no such tool\n", wantErr: "the plugin answered 404 Not Found: no such tool"},
		{name: "not json", status: http.StatusOK, body: "hi", wantErr: "reading the plugin's answer"},
		{name: "answer capped", status: http.StatusOK, body: `{"content":"` + strings.Repeat("x", maxToolResult) + `"}`, wantErr: "reading the plugin's answer"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var got ToolRequest
			var path, auth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path, auth = r.URL.EscapedPath(), r.Header.Get("Authorization")
				_ = json.NewDecoder(r.Body).Decode(&got)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			req := ToolRequest{RunID: "r1", Agent: "say", ThreadID: "plugin:echo-1", Input: json.RawMessage(`{"text":"hi"}`)}
			res, err := NewClient(srv.Client()).Tool(context.Background(), runtime.Endpoint{URL: srv.URL, Secret: "sec"}, "echo", req)
			require.Equal(s.T(), "/tools/echo", path)
			require.Equal(s.T(), "Bearer sec", auth)
			require.Equal(s.T(), req, got)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, res)
		})
	}
}

func (s *ClientSuite) TestToolBadInput() {
	_, err := NewClient(failingDoer{}).Tool(context.Background(), runtime.Endpoint{URL: "http://plugin"}, "echo", ToolRequest{Input: json.RawMessage("{")})
	require.ErrorContains(s.T(), err, "encoding the tool call")
}
