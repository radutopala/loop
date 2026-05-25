package workflow

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ParseReviewSuite struct {
	suite.Suite
}

func TestParseReviewSuite(t *testing.T) { suite.Run(t, new(ParseReviewSuite)) }

func (s *ParseReviewSuite) TestParsesCommentsAndSortsIDs() {
	rc := &RunContext{}
	parseReviewOutput(`{"status":"ready","no_comments":false,"comments":[{"id":"b"},{"id":"a"}]}`, rc)
	require.Equal(s.T(), []string{"a", "b"}, rc.Review.IDs)
	require.False(s.T(), rc.Review.NoComments)
	require.False(s.T(), rc.Review.SameAsPrev)
	require.NotEmpty(s.T(), rc.Review.CommentsJSON)
}

func (s *ParseReviewSuite) TestNoCommentsTrue() {
	rc := &RunContext{}
	parseReviewOutput(`{"status":"ready","no_comments":true,"comments":[]}`, rc)
	require.True(s.T(), rc.Review.NoComments)
	require.Empty(s.T(), rc.Review.IDs)
	require.Empty(s.T(), rc.Review.CommentsJSON)
}

func (s *ParseReviewSuite) TestSameAsPrevDetectsRepeatedIDs() {
	rc := &RunContext{}
	parseReviewOutput(`{"status":"ready","comments":[{"id":"x"},{"id":"y"}]}`, rc)
	require.False(s.T(), rc.Review.SameAsPrev)
	require.Equal(s.T(), []string{"x", "y"}, rc.Review.IDs)
	// second iteration with same IDs in different order should still match
	parseReviewOutput(`{"status":"ready","comments":[{"id":"y"},{"id":"x"}]}`, rc)
	require.True(s.T(), rc.Review.SameAsPrev)
	require.Equal(s.T(), []string{"x", "y"}, rc.Review.PrevIDs)
}

func (s *ParseReviewSuite) TestDifferingIDsNotSameAsPrev() {
	rc := &RunContext{}
	parseReviewOutput(`{"comments":[{"id":"x"}]}`, rc)
	parseReviewOutput(`{"comments":[{"id":"y"}]}`, rc)
	require.False(s.T(), rc.Review.SameAsPrev)
	require.Equal(s.T(), []string{"y"}, rc.Review.IDs)
	require.Equal(s.T(), []string{"x"}, rc.Review.PrevIDs)
}

func (s *ParseReviewSuite) TestInvalidJSONResetsState() {
	rc := &RunContext{}
	rc.Review.IDs = []string{"existing"}
	parseReviewOutput("not json", rc)
	require.Nil(s.T(), rc.Review.IDs)
	require.Equal(s.T(), []string{"existing"}, rc.Review.PrevIDs)
	require.False(s.T(), rc.Review.NoComments)
	// SameAsPrev must be false on parse failure so the loop's stop
	// condition doesn't trip and silently terminate with "no findings".
	require.False(s.T(), rc.Review.SameAsPrev)
}

func (s *ParseReviewSuite) TestEmptyStdoutDoesNotTerminateLoop() {
	rc := &RunContext{}
	parseReviewOutput("", rc)
	// Prev was empty, but the parse miss is a real signal (daemon/CLI bug,
	// $API_URL misconfig, future stdout pollution). Both gates stay false
	// so `{{ or .Review.NoComments .Review.SameAsPrev }}` is false and the
	// loop keeps iterating up to maxIter instead of completing as if the
	// review had returned a clean result.
	require.False(s.T(), rc.Review.NoComments)
	require.False(s.T(), rc.Review.SameAsPrev)
}

func (s *ParseReviewSuite) TestParsesJSONAfterDockerproxyPreamble() {
	// Real container stdout: dockerproxy logs to stdout before the CLI's
	// compact JSON line. The parser must still locate the JSON.
	stdout := "time=2026-05-25T11:48:18.834+03:00 level=INFO msg=\"loop-dockerproxy started\" socket=/var/run/docker.sock\n" +
		`{"status":"ready","no_comments":false,"comments":[{"id":"db0cc90b5f4e","path":"foo.go","line":10,"body":"nit"}]}` + "\n"
	rc := &RunContext{}
	parseReviewOutput(stdout, rc)
	require.Equal(s.T(), []string{"db0cc90b5f4e"}, rc.Review.IDs)
	require.False(s.T(), rc.Review.NoComments)
	require.Contains(s.T(), rc.Review.CommentsJSON, "db0cc90b5f4e")
}

func (s *ParseReviewSuite) TestPicksLastJSONLineWhenMultiplePresent() {
	// A stray earlier JSON line (unlikely in practice but defensive) must
	// be overridden by the CLI's final envelope.
	stdout := `{"status":"reviewing","comments":[]}` + "\n" +
		`{"status":"ready","no_comments":false,"comments":[{"id":"x"}]}` + "\n"
	rc := &RunContext{}
	parseReviewOutput(stdout, rc)
	require.Equal(s.T(), []string{"x"}, rc.Review.IDs)
}

func (s *ParseReviewSuite) TestFallsBackToTrimmedFullStdout() {
	// Pretty-printed (multi-line) JSON, no single line that parses on its
	// own. The whole-trimmed-stdout fallback should still recover it.
	stdout := "{\n  \"status\": \"ready\",\n  \"comments\": [{\"id\": \"a\"}]\n}\n"
	rc := &RunContext{}
	parseReviewOutput(stdout, rc)
	require.Equal(s.T(), []string{"a"}, rc.Review.IDs)
}

func (s *ParseReviewSuite) TestExtractReviewJSONReturnsFalseOnEmpty() {
	// Whitespace-only stdout exercises the explicit empty-string return.
	var out struct {
		Status string `json:"status"`
	}
	require.False(s.T(), extractReviewJSON("   \n\t  ", &out))
}
