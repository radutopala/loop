package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deny messages for the interactive tools Loop gates. They become the tool's
// result in the session transcript, so they must (a) tell the model the card
// is being handled by Loop's UI, and (b) explicitly forbid re-invoking the
// tool — a bare denial reads as "the tool failed" and makes the model retry.
const (
	askDenyMessage = "Loop is showing this question to the user in its UI. " +
		"Do NOT call AskUserQuestion again and do NOT guess the answers. " +
		"End your turn now; the user's answers will arrive as the next user message."
	planDenyMessage = "Loop is showing your plan to the user for review in its UI. " +
		"Do NOT start implementing and do NOT call ExitPlanMode again. " +
		"End your turn now; the user's decision will arrive as the next user message."
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
// with the user's answer.
//
// AskUserQuestion and ExitPlanMode are DENIED with an instructive message:
// allowing them lets Claude Code execute them natively, where — with no
// interactive TTY — they self-resolve ("The user did not answer the
// questions" / "approved your plan, you can now start coding"), racing Loop's
// cards and, for plans, executing before review. Blocking without resolving
// (an earlier approach) left a dangling tool_use in the session transcript,
// which the model read as an interrupted/failed attempt and retried on
// resume. The deny closes the tool_use with a persisted tool_result carrying
// explicit "wait for the user" guidance. EnterPlanMode is allowed — it
// legitimately flips the session into plan mode.
//
// The input type is map[string]any (not a struct) so the inferred JSON Schema
// is a permissive object that accepts the permission payload — a fixed struct
// would reject the payload's fields as "unexpected additional properties".
func (s *Server) handlePermissionPrompt(_ context.Context, _ *mcp.CallToolRequest, input map[string]any) (*mcp.CallToolResult, any, error) {
	toolName, _ := input["tool_name"].(string)
	s.logger.Info("mcp tool call", "tool", "permission_prompt", "for_tool", toolName)

	deny := func(message string) (*mcp.CallToolResult, any, error) {
		payload, _ := json.Marshal(map[string]any{
			"behavior": "deny",
			"message":  message,
		})
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(payload)}},
		}, nil, nil
	}

	switch toolName {
	case "AskUserQuestion":
		return deny(askDenyMessage)
	case "ExitPlanMode":
		return deny(planDenyMessage)
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
