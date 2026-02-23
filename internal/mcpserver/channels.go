package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createChannelInput struct {
	Name string `json:"name" jsonschema:"The name for the new channel"`
}

type searchChannelsInput struct {
	Query string `json:"query,omitempty" jsonschema:"Optional search term to filter channels and threads by name"`
}

func (s *Server) handleCreateChannel(_ context.Context, _ *mcp.CallToolRequest, input createChannelInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "create_channel", "name", input.Name)

	if input.Name == "" {
		return errorResult("name is required"), nil, nil
	}

	reqBody := map[string]string{
		"name": input.Name,
	}
	if s.authorID != "" {
		reqBody["author_id"] = s.authorID
	}
	data, _ := json.Marshal(reqBody)

	type channelResult struct {
		ChannelID string `json:"channel_id"`
	}
	result, errResult, err := doAPICall[channelResult](s, "POST", s.apiURL+"/api/channels/create", http.StatusCreated, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Channel created successfully (ID: %s).", result.ChannelID)},
		},
	}, nil, nil
}

func (s *Server) handleSearchChannels(_ context.Context, _ *mcp.CallToolRequest, input searchChannelsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "search_channels", "query", input.Query)

	url := fmt.Sprintf("%s/api/channels", s.apiURL)
	if input.Query != "" {
		url += fmt.Sprintf("?query=%s", input.Query)
	}

	type channelEntry struct {
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
		DirPath   string `json:"dir_path"`
		ParentID  string `json:"parent_id"`
		Active    bool   `json:"active"`
	}
	channels, errResult, err := doAPICall[[]channelEntry](s, "GET", url, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	if len(*channels) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No channels found."},
			},
		}, nil, nil
	}

	var text strings.Builder
	for _, ch := range *channels {
		chType := "channel"
		if ch.ParentID != "" {
			chType = "thread"
		}
		fmt.Fprintf(&text, "- %s [%s] (ID: %s, dir: %s, active: %v)\n", ch.Name, chType, ch.ChannelID, ch.DirPath, ch.Active)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text.String()},
		},
	}, nil, nil
}
