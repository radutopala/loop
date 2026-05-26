package fsmigrate

import (
	"context"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// The Restore* wrappers are thin: just confirm they reach the underlying
// seeder and return its result. The seeder's own branches are covered by
// TestSeed* in fsmigrate_test.go.

func (s *FSMigrateSuite) TestRestoreBuiltinShortcutsDelegatesAndReturnsAdded() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	added, err := RestoreBuiltinShortcuts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"builtin code review"}, added)
}

func (s *FSMigrateSuite) TestRestoreBuiltinShortcutsNoOpWhenConfigMissing() {
	sys := newFakeSystem()

	added, err := RestoreBuiltinShortcuts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), added)
}

func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsDelegatesAndReturnsAdded() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	added, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.ElementsMatch(s.T(), []string{"review-loop", "review-fix-loop"}, added)
}

func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsNoOpWhenConfigMissing() {
	sys := newFakeSystem()

	added, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), added)
}

// TestRestoreBuiltinWorkflowsPatchesStaleVerifyScript verifies that when a
// user already has the review-fix-loop workflow with an out-of-date verify
// script, RestoreBuiltinWorkflows brings it up to date — even though seeding
// itself is a no-op (the workflow is already "present" by name).
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsPatchesStaleVerifyScript() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	// Stale review-fix-loop with the OLD verify script the patcher replaces.
	stale := `{"workflows":[{"name":"review-fix-loop","nodes":[{"type":"loop","body":[{"id":"verify","script":"` + reviewFixVerifyScriptOld + `"}]}]}]}`
	sys.files[configPath] = []byte(stale)

	added, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), added, "review-loop", "review-loop is still missing and should be added")
	require.NotContains(s.T(), added, "review-fix-loop", "review-fix-loop is present by name — seed skips it")
	require.NotContains(s.T(), string(sys.files[configPath]), reviewFixVerifyScriptOld,
		"patcher should have replaced the stale verify script")
	require.Contains(s.T(), string(sys.files[configPath]), reviewFixVerifyScript,
		"patcher should have written the current verify script")
}

// TestRestoreBuiltinWorkflowsPatchesStaleBodyDeps: a hand-rolled fix-loop
// without depends_on on fix/verify must be patched in place by the second
// patcher in the chain.
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsPatchesStaleBodyDeps() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	stale := `{"workflows":[{"name":"review-fix-loop","nodes":[{"type":"loop","body":[{"id":"review"},{"id":"fix"},{"id":"verify"}]}]}]}`
	sys.files[configPath] = []byte(stale)

	_, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	out := string(sys.files[configPath])
	require.Contains(s.T(), out, `"depends_on"`, "fix/verify must gain depends_on after restore")
}
