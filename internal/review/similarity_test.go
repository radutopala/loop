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

// The pair that prompted this gate: one finding reported twice by
// consecutive iterations of a review loop. The tail is identical and only
// the opening sentence was rewritten, which is the shape a re-derived
// finding takes.
const (
	reportedOnce = "StoreBid treats an empty entry slice as an error, so a RecordBid call with a nil (or record-less) BidTelemetry now withdraws the bid, even though the same code path explicitly tolerates a nil bid when building the event.\n\n" +
		"Writer.RecordBid guards `if bid != nil` before reading bid.MAID, so a nil BidTelemetry is a supported input. With a bid cache attached, that same call reaches cacheBid -> bidCacheEntries(nil) -> nil slice -> StoreBid returns errNoBidCacheEntries without ever contacting the cache, so RecordBid fails and the caller converts an otherwise-valid response into a no-bid."
	reportedAgain = "StoreBid treats an empty entry slice as an error, so RecordBid with a nil or record-less BidTelemetry now withdraws the bid, although the same path explicitly tolerates a nil bid when building the event.\n\n" +
		"Writer.RecordBid guards `if bid != nil` before reading bid.MAID, so a nil BidTelemetry is a supported input. With a bid cache attached, that same call reaches cacheBid -> bidCacheEntries(nil) -> nil slice -> StoreBid returns errNoBidCacheEntries without contacting the cache, so RecordBid fails and the caller converts an otherwise-valid response into a no-bid."
	// A different claim about the same code, sharing its identifiers. This is
	// the case the threshold must not swallow.
	differentFinding = "The write is unbounded: StoreBid inherits the request context, so a slow cache keeps the auction goroutine parked for as long as the caller allows instead of failing fast on its own timeout."
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
