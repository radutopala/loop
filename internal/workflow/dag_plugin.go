package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// errPluginsOff is the error of a plugin node while plugins are off.
var errPluginsOff = errors.New("plugins are not enabled")

// executePluginNode runs a plugin node on the plugin's container in the
// run's channel, with its input rendered. An action succeeds once the
// plugin accepts it; a tool's content is the node's output, its structured
// content the node's data, and a result the plugin marks as an error fails
// the node.
func (e *defaultEngine) executePluginNode(ctx context.Context, run *db.WorkflowRun, node *config.NodeDef, runCtx *RunContext, mu *sync.Mutex) (nodeExecResult, error) {
	if e.plugins == nil {
		return nodeExecResult{}, errPluginsOff
	}
	var res nodeExecResult
	var data any
	in, err := pluginInput(node.Input)
	if err != nil {
		return res, err
	}
	if in == nil && node.Tool != "" {
		in = map[string]any{}
	}
	var body json.RawMessage
	if in != nil {
		mu.Lock()
		rendered, err := renderInput(in, runCtx)
		mu.Unlock()
		if err != nil {
			return res, fmt.Errorf("rendering input template: %w", err)
		}
		// The input is decoded JSON with rendered strings, which always
		// encodes.
		body, _ = json.Marshal(rendered)
		res.input = string(body)
	}
	callID := "workflow:" + run.ID + ":" + node.ID
	if node.Action != "" {
		if err := e.plugins.RunPluginAction(ctx, node.Plugin, node.Action, run.ChannelID, run.DirPath, callID, body); err != nil {
			return res, fmt.Errorf("running %s's action %s: %w", node.Plugin, node.Action, err)
		}
		res.output = "accepted"
	} else {
		tr, err := e.plugins.CallPluginTool(ctx, node.Plugin, node.Tool, run.ChannelID, run.DirPath, callID, body)
		if err != nil {
			return res, fmt.Errorf("calling %s's tool %s: %w", node.Plugin, node.Tool, err)
		}
		res.output = tr.Content
		if tr.IsError {
			return res, fmt.Errorf("%s's tool %s answered an error: %s", node.Plugin, node.Tool, tr.Content)
		}
		if len(tr.Structured) > 0 {
			if data, err = decodeJSON(tr.Structured); err != nil {
				return res, fmt.Errorf("%s's tool %s answered structured content that isn't JSON: %w", node.Plugin, node.Tool, err)
			}
			res.data = tr.Structured
		}
	}

	mu.Lock()
	runCtx.NodeOutputs[node.ID] = res.output
	if data != nil {
		runCtx.NodeData[node.ID] = data
	} else {
		// A loop body node's earlier iteration may have left data.
		delete(runCtx.NodeData, node.ID)
	}
	mu.Unlock()
	return res, nil
}

// renderInput renders every string in v, a decoded JSON value, as a
// template, in place.
func renderInput(v any, runCtx *RunContext) (any, error) {
	var err error
	switch v := v.(type) {
	case string:
		return renderTemplate(v, runCtx)
	case map[string]any:
		for k, x := range v {
			if v[k], err = renderInput(x, runCtx); err != nil {
				return nil, err
			}
		}
	case []any:
		for i, x := range v {
			if v[i], err = renderInput(x, runCtx); err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}
