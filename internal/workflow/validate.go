package workflow

import (
	"fmt"

	"github.com/radutopala/loop/internal/config"
)

// validateWorkflowDef checks workflow-level invariants that the executor
// relies on but config loading does not enforce. Today it rejects loop body
// children of types the body executor doesn't handle (loop/approval) and
// body-child IDs that collide with a top-level node ID (which would cause
// node_runs UPSERT collisions on the (run_id, node_id, 0) row).
func validateWorkflowDef(wfDef *config.WorkflowDef) error {
	topLevelIDs := make(map[string]struct{}, len(wfDef.Nodes))
	for i := range wfDef.Nodes {
		topLevelIDs[wfDef.Nodes[i].ID] = struct{}{}
	}
	for i := range wfDef.Nodes {
		n := &wfDef.Nodes[i]
		if n.Type != config.NodeTypeLoop {
			continue
		}
		for _, child := range n.Body {
			switch child.Type {
			case config.NodeTypePrompt, config.NodeTypeBash:
				// ok
			case config.NodeTypeLoop:
				return fmt.Errorf("loop %q: body child %q is a nested loop, which is not supported", n.ID, child.ID)
			case config.NodeTypeApproval:
				return fmt.Errorf("loop %q: body child %q is an approval, which is not supported inside a loop body", n.ID, child.ID)
			default:
				return fmt.Errorf("loop %q: body child %q has unsupported type %q", n.ID, child.ID, child.Type)
			}
			// The loop node itself is a top-level node, so a body child
			// reusing the same ID is fine (the loop persists at iter=0 once;
			// the body child persists per iteration). Any *other* top-level
			// ID collision would race UPSERTs at (run_id, node_id, 0).
			if child.ID == n.ID {
				continue
			}
			if _, clash := topLevelIDs[child.ID]; clash {
				return fmt.Errorf("loop %q: body child %q collides with top-level node %q (would race UPSERTs on (run_id, node_id, 0))", n.ID, child.ID, child.ID)
			}
		}
	}
	return nil
}
