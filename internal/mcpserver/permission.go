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

	// AskUserQuestion and ExitPlanMode are special: Loop surfaces its own card
	// on the host (an answer card / a plan-review card) and cancels the run the
	// instant it sees the tool_use in the stream (see orchestrator:
	// markAskedChannel / markPlannedChannel + runCancel). If we allowed them
	// here, Claude Code would execute them natively and — with no interactive
	// TTY — immediately self-resolve: AskUserQuestion as "The user did not
	// answer the questions", and ExitPlanMode as "approved your plan, you can
	// now start coding" (which makes the agent execute the plan before the user
	// has reviewed it). Block instead so the native tool never resolves; Loop
	// tears the container down (which cancels this ctx) while we wait. Safe
	// because Loop cancels the run on both tool_uses (see messages.go). Note
	// EnterPlanMode is NOT blocked — it legitimately enters plan mode and Loop
	// does not cancel it.
	if toolName == "AskUserQuestion" || toolName == "ExitPlanMode" {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}

	// Everything else (EnterPlanMode, …) is allowed to proceed.
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
