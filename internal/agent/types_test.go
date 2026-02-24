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
