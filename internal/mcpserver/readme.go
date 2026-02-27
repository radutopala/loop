package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radutopala/loop/internal/readme"
)

type getReadmeInput struct{}

func (s *Server) handleGetReadme(_ context.Context, _ *mcp.CallToolRequest, _ getReadmeInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "get_readme")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: readme.Content},
		},
	}, nil, nil
}
