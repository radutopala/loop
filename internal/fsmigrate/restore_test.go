package fsmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// corruptingSys wraps the fake system and corrupts the named file's stored
// bytes after the first write — so a subsequent read returns garbage HJSON.
// Drives RestoreBuiltinWorkflows' "seed succeeded, patcher errored" branch
// (which can't happen with the plain fakeSystem because both phases read
// the same file from the same map).
type corruptingSys struct {
	*fakeSystem
	target  string
	written bool
}

func (c *corruptingSys) WriteFile(name string, data []byte, perm os.FileMode) error {
	if err := c.fakeSystem.WriteFile(name, data, perm); err != nil {
		return err
	}
	if name == c.target && !c.written {
		c.written = true
		c.files[name] = []byte("{not valid hjson")
	}
	return nil
}

// Rename mirrors WriteFile's corrupt-on-first-success behavior so the
// atomicWriteConfig path (write tmp → rename to target) also triggers
// corruption when the *destination* equals the target.
func (c *corruptingSys) Rename(oldpath, newpath string) error {
	if err := c.fakeSystem.Rename(oldpath, newpath); err != nil {
		return err
	}
	if newpath == c.target && !c.written {
		c.written = true
		c.files[newpath] = []byte("{not valid hjson")
	}
	return nil
}

// The Restore* wrappers are thin: just confirm they reach the underlying
// seeder and return its result. The seeder's own branches are covered by
// TestSeed* in fsmigrate_test.go.

func (s *FSMigrateSuite) TestRestoreBuiltinShortcutsDelegatesAndReturnsAdded() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	added, patched, err := RestoreBuiltinShortcuts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.ElementsMatch(s.T(), []string{"builtin code review", "builtin simplify"}, added)
	require.Empty(s.T(), patched)
}

func (s *FSMigrateSuite) TestRestoreBuiltinShortcutsNoOpWhenConfigMissing() {
	sys := newFakeSystem()

	added, patched, err := RestoreBuiltinShortcuts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), added)
	require.Empty(s.T(), patched)
}

// TestRestoreBuiltinShortcutsUpgradesUnmodifiedEntry covers the upgrade path:
// an existing entry still holding the bare "/code-review" prompt is patched
// (not added), and the patched name is reported.
func (s *FSMigrateSuite) TestRestoreBuiltinShortcutsUpgradesUnmodifiedEntry() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"prompt_shortcuts":[{"name":"builtin code review","description":"Run Claude Code's built-in /code-review slash command","prompt":"/code-review"}]}`)

	added, patched, err := RestoreBuiltinShortcuts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	// The code-review entry is patched (not added); simplify is missing so it
	// is added.
	require.Equal(s.T(), []string{"builtin simplify"}, added)
	require.Equal(s.T(), []string{"builtin code review"}, patched)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	sc := cfg["prompt_shortcuts"].([]any)[0].(map[string]any)
	require.Equal(s.T(), builtinCodeReviewShortcutPrompt, sc["prompt"])
	require.Equal(s.T(), builtinCodeReviewShortcutDescription, sc["description"])
}

// TestRestoreBuiltinShortcutsLeavesUserEditedEntry verifies a user-customized
// prompt is never overwritten by the patcher (the missing simplify shortcut is
// still seeded alongside it).
func (s *FSMigrateSuite) TestRestoreBuiltinShortcutsLeavesUserEditedEntry() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"prompt_shortcuts":[{"name":"builtin code review","prompt":"my custom review prompt"}]}`)

	added, patched, err := RestoreBuiltinShortcuts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"builtin simplify"}, added)
	require.Empty(s.T(), patched)

	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(sys.files[configPath], &cfg))
	shortcuts := cfg["prompt_shortcuts"].([]any)
	require.Len(s.T(), shortcuts, 2)
	cr := shortcuts[0].(map[string]any)
	require.Equal(s.T(), "builtin code review", cr["name"])
	require.Equal(s.T(), "my custom review prompt", cr["prompt"], "user-edited prompt must not be rewritten")
	require.Equal(s.T(), "builtin simplify", shortcuts[1].(map[string]any)["name"])
}

// TestRestoreBuiltinShortcutsSeedError surfaces an error from the seed step
// (top-level config is not a JSON object).
func (s *FSMigrateSuite) TestRestoreBuiltinShortcutsSeedError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`["not an object"]`)

	_, _, err := RestoreBuiltinShortcuts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
}

// TestRestoreBuiltinShortcutsPatchError surfaces an error from the patch step:
// the entry is already present (so the seed no-ops) but the write fails when
// the patcher rewrites the unmodified prompt.
func (s *FSMigrateSuite) TestRestoreBuiltinShortcutsPatchError() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"prompt_shortcuts":[{"name":"builtin code review","prompt":"/code-review"}]}`)
	sys.writeErr[configPath+".tmp"] = errors.New("io error")

	_, _, err := RestoreBuiltinShortcuts(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
}

func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsDelegatesAndReturnsAdded() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.ElementsMatch(s.T(), []string{"review-loop", "review-fix-loop"}, added)
	require.Empty(s.T(), patched, "freshly seeded workflows have nothing to patch")
}

func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsNoOpWhenConfigMissing() {
	sys := newFakeSystem()

	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Empty(s.T(), added)
	require.Empty(s.T(), patched)
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

	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), added, "review-loop", "review-loop is still missing and should be added")
	require.NotContains(s.T(), added, "review-fix-loop", "review-fix-loop is present by name — seed skips it")
	require.Contains(s.T(), patched, "review-fix-loop", "review-fix-loop's verify script was patched in place")
	require.NotContains(s.T(), string(sys.files[configPath]), reviewFixVerifyScriptOld,
		"patcher should have replaced the stale verify script")
	require.Contains(s.T(), string(sys.files[configPath]), reviewFixVerifyScript,
		"patcher should have written the current verify script")
}

func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsSurfacesVerifyPatcherError() {
	// Seed succeeds (writes the two workflows into the fresh config), but
	// the wrapper corrupts the file on disk after that write so the verify
	// patcher's loadConfigHJSON fails. RestoreBuiltinWorkflows must bubble
	// the error rather than swallow it as "nothing patched."
	configPath := filepath.Join("/loop", "config.json")
	sys := &corruptingSys{fakeSystem: newFakeSystem(), target: configPath}
	sys.files[configPath] = []byte(`{}`)

	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Nil(s.T(), added)
	require.Nil(s.T(), patched)
}

// TestRestoreBuiltinWorkflowsSurfacesSeedError verifies that an error from
// the seed phase (call 1 of the read sequence) is surfaced rather than
// swallowed. Without this propagation, a disk failure during seed would let
// the patchers run on stale bytes, then RestoreBuiltinWorkflows would
// silently "succeed" with `added=nil`.
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsSurfacesSeedError() {
	configPath := filepath.Join("/loop", "config.json")
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{}`)
	wrapper := &readCountingSys{fakeSystem: sys, target: configPath, errOnCall: 1, err: errors.New("disk read failed")}
	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: wrapper, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Nil(s.T(), added)
	require.Nil(s.T(), patched)
	require.Contains(s.T(), err.Error(), "disk read failed")
}

func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsSurfacesDepsPatcherError() {
	// Seed both workflows successfully into a clean config, then arrange for
	// the *second* patcher (body-deps) to fail by injecting a read error on
	// its read pass. The verify patcher succeeds (reads the post-seed bytes),
	// but we then poison the file via readErr for the deps patcher's read.
	configPath := filepath.Join("/loop", "config.json")
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{}`)

	// Seed first.
	added, err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), added)

	// Now arrange so the verify patcher reads fine but the deps patcher errors.
	// Each phase re-reads target once: (1) seedReviewLoopWorkflows,
	// (2) patchReviewFixVerifyScriptReport, (3) patchReviewFixLoopBodyDepsReport.
	// We want the deps patcher (call 3) to fail. Reuse corruptingSys's
	// corrupt-on-first-write isn't useful here (nothing else writes); instead
	// use a per-call counter sys.
	wrapper := &readCountingSys{fakeSystem: sys, target: configPath, errOnCall: 3, err: errors.New("disk read failed")}
	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: wrapper, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Nil(s.T(), added)
	require.Nil(s.T(), patched)
	require.Contains(s.T(), err.Error(), "disk read failed")
}

// TestRestoreBuiltinWorkflowsPatchesStaleReviewScript covers the env/pr patcher
// path in RestoreBuiltinWorkflows: an existing review-loop on the old review
// script (seed skips it by name) gets its script upgraded and a `pr` input
// added, and is surfaced in `patched`.
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsPatchesStaleReviewScript() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	stale := `{"workflows":[{"name":"review-loop","inputs":{"max_iterations":{"default":"1","description":"n"}},"nodes":[{"type":"loop","body":[{"id":"review","type":"bash","script":"` + reviewRunScriptOld + `"}]}]}]}`
	sys.files[configPath] = []byte(stale)

	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), added, "review-fix-loop", "review-fix-loop was missing and seeded")
	require.Contains(s.T(), patched, "review-loop", "review-loop's review script was upgraded in place")
	got := string(sys.files[configPath])
	require.Contains(s.T(), got, reviewRunScript)
	require.NotContains(s.T(), got, reviewRunScriptOld)
	require.Contains(s.T(), got, `"pr"`)
}

func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsSurfacesEnvPatcherError() {
	// The env/pr patcher is the 4th read (seed, verify, deps, env). Fail that
	// read so RestoreBuiltinWorkflows bubbles the error.
	configPath := filepath.Join("/loop", "config.json")
	sys := newFakeSystem()
	sys.files[configPath] = []byte(`{}`)
	wrapper := &readCountingSys{fakeSystem: sys, target: configPath, errOnCall: 4, err: errors.New("disk read failed")}
	added, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: wrapper, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Nil(s.T(), added)
	require.Nil(s.T(), patched)
	require.Contains(s.T(), err.Error(), "disk read failed")
}

// readCountingSys errors on the N-th ReadFile call against `target`. Drives
// the deps-patcher error branch in RestoreBuiltinWorkflows without tripping
// the seed or the first patcher.
type readCountingSys struct {
	*fakeSystem
	target    string
	errOnCall int
	err       error
	calls     int
}

func (c *readCountingSys) ReadFile(name string) ([]byte, error) {
	if name == c.target {
		c.calls++
		if c.calls == c.errOnCall {
			return nil, c.err
		}
	}
	return c.fakeSystem.ReadFile(name)
}

// TestRestoreBuiltinWorkflowsPatchesStaleBodyDeps: a hand-rolled fix-loop
// without depends_on on fix/verify must be patched in place by the second
// patcher in the chain.
func (s *FSMigrateSuite) TestRestoreBuiltinWorkflowsPatchesStaleBodyDeps() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	stale := `{"workflows":[{"name":"review-fix-loop","nodes":[{"type":"loop","body":[{"id":"review"},{"id":"fix"},{"id":"verify"}]}]}]}`
	sys.files[configPath] = []byte(stale)

	_, patched, err := RestoreBuiltinWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), patched, "review-fix-loop", "body-deps patch must surface as patched")
	out := string(sys.files[configPath])
	require.Contains(s.T(), out, `"depends_on"`, "fix/verify must gain depends_on after restore")
}
