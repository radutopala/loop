package review

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SimilaritySuite struct {
	suite.Suite
}

func TestSimilaritySuite(t *testing.T) {
	suite.Run(t, new(SimilaritySuite))
}

// Modelled on the pair that prompted this gate: one finding reported twice by
// consecutive iterations of a review loop. The subject is invented, but the
// shape is the real one — the tail is identical and only the opening sentence
// was rewritten, which is what a re-derived finding looks like.
const (
	reportedOnce = "Flush treats an empty batch as an error, so an Append call with a nil (or record-less) Envelope now drops the record, even though the same code path explicitly tolerates a nil payload when building the entry.\n\n" +
		"Writer.Append guards `if payload != nil` before reading payload.Key, so a nil Envelope is a supported input. With a batch buffer attached, that same call reaches bufferAppend -> batchEntries(nil) -> nil slice -> Flush returns errNoBatchEntries without ever contacting the buffer, so Append fails and the caller converts an otherwise-valid write into a drop."
	reportedAgain = "Flush treats an empty batch as an error, so Append with a nil or record-less Envelope now drops the record, although the same path explicitly tolerates a nil payload when building the entry.\n\n" +
		"Writer.Append guards `if payload != nil` before reading payload.Key, so a nil Envelope is a supported input. With a batch buffer attached, that same call reaches bufferAppend -> batchEntries(nil) -> nil slice -> Flush returns errNoBatchEntries without contacting the buffer, so Append fails and the caller converts an otherwise-valid write into a drop."
	// A different claim about the same code, sharing its identifiers. This is
	// the case the threshold must not swallow.
	differentFinding = "The write is unbounded: Flush inherits the request context, so a slow buffer keeps the worker goroutine parked for as long as the caller allows instead of failing fast on its own timeout."
)

func (s *SimilaritySuite) TestNearDuplicate() {
	cases := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"same finding rewritten", reportedOnce, reportedAgain, true},
		{"identical", reportedOnce, reportedOnce, true},
		{"different claim, same identifiers", reportedOnce, differentFinding, false},
		{"unrelated", "the lock is never released on the error path", differentFinding, false},
		{"punctuation and case only", "Returns `errNoEntries`, always.", "returns errNoEntries always", true},
		{"same words, different order", "cache write fails before the timeout", "before the timeout fails cache write", false},
		{"one word, same", "typo", "Typo!", true},
		{"one word, different", "typo", "leak", false},
		{"empty against text", "", reportedOnce, false},
		{"both empty", "", "", false},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, nearDuplicate(tc.a, tc.b))
		})
	}
}

// The gap between a re-report and a distinct finding is what the threshold
// sits in, so assert the gap itself rather than only which side of it each
// pair lands on — a scoring change that narrows it should fail here.
func (s *SimilaritySuite) TestSimilarityLeavesRoomAroundTheThreshold() {
	dup := bodySimilarity(reportedOnce, reportedAgain)
	distinct := bodySimilarity(reportedOnce, differentFinding)
	require.Greater(s.T(), dup, duplicateThreshold+0.1)
	require.Less(s.T(), distinct, duplicateThreshold-0.1)
}

func (s *SimilaritySuite) TestSimilarityIsSymmetricAndBounded() {
	require.InDelta(s.T(), bodySimilarity(reportedOnce, reportedAgain), bodySimilarity(reportedAgain, reportedOnce), 1e-9)
	require.Equal(s.T(), 1.0, bodySimilarity(reportedOnce, reportedOnce))
	require.Equal(s.T(), 0.0, bodySimilarity("", ""))
}

func (s *SimilaritySuite) TestNormalizeWordsSplitsOnEverythingElse() {
	require.Equal(s.T(), []string{"foo", "bar", "baz", "12"}, normalizeWords("Foo.bar  `BAZ` -> 12"))
	require.Empty(s.T(), normalizeWords("  ->  "))
}

func (s *SimilaritySuite) TestBigramsAreASet() {
	require.Len(s.T(), bigrams(normalizeWords("a b a b")), 2)
	require.Empty(s.T(), bigrams(normalizeWords("single")))
}

func (s *SimilaritySuite) TestSameAnchor() {
	base := &Comment{Path: "a.go", Line: 12, Side: "RIGHT"}
	cases := []struct {
		name  string
		other *Comment
		want  bool
	}{
		{"identical", &Comment{Path: "a.go", Line: 12, Side: "RIGHT"}, true},
		{"empty side means RIGHT", &Comment{Path: "a.go", Line: 12}, true},
		{"other side", &Comment{Path: "a.go", Line: 12, Side: "LEFT"}, false},
		{"other line", &Comment{Path: "a.go", Line: 13, Side: "RIGHT"}, false},
		{"other file", &Comment{Path: "b.go", Line: 12, Side: "RIGHT"}, false},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, sameAnchor(base, tc.other))
		})
	}
}
