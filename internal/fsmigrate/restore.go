package fsmigrate

import "context"

// RestoreBuiltinShortcuts re-seeds any missing built-in prompt shortcuts into
// ~/.loop/config.json. Idempotent: present entries (including user-modified
// ones) are left untouched; only missing built-ins are appended. Returns the
// list of names that were added (empty when everything was already present).
func RestoreBuiltinShortcuts(ctx context.Context, c *Ctx) ([]string, error) {
	return seedBuiltinCodeReviewShortcut(ctx, c)
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
