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

// WithLearnTools makes this a learn pass's MCP server, with
// propose_learnings.
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
		Description: "File this learn pass's proposals for the user to accept or dismiss, and withdraw earlier ones this run shows are stale. Nothing is applied until they accept. Call once with every proposal (at most 5) and withdrawal; each proposal needs a kind (" + strings.Join(learn.Kinds, ", ") + "), a short title, a one-line rationale grounded in the run, and the kind's payload object, plus replaces when it supersedes a waiting proposal. withdraw takes the ids of pending or failed proposals waiting for the user, each with a one-line reason; a call may only withdraw. If the call fails, fix the item it names and call again.",
	}, s.handleProposeLearnings)
}

type learnProposalInput struct {
	Kind      string         `json:"kind" jsonschema:"required,The proposal's kind"`
	Title     string         `json:"title" jsonschema:"required,A short title, shown on the proposal card"`
	Rationale string         `json:"rationale,omitempty" jsonschema:"One line on what in the run prompted this"`
	Payload   map[string]any `json:"payload" jsonschema:"required,The kind's payload object, shaped as the system prompt describes"`
	Replaces  int64          `json:"replaces,omitempty" jsonschema:"The id of a waiting proposal this one supersedes; it's withdrawn"`
}

type learnWithdrawInput struct {
	ID     int64  `json:"id" jsonschema:"required,The id of a pending or failed proposal waiting for the user"`
	Reason string `json:"reason" jsonschema:"required,One line on why this run shows it's stale"`
}

type proposeLearningsInput struct {
	Proposals []learnProposalInput `json:"proposals,omitempty" jsonschema:"The proposals to file"`
	Withdraw  []learnWithdrawInput `json:"withdraw,omitempty" jsonschema:"Earlier proposals to withdraw"`
}

func (s *Server) handleProposeLearnings(_ context.Context, _ *mcp.CallToolRequest, input proposeLearningsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "propose_learnings", "count", len(input.Proposals), "withdraw", len(input.Withdraw))

	data, _ := json.Marshal(input)
	apiURL := fmt.Sprintf("%s/api/channels/%s/learn/proposals", s.apiURL, url.PathEscape(s.channelID))
	type proposalsResult struct {
		Proposals []json.RawMessage `json:"proposals"`
		Withdrawn []json.RawMessage `json:"withdrawn"`
	}
	result, errResult, err := doAPICall[proposalsResult](s, "POST", apiURL, 201, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	var lines []string
	if len(result.Proposals) > 0 {
		lines = append(lines, fmt.Sprintf("Filed %d proposal(s); the user will accept or dismiss them in the Learn view.", len(result.Proposals)))
	}
	if len(result.Withdrawn) > 0 {
		lines = append(lines, fmt.Sprintf("Withdrew %d proposal(s).", len(result.Withdrawn)))
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: strings.Join(lines, " ")},
		},
	}, nil, nil
}
