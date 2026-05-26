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
// Returns the names of newly-added workflows. Patcher-only updates are not
// returned (we don't track which patcher touched what) but the UI reads
// success as "tried, no harm done".
func RestoreBuiltinWorkflows(ctx context.Context, c *Ctx) ([]string, error) {
	added, err := seedReviewLoopWorkflows(ctx, c)
	if err != nil {
		return nil, err
	}
	if err := patchReviewFixVerifyScript(ctx, c); err != nil {
		return nil, err
	}
	if err := patchReviewFixLoopBodyDeps(ctx, c); err != nil {
		return nil, err
	}
	return added, nil
}
