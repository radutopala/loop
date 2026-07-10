package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// handlePermissionPrompt implements the tool named by Claude's
// --permission-prompt-tool flag. Claude Code invokes it with a
// {tool_name, input, tool_use_id} payload before running a tool that needs a
// permission decision.
//
// Loop runs Claude under --dangerously-skip-permissions, so ordinary tools
// never reach this gate. The flag exists solely to unlock the interactive
// tools (AskUserQuestion, EnterPlanMode, ExitPlanMode) in headless --print
// mode; Loop surfaces those via stream interception and resumes the session
// with the user's answer. This tool therefore always allows, echoing the
// tool's input back unchanged.
//
// The input type is map[string]any (not a struct) so the inferred JSON Schema
// is a permissive object that accepts the permission payload — a fixed struct
// would reject the payload's fields as "unexpected additional properties".
func (s *Server) handlePermissionPrompt(ctx context.Context, _ *mcp.CallToolRequest, input map[string]any) (*mcp.CallToolResult, any, error) {
	toolName, _ := input["tool_name"].(string)
	s.logger.Info("mcp tool call", "tool", "permission_prompt", "for_tool", toolName)

	// AskUserQuestion is special: Loop surfaces its own answer card on the host
	// and cancels the run the instant it sees the tool_use in the stream (see
	// orchestrator: markAskedChannel + runCancel). If we allowed the tool here,
	// Claude Code would execute it natively and — with no interactive TTY —
	// immediately resolve it as "The user did not answer the questions", dumping
	// a bogus tool_result into the transcript and racing Loop's card. Block
	// instead so the native tool never resolves; Loop tears the container down
	// (which cancels this ctx) while we wait. Safe because Loop unconditionally
	// cancels the run on an AskUserQuestion tool_use.
	if toolName == "AskUserQuestion" {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}

	// Everything else (EnterPlanMode, ExitPlanMode, …) is allowed to proceed —
	// Loop does not unconditionally cancel those, so blocking would hang them.
	// Echo the original tool input straight back as the approved input.
	updated := input["input"]
	if updated == nil {
		updated = map[string]any{}
	}
	payload, _ := json.Marshal(map[string]any{
		"behavior":     "allow",
		"updatedInput": updated,
	})
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(payload)}},
	}, nil, nil
}
