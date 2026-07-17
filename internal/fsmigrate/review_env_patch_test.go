package fsmigrate

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// oldReviewLoopConfig is a config with both seeded review loops on the
// pre-env review script and no `pr` input — the exact shape the patcher
// upgrades.
const oldReviewLoopConfig = `{"workflows":[
  {"name":"review-loop","inputs":{"max_iterations":{"default":"1","description":"n"}},
   "nodes":[{"type":"loop","body":[{"id":"review","type":"bash","script":"loop review run --channel-id {{.ChannelID}} --api-url $API_URL --wait"}]}]},
  {"name":"review-fix-loop","inputs":{"max_iterations":{"default":"1","description":"n"}},
   "nodes":[{"type":"loop","body":[
     {"id":"review","type":"bash","script":"loop review run --channel-id {{.ChannelID}} --api-url $API_URL --wait"},
     {"id":"fix","type":"prompt","prompt":"fix"}
   ]}]}
]}`

func (s *FSMigrateSuite) patchEnv(config string) (*fakeSystem, string) {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(config)
	return sys, configPath
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvUpgradesBoth() {
	sys, configPath := s.patchEnv(oldReviewLoopConfig)

	// Exercise the migration wrapper (Apply signature).
	require.NoError(s.T(), patchReviewLoopEnvAndPRInput(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"}))

	got := string(sys.files[configPath])
	require.Contains(s.T(), got, reviewRunScript)
	require.NotContains(s.T(), got, reviewRunScriptOld)
	require.Contains(s.T(), got, `"pr"`)
	require.Contains(s.T(), got, reviewPRInputDesc)
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvReportsBothNames() {
	sys, _ := s.patchEnv(oldReviewLoopConfig)
	patched, err := patchReviewLoopEnvAndPRInputReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.ElementsMatch(s.T(), []string{"review-loop", "review-fix-loop"}, patched)
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvNoOpWhenAlreadyCurrent() {
	// Already on the new script with a pr input → nothing to do, no write.
	current := `{"workflows":[{"name":"review-loop","inputs":{"max_iterations":{"default":"1","description":"n"},"pr":{"default":"","description":"x"}},"nodes":[{"type":"loop","body":[{"id":"review","type":"bash","script":"` + reviewRunScript + `"}]}]}]}`
	sys, configPath := s.patchEnv(current)
	before := string(sys.files[configPath])

	patched, err := patchReviewLoopEnvAndPRInputReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), patched)
	require.Equal(s.T(), before, string(sys.files[configPath]), "no write when nothing changed")
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvPreservesCustomScript() {
	// A user-customized review script is left alone; the pr input is still added.
	custom := `{"workflows":[{"name":"review-loop","inputs":{"max_iterations":{"default":"1","description":"n"}},"nodes":[{"type":"loop","body":[{"id":"review","type":"bash","script":"my custom review"}]}]}]}`
	sys, configPath := s.patchEnv(custom)

	patched, err := patchReviewLoopEnvAndPRInputReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"review-loop"}, patched)

	got := string(sys.files[configPath])
	require.Contains(s.T(), got, "my custom review", "custom script preserved")
	require.NotContains(s.T(), got, reviewRunScript)
	require.Contains(s.T(), got, `"pr"`)
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvNoInputsMember() {
	// Old script but no `inputs` object → script upgraded, no pr add.
	noInputs := `{"workflows":[{"name":"review-loop","nodes":[{"type":"loop","body":[{"id":"review","type":"bash","script":"` + reviewRunScriptOld + `"}]}]}]}`
	sys, configPath := s.patchEnv(noInputs)

	patched, err := patchReviewLoopEnvAndPRInputReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"review-loop"}, patched)

	got := string(sys.files[configPath])
	require.Contains(s.T(), got, reviewRunScript)
	require.NotContains(s.T(), got, `"pr"`)
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvMissingConfig() {
	sys := newFakeSystem() // no config file
	patched, err := patchReviewLoopEnvAndPRInputReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), patched)
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvNonObjectRoot() {
	sys, _ := s.patchEnv(`["array","root"]`)
	_, err := patchReviewLoopEnvAndPRInputReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expected JSON object")
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvWriteError() {
	sys, configPath := s.patchEnv(oldReviewLoopConfig)
	sys.writeErr[configPath+".tmp"] = errors.New("io error")
	_, err := patchReviewLoopEnvAndPRInputReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing")
}

func (s *FSMigrateSuite) TestPatchReviewLoopEnvSkipsMalformedShapes() {
	// Drives the defensive continues + arrayValue nil/non-array guards: no
	// workflows key, non-array workflows, scalar workflow entry, missing/scalar
	// nodes, non-loop node, missing/scalar body, scalar body child, and a review
	// child with a non-string script. None should error or write.
	cases := []string{
		`{}`,
		`{"workflows":"scalar"}`,
		`{"workflows":[{"name":"other-workflow","nodes":[]}]}`,
		`{"workflows":["scalar",{"name":"review-loop"}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[42]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":"scalar"}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"type":"prompt"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"type":"loop"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"type":"loop","body":"scalar"}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"type":"loop","body":["scalar"]}]}]}`,
		`{"workflows":[{"name":"review-loop","nodes":[{"type":"loop","body":[{"id":"review","type":"bash","script":42}]}]}]}`,
	}
	for _, cfg := range cases {
		sys, _ := s.patchEnv(cfg)
		patched, err := patchReviewLoopEnvAndPRInputReport(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
		require.NoError(s.T(), err, cfg)
		require.Empty(s.T(), patched, cfg)
	}
}
