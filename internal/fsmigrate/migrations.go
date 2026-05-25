package fsmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"

	containerimage "github.com/radutopala/loop/internal/container/image"
)

// migrations holds all filesystem migrations in order. Position in the slice
// is the version number (index 0 is the bootstrap placeholder, never executed).
// Append a new entry to ship a new filesystem change; never reorder or delete
// existing entries.
var migrations = []Migration{
	{Description: "bootstrap"},
	{
		Description: "refresh embedded container/ files",
		Apply:       refreshContainerFiles,
	},
	{
		Description: "refresh container/ files for Debian base switch",
		Apply:       refreshContainerFiles,
	},
	{
		Description: "refresh container/ files: HOST_UID/HOST_GID pinning + chrome alpine revert",
		Apply:       refreshContainerFiles,
	},
	{
		Description: "seed builtin code review prompt shortcut",
		Apply: func(ctx context.Context, c *Ctx) error {
			return seedBuiltinCodeReviewShortcut(ctx, c, json.MarshalIndent)
		},
	},
	{
		Description: "seed review-loop and review-fix-loop workflows",
		Apply: func(ctx context.Context, c *Ctx) error {
			return seedReviewLoopWorkflows(ctx, c, json.MarshalIndent)
		},
	},
	{
		Description: "patch review-fix-loop verify step to commit leftover changes",
		Apply: func(ctx context.Context, c *Ctx) error {
			return patchReviewFixVerifyScript(ctx, c, json.MarshalIndent)
		},
	},
	{
		// Re-runs the patcher to swap the interim `git add -A` verify
		// script (shipped on dev builds of this branch before merge) for
		// the safer `git add -u` version. Users who already advanced past
		// migration #7 with the buggy script in place wouldn't be caught
		// by #7 a second time; this dedicated entry fixes them.
		Description: "patch review-fix-loop verify step: replace unsafe git add -A with git add -u",
		Apply: func(ctx context.Context, c *Ctx) error {
			return patchReviewFixVerifyScript(ctx, c, json.MarshalIndent)
		},
	},
}

// versionedContainerFiles are tracked by the daemon: each release ships a
// canonical version, and the migration overwrites stale on-disk copies after
// backing up any user changes to <name>.bkp. setup.sh is intentionally absent
// — it is treated as user-editable (skip-if-exists).
var versionedContainerFiles = []string{
	"Dockerfile",
	"entrypoint.sh",
	"agent-bashrc",
	"chrome.Dockerfile",
	"chrome-entrypoint.sh",
}

// refreshContainerFiles writes the container build assets embedded in the
// binary into ~/.loop/container/. It exists because defaultEnsureImage
// previously skipped this whole block when the Dockerfile was already
// present, leaving stale entrypoint.sh / agent-bashrc on disk after a
// daemon upgrade.
//
// Versioned files are overwritten so they track the daemon. Any pre-existing
// copy whose contents differ from the embedded version is preserved as
// <name>.bkp before the overwrite, so user edits are never silently lost.
// setup.sh is treated as user-editable: written only when missing.
func refreshContainerFiles(_ context.Context, c *Ctx) error {
	containerDir := filepath.Join(c.LoopDir, "container")
	if err := c.Sys.MkdirAll(containerDir, 0755); err != nil {
		return fmt.Errorf("creating container directory: %w", err)
	}
	for _, name := range versionedContainerFiles {
		data := containerimage.MustRead(name)
		path := filepath.Join(containerDir, name)
		existing, err := c.Sys.ReadFile(path)
		switch {
		case err == nil:
			if !bytes.Equal(existing, data) {
				if err := c.Sys.WriteFile(path+".bkp", existing, 0644); err != nil {
					return fmt.Errorf("backing up %s: %w", name, err)
				}
			}
		case errors.Is(err, os.ErrNotExist):
			// no prior file, nothing to back up
		default:
			return fmt.Errorf("reading existing %s: %w", name, err)
		}
		if err := c.Sys.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	setupPath := filepath.Join(containerDir, "setup.sh")
	if _, err := c.Sys.Stat(setupPath); err != nil {
		if err := c.Sys.WriteFile(setupPath, containerimage.MustRead("setup.sh"), 0644); err != nil {
			return fmt.Errorf("writing setup.sh: %w", err)
		}
	}
	return nil
}

// builtinCodeReviewShortcutName is the unique name we look for / write under.
// Whitespace is intentional — it's how the entry renders in the # picker.
const builtinCodeReviewShortcutName = "builtin code review"

// marshalIndentFunc matches json.MarshalIndent. Parameterized so tests can
// exercise the otherwise-unreachable marshal-error branch without resorting
// to package-level var save/restore.
type marshalIndentFunc func(v any, prefix, indent string) ([]byte, error)

// seedBuiltinCodeReviewShortcut appends a default shortcut to the user's
// existing ~/.loop/config.json. Fresh installs get the same entry via
// config.global.example.json on first onboard; this migration covers the
// upgrade path for installs that already have a config.
//
// No-ops when the file doesn't exist (onboard will handle it) or when an
// entry with the same name is already present (user may have added it
// themselves; never duplicate).
func seedBuiltinCodeReviewShortcut(_ context.Context, c *Ctx, marshal marshalIndentFunc) error {
	configPath := filepath.Join(c.LoopDir, "config.json")
	data, err := c.Sys.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", configPath, err)
	}

	standardized, err := hujson.Standardize(data)
	if err != nil {
		return fmt.Errorf("standardizing %s: %w", configPath, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(standardized, &cfg); err != nil {
		return fmt.Errorf("parsing %s: %w", configPath, err)
	}

	shortcuts, _ := cfg["prompt_shortcuts"].([]any)
	for _, item := range shortcuts {
		if m, ok := item.(map[string]any); ok && m["name"] == builtinCodeReviewShortcutName {
			return nil
		}
	}

	shortcuts = append(shortcuts, map[string]any{
		"name":        builtinCodeReviewShortcutName,
		"description": "Run Claude Code's built-in /code-review slash command",
		"prompt":      "/code-review",
	})
	cfg["prompt_shortcuts"] = shortcuts

	out, err := marshal(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing %s: %w", configPath, err)
	}
	if err := c.Sys.WriteFile(configPath, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", configPath, err)
	}
	return nil
}

// seededReviewLoopName / seededReviewFixLoopName are the canonical names of
// the two seeded review workflows. The FE's split button refers to these by
// name when starting a run, so the names must stay stable across releases.
const (
	seededReviewLoopName    = "review-loop"
	seededReviewFixLoopName = "review-fix-loop"
)

// reviewFixVerifyScript is the post-fix bash. The fix prompt asks the agent
// to commit, but that's best-effort; this stages any leftover changes and
// commits them deterministically. `git add -u` is deliberate: it stages
// modifications and deletions of *tracked* files only, so scratch files,
// debug logs, or dependency caches the agent leaves around don't get swept
// into the auto-generated commit. New files the agent intentionally creates
// must be committed by the agent itself (per the fix prompt). `git diff
// --cached --quiet` exits 0 when there's nothing to commit (agent already
// committed, or `-u` found no tracked changes) which short-circuits `git
// commit` via `||`. The fallback commit message intentionally omits
// {{.Iteration}} to keep the script template-free — distinct iterations
// produce distinct commits by content.
const reviewFixVerifyScript = "git add -u && (git diff --cached --quiet || git commit -m 'fix: address review feedback')"

// reviewFixVerifyScriptOld was the original verify script (a no-op HEAD
// print). Migration #7 patches in-place only when the user's config still
// has this exact value, so a customized verify step is left alone.
const reviewFixVerifyScriptOld = "git rev-parse HEAD"

// reviewFixVerifyScriptBuggyAddAll was an interim version of the verify
// script (briefly shipped on this branch before merge) that used `git add
// -A`, which swept untracked files into the auto-commit. Migration #8 looks
// for this exact value and rewrites it to `reviewFixVerifyScript`.
const reviewFixVerifyScriptBuggyAddAll = "git add -A && (git diff --cached --quiet || git commit -m 'fix: address review feedback')"

// seedReviewLoopWorkflows ensures both built-in review workflows are present
// in the user's ~/.loop/config.json. Each is skipped individually if an entry
// with the same name already exists (user may have customized it). No-ops
// when the config file doesn't exist (onboard handles fresh installs).
func seedReviewLoopWorkflows(_ context.Context, c *Ctx, marshal marshalIndentFunc) error {
	configPath := filepath.Join(c.LoopDir, "config.json")
	data, err := c.Sys.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", configPath, err)
	}

	standardized, err := hujson.Standardize(data)
	if err != nil {
		return fmt.Errorf("standardizing %s: %w", configPath, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(standardized, &cfg); err != nil {
		return fmt.Errorf("parsing %s: %w", configPath, err)
	}

	workflows, _ := cfg["workflows"].([]any)
	existing := make(map[string]struct{}, len(workflows))
	for _, item := range workflows {
		if m, ok := item.(map[string]any); ok {
			if name, _ := m["name"].(string); name != "" {
				existing[name] = struct{}{}
			}
		}
	}

	changed := false
	if _, ok := existing[seededReviewLoopName]; !ok {
		workflows = append(workflows, builtinReviewLoopDef())
		changed = true
	}
	if _, ok := existing[seededReviewFixLoopName]; !ok {
		workflows = append(workflows, builtinReviewFixLoopDef())
		changed = true
	}
	if !changed {
		return nil
	}
	cfg["workflows"] = workflows

	out, err := marshal(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing %s: %w", configPath, err)
	}
	if err := c.Sys.WriteFile(configPath, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", configPath, err)
	}
	return nil
}

// patchReviewFixVerifyScript walks ~/.loop/config.json, finds the
// `review-fix-loop` workflow, and rewrites its `verify` bash node's script
// to the current `reviewFixVerifyScript` value when the existing script
// matches one of the known-replaceable old versions (original no-op
// `git rev-parse HEAD` or the buggy interim `git add -A` variant). Any
// other script value is treated as a user customization and left alone.
// No-ops when the config file is missing or the workflow is absent.
func patchReviewFixVerifyScript(_ context.Context, c *Ctx, marshal marshalIndentFunc) error {
	configPath := filepath.Join(c.LoopDir, "config.json")
	data, err := c.Sys.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", configPath, err)
	}

	standardized, err := hujson.Standardize(data)
	if err != nil {
		return fmt.Errorf("standardizing %s: %w", configPath, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(standardized, &cfg); err != nil {
		return fmt.Errorf("parsing %s: %w", configPath, err)
	}

	replaceable := map[string]struct{}{
		reviewFixVerifyScriptOld:         {},
		reviewFixVerifyScriptBuggyAddAll: {},
	}
	workflows, _ := cfg["workflows"].([]any)
	patched := false
	for _, item := range workflows {
		wf, ok := item.(map[string]any)
		if !ok || wf["name"] != seededReviewFixLoopName {
			continue
		}
		nodes, _ := wf["nodes"].([]any)
		for _, n := range nodes {
			loopNode, ok := n.(map[string]any)
			if !ok {
				continue
			}
			body, _ := loopNode["body"].([]any)
			for _, child := range body {
				cm, ok := child.(map[string]any)
				if !ok || cm["id"] != "verify" {
					continue
				}
				script, _ := cm["script"].(string)
				if _, ok := replaceable[script]; ok {
					cm["script"] = reviewFixVerifyScript
					patched = true
				}
			}
		}
	}
	if !patched {
		return nil
	}

	out, err := marshal(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing %s: %w", configPath, err)
	}
	if err := c.Sys.WriteFile(configPath, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", configPath, err)
	}
	return nil
}

// reviewBashBodyChild is the bash node every seeded review loop pins as its
// first body child. The loop body parser keys off `id == "review"` to
// populate runCtx.Review with the CLI's JSON output (see
// internal/workflow/dag.go reviewBodyNodeID).
func reviewBashBodyChild() map[string]any {
	return map[string]any{
		"id":     "review",
		"type":   "bash",
		"script": "loop review run --channel-id {{.ChannelID}} --api-url $API_URL --wait",
	}
}

// builtinLoopDef builds the outer shape (name, description, inputs, single
// loop node with same-IDs stop-condition) shared by both seeded review
// workflows. The body children differ between the two — see callers.
func builtinLoopDef(name, description, inputDesc string, body []any) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputs": map[string]any{
			"max_iterations": map[string]any{
				"description": inputDesc,
				"default":     "3",
			},
		},
		"nodes": []any{
			map[string]any{
				"id":        "loop",
				"type":      "loop",
				"condition": "{{ or .Review.NoComments .Review.SameAsPrev }}",
				"body":      body,
			},
		},
	}
}

// builtinReviewLoopDef is the JSON-shaped definition of the review-only loop.
// Each iteration runs `loop review run --wait` inside the agent container;
// the workflow stops when the iteration produces zero comments OR repeats the
// same comment-id set as the previous iteration. With max_iterations=1 the
// behavior matches today's single-shot Review button.
func builtinReviewLoopDef() map[string]any {
	return builtinLoopDef(
		seededReviewLoopName,
		"Run review N times in a row, deduping comments across iterations.",
		"Number of review passes (1-10).",
		[]any{reviewBashBodyChild()},
	)
}

// builtinReviewFixLoopDef defines the review → fix → verify loop. Same stop
// condition as the review-only variant; adds two body children gated on
// `not .Review.NoComments` so the fix prompt and verification only run when
// the latest review surfaced findings.
func builtinReviewFixLoopDef() map[string]any {
	return builtinLoopDef(
		seededReviewFixLoopName,
		"Iterate review → fix → re-review until clean or max_iterations.",
		"Maximum review/fix iterations (1-10).",
		[]any{
			reviewBashBodyChild(),
			map[string]any{
				"id":     "fix",
				"type":   "prompt",
				"when":   "{{ not .Review.NoComments }}",
				"prompt": "Fix the following review comments and commit your changes:\n\n{{.Review.CommentsJSON}}",
			},
			map[string]any{
				"id":     "verify",
				"type":   "bash",
				"when":   "{{ not .Review.NoComments }}",
				"script": reviewFixVerifyScript,
			},
		},
	)
}
