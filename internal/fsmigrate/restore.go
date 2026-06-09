package fsmigrate

import (
	"context"
	"sync"
)

// restoreMu serializes Restore* invocations so the three sequential
// load-modify-save cycles in RestoreBuiltinWorkflows (seed → patch verify →
// patch deps) can't interleave with each other or with RestoreBuiltinShortcuts.
// /api/builtins/restore is HTTP-driven and the user can double-click the
// Settings button; without serialization the second call's hujson AST is
// based on the pre-first-write snapshot and clobbers the first's mutations
// on Pack+Write.
var restoreMu sync.Mutex

// RestoreBuiltinShortcuts re-seeds any missing built-in prompt shortcuts into
// ~/.loop/config.json AND upgrades an unmodified "builtin code review" entry
// in place. Idempotent: a missing built-in is appended; an entry whose prompt
// still matches the original seed is patched to the current prompt; a
// user-edited entry is left untouched. Returns (added, patched) names — the
// two lists are disjoint, since a freshly-added entry already has the current
// prompt and won't be patched.
func RestoreBuiltinShortcuts(ctx context.Context, c *Ctx) (added []string, patched []string, err error) {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	added, err = seedBuiltinCodeReviewShortcut(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	didPatch, err := patchBuiltinCodeReviewShortcutPromptReport(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	if didPatch {
		patched = append(patched, builtinCodeReviewShortcutName)
	}
	addedSimplify, err := seedBuiltinSimplifyShortcut(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	added = append(added, addedSimplify...)
	return added, patched, nil
}

// RestoreBuiltinWorkflows re-seeds any missing built-in workflows into
// ~/.loop/config.json AND re-applies the body-shape patchers on top.
//
// Why two phases: seedReviewLoopWorkflows is skip-if-present, so a user who
// hand-edited an older review-fix-loop without the verify-script /
// body-depends_on fixes would keep the broken shape forever — Settings'
// "Restore built-ins" would silently return [] and the workflow would still
// crash mid-loop. The patchers below are idempotent and only mutate when
// the expected shape is missing, so running them on a clean seed is a no-op
// and running them on a stale workflow brings it up to date.
//
// Returns (added, patched, error). `added` is the names of newly-seeded
// workflows; `patched` is the names of workflows the patchers mutated in
// place (e.g. `review-fix-loop` when its verify script was stale). The two
// lists are disjoint — a freshly-added workflow has nothing to patch. The
// caller surfaces both to the FE so a "Restore" that only patched (no new
// seeds) is reported as "updated" rather than the misleading
// "already present" implied by an empty `added`.
func RestoreBuiltinWorkflows(ctx context.Context, c *Ctx) (added []string, patched []string, err error) {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	added, err = seedReviewLoopWorkflows(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	verifyPatched, err := patchReviewFixVerifyScriptReport(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	depsPatched, err := patchReviewFixLoopBodyDepsReport(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	// Both patchers operate only on `review-fix-loop`. De-dupe via a set.
	patchedSet := map[string]struct{}{}
	if verifyPatched {
		patchedSet[seededReviewFixLoopName] = struct{}{}
	}
	if depsPatched {
		patchedSet[seededReviewFixLoopName] = struct{}{}
	}
	for n := range patchedSet {
		patched = append(patched, n)
	}
	return added, patched, nil
}
