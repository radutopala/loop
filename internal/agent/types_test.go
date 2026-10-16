package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TypesSuite struct {
	suite.Suite
}

func TestTypesSuite(t *testing.T) {
	suite.Run(t, new(TypesSuite))
}

func (s *TypesSuite) TestBuildPromptWithMessages() {
	req := &AgentRequest{
		Messages: []AgentMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}
	require.Equal(s.T(), "user: hello\nassistant: hi there\n", req.BuildPrompt())
}

func (s *TypesSuite) TestBuildPromptSingleSlashMessageStaysBare() {
	// A lone slash command must not get the "role: " prefix — the prefix
	// stops the CLI from expanding it as a user-invoked skill.
	req := &AgentRequest{
		Messages: []AgentMessage{
			{Role: "user", Content: "/code-review"},
		},
	}
	require.Equal(s.T(), "/code-review", req.BuildPrompt())
}

func (s *TypesSuite) TestBuildPromptSingleMessage() {
	req := &AgentRequest{
		Messages: []AgentMessage{
			{Role: "user", Content: "hello"},
		},
	}
	require.Equal(s.T(), "user: hello\n", req.BuildPrompt())
}

func (s *TypesSuite) TestBuildPromptEmptyMessages() {
	req := &AgentRequest{}
	require.Equal(s.T(), "", req.BuildPrompt())
}

func (s *TypesSuite) TestBuildPromptWithPromptFieldNoSession() {
	req := &AgentRequest{
		Prompt: "summarize the results",
	}
	require.Equal(s.T(), "summarize the results", req.BuildPrompt())
}

func (s *TypesSuite) TestBuildPromptSessionWithPrompt() {
	req := &AgentRequest{
		SessionID: "sess-1",
		Prompt:    "do stuff",
		Messages:  []AgentMessage{{Role: "user", Content: "ignored"}},
	}
	require.Equal(s.T(), "do stuff", req.BuildPrompt())
}

func (s *TypesSuite) TestBuildPromptSessionWithMessages() {
	req := &AgentRequest{
		SessionID: "sess-1",
		Messages: []AgentMessage{
			{Role: "user", Content: "first"},
			{Role: "user", Content: "latest"},
		},
	}
	require.Equal(s.T(), "latest", req.BuildPrompt())
}

func (s *TypesSuite) TestBackgroundSummary() {
	tests := []struct {
		name  string
		tasks []BackgroundTask
		want  string
	}{
		{name: "none", want: ""},
		{name: "one without description", tasks: []BackgroundTask{{ID: "b1"}}, want: "1 background task"},
		{name: "one", tasks: []BackgroundTask{{ID: "b1", Description: "watch CI"}}, want: "1 background task: watch CI"},
		{name: "several, some described", tasks: []BackgroundTask{{Description: "a"}, {}, {Description: "b"}}, want: "3 background tasks: a, b"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, BackgroundSummary(tt.tasks))
		})
	}
}
