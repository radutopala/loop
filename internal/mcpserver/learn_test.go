package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LearnToolSuite struct {
	baseToolSuite
}

func TestLearnToolSuite(t *testing.T) {
	s := new(LearnToolSuite)
	s.serverOpts = []MemoryOption{WithLearnTools()}
	suite.Run(t, s)
}

func (s *LearnToolSuite) TestProposeLearnings() {
	var gotURL string
	var gotBody []byte
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotBody, _ = io.ReadAll(req.Body)
		return jsonResponse(201, `{"proposals":[{"title":"a"},{"title":"b"}]}`), nil
	}

	text, isError := s.callTool("propose_learnings", map[string]any{
		"proposals": []map[string]any{
			{"kind": "rename", "title": "a", "rationale": "r", "payload": map[string]any{"name": "x"}},
			{"kind": "mount", "title": "b", "payload": map[string]any{"mount": "a:b"}},
		},
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Filed 2 proposal(s)")
	require.Equal(s.T(), "http://localhost:8222/api/channels/test-channel/learn/proposals", gotURL)

	var body proposeLearningsInput
	require.NoError(s.T(), json.Unmarshal(gotBody, &body))
	require.Equal(s.T(), proposeLearningsInput{Proposals: []learnProposalInput{
		{Kind: "rename", Title: "a", Rationale: "r", Payload: map[string]any{"name": "x"}},
		{Kind: "mount", Title: "b", Payload: map[string]any{"mount": "a:b"}},
	}}, body)
}

func (s *LearnToolSuite) TestProposeLearningsWithdraw() {
	tests := []struct {
		name     string
		args     map[string]any
		response string
		want     proposeLearningsInput
		wantText string
	}{
		{
			name:     "withdraw only",
			args:     map[string]any{"withdraw": []map[string]any{{"id": 3, "reason": "stale"}}},
			response: `{"proposals":[],"withdrawn":[{"id":3}]}`,
			want:     proposeLearningsInput{Withdraw: []learnWithdrawInput{{ID: 3, Reason: "stale"}}},
			wantText: "Withdrew 1 proposal(s).",
		},
		{
			name: "replace and withdraw",
			args: map[string]any{
				"proposals": []map[string]any{{"kind": "rename", "title": "a", "payload": map[string]any{"name": "x"}, "replaces": 4}},
				"withdraw":  []map[string]any{{"id": 3, "reason": "stale"}},
			},
			response: `{"proposals":[{"title":"a"}],"withdrawn":[{"id":3},{"id":4}]}`,
			want: proposeLearningsInput{
				Proposals: []learnProposalInput{{Kind: "rename", Title: "a", Payload: map[string]any{"name": "x"}, Replaces: 4}},
				Withdraw:  []learnWithdrawInput{{ID: 3, Reason: "stale"}},
			},
			wantText: "Filed 1 proposal(s); the user will accept or dismiss them in the Learn view. Withdrew 2 proposal(s).",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			var gotBody []byte
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				gotBody, _ = io.ReadAll(req.Body)
				return jsonResponse(201, tc.response), nil
			}
			text, isError := s.callTool("propose_learnings", tc.args)
			require.False(s.T(), isError, text)
			require.Equal(s.T(), tc.wantText, text)
			var body proposeLearningsInput
			require.NoError(s.T(), json.Unmarshal(gotBody, &body))
			require.Equal(s.T(), tc.want, body)
		})
	}
}

func (s *LearnToolSuite) TestProposeLearningsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool: "propose_learnings",
		args: map[string]any{
			"proposals": []map[string]any{{"kind": "rename", "title": "a", "payload": map[string]any{"name": "x"}}},
		},
		apiStatus:    400,
		apiBody:      `proposal 1: rename payload: name is required`,
		decodeStatus: 201,
	})
}

// toolNames lists the tools a server built with opts offers.
func (s *LearnToolSuite) toolNames(opts ...MemoryOption) []string {
	srv := New("ch", "http://localhost:8222", "", s.httpClient, nil, opts...)
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(s.ctx, t1) }()
	session, err := client.Connect(s.ctx, t2, nil)
	require.NoError(s.T(), err)
	defer session.Close()
	tools, err := session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestOnlyInLearnPass checks other agents don't get the tool, and a learn
// pass doesn't get the inter-agent ones.
func (s *LearnToolSuite) TestOnlyInLearnPass() {
	require.NotContains(s.T(), s.toolNames(WithAgentTools("agent-0")), "propose_learnings")
	learnTools := s.toolNames(WithLearnTools())
	require.Contains(s.T(), learnTools, "propose_learnings")
	require.NotContains(s.T(), learnTools, "list_agents")
	require.NotContains(s.T(), learnTools, "send_agent_message")
}
