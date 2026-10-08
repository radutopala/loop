package fsmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

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

// Restore doesn't give a review-loop without the final dedup node one: the
// patcher that once added it no longer runs there.
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsAddsNoDedupNode() {
	sys, configPath := s.patchEnv(seededReviewLoopConfig)
	added, _, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"review-fix-loop"}, added)
	require.NotContains(s.T(), string(sys.files[configPath]), reviewDedupScript)
}

// Restore drops the final dedup node from a review-loop seeded with it.
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsDropsDedupNode() {
	sys, configPath := s.patchEnv(seededReviewLoopFinalDedupConfig)
	_, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"review-loop"}, patched)
	require.NotContains(s.T(), string(sys.files[configPath]), reviewDedupScript)
}

// Reads: seed, verify, deps, env/pr, then the dedup node patcher's (call 5).
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

// seededReviewLoopFinalDedupConfig is a review-loop as seeded with the final
// dedup node.
const seededReviewLoopFinalDedupConfig = `{
  // user comment
  "workflows":[
  {"name":"other","nodes":[]},
  {"name":"review-loop","inputs":{"max_iterations":{"default":"1","description":"n"}},
   "nodes":[
     {"id":"loop","type":"loop","condition":"{{ or .Review.NoComments .Review.SameAsPrev }}","body":[{"id":"review","type":"bash","script":"` + reviewRunScript + `"}]},
     {"id":"dedup","type":"bash","when":"{{ ne .Inputs.max_iterations \"1\" }}","script":"` + reviewDedupScript + `","depends_on":["loop"]}
   ]}
]}`

func (s *FSMigrateSuite) TestPatchReviewLoopDropDedupNode() {
	sys, configPath := s.patchEnv(seededReviewLoopFinalDedupConfig)

	require.NoError(s.T(), patchReviewLoopDropDedupNode(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))

	got := sys.files[configPath]
	require.Contains(s.T(), string(got), "// user comment")
	v, err := loadHJSONAt(sys, configPath)
	require.NoError(s.T(), err)
	v.Standardize()
	var cfg struct {
		Workflows []struct {
			Nodes []map[string]any `json:"nodes"`
		} `json:"workflows"`
	}
	require.NoError(s.T(), json.Unmarshal(v.Pack(), &cfg))
	require.Empty(s.T(), cfg.Workflows[0].Nodes)
	nodes := cfg.Workflows[1].Nodes
	require.Len(s.T(), nodes, 1)
	require.Equal(s.T(), "{{ or .Review.NoComments .Review.SameAsPrev }}", nodes[0]["condition"])
	require.Equal(s.T(), []any{map[string]any{"id": "review", "type": "bash", "script": reviewRunScript}}, nodes[0]["body"])

	// A second pass finds nothing to drop and leaves the file alone.
	patched, err := patchReviewLoopDropDedupNodeReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.False(s.T(), patched)
	require.Equal(s.T(), got, sys.files[configPath])
}

func (s *FSMigrateSuite) TestPatchReviewLoopDropDedupNodeLeavesCustomisedLoops() {
	loop := `{"id":"loop","type":"loop","body":[{"id":"review","script":"` + reviewRunScript + `"}]}`
	dedup := `{"id":"dedup","script":"` + reviewDedupScript + `"}`
	cases := []string{
		`{}`,
		`["array","root"]`,
		`{"workflows":"scalar"}`,
		`{"workflows":["scalar"]}`,
		`{"workflows":[{"name":"other","nodes":[` + loop + `,` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop"}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[` + loop + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[` + loop + `,42]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[` + loop + `,{"id":"mine","script":"` + reviewDedupScript + `"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[` + loop + `,{"id":"dedup","script":"my dedup"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":[{"id":"review","script":"my review"}]},` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[42,` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"other","type":"loop"},` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"bash"},` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop"},` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":[]},` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":["scalar"]},` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":[{"id":"other","script":"` + reviewRunScript + `"}]},` + dedup + `]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[` + loop + `,` + dedup + `,{"id":"mine"}]}]}`,
	}
	for _, cfg := range cases {
		sys, configPath := s.patchEnv(cfg)
		patched, err := patchReviewLoopDropDedupNodeReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
		if cfg == `["array","root"]` {
			require.ErrorContains(s.T(), err, "expected JSON object")
			continue
		}
		require.NoError(s.T(), err, cfg)
		require.False(s.T(), patched, cfg)
		require.Equal(s.T(), cfg, string(sys.files[configPath]), cfg)
	}

	// No config file at all.
	patched, err := patchReviewLoopDropDedupNodeReport(context.Background(), &Ctx{Sys: newFakeSystem(), LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.False(s.T(), patched)
}

func (s *FSMigrateSuite) TestPatchReviewLoopDropDedupNodeWriteError() {
	sys, configPath := s.patchEnv(seededReviewLoopFinalDedupConfig)
	sys.writeErr[configPath+".tmp"] = errors.New("io error")
	_, err := patchReviewLoopDropDedupNodeReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.ErrorContains(s.T(), err, "writing")
}

// The dedup drop runs before the colon-path patch, so it still knows a
// review-loop seeded with `loop review run`.
func (s *FSMigrateSuite) TestPatchReviewLoopDropDedupNodeOnSpacedScript() {
	spaced := strings.ReplaceAll(seededReviewLoopFinalDedupConfig, reviewRunScript, reviewRunScriptSpaced)
	sys, configPath := s.patchEnv(spaced)

	patched, err := patchReviewLoopDropDedupNodeReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.True(s.T(), patched)
	got := string(sys.files[configPath])
	require.NotContains(s.T(), got, reviewDedupScript)
	require.Contains(s.T(), got, reviewRunScriptSpaced)
}
