package workflow

import (
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
	require.NoError(s.T(), validateWorkflowDef(wf))
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
	err := validateWorkflowDef(wf)
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
	err := validateWorkflowDef(wf)
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
	err := validateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "wat")
}

func (s *ValidateSuite) TestLoopBodyChildSameIDAsParentLoopIsAllowed() {
	// The loop persists its top-level node_runs row at iteration=0; body
	// children persist with their own iteration values, so a body child
	// sharing the loop's own ID does NOT race UPSERTs (only an external
	// top-level node with the same ID would). Validator allows the reuse.
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
	require.NoError(s.T(), validateWorkflowDef(wf))
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
	err := validateWorkflowDef(wf)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "collides")
	require.Contains(s.T(), err.Error(), "shared")
}

func (s *ValidateSuite) TestNonLoopNodeBodyIgnored() {
	wf := &config.WorkflowDef{
		Name: "wf",
		Nodes: []config.NodeDef{{
			ID:   "p",
			Type: config.NodeTypePrompt,
		}},
	}
	require.NoError(s.T(), validateWorkflowDef(wf))
}
