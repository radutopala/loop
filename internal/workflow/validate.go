package workflow

import (
	"fmt"

	"github.com/radutopala/loop/internal/config"
)

// validateWorkflowDef checks workflow-level invariants that the executor
// relies on but config loading does not enforce. Today it rejects loop body
// children of types the body executor doesn't handle (loop/approval) so the
// failure surfaces at StartRun rather than mid-execution.
func validateWorkflowDef(wfDef *config.WorkflowDef) error {
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
		}
	}
	return nil
}
