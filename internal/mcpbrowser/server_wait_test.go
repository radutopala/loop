package mcpbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

// waitServer returns a Server whose actions go to dispatch and whose sleeps
// are recorded in slept rather than taken.
func waitServer(dispatch actionDispatcher) (*Server, *[]time.Duration) {
	srv := New("http://x", "ch", nil)
	srv.dispatch = dispatch
	var slept []time.Duration
	srv.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	return srv, &slept
}

// pageReadyAfter answers evaluate_js with "false" until it has been asked n
// times, then "true"; other actions get ok.
func pageReadyAfter(n int, actions *[]string) actionDispatcher {
	checks := 0
	return func(_ context.Context, action string, _ map[string]any) (*actionResponse, error) {
		*actions = append(*actions, action)
		if action != "evaluate_js" {
			return &actionResponse{Result: "ok", Image: base64.StdEncoding.EncodeToString([]byte("png"))}, nil
		}
		checks++
		if checks >= n {
			return &actionResponse{Result: "true"}, nil
		}
		return &actionResponse{Result: "false"}, nil
	}
}

func (s *ServerSuite) TestWaitFor() {
	tests := []struct {
		name       string
		spec       waitSpec
		results    []string // evaluate_js answers in order; "err" fails the call
		wantErr    string
		wantChecks int
		wantSleeps int
	}{
		{name: "nothing to wait for", spec: waitSpec{}, wantChecks: 0},
		{name: "already there", spec: waitSpec{WaitForSelector: ".feed"}, results: []string{"true"}, wantChecks: 1},
		{
			name:       "shows up after a few checks, failed checks retried",
			spec:       waitSpec{WaitForText: "Results"},
			results:    []string{"false", "err", "true"},
			wantChecks: 3, wantSleeps: 2,
		},
		{
			name:       "times out",
			spec:       waitSpec{WaitForSelector: ".feed", WaitForText: "Results", WaitTimeoutMs: 500},
			results:    []string{"false", "false", "false"},
			wantErr:    `timed out after 500ms waiting for selector ".feed" and text "Results"`,
			wantChecks: 3, wantSleeps: 2,
		},
		{
			name:       "times out with the last check failing",
			spec:       waitSpec{WaitForText: "x", WaitTimeoutMs: 1},
			results:    []string{"false", "err"},
			wantErr:    `timed out after 1ms waiting for text "x" (last check failed: page gone)`,
			wantChecks: 2, wantSleeps: 1,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			checks := 0
			srv, slept := waitServer(func(_ context.Context, action string, params map[string]any) (*actionResponse, error) {
				require.Equal(s.T(), "evaluate_js", action)
				require.Equal(s.T(), waitForJS(tt.spec), params["expression"])
				r := tt.results[checks]
				checks++
				if r == "err" {
					return nil, errors.New("page gone")
				}
				return &actionResponse{Result: r}, nil
			})
			err := srv.waitFor(context.Background(), tt.spec)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tt.wantChecks, checks)
			require.Len(s.T(), *slept, tt.wantSleeps)
			for _, d := range *slept {
				require.Equal(s.T(), waitPollInterval, d)
			}
		})
	}
}

func (s *ServerSuite) TestWaitForTimeoutDefaultAndCap() {
	tests := []struct {
		name   string
		ms     int
		checks int
	}{
		{name: "default", ms: 0, checks: int(defaultWaitTimeout/waitPollInterval) + 1},
		{name: "capped", ms: 10 * 60 * 1000, checks: int(maxWaitTimeout/waitPollInterval) + 1},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			checks := 0
			srv, _ := waitServer(func(context.Context, string, map[string]any) (*actionResponse, error) {
				checks++
				return &actionResponse{Result: "false"}, nil
			})
			err := srv.waitFor(context.Background(), waitSpec{WaitForText: "x", WaitTimeoutMs: tt.ms})
			require.ErrorContains(s.T(), err, "timed out")
			require.Equal(s.T(), tt.checks, checks)
		})
	}
}

func (s *ServerSuite) TestWaitForStopsWhenContextEnds() {
	srv, _ := waitServer(func(context.Context, string, map[string]any) (*actionResponse, error) {
		return &actionResponse{Result: "false"}, nil
	})
	srv.sleep = sleepCtx
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := srv.waitFor(ctx, waitSpec{WaitForText: "x"})
	require.ErrorIs(s.T(), err, context.Canceled)
}

func (s *ServerSuite) TestSleepCtx() {
	require.NoError(s.T(), sleepCtx(context.Background(), time.Millisecond))
}

func (s *ServerSuite) TestWaitForJS() {
	require.Equal(s.T(),
		`(() => { try { return String(document.querySelector("a[href=\"x\"]") !== null && (document.body?.innerText ?? "").includes("it's \"ok\"")); } catch (e) { return "false"; } })()`,
		waitForJS(waitSpec{WaitForSelector: `a[href="x"]`, WaitForText: `it's "ok"`}))
}

func (s *ServerSuite) TestNavigateWaitsFor() {
	tests := []struct {
		name    string
		ready   int
		timeout int
		wantErr bool
		want    string
	}{
		{name: "page shows it", ready: 2, want: "Navigated to https://e.com — E"},
		{name: "page never shows it", ready: 100, timeout: 250, wantErr: true,
			want: `Navigated to https://e.com — E, but timed out after 250ms waiting for selector ".feed"`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var actions []string
			ready := pageReadyAfter(tt.ready, &actions)
			srv, _ := waitServer(func(ctx context.Context, action string, params map[string]any) (*actionResponse, error) {
				if action == "navigate" {
					actions = append(actions, action)
					return &actionResponse{PageInfo: &browser.PageInfo{URL: "https://e.com", Title: "E"}}, nil
				}
				return ready(ctx, action, params)
			})
			session := connectClient(s.T(), srv)
			res := callTool(s.T(), session, "navigate", map[string]any{
				"url": "https://e.com", "wait_for_selector": ".feed", "wait_timeout_ms": tt.timeout,
			})
			require.Equal(s.T(), tt.wantErr, res.IsError)
			require.Equal(s.T(), tt.want, getText(s.T(), res))
			require.Equal(s.T(), "navigate", actions[0])
		})
	}
}

func (s *ServerSuite) TestScreenshotWaitsFirst() {
	var actions []string
	srv, _ := waitServer(pageReadyAfter(2, &actions))
	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "screenshot", map[string]any{"wait_for_text": "Results"})
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), []string{"evaluate_js", "evaluate_js", "screenshot"}, actions)
}

func (s *ServerSuite) TestScreenshotWaitTimesOut() {
	var actions []string
	srv, _ := waitServer(pageReadyAfter(100, &actions))
	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "screenshot", map[string]any{"wait_for_text": "Results", "wait_timeout_ms": 1})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `waiting for text "Results"`)
	require.NotContains(s.T(), actions, "screenshot")
}

func (s *ServerSuite) TestSaveScreenshotWaits() {
	tests := []struct {
		name    string
		ready   int
		wantErr bool
	}{
		{name: "page shows it", ready: 1},
		{name: "page never shows it", ready: 100, wantErr: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var actions []string
			srv, _ := waitServer(pageReadyAfter(tt.ready, &actions))
			session := connectClient(s.T(), srv)
			path := filepath.Join(s.T().TempDir(), "shot.png")
			res := callTool(s.T(), session, "save_screenshot", map[string]any{
				"path": path, "wait_for_selector": ".feed", "wait_timeout_ms": 1,
			})
			require.Equal(s.T(), tt.wantErr, res.IsError)
			if tt.wantErr {
				require.NotContains(s.T(), actions, "screenshot")
				return
			}
			require.Equal(s.T(), []string{"evaluate_js", "screenshot"}, actions)
		})
	}
}

func (s *ServerSuite) TestComputerWaits() {
	tests := []struct {
		name        string
		input       computerInput
		ready       int
		wantErr     bool
		want        string
		wantActions []string
		wantSlept   []time.Duration
	}{
		{
			name:  "wait for duration",
			input: computerInput{Action: "wait", Duration: 1500},
			want:  "Waited", wantSlept: []time.Duration{1500 * time.Millisecond},
		},
		{
			name:  "wait for duration is capped",
			input: computerInput{Action: "wait", Duration: 10 * 60 * 1000},
			want:  "Waited", wantSlept: []time.Duration{maxWaitTimeout},
		},
		{
			name:  "wait for text",
			input: computerInput{Action: "wait", waitSpec: waitSpec{WaitForText: "Done"}},
			ready: 2, want: `Page shows text "Done"`,
			wantActions: []string{"evaluate_js", "evaluate_js"}, wantSlept: []time.Duration{waitPollInterval},
		},
		{
			name:  "wait for text times out",
			input: computerInput{Action: "wait", waitSpec: waitSpec{WaitForText: "Done", WaitTimeoutMs: 1}},
			ready: 100, wantErr: true, want: `timed out after 1ms waiting for text "Done"`,
			wantActions: []string{"evaluate_js", "evaluate_js"}, wantSlept: []time.Duration{waitPollInterval},
		},
		{
			name:  "click then wait",
			input: computerInput{Action: "click", X: 1, Y: 2, waitSpec: waitSpec{WaitForSelector: ".menu"}},
			ready: 1, want: "Clicked at (1, 2)",
			wantActions: []string{"mouse_click", "evaluate_js"},
		},
		{
			name:  "click then wait times out",
			input: computerInput{Action: "click", X: 1, Y: 2, waitSpec: waitSpec{WaitForSelector: ".menu", WaitTimeoutMs: 1}},
			ready: 100, wantErr: true, want: `Clicked at (1, 2), but timed out after 1ms waiting for selector ".menu"`,
			wantActions: []string{"mouse_click", "evaluate_js", "evaluate_js"}, wantSlept: []time.Duration{waitPollInterval},
		},
		{
			name:    "failed action skips the wait",
			input:   computerInput{Action: "type", waitSpec: waitSpec{WaitForSelector: ".menu"}},
			wantErr: true, want: "text is required for type action",
		},
		{
			name:  "screenshot waits first",
			input: computerInput{Action: "screenshot", waitSpec: waitSpec{WaitForText: "Done"}},
			ready: 1, wantActions: []string{"evaluate_js", "screenshot"},
		},
		{
			name:  "screenshot wait times out",
			input: computerInput{Action: "screenshot", waitSpec: waitSpec{WaitForText: "Done", WaitTimeoutMs: 1}},
			ready: 100, wantErr: true, want: `timed out after 1ms waiting for text "Done"`,
			wantActions: []string{"evaluate_js", "evaluate_js"}, wantSlept: []time.Duration{waitPollInterval},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var actions []string
			srv, slept := waitServer(pageReadyAfter(tt.ready, &actions))
			res, _, err := srv.handleComputer(context.Background(), tt.input)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantErr, res.IsError)
			if tt.want != "" {
				require.Equal(s.T(), tt.want, getText(s.T(), res))
			}
			require.Equal(s.T(), tt.wantActions, actions)
			require.Equal(s.T(), tt.wantSlept, *slept)
		})
	}
}

func (s *ServerSuite) TestComputerWaitInterrupted() {
	srv, _ := waitServer(nil)
	srv.sleep = sleepCtx
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _, err := srv.handleComputer(ctx, computerInput{Action: "wait", Duration: 60000})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Equal(s.T(), "wait failed: context canceled", getText(s.T(), res))
}

func (s *ServerSuite) TestTabByIndex() {
	tabs := []browser.TabInfo{{TargetID: "t1"}, {TargetID: "t2"}}
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		listErr  error
		wantErr  bool
		want     string
		wantSent string
	}{
		{name: "switch by index", tool: "switch_tab", args: map[string]any{"index": 2}, want: "Switched to tab t2", wantSent: "t2"},
		{name: "close by index", tool: "close_tab", args: map[string]any{"index": 1}, want: "Closed tab t1", wantSent: "t1"},
		{name: "target_id wins over index", tool: "switch_tab", args: map[string]any{"index": 1, "target_id": "t9"}, want: "Switched to tab t9", wantSent: "t9"},
		{name: "index past the last tab", tool: "switch_tab", args: map[string]any{"index": 3}, wantErr: true,
			want: "switch tab failed: index 3 out of range: 2 tab(s) open"},
		{name: "negative index", tool: "close_tab", args: map[string]any{"index": -1}, wantErr: true,
			want: "close tab failed: index -1 out of range: 2 tab(s) open"},
		{name: "listing fails", tool: "switch_tab", args: map[string]any{"index": 1}, listErr: errors.New("boom"), wantErr: true,
			want: "switch tab failed: listing tabs: boom"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			sent := ""
			srv, _ := waitServer(func(_ context.Context, action string, params map[string]any) (*actionResponse, error) {
				if action == "list_tabs" {
					return &actionResponse{Tabs: tabs}, tt.listErr
				}
				require.Equal(s.T(), tt.tool, action)
				sent, _ = params["target_id"].(string)
				return &actionResponse{Result: "ok"}, nil
			})
			session := connectClient(s.T(), srv)
			res := callTool(s.T(), session, tt.tool, tt.args)
			require.Equal(s.T(), tt.wantErr, res.IsError)
			require.Equal(s.T(), tt.want, getText(s.T(), res))
			require.Equal(s.T(), tt.wantSent, sent)
		})
	}
}

func (s *ServerSuite) TestToolSchemasOfferWaitAndIndex() {
	srv := New("http://x", "ch", nil)
	session := connectClient(s.T(), srv)
	res, err := session.ListTools(context.Background(), nil)
	require.NoError(s.T(), err)
	props := map[string]string{}
	for _, tool := range res.Tools {
		b, err := json.Marshal(tool.InputSchema)
		require.NoError(s.T(), err)
		props[tool.Name] = string(b)
	}
	for _, name := range []string{"navigate", "computer", "screenshot", "save_screenshot"} {
		for _, field := range []string{"wait_for_selector", "wait_for_text", "wait_timeout_ms"} {
			require.True(s.T(), strings.Contains(props[name], `"`+field+`"`), "%s lacks %s", name, field)
		}
	}
	for _, name := range []string{"switch_tab", "close_tab"} {
		require.Contains(s.T(), props[name], `"index"`)
		require.NotContains(s.T(), props[name], `"required"`)
	}
}
