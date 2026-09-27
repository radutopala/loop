package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radutopala/loop/internal/learn"
)

// WithLearnTools makes this a learn pass's MCP server: it gets
// propose_learnings, and none of the inter-agent tools or channel push (a
// learn pass works alone, and other agents shouldn't see or message it).
func WithLearnTools() MemoryOption {
	return func(s *Server) {
		s.learnTools = true
	}
}

// registerLearnTools adds the tool a learn pass files its proposals with.
// Only a learn pass's MCP server (see WithLearnTools) has it.
func (s *Server) registerLearnTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "propose_learnings",
		Description: "File this learn pass's proposals for the user to accept or dismiss. Nothing is applied until they accept. Call once with every proposal (at most 5); each needs a kind (" + strings.Join(learn.Kinds, ", ") + "), a short title, a one-line rationale grounded in the run, and the kind's payload object. If the call fails, fix the proposal it names and call again.",
	}, s.handleProposeLearnings)
}

type learnProposalInput struct {
	Kind      string         `json:"kind" jsonschema:"required,The proposal's kind"`
	Title     string         `json:"title" jsonschema:"required,A short title, shown on the proposal card"`
	Rationale string         `json:"rationale,omitempty" jsonschema:"One line on what in the run prompted this"`
	Payload   map[string]any `json:"payload" jsonschema:"required,The kind's payload object, shaped as the system prompt describes"`
}

type proposeLearningsInput struct {
	Proposals []learnProposalInput `json:"proposals" jsonschema:"required,The proposals to file"`
}

func (s *Server) handleProposeLearnings(_ context.Context, _ *mcp.CallToolRequest, input proposeLearningsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "propose_learnings", "count", len(input.Proposals))

	data, _ := json.Marshal(input)
	apiURL := fmt.Sprintf("%s/api/channels/%s/learn/proposals", s.apiURL, url.PathEscape(s.channelID))
	type proposalsResult struct {
		Proposals []struct {
			Title string `json:"title"`
		} `json:"proposals"`
	}
	result, errResult, err := doAPICall[proposalsResult](s, "POST", apiURL, 201, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Filed %d proposal(s); the user will accept or dismiss them in the Learn drawer.", len(result.Proposals))},
		},
	}, nil, nil
}
