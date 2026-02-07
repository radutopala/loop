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

func (s *TypesSuite) TestBuildPromptWithSystemPromptAndMessages() {
	messages := []AgentMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	result := BuildPrompt(messages, "You are helpful")
	require.Equal(s.T(), "You are helpful\n\nuser: hello\nassistant: hi there\n", result)
}

func (s *TypesSuite) TestBuildPromptWithoutSystemPrompt() {
	messages := []AgentMessage{
		{Role: "user", Content: "hello"},
	}
	result := BuildPrompt(messages, "")
	require.Equal(s.T(), "user: hello\n", result)
}

func (s *TypesSuite) TestBuildPromptEmptyMessages() {
	result := BuildPrompt(nil, "")
	require.Equal(s.T(), "", result)
}

func (s *TypesSuite) TestBuildPromptSystemPromptOnly() {
	result := BuildPrompt(nil, "System prompt")
	require.Equal(s.T(), "System prompt\n\n", result)
}
