package fsmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// seededReviewLoopConfig is a review-loop as seeded before the dedup node:
// one loop node whose body is the current review script.
const seededReviewLoopConfig = `{
  // user comment
  "workflows":[
  {"name":"other","nodes":[]},
  {"name":"review-loop","inputs":{"max_iterations":{"default":"1","description":"n"}},
   "nodes":[{"id":"loop","type":"loop","body":[{"id":"review","type":"bash","script":"` + reviewRunScript + `"}]}]}
]}`

func (s *FSMigrateSuite) TestPatchReviewLoopDedupNodeAppends() {
	sys, configPath := s.patchEnv(seededReviewLoopConfig)

	require.NoError(s.T(), patchReviewLoopDedupNode(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))

	got := sys.files[configPath]
	require.Contains(s.T(), string(got), "// user comment")
	v, err := loadHJSONAt(sys, configPath)
	require.NoError(s.T(), err)
	v.Standardize()
	var cfg struct {
		Workflows []struct {
			Name  string           `json:"name"`
			Nodes []map[string]any `json:"nodes"`
		} `json:"workflows"`
	}
	require.NoError(s.T(), json.Unmarshal(v.Pack(), &cfg))
	nodes := cfg.Workflows[1].Nodes
	require.Len(s.T(), nodes, 2)
	require.Equal(s.T(), map[string]any{
		"id":         "dedup",
		"type":       "bash",
		"when":       reviewDedupWhenExpr,
		"script":     reviewDedupScript,
		"depends_on": []any{"loop"},
	}, nodes[1])
	require.Empty(s.T(), cfg.Workflows[0].Nodes)

	// A second pass finds the node and leaves the file alone.
	patched, err := patchReviewLoopDedupNodeReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.False(s.T(), patched)
	require.Equal(s.T(), got, sys.files[configPath])
}

// The seeded definition and a patched install end up with the same node;
// review-fix-loop gets none.
func (s *FSMigrateSuite) TestBuiltinReviewLoopDefHasDedupNode() {
	nodes := builtinReviewLoopDef()["nodes"].([]any)
	require.Equal(s.T(), reviewDedupNode(), nodes[len(nodes)-1])
	require.Len(s.T(), builtinReviewFixLoopDef()["nodes"].([]any), 1)
}

func (s *FSMigrateSuite) TestPatchReviewLoopDedupNodeLeavesCustomisedLoops() {
	cases := []string{
		`{}`,
		`["array","root"]`,
		`{"workflows":"scalar"}`,
		`{"workflows":["scalar"]}`,
		`{"workflows":[{"name":"review-loop"}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[42]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"other","type":"loop"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"bash"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":[]}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":["scalar"]}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":[{"id":"review","script":"my review"}]}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":[{"id":"other","script":"` + reviewRunScript + `"}]}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":[{"id":"review","script":"` + reviewRunScript + `"}]},{"id":"mine","type":"bash"}]}]}`,
	}
	for _, cfg := range cases {
		sys, configPath := s.patchEnv(cfg)
		patched, err := patchReviewLoopDedupNodeReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
		if cfg == `["array","root"]` {
			require.ErrorContains(s.T(), err, "expected JSON object")
			continue
		}
		require.NoError(s.T(), err, cfg)
		require.False(s.T(), patched, cfg)
		require.Equal(s.T(), cfg, string(sys.files[configPath]), cfg)
	}

	// No config file at all.
	patched, err := patchReviewLoopDedupNodeReport(context.Background(), &Ctx{Sys: newFakeSystem(), LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.False(s.T(), patched)
}

func (s *FSMigrateSuite) TestPatchReviewLoopDedupNodeWriteError() {
	sys, configPath := s.patchEnv(seededReviewLoopConfig)
	sys.writeErr[configPath+".tmp"] = errors.New("io error")
	_, err := patchReviewLoopDedupNodeReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.ErrorContains(s.T(), err, "writing")
}

func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsAddsDedupNode() {
	sys, configPath := s.patchEnv(seededReviewLoopConfig)
	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"review-fix-loop"}, added)
	require.Equal(s.T(), []string{"review-loop"}, patched)
	require.Contains(s.T(), string(sys.files[configPath]), reviewDedupScript)
}

// Reads: seed, verify, deps, env/pr, then the dedup patcher's (call 5).
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsSurfacesDedupPatcherError() {
	configPath := filepath.Join("/loop", "config.json")
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{}`)
	wrapper := &readCountingSys{fakeSystem: sys, target: configPath, errOnCall: 5, err: errors.New("disk read failed")}
	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: wrapper, LoopDir: "/loop"})
	require.ErrorContains(s.T(), err, "disk read failed")
	require.Nil(s.T(), added)
	require.Nil(s.T(), patched)
}
