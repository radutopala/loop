package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radutopala/loop/internal/types"
)

type scheduleTaskInput struct {
	Schedule      string `json:"schedule" jsonschema:"Cron expression (e.g. 0 9 * * *), Go time.Duration (e.g. 5m, 1h), or RFC3339 timestamp (e.g. 2026-02-09T14:30:00Z) for once type"`
	Type          string `json:"type" jsonschema:"Task type: cron, interval, or once"`
	Prompt        string `json:"prompt" jsonschema:"The prompt to execute on schedule"`
	AutoDeleteSec int    `json:"auto_delete_sec,omitempty" jsonschema:"Seconds after execution to auto-delete the thread (0 = disabled)"`
}

type cancelTaskInput struct {
	TaskID int64 `json:"task_id" jsonschema:"The ID of the task to cancel"`
}

type toggleTaskInput struct {
	TaskID  int64 `json:"task_id" jsonschema:"The ID of the task to enable or disable"`
	Enabled bool  `json:"enabled" jsonschema:"Whether to enable (true) or disable (false) the task"`
}

type editTaskInput struct {
	TaskID        int64   `json:"task_id" jsonschema:"The ID of the task to edit"`
	Schedule      *string `json:"schedule,omitempty" jsonschema:"New schedule expression (cron, Go time.Duration, or RFC3339 timestamp for once type)"`
	Type          *string `json:"type,omitempty" jsonschema:"New task type: cron, interval, or once"`
	Prompt        *string `json:"prompt,omitempty" jsonschema:"New prompt to execute on schedule"`
	AutoDeleteSec *int    `json:"auto_delete_sec,omitempty" jsonschema:"Seconds after execution to auto-delete the thread (0 = disabled)"`
}

type listTasksInput struct{}

func (s *Server) handleScheduleTask(_ context.Context, _ *mcp.CallToolRequest, input scheduleTaskInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "schedule_task", "schedule", input.Schedule, "type", input.Type, "prompt", input.Prompt)

	switch input.Type {
	case "once":
		if _, err := time.Parse(time.RFC3339, input.Schedule); err != nil {
			return errorResult(fmt.Sprintf("invalid schedule for type \"once\": must be RFC3339 (e.g. 2026-02-09T14:30:00Z): %v", err)), nil, nil
		}
	case "interval":
		if _, err := time.ParseDuration(input.Schedule); err != nil {
			return errorResult(fmt.Sprintf("invalid schedule for type %q: must be a valid Go time.Duration (e.g. 5m, 1h, 24h): %v", input.Type, err)), nil, nil
		}
	}

	data, _ := json.Marshal(map[string]any{
		"channel_id":      s.channelID,
		"schedule":        input.Schedule,
		"type":            input.Type,
		"prompt":          input.Prompt,
		"auto_delete_sec": input.AutoDeleteSec,
	})

	type taskResult struct {
		ID int64 `json:"id"`
	}
	result, errResult, err := doAPICall[taskResult](s, "POST", s.apiURL+"/api/tasks", http.StatusCreated, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task scheduled successfully (ID: %d).", result.ID)},
		},
	}, nil, nil
}

func (s *Server) handleListTasks(_ context.Context, _ *mcp.CallToolRequest, _ listTasksInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "list_tasks", "channel_id", s.channelID)

	type taskEntry struct {
		ID            int64  `json:"id"`
		Schedule      string `json:"schedule"`
		Type          string `json:"type"`
		Prompt        string `json:"prompt"`
		Enabled       bool   `json:"enabled"`
		NextRunAt     string `json:"next_run_at"`
		TemplateName  string `json:"template_name"`
		AutoDeleteSec int    `json:"auto_delete_sec"`
	}
	tasks, errResult, err := doAPICall[[]taskEntry](s, "GET", fmt.Sprintf("%s/api/tasks?channel_id=%s", s.apiURL, s.channelID), http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	if len(*tasks) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No scheduled tasks."},
			},
		}, nil, nil
	}

	var text strings.Builder
	for _, t := range *tasks {
		prompt := types.TruncateString(t.Prompt, 80)
		if t.TemplateName != "" {
			fmt.Fprintf(&text, "- ID %d: %s (schedule: %s, type: %s, enabled: %v, template_name: %s", t.ID, prompt, t.Schedule, t.Type, t.Enabled, t.TemplateName)
		} else {
			fmt.Fprintf(&text, "- ID %d: %s (schedule: %s, type: %s, enabled: %v", t.ID, prompt, t.Schedule, t.Type, t.Enabled)
		}
		if t.AutoDeleteSec > 0 {
			fmt.Fprintf(&text, ", auto_delete: %ds", t.AutoDeleteSec)
		}
		text.WriteString(")\n")
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text.String()},
		},
	}, nil, nil
}

func (s *Server) handleCancelTask(_ context.Context, _ *mcp.CallToolRequest, input cancelTaskInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "cancel_task", "task_id", input.TaskID)

	if errResult, err := doAPICallNoBody(s, "DELETE", fmt.Sprintf("%s/api/tasks/%d", s.apiURL, input.TaskID), http.StatusNoContent, nil); errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task %d cancelled successfully.", input.TaskID)},
		},
	}, nil, nil
}

func (s *Server) handleEditTask(_ context.Context, _ *mcp.CallToolRequest, input editTaskInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "edit_task", "task_id", input.TaskID)

	body := map[string]any{}
	if input.Schedule != nil {
		body["schedule"] = *input.Schedule
	}
	if input.Type != nil {
		body["type"] = *input.Type
	}
	if input.Prompt != nil {
		body["prompt"] = *input.Prompt
	}
	if input.AutoDeleteSec != nil {
		body["auto_delete_sec"] = *input.AutoDeleteSec
	}

	if len(body) == 0 {
		return errorResult("at least one of schedule, type, prompt, or auto_delete_sec is required"), nil, nil
	}

	// Validate schedule when editing to once/interval type with a schedule
	if input.Schedule != nil && input.Type != nil {
		switch *input.Type {
		case "once":
			if _, err := time.Parse(time.RFC3339, *input.Schedule); err != nil {
				return errorResult(fmt.Sprintf("invalid schedule for type \"once\": must be RFC3339 (e.g. 2026-02-09T14:30:00Z): %v", err)), nil, nil
			}
		case "interval":
			if _, err := time.ParseDuration(*input.Schedule); err != nil {
				return errorResult(fmt.Sprintf("invalid schedule for type %q: must be a valid Go time.Duration (e.g. 5m, 1h, 24h): %v", *input.Type, err)), nil, nil
			}
		}
	}

	data, _ := json.Marshal(body)

	if errResult, err := doAPICallNoBody(s, "PATCH", fmt.Sprintf("%s/api/tasks/%d", s.apiURL, input.TaskID), http.StatusOK, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task %d updated successfully.", input.TaskID)},
		},
	}, nil, nil
}

func (s *Server) handleToggleTask(_ context.Context, _ *mcp.CallToolRequest, input toggleTaskInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "toggle_task", "task_id", input.TaskID, "enabled", input.Enabled)

	data, _ := json.Marshal(map[string]bool{
		"enabled": input.Enabled,
	})

	if errResult, err := doAPICallNoBody(s, "PATCH", fmt.Sprintf("%s/api/tasks/%d", s.apiURL, input.TaskID), http.StatusOK, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	state := "disabled"
	if input.Enabled {
		state = "enabled"
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task %d %s.", input.TaskID, state)},
		},
	}, nil, nil
}
