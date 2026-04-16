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

func (s *LocalBashSuite) TestRunBashContextCancel() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.runner.RunBash(ctx, "sleep 10", "", "")
	require.Error(s.T(), err)
}
