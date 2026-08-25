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
	_, err := r.Run(context.Background(), "ch", "/dir", "/parent", "sys", "p", "", nil)
	require.ErrorContains(s.T(), err, "agent not configured")
}

func (s *RunnerSuite) TestRunAgentErrorPropagates() {
	a := new(mockAgentRunner)
	a.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	r := &Runner{Agent: a}
	_, err := r.Run(context.Background(), "ch", "/dir", "/parent", "sys", "p", "", nil)
	require.ErrorContains(s.T(), err, "boom")
}

// A nil onComment leaves OnToolUse unset so the container runner keeps its
// cheaper non-streaming path.
func (s *RunnerSuite) TestRunBuildsRequest() {
	a := new(mockAgentRunner)
	a.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "ch" && req.DirPath == "/wt" && req.ParentDirPath == "/repo" &&
			req.SystemPrompt == "sys" && req.Prompt == "p" && req.ReviewMode && req.OnToolUseRaw == nil
	})).Return(&agent.AgentResponse{Response: "done"}, nil)

	r := &Runner{Agent: a}
	resp, err := r.Run(context.Background(), "ch", "/wt", "/repo", "sys", "p", "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp.Response)
	a.AssertExpectations(s.T())
}

// A fork session id rides through as --resume + --fork-session; without
// one the request stays blank so the run starts a fresh session.
func (s *RunnerSuite) TestRunForkSession() {
	tests := []struct {
		name        string
		forkID      string
		wantSession string
		wantFork    bool
	}{
		{name: "no fork", forkID: "", wantSession: "", wantFork: false},
		{name: "fork", forkID: "sess-abc", wantSession: "sess-abc", wantFork: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			a := new(mockAgentRunner)
			a.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				return req.SessionID == tc.wantSession && req.ForkSession == tc.wantFork
			})).Return(&agent.AgentResponse{}, nil)

			r := &Runner{Agent: a}
			_, err := r.Run(context.Background(), "ch", "/wt", "/repo", "sys", "p", tc.forkID, nil)
			require.NoError(s.T(), err)
			a.AssertExpectations(s.T())
		})
	}
}

// The ReportFindings tool_use is fanned out to onComment one comment at a
// time; every other tool the agent uses is ignored.
func (s *RunnerSuite) TestRunForwardsReportFindings() {
	tests := []struct {
		name     string
		toolName string
		input    string
		want     []string
	}{
		{
			name:     "report findings",
			toolName: ReportFindingsTool,
			input:    `{"level":"high","findings":[{"file":"a.go","line":3,"summary":"leak","failure_scenario":"fd stays open"},{"file":"b.go","line":9,"summary":"panic","failure_scenario":"nil deref"}]}`,
			want:     []string{"leak\n\nfd stays open", "panic\n\nnil deref"},
		},
		{
			name:     "unplaceable findings dropped",
			toolName: ReportFindingsTool,
			input:    `{"findings":[{"file":"a.go","summary":"no line"},{"file":"b.go","line":2,"summary":"kept","failure_scenario":"boom"}]}`,
			want:     []string{"kept\n\nboom"},
		},
		{
			name:     "other tool ignored",
			toolName: "Bash",
			input:    `{"command":"git diff"}`,
		},
		{
			name:     "malformed input ignored",
			toolName: ReportFindingsTool,
			input:    "not json",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			a := new(mockAgentRunner)
			a.On("Run", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req, _ := args.Get(1).(*agent.AgentRequest)
				req.OnToolUseRaw("tu-1", tc.toolName, tc.input)
			}).Return(&agent.AgentResponse{}, nil)

			var got []string
			r := &Runner{Agent: a}
			_, err := r.Run(context.Background(), "ch", "/wt", "/repo", "sys", "p", "", func(c *Comment) {
				got = append(got, c.Body)
			})
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}
