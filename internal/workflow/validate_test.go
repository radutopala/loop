package workflow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
)

type ValidateSuite struct {
	suite.Suite
}

func TestValidateSuite(t *testing.T) { suite.Run(t, new(ValidateSuite)) }

func (s *ValidateSuite) TestLoopBodyAcceptsPromptAndBash() {
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "loop",
			Type: config.NodeTypeLoop,
			Body: []*config.NodeDef{
				{ID: "p", Type: config.NodeTypePrompt, Prompt: "hi"},
				{ID: "b", Type: config.NodeTypeBash, Script: "true"},
			},
		}},
	}
	require.NoError(s.T(), ValidateWorkflowDef(wf))
}

func (s *ValidateSuite) TestLoopBodyRejectsNestedLoop() {
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "loop",
			Type: config.NodeTypeLoop,
			Body: []*config.NodeDef{
				{ID: "inner", Type: config.NodeTypeLoop},
			},
		}},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "nested loop")
}

func (s *ValidateSuite) TestLoopBodyRejectsApproval() {
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "loop",
			Type: config.NodeTypeLoop,
			Body: []*config.NodeDef{
				{ID: "ok", Type: config.NodeTypeApproval},
			},
		}},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "approval")
}

func (s *ValidateSuite) TestLoopBodyRejectsUnknownType() {
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "loop",
			Type: config.NodeTypeLoop,
			Body: []*config.NodeDef{
				{ID: "x", Type: "wat"},
			},
		}},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "wat")
}

func (s *ValidateSuite) TestLoopBodyChildSameIDAsParentLoopIsRejected() {
	// The loop's own node_runs row is persisted at (run_id, loop_id, 0) by
	// executeNode; the body child's iteration=0 row would be persisted at
	// the same key by executeLoopBody and race UPSERTs.
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "loop",
			Type: config.NodeTypeLoop,
			Body: []*config.NodeDef{
				{ID: "loop", Type: config.NodeTypeBash, Script: "echo loop"},
			},
		}},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "collides")
}

func (s *ValidateSuite) TestLoopBodyChildIDClashesWithSiblingTopLevelNode() {
	// A bash child inside the loop reuses the ID of a sibling top-level
	// prompt node — that would race UPSERTs at (run_id, "shared", 0).
	// Validator must reject.
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{
			{ID: "shared", Type: config.NodeTypePrompt, Prompt: "hi"},
			{
				ID:   "loop",
				Type: config.NodeTypeLoop,
				Body: []*config.NodeDef{
					{ID: "shared", Type: config.NodeTypeBash, Script: "echo dup"},
				},
			},
		},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "collides")
	require.Contains(s.T(), err.Error(), "shared")
}

func (s *ValidateSuite) TestLoopBodyRejectsDuplicateIDWithinSameLoop() {
	// Two body children of the same loop reusing the same ID would race
	// UPSERTs at (run_id, "dup", iteration) — one child's row would clobber
	// the other every iteration.
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "loop",
			Type: config.NodeTypeLoop,
			Body: []*config.NodeDef{
				{ID: "dup", Type: config.NodeTypeBash, Script: "echo a"},
				{ID: "dup", Type: config.NodeTypeBash, Script: "echo b"},
			},
		}},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "appears twice")
	require.Contains(s.T(), err.Error(), "dup")
}

func (s *ValidateSuite) TestLoopBodyRejectsDuplicateIDAcrossLoops() {
	// Two loops sharing a body-child ID would race UPSERTs at
	// (run_id, "shared", iteration) AND cause WorkflowGraph's expandLoopBodies
	// to inflate each loop's iteration count by counting the other's rows.
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{
			{
				ID:   "loopA",
				Type: config.NodeTypeLoop,
				Body: []*config.NodeDef{
					{ID: "shared", Type: config.NodeTypeBash, Script: "echo a"},
				},
			},
			{
				ID:   "loopB",
				Type: config.NodeTypeLoop,
				Body: []*config.NodeDef{
					{ID: "shared", Type: config.NodeTypeBash, Script: "echo b"},
				},
			},
		},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "already used by loop")
	require.Contains(s.T(), err.Error(), "loopA")
	require.Contains(s.T(), err.Error(), "loopB")
}

func (s *ValidateSuite) TestLoopBodyAcceptsSameIDInDifferentNonOverlappingLoops() {
	// Two loops with disjoint body-child IDs are fine — the cross-loop check
	// only fires when the same ID appears under two different loop owners.
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{
			{
				ID:   "loopA",
				Type: config.NodeTypeLoop,
				Body: []*config.NodeDef{
					{ID: "childA", Type: config.NodeTypeBash, Script: "echo a"},
				},
			},
			{
				ID:   "loopB",
				Type: config.NodeTypeLoop,
				Body: []*config.NodeDef{
					{ID: "childB", Type: config.NodeTypeBash, Script: "echo b"},
				},
			},
		},
	}
	require.NoError(s.T(), ValidateWorkflowDef(wf))
}

func (s *ValidateSuite) TestNonLoopNodeBodyIgnored() {
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "p",
			Type: config.NodeTypePrompt,
		}},
	}
	require.NoError(s.T(), ValidateWorkflowDef(wf))
}

func (s *ValidateSuite) TestLoopBodyRejectsNilChildEntry() {
	// A nil entry in n.Body (manual config edit or future parser hiccup)
	// would panic on the child.Type switch — reject with a precise message
	// at validation time.
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "loop",
			Type: config.NodeTypeLoop,
			Body: []*config.NodeDef{nil},
		}},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "nil child entry")
}

func (s *ValidateSuite) TestLoopBodyRejectsForwardDependsOn() {
	// A body child whose depends_on references a sibling declared LATER in
	// the body cannot be honored by executeLoopBody (which walks body
	// children in declaration order). Reject at StartRun time so the user
	// sees the contradiction immediately.
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "loop",
			Type: config.NodeTypeLoop,
			Body: []*config.NodeDef{
				{ID: "first", Type: config.NodeTypeBash, Script: "echo a", DependsOn: []string{"second"}},
				{ID: "second", Type: config.NodeTypeBash, Script: "echo b"},
			},
		}},
	}
	err := ValidateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "depends_on")
	require.Contains(s.T(), err.Error(), "second")
}

func (s *ValidateSuite) TestPluginNode() {
	plugin := func(n config.NodeDef) config.NodeDef {
		n.ID = "n"
		n.Type = config.NodeTypePlugin
		return n
	}
	tests := []struct {
		name    string
		node    config.NodeDef
		wantErr string
	}{
		{name: "action", node: plugin(config.NodeDef{Plugin: "quiz", Action: "daily"})},
		{name: "action with empty input", node: plugin(config.NodeDef{Plugin: "quiz", Action: "daily", Input: json.RawMessage(`{}`)})},
		{name: "action with null input", node: plugin(config.NodeDef{Plugin: "quiz", Action: "daily", Input: json.RawMessage(`null`)})},
		{name: "tool", node: plugin(config.NodeDef{Plugin: "quiz", Tool: "ask"})},
		{name: "tool with input", node: plugin(config.NodeDef{Plugin: "quiz", Tool: "ask", Input: json.RawMessage(`{"topic":"{{.Inputs.t}}"}`)})},
		{name: "no plugin", node: plugin(config.NodeDef{Tool: "ask"}), wantErr: `plugin node "n": plugin is required`},
		{name: "neither", node: plugin(config.NodeDef{Plugin: "quiz"}), wantErr: `plugin node "n": set exactly one of action and tool`},
		{name: "both", node: plugin(config.NodeDef{Plugin: "quiz", Action: "daily", Tool: "ask"}), wantErr: `plugin node "n": set exactly one of action and tool`},
		{name: "input not an object", node: plugin(config.NodeDef{Plugin: "quiz", Tool: "ask", Input: json.RawMessage(`"x"`)}), wantErr: `plugin node "n": input must be a JSON object`},
		{name: "input not JSON", node: plugin(config.NodeDef{Plugin: "quiz", Tool: "ask", Input: json.RawMessage(`{`)}), wantErr: `plugin node "n": input must be a JSON object: `},
		{name: "action with input", node: plugin(config.NodeDef{Plugin: "quiz", Action: "daily", Input: json.RawMessage(`{"a":"{{.Inputs.a}}"}`)})},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := ValidateWorkflowDef(&config.WorkflowDef{Name: "wf", Nodes: []config.NodeDef{tt.node}})
			if tt.wantErr == "" {
				require.NoError(s.T(), err)
				return
			}
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}
}

func (s *ValidateSuite) TestLoopBodyPluginChild() {
	body := func(child *config.NodeDef) *config.WorkflowDef {
		return &config.WorkflowDef{Name: "wf", Nodes: []config.NodeDef{{
			ID: "loop", Type: config.NodeTypeLoop, Body: []*config.NodeDef{child},
		}}}
	}
	require.NoError(s.T(), ValidateWorkflowDef(body(&config.NodeDef{ID: "p", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask"})))
	err := ValidateWorkflowDef(body(&config.NodeDef{ID: "p", Type: config.NodeTypePlugin, Plugin: "quiz"}))
	require.EqualError(s.T(), err, `loop "loop": plugin node "p": set exactly one of action and tool`)
}
