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
