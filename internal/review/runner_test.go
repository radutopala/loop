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
	_, err := r.Run(context.Background(), "ch", "/dir", "/parent", "sys", "p", nil)
	require.ErrorContains(s.T(), err, "agent not configured")
}

func (s *RunnerSuite) TestRunAgentErrorPropagates() {
	a := new(mockAgentRunner)
	a.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	r := &Runner{Agent: a}
	_, err := r.Run(context.Background(), "ch", "/dir", "/parent", "sys", "p", nil)
	require.ErrorContains(s.T(), err, "boom")
}

func (s *RunnerSuite) TestRunParsesAndDispatchesComments() {
	var got []*Comment
	a := new(mockAgentRunner)
	a.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.DirPath == "/wt" && req.ParentDirPath == "/repo" && req.SystemPrompt == "sys" && req.Prompt == "p" && req.OnTurn != nil
	})).Run(func(args mock.Arguments) {
		req := args.Get(1).(*agent.AgentRequest)
		// Simulate two turns where the second repeats one comment id.
		req.OnTurn(`<review-comment path="a.go" line="1">first</review-comment>`)
		req.OnTurn(`<review-comment path="a.go" line="1">first</review-comment><review-comment path="b.go" line="2">second</review-comment>`)
	}).Return(&agent.AgentResponse{Response: "done"}, nil)

	r := &Runner{Agent: a}
	resp, err := r.Run(context.Background(), "ch", "/wt", "/repo", "sys", "p", func(c *Comment) {
		got = append(got, c)
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp.Response)
	require.Len(s.T(), got, 2)
	require.Equal(s.T(), "a.go", got[0].Path)
	require.Equal(s.T(), "b.go", got[1].Path)
}

func (s *RunnerSuite) TestRunNilCallbackSafe() {
	a := new(mockAgentRunner)
	a.On("Run", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		req := args.Get(1).(*agent.AgentRequest)
		req.OnTurn(`<review-comment path="a.go" line="1">x</review-comment>`)
	}).Return(&agent.AgentResponse{}, nil)
	r := &Runner{Agent: a}
	_, err := r.Run(context.Background(), "ch", "/wt", "/repo", "sys", "p", nil)
	require.NoError(s.T(), err)
}
