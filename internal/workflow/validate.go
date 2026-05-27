package workflow

import (
	"fmt"

	"github.com/radutopala/loop/internal/config"
)

// validateWorkflowDef checks workflow-level invariants that the executor
// relies on but config loading does not enforce. Today it rejects:
//   - loop body children of types the body executor doesn't handle
//     (loop/approval)
//   - body-child IDs that collide with a top-level node ID (which would
//     cause node_runs UPSERT collisions on the (run_id, node_id, 0) row)
//   - duplicate body-child IDs across loops in the same workflow (two
//     loops sharing a body-child ID would collide on the schema's
//     UNIQUE(run_id, node_id, iteration) AND cause WorkflowGraph's
//     expandLoopBodies to inflate each loop's iteration count from
//     the other's rows)
func validateWorkflowDef(wfDef *config.WorkflowDef) error {
	topLevelIDs := make(map[string]struct{}, len(wfDef.Nodes))
	for i := range wfDef.Nodes {
		topLevelIDs[wfDef.Nodes[i].ID] = struct{}{}
	}
	// Tracks which loop first claimed a given body-child ID, so a second
	// loop reusing the same ID gets a precise "owned by X" error message.
	bodyChildOwner := map[string]string{}
	for i := range wfDef.Nodes {
		n := &wfDef.Nodes[i]
		if n.Type != config.NodeTypeLoop {
			continue
		}
		// Track per-loop occurrences so two body children of the SAME loop
		// with the same ID also surface as a precise error.
		thisLoopChildIDs := map[string]struct{}{}
		// Track which body-child IDs have been declared earlier in this
		// loop so a depends_on reference to a later sibling can be rejected
		// at StartRun time. executeLoopBody iterates body children in
		// declaration order and never inspects child.DependsOn, so an
		// "out-of-order" dep would silently run before the dep was satisfied.
		declaredEarlier := map[string]struct{}{}
		for _, child := range n.Body {
			// n.Body is []*config.NodeDef; a nil entry (manual config edit or
			// future parser hiccup) would panic on the child.Type switch
			// below. Reject with a clear message instead.
			if child == nil {
				return fmt.Errorf("loop %q: body contains a nil child entry", n.ID)
			}
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
			// The loop's own node_runs row is persisted at
			// (run_id, loop_id, 0) by executeNode before executeLoopNode
			// runs (dag.go:302-309), AND each body child's iteration=0 row
			// is persisted at (run_id, child_id, 0) by executeLoopBody. So
			// a body child reusing the loop's own ID would race UPSERTs at
			// (run_id, loop_id, 0) — the body child's status writes would
			// clobber the loop's Running row (and vice versa for completion).
			// Same race applies to any other top-level node ID collision.
			if _, clash := topLevelIDs[child.ID]; clash {
				return fmt.Errorf("loop %q: body child %q collides with top-level node %q (would race UPSERTs on (run_id, node_id, 0))", n.ID, child.ID, child.ID)
			}
			if _, dup := thisLoopChildIDs[child.ID]; dup {
				return fmt.Errorf("loop %q: body child %q appears twice in this loop's body (would race UPSERTs on (run_id, node_id, iteration))", n.ID, child.ID)
			}
			thisLoopChildIDs[child.ID] = struct{}{}
			// executeLoopBody walks body children in declaration order; a
			// depends_on reference to a later sibling is a contradiction the
			// executor can't honor. Reject at validation time so the user
			// sees a precise message at StartRun instead of a confusing
			// runtime behavior where the dep fires before the dependency.
			for _, dep := range child.DependsOn {
				if _, ok := declaredEarlier[dep]; !ok {
					return fmt.Errorf("loop %q: body child %q depends_on %q which is not declared earlier in this loop's body", n.ID, child.ID, dep)
				}
			}
			declaredEarlier[child.ID] = struct{}{}
			if owner, taken := bodyChildOwner[child.ID]; taken && owner != n.ID {
				return fmt.Errorf("loop %q: body child %q is already used by loop %q — two loops cannot share a body-child ID (would race UPSERTs on (run_id, node_id, iteration) and inflate WorkflowGraph iteration counts)", n.ID, child.ID, owner)
			}
			bodyChildOwner[child.ID] = n.ID
		}
	}
	return nil
}
