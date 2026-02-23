package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type memorySearchAPIResponse struct {
	Results []memorySearchResult `json:"results"`
}

type memorySearchResult struct {
	FilePath string  `json:"file_path"`
	Content  string  `json:"content,omitempty"`
	Score    float32 `json:"score"`
}

type searchMemoryInput struct {
	Query string `json:"query" jsonschema:"The search query to find relevant memory notes"`
	TopK  int    `json:"top_k,omitempty" jsonschema:"Number of results to return (default 5)"`
}

type indexMemoryInput struct{}

func (s *Server) handleSearchMemory(_ context.Context, _ *mcp.CallToolRequest, input searchMemoryInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "search_memory", "query", input.Query, "top_k", input.TopK)

	if input.Query == "" {
		return errorResult("query is required"), nil, nil
	}

	topK := input.TopK
	if topK <= 0 {
		topK = 5
	}

	body := map[string]any{
		"query": input.Query,
		"top_k": topK,
	}
	if s.dirPath != "" {
		body["dir_path"] = s.dirPath
	} else {
		body["channel_id"] = s.channelID
	}
	data, _ := json.Marshal(body)

	resp, errResult, err := doAPICall[memorySearchAPIResponse](s, "POST", s.apiURL+"/api/memory/search", http.StatusOK, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	if len(resp.Results) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No results found."},
			},
		}, nil, nil
	}

	var text strings.Builder
	for i, r := range resp.Results {
		fmt.Fprintf(&text, "## Result %d (score: %.3f)\n", i+1, r.Score)
		fmt.Fprintf(&text, "**File:** %s\n", r.FilePath)
		if r.Content != "" {
			fmt.Fprintf(&text, "\n%s\n\n", r.Content)
		} else {
			text.WriteString("\n")
		}
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text.String()},
		},
	}, nil, nil
}

func (s *Server) handleIndexMemory(_ context.Context, _ *mcp.CallToolRequest, _ indexMemoryInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "index_memory")

	body := map[string]string{}
	if s.dirPath != "" {
		body["dir_path"] = s.dirPath
	} else {
		body["channel_id"] = s.channelID
	}
	data, _ := json.Marshal(body)

	type indexResult struct {
		Count int `json:"count"`
	}
	resp, errResult, err := doAPICall[indexResult](s, "POST", s.apiURL+"/api/memory/index", http.StatusOK, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Indexed %d chunks.", resp.Count)},
		},
	}, nil, nil
}
