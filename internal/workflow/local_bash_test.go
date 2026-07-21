package workflow

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LocalBashSuite struct {
	suite.Suite
	runner *LocalBashRunner
}

func TestLocalBashSuite(t *testing.T) {
	suite.Run(t, new(LocalBashSuite))
}

func (s *LocalBashSuite) SetupTest() {
	s.runner = &LocalBashRunner{SafeDir: "/tmp"}
}

func (s *LocalBashSuite) TestRunBashSuccess() {
	output, err := s.runner.RunBash(context.Background(), "echo hello", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello\n", output)
}

func (s *LocalBashSuite) TestRunBashWithDir() {
	output, err := s.runner.RunBash(context.Background(), "pwd", "", "/tmp")
	require.NoError(s.T(), err)
	require.Contains(s.T(), output, "/tmp")
}

func (s *LocalBashSuite) TestRunBashWithNonExistentDir() {
	output, err := s.runner.RunBash(context.Background(), "echo works", "", "/tmp/nonexistent-dir-12345")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "works\n", output)
}

func (s *LocalBashSuite) TestRunBashRejectsOutsidePath() {
	output, err := s.runner.RunBash(context.Background(), "echo safe", "", "/etc")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "safe\n", output)
}

func (s *LocalBashSuite) TestRunBashRejectsTraversal() {
	output, err := s.runner.RunBash(context.Background(), "echo safe", "", "/tmp/../etc")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "safe\n", output)
}

func (s *LocalBashSuite) TestRunBashEmptySafeDirRejectsAll() {
	r := &LocalBashRunner{}
	output, err := r.RunBash(context.Background(), "echo safe", "", "/tmp")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "safe\n", output)
}

func (s *LocalBashSuite) TestRunBashFailure() {
	output, err := s.runner.RunBash(context.Background(), "exit 1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "local bash:")
	require.Empty(s.T(), output)
}

// TestSafePathAbsErrorReturnsFalse triggers filepath.Abs's rare error path
// by chdir-ing into a removed directory — the kernel's getwd(2) then fails
// and filepath.Abs on a relative path returns that error.
func (s *LocalBashSuite) TestSafePathAbsErrorReturnsFalse() {
	orig, err := os.Getwd()
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = os.Chdir(orig) })

	tmp, err := os.MkdirTemp("", "safepath-abs-*")
	require.NoError(s.T(), err)
	require.NoError(s.T(), os.Chdir(tmp))
	require.NoError(s.T(), os.Remove(tmp))

	r := &LocalBashRunner{SafeDir: "/tmp"}
	abs, ok := r.safePath("relative")
	require.False(s.T(), ok)
	require.Empty(s.T(), abs)
}

func (s *LocalBashSuite) TestRunBashContextCancel() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.runner.RunBash(ctx, "sleep 10", "", "")
	require.Error(s.T(), err)
}

func (s *LocalBashSuite) TestRunBashInjectsChannelAndAPIURL() {
	r := &LocalBashRunner{SafeDir: s.T().TempDir(), APIURL: "http://localhost:9999"}
	out, err := r.RunBash(context.Background(), `printf '%s|%s' "$CHANNEL_ID" "$API_URL"`, "ch-42", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ch-42|http://localhost:9999", out)
}

func (s *LocalBashSuite) TestRunBashEmptyAPIURLInheritsProcessEnv() {
	// With no configured APIURL the runner leaves any inherited $API_URL
	// alone (and a set APIURL, as the other test shows, appends after
	// os.Environ so it wins).
	s.T().Setenv("API_URL", "http://inherited:1")
	r := &LocalBashRunner{SafeDir: s.T().TempDir()}
	out, err := r.RunBash(context.Background(), `printf '%s|%s' "$CHANNEL_ID" "$API_URL"`, "ch-1", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ch-1|http://inherited:1", out)
}
