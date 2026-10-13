package fsmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// seededReviewLoopsConfig holds both seeded review loops as they were before
// the final check node.
const seededReviewLoopsConfig = `{
  // user comment
  "workflows":[
  {"name":"other","nodes":[]},
  {"name":"review-loop","nodes":[{"id":"loop","type":"loop","body":[{"id":"review","type":"bash","script":"` + reviewRunScript + `"}]}]},
  {"name":"review-fix-loop","nodes":[{"id":"loop","type":"loop","body":[
    {"id":"review","type":"bash","script":"` + reviewRunScript + `"},
    {"id":"fix","type":"prompt","prompt":"fix","depends_on":["review"]}
  ]}]}
]}`

func (s *FSMigrateSuite) TestPatchReviewLoopFinalCheck() {
	sys, configPath := s.patchEnv(seededReviewLoopsConfig)

	require.NoError(s.T(), patchReviewLoopFinalCheck(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))

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
	check := map[string]any{"id": "check", "type": "bash", "script": reviewCheckScript, "depends_on": []any{"loop"}}
	for _, wf := range cfg.Workflows[1:] {
		require.Len(s.T(), wf.Nodes, 2)
		review := wf.Nodes[0]["body"].([]any)[0].(map[string]any)
		require.Equal(s.T(), reviewRunScriptNoVerify, review["script"])
		require.Equal(s.T(), check, wf.Nodes[1])
	}
	require.Len(s.T(), cfg.Workflows[2].Nodes[0]["body"], 2)

	// A second pass finds nothing to patch and leaves the file alone.
	patched, err := patchReviewLoopFinalCheckReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Nil(s.T(), patched)
	require.Equal(s.T(), got, sys.files[configPath])
}

func (s *FSMigrateSuite) TestPatchReviewLoopFinalCheckLeavesCustomisedLoops() {
	review := `{"id":"review","script":"` + reviewRunScript + `"}`
	cases := []string{
		`{}`,
		`["array","root"]`,
		`{"workflows":"scalar"}`,
		`{"workflows":["scalar"]}`,
		`{"workflows":[{"name":"other","nodes":[{"id":"loop","body":[` + review + `]}]}]}`,
		`{"workflows":[{"name":"review-loop"}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","body":[` + review + `]},{"id":"mine"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[42]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"other","body":[` + review + `]}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","body":[]}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","body":["scalar"]}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"id":"loop","body":[{"id":"other","script":"` + reviewRunScript + `"}]}]}]}`,
		`{"workflows":[{"name":"review-fix-loop","nodes":[{"id":"loop","body":[{"id":"review","script":"my review"}]}]}]}`,
	}
	for _, cfg := range cases {
		sys, configPath := s.patchEnv(cfg)
		patched, err := patchReviewLoopFinalCheckReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
		if cfg == `["array","root"]` {
			require.ErrorContains(s.T(), err, "expected JSON object")
			continue
		}
		require.NoError(s.T(), err, cfg)
		require.Nil(s.T(), patched, cfg)
		require.Equal(s.T(), cfg, string(sys.files[configPath]), cfg)
	}

	// No config file at all.
	patched, err := patchReviewLoopFinalCheckReport(context.Background(), &Ctx{Sys: newFakeSystem(), LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Nil(s.T(), patched)
}

func (s *FSMigrateSuite) TestPatchReviewLoopFinalCheckWriteError() {
	sys, configPath := s.patchEnv(seededReviewLoopsConfig)
	sys.writeErr[configPath+".tmp"] = errors.New("io error")
	_, err := patchReviewLoopFinalCheckReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.ErrorContains(s.T(), err, "writing")
}

// Restore brings both seeded loops up to the final check.
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsAddsFinalCheck() {
	sys, configPath := s.patchEnv(seededReviewLoopsConfig)
	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), added)
	require.ElementsMatch(s.T(), []string{"review-loop", "review-fix-loop"}, patched)
	require.Contains(s.T(), string(sys.files[configPath]), reviewCheckScript)
}

// Reads: seed, verify, deps, env/pr, dedup, then the final-check patcher's
// (call 6).
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsSurfacesFinalCheckPatcherError() {
	configPath := filepath.Join("/loop", "config.json")
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{}`)
	wrapper := &readCountingSys{fakeSystem: sys, target: configPath, errOnCall: 6, err: errors.New("disk read failed")}
	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: wrapper, LoopDir: "/loop"})
	require.ErrorContains(s.T(), err, "disk read failed")
	require.Nil(s.T(), added)
	require.Nil(s.T(), patched)
}
