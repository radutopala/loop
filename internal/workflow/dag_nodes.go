package workflow

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

func (e *defaultEngine) executePromptNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (nodeExecResult, error) {
	promptText, err := node.ResolvePrompt(e.loopDir, os.ReadFile)
	if err != nil {
		return nodeExecResult{}, fmt.Errorf("resolving prompt: %w", err)
	}
	mu.Lock()
	prompt, err := renderTemplate(promptText, runCtx)
	mu.Unlock()
	if err != nil {
		return nodeExecResult{}, fmt.Errorf("rendering prompt template: %w", err)
	}

	var systemPrompt string
	if node.SystemPrompt != "" {
		mu.Lock()
		systemPrompt, err = renderTemplate(node.SystemPrompt, runCtx)
		mu.Unlock()
		if err != nil {
			return nodeExecResult{input: prompt}, fmt.Errorf("rendering system prompt template: %w", err)
		}
	}

	req := &agent.AgentRequest{
		ChannelID:    run.ChannelID,
		DirPath:      run.DirPath,
		Prompt:       prompt,
		SystemPrompt: systemPrompt,
	}

	resp, err := e.runner.Run(ctx, req)
	if err != nil {
		return nodeExecResult{input: prompt}, err
	}
	// Capture the session id even on agent error so the transcript is locatable.
	res := nodeExecResult{output: resp.Response, input: prompt, sessionID: resp.SessionID}
	if resp.Error != "" {
		return res, fmt.Errorf("agent error: %s", resp.Error)
	}

	// Store output for downstream nodes.
	mu.Lock()
	runCtx.NodeOutputs[node.ID] = resp.Response
	mu.Unlock()

	return res, nil
}

func (e *defaultEngine) executeBashNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (nodeExecResult, error) {
	mu.Lock()
	script, err := renderBashScript(node.Script, runCtx)
	mu.Unlock()
	if err != nil {
		return nodeExecResult{}, fmt.Errorf("rendering script template: %w", err)
	}

	output, err := e.bashRunner.RunBash(ctx, script, run.ChannelID, run.DirPath)
	if err != nil {
		return nodeExecResult{output: output, input: script}, err
	}

	// Store output for downstream nodes.
	mu.Lock()
	runCtx.NodeOutputs[node.ID] = output
	mu.Unlock()

	return nodeExecResult{output: output, input: script}, nil
}

func (e *defaultEngine) executeApprovalNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (string, error) {
	// Render message template.
	mu.Lock()
	message, err := renderTemplate(node.Message, runCtx)
	mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("rendering approval message template: %w", err)
	}

	// Parse timeout.
	timeout := 24 * time.Hour // default 24h
	if node.Timeout != "" {
		if parsed, parseErr := time.ParseDuration(node.Timeout); parseErr == nil {
			timeout = parsed
		}
	}

	// Create approval channel keyed by run:node to support parallel approvals.
	approvalKey := run.ID + ":" + node.ID
	approvalCh := make(chan string, 1)
	e.pendingApprovals.Store(approvalKey, approvalCh)
	defer e.pendingApprovals.Delete(approvalKey)

	// Persist paused status via a fresh DB write to avoid racing on the shared
	// run struct. Other goroutines may read/write run fields concurrently.
	if err := e.updateRunStatus(ctx, run.ID, db.WorkflowRunStatusPaused, node.ID); err != nil {
		return "", fmt.Errorf("failed to persist paused status: %w", err)
	}

	// Broadcast paused event.
	if e.broadcaster != nil {
		e.broadcaster.BroadcastWorkflowRunPaused(events.WorkflowRunEventData{
			RunID:        run.ID,
			WorkflowName: run.WorkflowName,
			ChannelID:    run.ChannelID,
			Status:       string(db.WorkflowRunStatusPaused),
			PausedNodeID: node.ID,
		})
	}

	// Wait for resume, timeout, or cancellation.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case response := <-approvalCh:
		// Resume — restore running status.
		if err := e.updateRunStatus(ctx, run.ID, db.WorkflowRunStatusRunning, ""); err != nil {
			return "", fmt.Errorf("failed to restore running status: %w", err)
		}

		// Store the response as node output.
		if response == "" {
			response = "approved"
		}
		mu.Lock()
		runCtx.NodeOutputs[node.ID] = response
		mu.Unlock()
		return message + "\nApproval response: " + response, nil

	case <-timer.C:
		return "", fmt.Errorf("approval timed out after %s", timeout)

	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// evaluateWhen evaluates the "when" condition. Must be called with mu held.
func (e *defaultEngine) evaluateWhen(node *config.NodeDef, runCtx *RunContext) bool {
	if node.When == "" {
		return true
	}
	result, err := renderTemplate(node.When, runCtx)
	if err != nil {
		e.logger.Warn("workflow: when condition failed", "node_id", node.ID, "error", err)
		return true // default to running on template error
	}
	return result == "true"
}

// checkTriggerRule checks whether a node's trigger rule is satisfied. Must be called with mu held.
func (e *defaultEngine) checkTriggerRule(node *config.NodeDef, nodeStatus map[string]db.NodeRunStatus) bool {
	rule := node.TriggerRule
	if rule == "" {
		rule = "all_success"
	}

	switch rule {
	case "all_success":
		for _, dep := range node.DependsOn {
			if nodeStatus[dep] != db.NodeRunStatusSuccess {
				return false
			}
		}
		return true
	case "all_done":
		for _, dep := range node.DependsOn {
			s := nodeStatus[dep]
			if s != db.NodeRunStatusSuccess && s != db.NodeRunStatusFailed && s != db.NodeRunStatusSkipped {
				return false
			}
		}
		return true
	case "one_success":
		for _, dep := range node.DependsOn {
			if nodeStatus[dep] == db.NodeRunStatusSuccess {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// renderTemplate renders a Go text/template string with the given RunContext.
func renderTemplate(tmplStr string, data *RunContext) (string, error) {
	if tmplStr == "" {
		return "", nil
	}
	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
