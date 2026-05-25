package fsmigrate

import (
	"context"
	"encoding/json"
)

// RestoreBuiltinShortcuts re-seeds any missing built-in prompt shortcuts into
// ~/.loop/config.json. Idempotent: present entries (including user-modified
// ones) are left untouched; only missing built-ins are appended. Returns the
// list of names that were added (empty when everything was already present).
func RestoreBuiltinShortcuts(ctx context.Context, c *Ctx) ([]string, error) {
	return seedBuiltinCodeReviewShortcut(ctx, c, json.MarshalIndent)
}

// RestoreBuiltinWorkflows re-seeds any missing built-in workflows into
// ~/.loop/config.json. Same skip-if-present semantics as
// RestoreBuiltinShortcuts. Returns the names that were added.
func RestoreBuiltinWorkflows(ctx context.Context, c *Ctx) ([]string, error) {
	return seedReviewLoopWorkflows(ctx, c, json.MarshalIndent)
}
