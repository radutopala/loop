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
