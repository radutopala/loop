package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

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
			{"path": "a.go", "line": 3, "side": "RIGHT", "body": "bug one"},
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
		{"id":"c1","path":"a.go","line":3,"side":"RIGHT","body":"bug one\nsecond line","pushed":false,"source":"agent"},
		{"id":"c2","path":"b.go","line":9,"side":"LEFT","body":"bug two","pushed":true,"source":"agent","github_id":77},
		{"id":"c3","path":"a.go","line":5,"side":"RIGHT","body":"human note","pushed":true,"source":"github","author":"octo","github_id":88,"outdated":true,"resolved":true},
		{"id":"c4","path":"a.go","line":7,"side":"RIGHT","body":"no source","pushed":false}
	]}}`

func (s *ReviewToolSuite) TestGetReviewFindings() {
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
				"[c1] a.go:3 RIGHT, agent, unpushed\n  bug one\n  second line\n",
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
			name:        "one file of another channel",
			args:        map[string]any{"channel_id": "other", "path": "b.go"},
			wantURL:     "http://localhost:8222/api/channels/other/review?diff=false",
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
			text, isError := s.callTool("get_review_findings", tc.args)
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

func (s *ReviewToolSuite) TestGetReviewFindingsNoPRAndError() {
	s.httpClient.doFunc = func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"present":true,"session":{"status":"error","error":"gh failed","comments":[]}}`), nil
	}
	text, isError := s.callTool("get_review_findings", map[string]any{})
	require.False(s.T(), isError)
	require.Equal(s.T(), "head_sha: \nstatus: error\nerror: gh failed\n\n0 of 0 comment(s):\n", text)
}

func (s *ReviewToolSuite) TestGetReviewFindingsNoSession() {
	s.httpClient.doFunc = func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"present":false}`), nil
	}
	text, isError := s.callTool("get_review_findings", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No review session for this channel")
}

func (s *ReviewToolSuite) TestGetReviewFindingsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "get_review_findings",
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
			body:    `{"removed":["c2","c3"],"clusters":[{"kept":"c1","removed":["c2","c3"],"reason":"same nil check"}],"related":[{"ids":["c1","c4"],"reason":"same root cause"}],"moved":[{"id":"c4","from":7,"to":9}],"checked":4,"errors":["deleting c5: 502"]}`,
			wantURL: "http://localhost:8222/api/channels/test-channel/review/dedup",
			want: "Checked 4 comment(s); removed 2.\n" +
				"- kept c1, removed c2, c3: same nil check\n" +
				"- moved c4 from line 7 to 9\n" +
				"- related c1, c4: same root cause\n" +
				"- error: deleting c5: 502\n",
		},
		{
			name:    "nothing to check",
			args:    map[string]any{"channel_id": "other"},
			body:    `{"removed":[],"clusters":[],"related":[],"moved":[],"checked":0}`,
			wantURL: "http://localhost:8222/api/channels/other/review/dedup",
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
