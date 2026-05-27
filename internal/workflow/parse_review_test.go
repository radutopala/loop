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
	parseReviewOutput(`{"status":"ready","comments":[{"id":"x"}]}`, rc)
	parseReviewOutput(`{"status":"ready","comments":[{"id":"y"}]}`, rc)
	require.False(s.T(), rc.Review.SameAsPrev)
	require.Equal(s.T(), []string{"y"}, rc.Review.IDs)
	require.Equal(s.T(), []string{"x"}, rc.Review.PrevIDs)
}

func (s *ParseReviewSuite) TestInvalidJSONPreservesPrevIDsAndClearsTerminators() {
	rc := &RunContext{}
	rc.Review.IDs = []string{"existing"}
	parseReviewOutput("not json", rc)
	// IDs/PrevIDs intentionally NOT rotated on parse miss. A transient
	// parse failure between two identical good reviews would otherwise
	// mask the no-progress (SameAsPrev) signal on the next pass and burn
	// an extra fix iteration; keeping IDs preserves the last good baseline.
	require.Equal(s.T(), []string{"existing"}, rc.Review.IDs)
	require.Empty(s.T(), rc.Review.PrevIDs)
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

func (s *ParseReviewSuite) TestSkipsNonTerminalEnvelopesAndPicksTerminalOne() {
	// Forward scan skips an interim `status:"reviewing"` envelope (filtered by
	// the Status="ready"|"error" check in extractReviewJSON) and lands on the
	// CLI's terminal envelope.
	stdout := `{"status":"reviewing","comments":[]}` + "\n" +
		`{"status":"ready","no_comments":false,"comments":[{"id":"x"}]}` + "\n"
	rc := &RunContext{}
	parseReviewOutput(stdout, rc)
	require.Equal(s.T(), []string{"x"}, rc.Review.IDs)
}

func (s *ParseReviewSuite) TestPicksFirstValidEnvelopeWhenMultipleTerminalPresent() {
	// Forward semantics: the FIRST envelope with a recognized terminal Status
	// wins. A second valid envelope appearing later in stdout (debug echo, set-x
	// trace surfacing a cached envelope, future stdout pollution) must NOT
	// displace the real envelope. Backward scan would silently swap.
	stdout := `{"status":"ready","no_comments":false,"comments":[{"id":"first"}]}` + "\n" +
		`{"status":"ready","no_comments":true,"comments":[]}` + "\n"
	rc := &RunContext{}
	parseReviewOutput(stdout, rc)
	require.Equal(s.T(), []string{"first"}, rc.Review.IDs)
	require.False(s.T(), rc.Review.NoComments, "second envelope (no_comments=true) must not override the first")
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
	var out reviewEnvelope
	require.False(s.T(), extractReviewJSON("   \n\t  ", &out))
}

func (s *ParseReviewSuite) TestErrorStatusRotatesPrevButDoesNotTerminateLoop() {
	// status=="error" decodes via extractReviewJSON (the CLI distinguishes
	// it from "ready") but must NOT flip NoComments true — that would let
	// the seeded review-fix loop exit with a "clean" verdict whenever the
	// daemon reports an error. We still rotate PrevIDs so a subsequent
	// successful retry has the right baseline.
	rc := &RunContext{}
	rc.Review.IDs = []string{"prev1", "prev2"}
	parseReviewOutput(`{"status":"error","comments":[]}`, rc)
	require.Equal(s.T(), []string{"prev1", "prev2"}, rc.Review.PrevIDs)
	require.Nil(s.T(), rc.Review.IDs)
	require.False(s.T(), rc.Review.NoComments)
	require.False(s.T(), rc.Review.SameAsPrev)
	require.Empty(s.T(), rc.Review.CommentsJSON)
}

func (s *ParseReviewSuite) TestExtractReviewJSONRejectsUnrelatedEnvelope() {
	// A JSON object emitted to stdout by something other than the review
	// CLI (sidecar log, future stdout pollution) must not be accepted as
	// the envelope just because it parses — the seeded review-fix loop
	// would otherwise terminate with a false "no findings" verdict.
	rc := &RunContext{}
	parseReviewOutput(`{"level":"info","msg":"unrelated log line"}`+"\n", rc)
	require.False(s.T(), rc.Review.NoComments)
	require.False(s.T(), rc.Review.SameAsPrev)
	require.Nil(s.T(), rc.Review.Comments)
}

func (s *ParseReviewSuite) TestExtractReviewJSONRejectsMissingCommentsKey() {
	// `{"status":"ready"}` has a recognized status but no `comments` key.
	// Probe decode succeeds (status=ready, comments=nil), but the missing
	// key disqualifies the envelope: a no-findings response must explicitly
	// carry `comments: []` (the CLI always emits it). Without this check
	// the seeded review-fix loop would terminate with a false-clean verdict
	// when the daemon emits an incomplete envelope.
	rc := &RunContext{}
	parseReviewOutput(`{"status":"ready"}`, rc)
	require.False(s.T(), rc.Review.NoComments)
	require.False(s.T(), rc.Review.SameAsPrev)
	require.Nil(s.T(), rc.Review.Comments)
	require.True(s.T(), rc.Review.ParseFailed)
}

func (s *ParseReviewSuite) TestExtractReviewJSONRejectsTypeMismatchInComments() {
	// The probe decoder uses json.RawMessage for comments so a type
	// mismatch (string instead of object) passes the probe but fails the
	// strict-typed candidate decode. extractReviewJSON must reject the
	// envelope so parseReviewOutput surfaces ParseFailed.
	rc := &RunContext{}
	parseReviewOutput(`{"status":"ready","comments":["broken"]}`, rc)
	require.False(s.T(), rc.Review.NoComments)
	require.True(s.T(), rc.Review.ParseFailed)
	require.Nil(s.T(), rc.Review.Comments)
}
