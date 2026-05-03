package evolution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EvolutionSuite struct {
	suite.Suite
}

func TestEvolutionSuite(t *testing.T) {
	suite.Run(t, new(EvolutionSuite))
}

type fakeReader struct {
	commits []CommitFiles
	err     error
	called  bool
	dir     string
	since   int
	max     int
}

func (r *fakeReader) Read(_ context.Context, dirPath string, sinceMonths, maxCommits int) ([]CommitFiles, error) {
	r.called = true
	r.dir = dirPath
	r.since = sinceMonths
	r.max = maxCommits
	if r.err != nil {
		return nil, r.err
	}
	return r.commits, nil
}

func (s *EvolutionSuite) ts(day int) time.Time {
	return time.Date(2026, 1, day, 12, 0, 0, 0, time.UTC)
}

func (s *EvolutionSuite) TestAnalyzeNilReaderReturnsError() {
	_, err := Analyze(context.Background(), nil, "/repo", Options{})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reader is nil")
}

func (s *EvolutionSuite) TestAnalyzeReaderErrorIsWrapped() {
	r := &fakeReader{err: errors.New("boom")}
	_, err := Analyze(context.Background(), r, "/repo", Options{})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "read history")
	require.Contains(s.T(), err.Error(), "boom")
}

func (s *EvolutionSuite) TestAnalyzeNoCommitsReturnsErrNoHistory() {
	r := &fakeReader{commits: nil}
	_, err := Analyze(context.Background(), r, "/repo", Options{})
	require.ErrorIs(s.T(), err, ErrNoHistory)
}

func (s *EvolutionSuite) TestAnalyzeUsesProvidedOptions() {
	r := &fakeReader{commits: []CommitFiles{{Hash: "h", Author: "a", Timestamp: s.ts(1), Files: []string{"x.go"}}}}
	_, err := Analyze(context.Background(), r, "/repo", Options{SinceMonths: 6, MaxCommits: 100, MinCoupling: 0.1, MaxCouplePairs: 5, MaxHotspots: 5, MaxBusFactor: 5, MinBusFactor: 0.7})
	require.NoError(s.T(), err)
	require.Equal(s.T(), 6, r.since)
	require.Equal(s.T(), 100, r.max)
}

func (s *EvolutionSuite) TestAnalyzeAppliesDefaultsForZeroOptions() {
	r := &fakeReader{commits: []CommitFiles{{Hash: "h", Author: "a", Timestamp: s.ts(1), Files: []string{"x.go"}}}}
	_, err := Analyze(context.Background(), r, "/repo", Options{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), DefaultSinceMonths, r.since)
	require.Equal(s.T(), DefaultMaxCommits, r.max)
}

func (s *EvolutionSuite) TestAnalyzeFlagsShallowWarningUnder50Commits() {
	r := &fakeReader{commits: []CommitFiles{{Hash: "h", Author: "a", Timestamp: s.ts(1), Files: []string{"x.go"}}}}
	res, err := Analyze(context.Background(), r, "/repo", Options{})
	require.NoError(s.T(), err)
	require.True(s.T(), res.ShallowWarning)
	require.Equal(s.T(), 1, res.CommitsScanned)
}

func (s *EvolutionSuite) TestAnalyzeNoShallowWarningWith50PlusCommits() {
	commits := make([]CommitFiles, 60)
	for i := range commits {
		commits[i] = CommitFiles{Hash: "h", Author: "a", Timestamp: s.ts(1 + i%28), Files: []string{"x.go"}}
	}
	r := &fakeReader{commits: commits}
	res, err := Analyze(context.Background(), r, "/repo", Options{})
	require.NoError(s.T(), err)
	require.False(s.T(), res.ShallowWarning)
}

func (s *EvolutionSuite) TestCouplingFindsAlwaysTogetherPairs() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"a.go", "b.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"a.go", "b.go"}},
		{Author: "a", Timestamp: s.ts(3), Files: []string{"a.go", "b.go"}},
	}
	pairs := coupling(commits, Options{}.resolved())
	require.Len(s.T(), pairs, 1)
	require.Equal(s.T(), "a.go", pairs[0].FileA)
	require.Equal(s.T(), "b.go", pairs[0].FileB)
	require.InDelta(s.T(), 1.0, pairs[0].Jaccard, 1e-9)
	require.Equal(s.T(), 3, pairs[0].CoChangeCount)
}

func (s *EvolutionSuite) TestCouplingFiltersByMinCoupling() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"a.go", "b.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"a.go"}},
		{Author: "a", Timestamp: s.ts(3), Files: []string{"a.go"}},
		{Author: "a", Timestamp: s.ts(4), Files: []string{"b.go"}},
	}
	pairs := coupling(commits, Options{MinCoupling: 0.9}.resolved())
	require.Empty(s.T(), pairs)
}

func (s *EvolutionSuite) TestCouplingDetectsCrossModule() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"internal/a.go", "cmd/main.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"internal/a.go", "cmd/main.go"}},
	}
	pairs := coupling(commits, Options{}.resolved())
	require.Len(s.T(), pairs, 1)
	require.True(s.T(), pairs[0].CrossModule)
}

func (s *EvolutionSuite) TestCouplingNormalizesPairOrder() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"z.go", "a.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"z.go", "a.go"}},
	}
	pairs := coupling(commits, Options{}.resolved())
	require.Len(s.T(), pairs, 1)
	require.Equal(s.T(), "a.go", pairs[0].FileA)
	require.Equal(s.T(), "z.go", pairs[0].FileB)
}

func (s *EvolutionSuite) TestCouplingCapsToMaxPairs() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"a.go", "b.go", "c.go", "d.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"a.go", "b.go", "c.go", "d.go"}},
	}
	pairs := coupling(commits, Options{MaxCouplePairs: 2, MinCoupling: 0.1}.resolved())
	require.Len(s.T(), pairs, 2)
}

func (s *EvolutionSuite) TestCouplingSortsByJaccardDesc() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"a.go", "b.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"a.go", "b.go"}},
		{Author: "a", Timestamp: s.ts(3), Files: []string{"c.go", "d.go"}},
		{Author: "a", Timestamp: s.ts(4), Files: []string{"c.go"}},
	}
	pairs := coupling(commits, Options{MinCoupling: 0.4}.resolved())
	require.Len(s.T(), pairs, 2)
	require.GreaterOrEqual(s.T(), pairs[0].Jaccard, pairs[1].Jaccard)
}

func (s *EvolutionSuite) TestCouplingSortsByFileWhenJaccardTies() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"a.go", "b.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"c.go", "d.go"}},
	}
	pairs := coupling(commits, Options{}.resolved())
	require.Len(s.T(), pairs, 2)
	require.Equal(s.T(), "a.go", pairs[0].FileA)
}

func (s *EvolutionSuite) TestCouplingTieBreaksOnFileBWhenAEquals() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"a.go", "b.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"a.go", "c.go"}},
	}
	pairs := coupling(commits, Options{MinCoupling: 0.4}.resolved())
	require.Len(s.T(), pairs, 2)
	require.Equal(s.T(), "a.go", pairs[0].FileA)
	require.Equal(s.T(), "b.go", pairs[0].FileB)
	require.Equal(s.T(), "c.go", pairs[1].FileB)
}

func (s *EvolutionSuite) TestCouplingSkipsZeroUnion() {
	pairs := coupling([]CommitFiles{}, Options{}.resolved())
	require.Empty(s.T(), pairs)
}

func (s *EvolutionSuite) TestHotspotsRanksFilesByCommitCount() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"a.go"}},
		{Author: "a", Timestamp: s.ts(2), Files: []string{"a.go", "b.go"}},
		{Author: "a", Timestamp: s.ts(3), Files: []string{"a.go"}},
	}
	h := hotspots(commits, Options{}.resolved())
	require.Len(s.T(), h, 2)
	require.Equal(s.T(), "a.go", h[0].File)
	require.Equal(s.T(), 3, h[0].ChangeCount)
	require.Equal(s.T(), s.ts(3), h[0].LastChangedAt)
}

func (s *EvolutionSuite) TestHotspotsCapsToMaxHotspots() {
	commits := make([]CommitFiles, 0)
	for range 5 {
		commits = append(commits, CommitFiles{Author: "a", Timestamp: s.ts(1), Files: []string{"a.go", "b.go", "c.go", "d.go"}})
	}
	h := hotspots(commits, Options{MaxHotspots: 2}.resolved())
	require.Len(s.T(), h, 2)
}

func (s *EvolutionSuite) TestHotspotsTieBreaksByFileName() {
	commits := []CommitFiles{
		{Author: "a", Timestamp: s.ts(1), Files: []string{"z.go", "a.go"}},
	}
	h := hotspots(commits, Options{}.resolved())
	require.Len(s.T(), h, 2)
	require.Equal(s.T(), "a.go", h[0].File)
}

func (s *EvolutionSuite) TestBusFactorFlagsSingleAuthorFiles() {
	commits := []CommitFiles{
		{Author: "alice", Timestamp: s.ts(1), Files: []string{"solo.go"}},
		{Author: "alice", Timestamp: s.ts(2), Files: []string{"solo.go"}},
		{Author: "alice", Timestamp: s.ts(3), Files: []string{"solo.go"}},
	}
	risks := busFactor(commits, Options{}.resolved())
	require.Len(s.T(), risks, 1)
	require.Equal(s.T(), "solo.go", risks[0].File)
	require.Equal(s.T(), "alice", risks[0].SoleAuthor)
	require.InDelta(s.T(), 1.0, risks[0].SoleAuthorRatio, 1e-9)
	require.Equal(s.T(), 3, risks[0].TotalCommits)
	require.Equal(s.T(), -1, risks[0].DaysSinceLastOther)
}

func (s *EvolutionSuite) TestBusFactorIgnoresFilesBelowThreshold() {
	commits := []CommitFiles{
		{Author: "alice", Timestamp: s.ts(1), Files: []string{"shared.go"}},
		{Author: "bob", Timestamp: s.ts(2), Files: []string{"shared.go"}},
	}
	risks := busFactor(commits, Options{MinBusFactor: 0.9}.resolved())
	require.Empty(s.T(), risks)
}

func (s *EvolutionSuite) TestBusFactorComputesDaysSinceOtherAuthor() {
	commits := []CommitFiles{
		{Author: "alice", Timestamp: s.ts(1), Files: []string{"f.go"}},
		{Author: "bob", Timestamp: s.ts(5), Files: []string{"f.go"}},
		{Author: "alice", Timestamp: s.ts(15), Files: []string{"f.go"}},
		{Author: "alice", Timestamp: s.ts(16), Files: []string{"f.go"}},
		{Author: "alice", Timestamp: s.ts(17), Files: []string{"f.go"}},
		{Author: "alice", Timestamp: s.ts(18), Files: []string{"f.go"}},
		{Author: "alice", Timestamp: s.ts(19), Files: []string{"f.go"}},
	}
	risks := busFactor(commits, Options{MinBusFactor: 0.7}.resolved())
	require.Len(s.T(), risks, 1)
	require.Equal(s.T(), "alice", risks[0].SoleAuthor)
	require.Equal(s.T(), s.ts(5), risks[0].LastOtherAuthorAt)
	require.Equal(s.T(), 14, risks[0].DaysSinceLastOther)
}

func (s *EvolutionSuite) TestBusFactorTieBreakingPicksAlphabeticallyFirst() {
	commits := []CommitFiles{
		{Author: "zorro", Timestamp: s.ts(1), Files: []string{"f.go"}},
		{Author: "alice", Timestamp: s.ts(2), Files: []string{"f.go"}},
	}
	risks := busFactor(commits, Options{MinBusFactor: 0.4}.resolved())
	require.Len(s.T(), risks, 1)
	require.Equal(s.T(), "alice", risks[0].SoleAuthor)
}

func (s *EvolutionSuite) TestBusFactorSortsByRatioThenCommitsThenName() {
	commits := []CommitFiles{
		{Author: "alice", Timestamp: s.ts(1), Files: []string{"a.go", "b.go", "c.go"}},
		{Author: "alice", Timestamp: s.ts(2), Files: []string{"a.go", "b.go"}},
		{Author: "alice", Timestamp: s.ts(3), Files: []string{"a.go"}},
	}
	risks := busFactor(commits, Options{MinBusFactor: 0.9}.resolved())
	require.Len(s.T(), risks, 3)
	require.Equal(s.T(), "a.go", risks[0].File)
	require.GreaterOrEqual(s.T(), risks[0].TotalCommits, risks[1].TotalCommits)
}

func (s *EvolutionSuite) TestBusFactorSortsByRatioBeforeCommitCount() {
	commits := []CommitFiles{
		{Author: "alice", Timestamp: s.ts(1), Files: []string{"a.go"}},
		{Author: "alice", Timestamp: s.ts(2), Files: []string{"a.go"}},
		{Author: "alice", Timestamp: s.ts(3), Files: []string{"a.go"}},
		{Author: "bob", Timestamp: s.ts(4), Files: []string{"a.go"}},
		{Author: "alice", Timestamp: s.ts(1), Files: []string{"b.go"}},
		{Author: "alice", Timestamp: s.ts(2), Files: []string{"b.go"}},
	}
	risks := busFactor(commits, Options{MinBusFactor: 0.7}.resolved())
	require.Len(s.T(), risks, 2)
	// b.go is 100% alice (2/2 = 1.0), a.go is 75% alice (3/4 = 0.75).
	// Sort by ratio desc → b.go first even though it has fewer commits.
	require.Equal(s.T(), "b.go", risks[0].File)
	require.Greater(s.T(), risks[0].SoleAuthorRatio, risks[1].SoleAuthorRatio)
}

func (s *EvolutionSuite) TestBusFactorCapsToMaxBusFactor() {
	commits := []CommitFiles{
		{Author: "alice", Timestamp: s.ts(1), Files: []string{"a.go", "b.go", "c.go", "d.go"}},
	}
	risks := busFactor(commits, Options{MaxBusFactor: 2, MinBusFactor: 0.5}.resolved())
	require.Len(s.T(), risks, 2)
}

func (s *EvolutionSuite) TestTopLevelExtractsFirstSegment() {
	require.Equal(s.T(), "internal", topLevel("internal/api/x.go"))
	require.Equal(s.T(), "main.go", topLevel("main.go"))
}

// ───── ExecReader / parseGitLog ─────

type recordingRunner struct {
	out  []byte
	err  error
	dir  string
	name string
	args []string
}

func (r *recordingRunner) run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	r.dir = dir
	r.name = name
	r.args = args
	return r.out, r.err
}

func (s *EvolutionSuite) TestNewExecReaderHasDefaultRunner() {
	r := NewExecReader()
	require.NotNil(s.T(), r)
	require.NotNil(s.T(), r.run)
}

func (s *EvolutionSuite) TestExecReaderRequiresDirPath() {
	r := &ExecReader{run: defaultRunner}
	_, err := r.Read(context.Background(), "", 12, 100)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "dir path required")
}

func (s *EvolutionSuite) TestExecReaderWrapsRunError() {
	rr := &recordingRunner{err: errors.New("git missing")}
	r := &ExecReader{run: rr.run}
	_, err := r.Read(context.Background(), "/repo", 12, 100)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "git log")
}

func (s *EvolutionSuite) TestExecReaderInvokesGitWithExpectedArgs() {
	rr := &recordingRunner{out: []byte("")}
	r := &ExecReader{run: rr.run}
	_, _ = r.Read(context.Background(), "/repo", 6, 250)
	require.Equal(s.T(), "/repo", rr.dir)
	require.Equal(s.T(), "git", rr.name)
	require.Contains(s.T(), rr.args, "--since=6.months.ago")
	require.Contains(s.T(), rr.args, "--max-count=250")
	require.Contains(s.T(), rr.args, "--no-merges")
}

func (s *EvolutionSuite) TestParseGitLogSingleCommit() {
	stream := "COMMIT\x00abc\x00alice\x002026-01-01T12:00:00Z\nfile.go\n\n"
	commits, err := parseGitLog(stream)
	require.NoError(s.T(), err)
	require.Len(s.T(), commits, 1)
	require.Equal(s.T(), "abc", commits[0].Hash)
	require.Equal(s.T(), "alice", commits[0].Author)
	require.Equal(s.T(), []string{"file.go"}, commits[0].Files)
}

func (s *EvolutionSuite) TestParseGitLogMultipleCommitsBackToBack() {
	stream := "COMMIT\x00a\x00alice\x002026-01-01T00:00:00Z\nf1.go\nCOMMIT\x00b\x00bob\x002026-01-02T00:00:00Z\nf2.go\n"
	commits, err := parseGitLog(stream)
	require.NoError(s.T(), err)
	require.Len(s.T(), commits, 2)
	require.Equal(s.T(), "a", commits[0].Hash)
	require.Equal(s.T(), "b", commits[1].Hash)
}

func (s *EvolutionSuite) TestParseGitLogSkipsEmptyFileCommits() {
	stream := "COMMIT\x00a\x00alice\x002026-01-01T00:00:00Z\n\nCOMMIT\x00b\x00bob\x002026-01-02T00:00:00Z\nf.go\n"
	commits, err := parseGitLog(stream)
	require.NoError(s.T(), err)
	require.Len(s.T(), commits, 1)
	require.Equal(s.T(), "b", commits[0].Hash)
}

func (s *EvolutionSuite) TestParseGitLogTrailingCommitWithoutBlankLine() {
	stream := "COMMIT\x00a\x00alice\x002026-01-01T00:00:00Z\nf.go"
	commits, err := parseGitLog(stream)
	require.NoError(s.T(), err)
	require.Len(s.T(), commits, 1)
}

func (s *EvolutionSuite) TestParseGitLogRejectsMalformedHeader() {
	stream := "COMMIT\x00a\x00alice\nf.go\n"
	_, err := parseGitLog(stream)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "malformed commit header")
}

func (s *EvolutionSuite) TestParseGitLogRejectsBadTimestamp() {
	stream := "COMMIT\x00a\x00alice\x00not-a-time\nf.go\n"
	_, err := parseGitLog(stream)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "bad timestamp")
}

func (s *EvolutionSuite) TestParseGitLogIgnoresStrayLineWithNoCurrent() {
	stream := "stray\nCOMMIT\x00a\x00alice\x002026-01-01T00:00:00Z\nf.go\n"
	commits, err := parseGitLog(stream)
	require.NoError(s.T(), err)
	require.Len(s.T(), commits, 1)
}

func (s *EvolutionSuite) TestParseGitLogReportsScannerError() {
	// Build one line longer than the 1MB scanner cap to force bufio.ErrTooLong.
	huge := make([]byte, 1024*1024+10)
	for i := range huge {
		huge[i] = 'x'
	}
	_, err := parseGitLog(string(huge))
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "scan")
}

func (s *EvolutionSuite) TestParseGitLogEmptyInputReturnsEmptySlice() {
	commits, err := parseGitLog("")
	require.NoError(s.T(), err)
	require.Empty(s.T(), commits)
}

func (s *EvolutionSuite) TestDefaultRunnerExecutesCommand() {
	out, err := defaultRunner(context.Background(), "/", "true")
	require.NoError(s.T(), err)
	require.Empty(s.T(), out)
}
