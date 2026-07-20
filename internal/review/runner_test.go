package review

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
)

type mockAgentRunner struct{ mock.Mock }

func (m *mockAgentRunner) Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error) {
	args := m.Called(ctx, req)
	resp, _ := args.Get(0).(*agent.AgentResponse)
	return resp, args.Error(1)
}

type RunnerSuite struct{ suite.Suite }

func TestRunnerSuite(t *testing.T) { suite.Run(t, new(RunnerSuite)) }

func (s *RunnerSuite) TestRunNoAgentConfigured() {
	r := &Runner{}
	_, err := r.Run(context.Background(), "ch", "/dir", "/parent", "sys", "p")
	require.ErrorContains(s.T(), err, "agent not configured")
}

func (s *RunnerSuite) TestRunAgentErrorPropagates() {
	a := new(mockAgentRunner)
	a.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	r := &Runner{Agent: a}
	_, err := r.Run(context.Background(), "ch", "/dir", "/parent", "sys", "p")
	require.ErrorContains(s.T(), err, "boom")
}

func (s *RunnerSuite) TestRunBuildsRequest() {
	a := new(mockAgentRunner)
	a.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "ch" && req.DirPath == "/wt" && req.ParentDirPath == "/repo" &&
			req.SystemPrompt == "sys" && req.Prompt == "p"
	})).Return(&agent.AgentResponse{Response: "done"}, nil)

	r := &Runner{Agent: a}
	resp, err := r.Run(context.Background(), "ch", "/wt", "/repo", "sys", "p")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp.Response)
	a.AssertExpectations(s.T())
}
