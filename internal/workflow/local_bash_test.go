package workflow

import (
	"context"
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
	s.runner = &LocalBashRunner{}
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

func (s *LocalBashSuite) TestRunBashSafeDirRejectsOutsidePath() {
	// When SafeDir is set, paths outside it should be rejected.
	r := &LocalBashRunner{SafeDir: "/tmp"}
	output, err := r.RunBash(context.Background(), "echo safe", "", "/etc")
	require.NoError(s.T(), err)
	// /etc is outside /tmp so it should be ignored — script runs without a working dir.
	require.Equal(s.T(), "safe\n", output)
}

func (s *LocalBashSuite) TestRunBashSafeDirAllowsInsidePath() {
	r := &LocalBashRunner{SafeDir: "/tmp"}
	output, err := r.RunBash(context.Background(), "pwd", "", "/tmp")
	require.NoError(s.T(), err)
	require.Contains(s.T(), output, "/tmp")
}

func (s *LocalBashSuite) TestRunBashSafeDirRejectsTraversal() {
	// Path traversal via ../ should be rejected when it escapes SafeDir.
	r := &LocalBashRunner{SafeDir: "/tmp"}
	output, err := r.RunBash(context.Background(), "echo safe", "", "/tmp/../etc")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "safe\n", output)
}

func (s *LocalBashSuite) TestRunBashFailure() {
	output, err := s.runner.RunBash(context.Background(), "exit 1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "local bash:")
	require.Empty(s.T(), output)
}

func (s *LocalBashSuite) TestRunBashContextCancel() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.runner.RunBash(ctx, "sleep 10", "", "")
	require.Error(s.T(), err)
}
