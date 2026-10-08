package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ReviewToolSuite struct {
	baseToolSuite
}

func TestReviewToolSuite(t *testing.T) {
	suite.Run(t, new(ReviewToolSuite))
}

func (s *ReviewToolSuite) TestReportReviewFindings() {
	var gotURL string
	var gotBody []byte
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotBody, _ = io.ReadAll(req.Body)
		return jsonResponse(200, `{"added":2,"skipped":1}`), nil
	}

	text, isError := s.callTool("report_review_findings", map[string]any{
		"findings": []map[string]any{
			{"path": "a.go", "line": 3, "side": "RIGHT", "body": "bug one", "category": "correctness"},
			{"path": "b.go", "line": 9, "body": "bug two"},
			{"path": "a.go", "line": 3, "side": "RIGHT", "body": "bug one"},
		},
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Recorded 2 finding(s)")
	require.Contains(s.T(), text, "1 duplicate/invalid skipped")
	require.Equal(s.T(), "http://localhost:8222/api/channels/test-channel/review/comments", gotURL)

	var payload struct {
		Findings []map[string]any `json:"findings"`
	}
	require.NoError(s.T(), json.Unmarshal(gotBody, &payload))
	require.Len(s.T(), payload.Findings, 3)
	require.Equal(s.T(), "a.go", payload.Findings[0]["path"])
	require.Equal(s.T(), "correctness", payload.Findings[0]["category"])
	require.NotContains(s.T(), payload.Findings[1], "category")
}

func (s *ReviewToolSuite) TestReportReviewFindingsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool: "report_review_findings",
		args: map[string]any{
			"findings": []map[string]any{{"path": "a.go", "line": 1, "body": "x"}},
		},
		apiStatus:    404,
		apiBody:      `no review session for channel`,
		decodeStatus: 200,
	})
}

const reviewSessionJSON = `{"present":true,"session":{
	"pr":{"number":104,"url":"https://github.com/o/r/pull/104","title":"Add thing"},
	"head_sha":"abc123","status":"ready","error":"",
	"comments":[
		{"id":"c1","path":"a.go","line":3,"side":"RIGHT","body":"bug one\nsecond line","pushed":false,"source":"agent","category":"correctness","verdict":"false_positive","verdict_reason":"made in init"},
		{"id":"c2","path":"b.go","line":9,"side":"LEFT","body":"bug two","pushed":true,"source":"agent","github_id":77},
		{"id":"c3","path":"a.go","line":5,"side":"RIGHT","body":"human note","pushed":true,"source":"github","author":"octo","github_id":88,"outdated":true,"resolved":true},
		{"id":"c4","path":"a.go","line":7,"side":"RIGHT","body":"no source","pushed":false}
	]}}`

func (s *ReviewToolSuite) TestGetReviewComments() {
	cases := []struct {
		name        string
		args        map[string]any
		wantURL     string
		contains    []string
		notContains []string
	}{
		{
			name:    "all comments of the current channel",
			args:    map[string]any{},
			wantURL: "http://localhost:8222/api/channels/test-channel/review?diff=false",
			contains: []string{
				"PR #104 Add thing\nhttps://github.com/o/r/pull/104\n",
				"head_sha: abc123\nstatus: ready\n",
				"4 of 4 comment(s):",
				"[c1] a.go:3 RIGHT, agent, unpushed, category correctness, verdict false_positive\n  bug one\n  second line\n  (verdict: made in init)\n",
				"[c2] b.go:9 LEFT, agent, pushed (id 77)\n",
				"[c3] a.go:5 RIGHT, github by octo (id 88), outdated, resolved\n",
				"[c4] a.go:7 RIGHT, agent, unpushed\n",
			},
			notContains: []string{"error:"},
		},
		{
			name:        "unpushed only",
			args:        map[string]any{"unpushed_only": true},
			wantURL:     "http://localhost:8222/api/channels/test-channel/review?diff=false",
			contains:    []string{"2 of 4 comment(s):", "[c1]", "[c4]"},
			notContains: []string{"[c2]", "[c3]"},
		},
		{
			name:        "one file",
			args:        map[string]any{"path": "b.go"},
			wantURL:     "http://localhost:8222/api/channels/test-channel/review?diff=false",
			contains:    []string{"1 of 4 comment(s):", "[c2]"},
			notContains: []string{"[c1]", "[c3]", "[c4]"},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			var gotURL string
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				gotURL = req.URL.String()
				return jsonResponse(200, reviewSessionJSON), nil
			}
			text, isError := s.callTool("get_review_comments", tc.args)
			require.False(s.T(), isError)
			require.Equal(s.T(), tc.wantURL, gotURL)
			for _, want := range tc.contains {
				require.Contains(s.T(), text, want)
			}
			for _, unwanted := range tc.notContains {
				require.NotContains(s.T(), text, unwanted)
			}
		})
	}
}

func (s *ReviewToolSuite) TestGetReviewCommentsNoPRAndError() {
	s.httpClient.doFunc = func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"present":true,"session":{"status":"error","error":"gh failed","comments":[]}}`), nil
	}
	text, isError := s.callTool("get_review_comments", map[string]any{})
	require.False(s.T(), isError)
	require.Equal(s.T(), "head_sha: \nstatus: error\nerror: gh failed\n\n0 of 0 comment(s):\n", text)
}

func (s *ReviewToolSuite) TestGetReviewCommentsNoSession() {
	s.httpClient.doFunc = func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"present":false}`), nil
	}
	text, isError := s.callTool("get_review_comments", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No review session for this channel")
}

func (s *ReviewToolSuite) TestGetReviewCommentsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "get_review_comments",
		args:         map[string]any{},
		apiStatus:    403,
		apiBody:      `channel x is outside this agent's project`,
		decodeStatus: 200,
	})
}

func (s *ReviewToolSuite) TestDedupReviewFindings() {
	cases := []struct {
		name    string
		args    map[string]any
		body    string
		wantURL string
		want    string
	}{
		{
			name:    "a pass that changed things",
			args:    map[string]any{},
			body:    `{"removed":["c2","c3"],"clusters":[{"kept":"c1","removed":["c2","c3"],"reason":"same nil check"}],"related":[{"ids":["c1","c4"],"reason":"same root cause"}],"moved":[{"id":"c4","from":7,"to":9}],"trimmed":[{"id":"c6","covered_by":"c1","reason":"bundles the nil check"}],"verdicts":[{"id":"c1","verdict":"real","reason":"no nil check"}],"checked":4,"errors":["deleting c5: 502"]}`,
			wantURL: "http://localhost:8222/api/channels/test-channel/review/dedup",
			want: "Checked 4 comment(s); removed 2.\n" +
				"- kept c1, removed c2, c3: same nil check\n" +
				"- trimmed c6 to what c1 doesn't cover: bundles the nil check\n" +
				"- moved c4 from line 7 to 9\n" +
				"- related c1, c4: same root cause\n" +
				"- verdict c1 real: no nil check\n" +
				"- error: deleting c5: 502\n",
		},
		{
			name:    "nothing to check",
			args:    map[string]any{},
			body:    `{"removed":[],"clusters":[],"related":[],"moved":[],"checked":0}`,
			wantURL: "http://localhost:8222/api/channels/test-channel/review/dedup",
			want:    "Nothing to dedup: no file has an agent comment next to another comment.",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			var gotURL, gotMethod string
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				gotURL, gotMethod = req.URL.String(), req.Method
				return jsonResponse(200, tc.body), nil
			}
			text, isError := s.callTool("dedup_review_findings", tc.args)
			require.False(s.T(), isError)
			require.Equal(s.T(), "POST", gotMethod)
			require.Equal(s.T(), tc.wantURL, gotURL)
			require.Equal(s.T(), tc.want, text)
		})
	}
}

func (s *ReviewToolSuite) TestDedupReviewFindingsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "dedup_review_findings",
		args:         map[string]any{},
		apiStatus:    409,
		apiBody:      `a review run is in progress`,
		decodeStatus: 200,
	})
}

// The review tools act on the agent's own channel only: none takes a
// channel_id, so naming another channel is refused before any API call.
func (s *ReviewToolSuite) TestReviewToolsRefuseAnotherChannel() {
	tools := map[string]map[string]any{
		"get_review_comments":      {},
		"dedup_review_findings":    {},
		"delete_review_comment":    {"comment_id": "c1"},
		"update_review_comment":    {"comment_id": "c1", "body": "b"},
		"push_review_comment":      {"comment_id": "c1"},
		"push_all_review_comments": {},
	}
	for tool, args := range tools {
		s.Run(tool, func() {
			called := false
			s.httpClient.doFunc = func(*http.Request) (*http.Response, error) {
				called = true
				return jsonResponse(200, `{}`), nil
			}
			withChannel := map[string]any{"channel_id": "other"}
			for k, v := range args {
				withChannel[k] = v
			}
			res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{Name: tool, Arguments: withChannel})
			if err == nil {
				require.True(s.T(), res.IsError)
			}
			require.False(s.T(), called)
		})
	}
}

func (s *ReviewToolSuite) TestReviewCommentTools() {
	cases := []struct {
		name       string
		tool       string
		args       map[string]any
		respStatus int
		respBody   string
		wantMethod string
		wantURL    string
		wantBody   string
		want       string
	}{
		{
			name: "delete", tool: "delete_review_comment", args: map[string]any{"comment_id": "c/1"},
			respStatus: 204, wantMethod: "DELETE",
			wantURL: "http://localhost:8222/api/channels/test-channel/review/comments/c%2F1",
			want:    "Deleted review comment c/1.",
		},
		{
			name: "update", tool: "update_review_comment", args: map[string]any{"comment_id": "c1", "body": "Nil map write.\n\nAlso flagged: reload."},
			respStatus: 200, respBody: `{"id":"c1","path":"a.go","line":3,"body":"Nil map write.\n\nAlso flagged: reload."}`,
			wantMethod: "PATCH", wantURL: "http://localhost:8222/api/channels/test-channel/review/comments/c1",
			wantBody: `{"body":"Nil map write.\n\nAlso flagged: reload."}`,
			want:     "Updated review comment c1 at a.go:3.",
		},
		{
			name: "push", tool: "push_review_comment", args: map[string]any{"comment_id": "c1"},
			respStatus: 200, respBody: `{"pushed":true}`,
			wantMethod: "POST", wantURL: "http://localhost:8222/api/channels/test-channel/review/comments/c1/push",
			want: "Pushed review comment c1 to the PR.",
		},
		{
			name: "push already pushed", tool: "push_review_comment", args: map[string]any{"comment_id": "c1"},
			respStatus: 200, respBody: `{"pushed":true,"already":true}`,
			wantMethod: "POST", wantURL: "http://localhost:8222/api/channels/test-channel/review/comments/c1/push",
			want: "Review comment c1 was already pushed.",
		},
		{
			name: "push all", tool: "push_all_review_comments", args: map[string]any{},
			respStatus: 200, respBody: `{"pushed":2,"failed":1,"errors":["c3: 422"]}`,
			wantMethod: "POST", wantURL: "http://localhost:8222/api/channels/test-channel/review/push-all",
			want: "Pushed 2 review comment(s) to the PR; 1 failed.\n- error: c3: 422\n",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			var gotMethod, gotURL, gotBody string
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				gotMethod, gotURL = req.Method, req.URL.String()
				if req.Body != nil {
					b, _ := io.ReadAll(req.Body)
					gotBody = string(b)
				}
				if tc.respBody == "" {
					return noContentResponse(tc.respStatus), nil
				}
				return jsonResponse(tc.respStatus, tc.respBody), nil
			}
			text, isError := s.callTool(tc.tool, tc.args)
			require.False(s.T(), isError, text)
			require.Equal(s.T(), tc.wantMethod, gotMethod)
			require.Equal(s.T(), tc.wantURL, gotURL)
			require.Equal(s.T(), tc.wantBody, gotBody)
			require.Equal(s.T(), tc.want, text)
		})
	}
}

func (s *ReviewToolSuite) TestReviewCommentToolsNeedArgs() {
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"delete_review_comment", map[string]any{"comment_id": ""}, "comment_id is required"},
		{"update_review_comment", map[string]any{"comment_id": "", "body": "b"}, "comment_id is required"},
		{"update_review_comment", map[string]any{"comment_id": "c1", "body": "  "}, "body is required"},
		{"push_review_comment", map[string]any{"comment_id": ""}, "comment_id is required"},
	}
	for _, tc := range cases {
		s.Run(tc.tool+" "+tc.want, func() {
			s.httpClient.doFunc = func(*http.Request) (*http.Response, error) {
				s.T().Fatal("no API call expected")
				return nil, nil
			}
			text, isError := s.callTool(tc.tool, tc.args)
			require.True(s.T(), isError)
			require.Equal(s.T(), tc.want, text)
		})
	}
}

func (s *ReviewToolSuite) TestReviewCommentToolsErrors() {
	specs := []toolErrorSpec{
		{tool: "delete_review_comment", args: map[string]any{"comment_id": "gh-1"}, apiStatus: 403, apiBody: "agents can only delete agent comments"},
		{tool: "update_review_comment", args: map[string]any{"comment_id": "c1", "body": "b"}, apiStatus: 409, apiBody: "only unpushed agent comments can be edited", decodeStatus: 200},
		{tool: "push_review_comment", args: map[string]any{"comment_id": "c1"}, apiStatus: 404, apiBody: "comment not found", decodeStatus: 200},
		{tool: "push_all_review_comments", args: map[string]any{}, apiStatus: 403, apiBody: "agents can only change review comments in their own channel", decodeStatus: 200},
	}
	for _, spec := range specs {
		s.Run(spec.tool, func() { s.runToolErrorCases(spec) })
	}
}
