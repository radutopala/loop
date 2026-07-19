package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/radutopala/loop/internal/config"
)

// registerWorkflowTools adds workflow-related MCP tools to the server.
func (s *Server) registerWorkflowTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "run_workflow",
		Description: "Start a workflow run. Workflows are declarative DAG-based pipelines of prompt and bash nodes. Use list_workflows to discover available workflows and their required inputs.",
	}, s.handleRunWorkflow)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_workflow_run",
		Description: "Get the status and node outputs of a workflow run by its run ID.",
	}, s.handleGetWorkflowRun)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_workflows",
		Description: "List all available workflow definitions. Shows workflow names, descriptions, and required inputs.",
	}, s.handleListWorkflows)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_workflow_runs",
		Description: "List recent workflow runs. Optionally filter by channel_id.",
	}, s.handleListWorkflowRuns)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "cancel_workflow_run",
		Description: "Cancel a running workflow by its run ID. Stops execution of pending and running nodes.",
	}, s.handleCancelWorkflowRun)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "resume_workflow_run",
		Description: "Resume a paused workflow run (e.g. after an approval node). Pass the run_id and an optional response string.",
	}, s.handleResumeWorkflowRun)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_workflow_run",
		Description: "Permanently delete a workflow run and its node records by run ID.",
	}, s.handleDeleteWorkflowRunMCP)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "retry_workflow_run",
		Description: "Retry a failed, cancelled, or completed workflow run. Creates a new run with the same workflow and inputs. Returns the new run ID.",
	}, s.handleRetryWorkflowRunMCP)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "save_workflow",
		Description: "Create or update a workflow definition in the config file. Pass the full workflow JSON (name, description, inputs, nodes). Use action 'add' for new workflows, 'update' to modify existing ones.",
	}, s.handleSaveWorkflow)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_workflow",
		Description: "Delete a workflow definition from the config file by name.",
	}, s.handleDeleteWorkflow)
}

type runWorkflowInput struct {
	WorkflowName string            `json:"workflow_name" jsonschema:"required,Name of the workflow to run (see list_workflows)"`
	Inputs       map[string]string `json:"inputs,omitempty" jsonschema:"Input values keyed by input name"`
	DirPath      string            `json:"dir_path,omitempty" jsonschema:"Project directory for the workflow run"`
}

func (s *Server) handleRunWorkflow(_ context.Context, _ *mcp.CallToolRequest, input runWorkflowInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "run_workflow", "workflow", input.WorkflowName)

	body := map[string]any{
		"workflow_name": input.WorkflowName,
		"channel_id":    s.channelID,
		"inputs":        input.Inputs,
	}
	if input.DirPath != "" {
		body["dir_path"] = input.DirPath
	} else if s.dirPath != "" {
		body["dir_path"] = s.dirPath
	}

	data, _ := json.Marshal(body)

	type runResult struct {
		RunID string `json:"run_id"`
	}
	result, errResult, err := doAPICall[runResult](s, "POST", s.apiURL+"/api/workflows/runs", http.StatusCreated, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Workflow %q started (run ID: %s). Use get_workflow_run to check progress.", input.WorkflowName, result.RunID)},
		},
	}, nil, nil
}

type getWorkflowRunInput struct {
	RunID string `json:"run_id" jsonschema:"required,The workflow run ID"`
}

func (s *Server) handleGetWorkflowRun(_ context.Context, _ *mcp.CallToolRequest, input getWorkflowRunInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "get_workflow_run", "run_id", input.RunID)

	apiURL := fmt.Sprintf("%s/api/workflows/runs/%s", s.apiURL, url.PathEscape(input.RunID))

	type runDetail struct {
		Run      json.RawMessage `json:"run"`
		NodeRuns json.RawMessage `json:"node_runs"`
	}
	result, errResult, err := doAPICall[runDetail](s, "GET", apiURL, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(output)},
		},
	}, nil, nil
}

type listWorkflowsInput struct{}

func (s *Server) handleListWorkflows(_ context.Context, _ *mcp.CallToolRequest, _ listWorkflowsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "list_workflows")

	params := url.Values{}
	if s.channelID != "" {
		params.Set("channel_id", s.channelID)
	}
	if s.dirPath != "" {
		params.Set("dir_path", s.dirPath)
	}
	apiURL := s.apiURL + "/api/workflows"
	if qs := params.Encode(); qs != "" {
		apiURL += "?" + qs
	}

	type workflowDef struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Inputs      any    `json:"inputs,omitempty"`
		Nodes       any    `json:"nodes"`
	}
	result, errResult, err := doAPICall[[]workflowDef](s, "GET", apiURL, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(output)},
		},
	}, nil, nil
}

type listWorkflowRunsInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"Optional channel ID filter"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Max number of runs to return (default 20)"`
}

func (s *Server) handleListWorkflowRuns(_ context.Context, _ *mcp.CallToolRequest, input listWorkflowRunsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "list_workflow_runs")

	apiURL := s.apiURL + "/api/workflows/runs?"
	params := url.Values{}
	if input.ChannelID != "" {
		params.Set("channel_id", input.ChannelID)
	}
	limit := 20
	if input.Limit > 0 {
		limit = input.Limit
	}
	params.Set("limit", fmt.Sprintf("%d", limit))
	apiURL += params.Encode()

	type runSummary struct {
		ID           string `json:"id"`
		WorkflowName string `json:"workflow_name"`
		Status       string `json:"status"`
		StartedAt    string `json:"started_at"`
	}
	result, errResult, err := doAPICall[[]runSummary](s, "GET", apiURL, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(output)},
		},
	}, nil, nil
}

type cancelWorkflowRunInput struct {
	RunID string `json:"run_id" jsonschema:"required,The workflow run ID to cancel"`
}

func (s *Server) handleCancelWorkflowRun(_ context.Context, _ *mcp.CallToolRequest, input cancelWorkflowRunInput) (*mcp.CallToolResult, any, error) {
	return s.doWorkflowRunAction("cancel_workflow_run", input.RunID, "POST",
		fmt.Sprintf("%s/api/workflows/runs/%s/cancel", s.apiURL, url.PathEscape(input.RunID)),
		"cancelled successfully")
}

type resumeWorkflowRunInput struct {
	RunID    string `json:"run_id" jsonschema:"required,The paused workflow run ID to resume"`
	Response string `json:"response,omitempty" jsonschema:"Optional response text for the approval node"`
}

func (s *Server) handleResumeWorkflowRun(_ context.Context, _ *mcp.CallToolRequest, input resumeWorkflowRunInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "resume_workflow_run", "run_id", input.RunID)

	body := map[string]any{
		"response": input.Response,
	}
	data, _ := json.Marshal(body)

	apiURL := fmt.Sprintf("%s/api/workflows/runs/%s/resume", s.apiURL, url.PathEscape(input.RunID))
	if errResult, err := doAPICallNoBody(s, "POST", apiURL, http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Workflow run %s resumed successfully.", input.RunID)},
		},
	}, nil, nil
}

type deleteWorkflowRunInput struct {
	RunID string `json:"run_id" jsonschema:"required,The workflow run ID to delete"`
}

func (s *Server) handleDeleteWorkflowRunMCP(_ context.Context, _ *mcp.CallToolRequest, input deleteWorkflowRunInput) (*mcp.CallToolResult, any, error) {
	return s.doWorkflowRunAction("delete_workflow_run", input.RunID, "DELETE",
		fmt.Sprintf("%s/api/workflows/runs/%s", s.apiURL, url.PathEscape(input.RunID)),
		"deleted")
}

func (s *Server) doWorkflowRunAction(tool, runID, method, apiURL, successVerb string) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", tool, "run_id", runID)

	if errResult, err := doAPICallNoBody(s, method, apiURL, http.StatusNoContent, nil); errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Workflow run %s %s.", runID, successVerb)},
		},
	}, nil, nil
}

type retryWorkflowRunInput struct {
	RunID string `json:"run_id" jsonschema:"required,The workflow run ID to retry"`
}

func (s *Server) handleRetryWorkflowRunMCP(_ context.Context, _ *mcp.CallToolRequest, input retryWorkflowRunInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "retry_workflow_run", "run_id", input.RunID)

	apiURL := fmt.Sprintf("%s/api/workflows/runs/%s/retry", s.apiURL, url.PathEscape(input.RunID))

	type retryResult struct {
		RunID string `json:"run_id"`
	}
	result, errResult, err := doAPICall[retryResult](s, "POST", apiURL, http.StatusCreated, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Workflow run retried (new run ID: %s). Use get_workflow_run to check progress.", result.RunID)},
		},
	}, nil, nil
}

// schemaNodeDef mirrors config.NodeDef for the MCP schema layer. It uses
// []any for Body so jsonschema-go does not recurse into NodeDef → []*NodeDef
// → NodeDef and trip its cycle detector. Field tags, including
// `jsonschema:"required,..."` annotations, must stay in sync with
// config.NodeDef.
type schemaNodeDef struct {
	ID            string              `json:"id" jsonschema:"required,Unique node identifier within the workflow"`
	Type          string              `json:"type" jsonschema:"required,Node type: 'prompt' (AI agent), 'bash' (shell script), 'loop' (prompt repeated until condition), or 'approval' (human decision)"`
	DependsOn     []string            `json:"depends_on,omitempty" jsonschema:"IDs of nodes that must complete before this one starts"`
	When          string              `json:"when,omitempty" jsonschema:"Go template expression; node is skipped when it renders 'false'"`
	TriggerRule   string              `json:"trigger_rule,omitempty" jsonschema:"How dependencies gate this node: 'all_success' (default), 'all_done', or 'one_success'"`
	Prompt        string              `json:"prompt,omitempty" jsonschema:"Inline prompt text for 'prompt'/'loop' nodes. Supports Go text/template. Mutually exclusive with prompt_path."`
	PromptPath    string              `json:"prompt_path,omitempty" jsonschema:"Path to a prompt file, resolved as {loopDir}/workflows/{prompt_path}. Mutually exclusive with prompt."`
	SystemPrompt  string              `json:"system_prompt,omitempty" jsonschema:"Optional system prompt for 'prompt' nodes; supports templates"`
	Model         string              `json:"model,omitempty" jsonschema:"Optional Claude model override (e.g. 'claude-sonnet-4-6')"`
	Script        string              `json:"script,omitempty" jsonschema:"Shell command(s) for 'bash' nodes, passed to /bin/sh -c."`
	MaxIterations int                 `json:"max_iterations,omitempty" jsonschema:"Maximum iterations for 'loop' nodes (default 10)"`
	Condition     string              `json:"condition,omitempty" jsonschema:"Go template evaluated after each 'loop' iteration; stops when it renders 'true'"`
	Body          []any               `json:"body,omitempty" jsonschema:"Child nodes executed in order per iteration. For 'loop' nodes only. Empty body keeps the legacy self-prompt behavior."`
	Message       string              `json:"message,omitempty" jsonschema:"Approval message shown to the human for 'approval' nodes; supports templates"`
	Timeout       string              `json:"timeout,omitempty" jsonschema:"Per-node timeout as a Go time.Duration (e.g. '5m')."`
	Retry         *config.RetryConfig `json:"retry,omitempty" jsonschema:"Optional retry policy for transient failures"`
}

// schemaWorkflowDef mirrors config.WorkflowDef but uses schemaNodeDef so the
// node body schema can be expressed without a self-reference cycle.
type schemaWorkflowDef struct {
	Name        string                          `json:"name" jsonschema:"required,Workflow name (unique within its scope)"`
	Description string                          `json:"description,omitempty" jsonschema:"Human-readable description of what the workflow does"`
	Timeout     string                          `json:"timeout,omitempty" jsonschema:"Optional whole-DAG timeout as a Go time.Duration (e.g. '30m')"`
	Inputs      map[string]config.WorkflowInput `json:"inputs,omitempty" jsonschema:"Named input parameters the workflow expects at run time"`
	Nodes       []schemaNodeDef                 `json:"nodes" jsonschema:"required,Ordered list of DAG nodes; execution order is derived from depends_on"`
}

type saveWorkflowInput struct {
	Action   string            `json:"action" jsonschema:"required,Action: 'add' (create new) or 'update' (modify existing)"`
	Workflow schemaWorkflowDef `json:"workflow" jsonschema:"required,Full workflow definition with name, description, inputs, and nodes"`
	Scope    string            `json:"scope,omitempty" jsonschema:"Storage scope: 'global' (default) or 'project'. Requires channel context for project scope."`
}

func (s *Server) handleSaveWorkflow(_ context.Context, _ *mcp.CallToolRequest, input saveWorkflowInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "save_workflow", "action", input.Action, "scope", input.Scope)

	if input.Action != "add" && input.Action != "update" {
		return errorResult("action must be 'add' or 'update'"), nil, nil
	}

	// schemaWorkflowDef is plain data (strings, typed maps, slices), so the
	// marshal cannot fail, and the struct-to-struct round-trip into the
	// matching config.WorkflowDef field types cannot fail either.
	wfJSON, _ := json.Marshal(input.Workflow)
	var wf config.WorkflowDef
	_ = json.Unmarshal(wfJSON, &wf)

	body := map[string]any{
		"action":   input.Action,
		"workflow": json.RawMessage(wfJSON),
	}
	if input.Scope == "project" {
		body["scope"] = "project"
		if s.channelID != "" {
			body["channel_id"] = s.channelID
		}
	} else {
		body["scope"] = "global"
	}

	data, _ := json.Marshal(body)
	apiURL := fmt.Sprintf("%s/api/workflows", s.apiURL)

	if errResult, err := doAPICallNoBody(s, "POST", apiURL, http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	name := wf.Name
	scope := "global"
	if input.Scope == "project" {
		scope = "project"
	}
	verb := "Added"
	if input.Action == "update" {
		verb = "Updated"
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("%s workflow %q (%s scope).", verb, name, scope)},
		},
	}, nil, nil
}

type deleteWorkflowInput struct {
	Name  string `json:"name" jsonschema:"required,Name of the workflow to delete"`
	Scope string `json:"scope,omitempty" jsonschema:"Storage scope: 'global' (default) or 'project'. Requires channel context for project scope."`
}

func (s *Server) handleDeleteWorkflow(_ context.Context, _ *mcp.CallToolRequest, input deleteWorkflowInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "delete_workflow", "name", input.Name, "scope", input.Scope)

	if input.Name == "" {
		return errorResult("name is required"), nil, nil
	}

	body := map[string]any{
		"action": "delete",
		"name":   input.Name,
	}
	if input.Scope == "project" {
		body["scope"] = "project"
		if s.channelID != "" {
			body["channel_id"] = s.channelID
		}
	} else {
		body["scope"] = "global"
	}

	data, _ := json.Marshal(body)
	apiURL := fmt.Sprintf("%s/api/workflows", s.apiURL)

	if errResult, err := doAPICallNoBody(s, "POST", apiURL, http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	scope := "global"
	if input.Scope == "project" {
		scope = "project"
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Deleted workflow %q (%s scope).", input.Name, scope)},
		},
	}, nil, nil
}
