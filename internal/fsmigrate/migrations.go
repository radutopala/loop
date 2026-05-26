package fsmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
			_, err := seedBuiltinCodeReviewShortcut(ctx, c)
			return err
		},
	},
	{
		Description: "seed review-loop and review-fix-loop workflows",
		Apply: func(ctx context.Context, c *Ctx) error {
			_, err := seedReviewLoopWorkflows(ctx, c)
			return err
		},
	},
	{
		Description: "patch review-fix-loop verify step to commit leftover changes",
		Apply:       patchReviewFixVerifyScript,
	},
	{
		// Re-runs the patcher to swap the interim `git add -A` verify
		// script (shipped on dev builds of this branch before merge) for
		// the safer `git add -u` version. Users who already advanced past
		// migration #7 with the buggy script in place wouldn't be caught
		// by #7 a second time; this dedicated entry fixes them.
		Description: "patch review-fix-loop verify step: replace unsafe git add -A with git add -u",
		Apply:       patchReviewFixVerifyScript,
	},
	{
		// Backfill body-child `depends_on` so the FE WorkflowGraph draws
		// review → fix → verify edges within each iteration. Without this
		// the graph only shows cross-iteration verify[i-1] → review[i]
		// links because expandLoopBodies emits edges from depends_on only;
		// executeLoopNode runs body children in array order regardless,
		// so the change is purely visual.
		Description: "patch review-fix-loop body children with explicit depends_on",
		Apply:       patchReviewFixLoopBodyDeps,
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

// seedBuiltinCodeReviewShortcut appends a default shortcut to the user's
// existing ~/.loop/config.json. Fresh installs get the same entry via
// config.global.example.json on first onboard; this migration covers the
// upgrade path for installs that already have a config.
//
// No-ops when the file doesn't exist (onboard will handle it) or when an
// entry with the same name is already present (user may have added it
// themselves; never duplicate). Mutations go through the hujson AST so the
// user's HJSON comments and key ordering survive — round-tripping through
// json.Unmarshal would silently strip both.
func seedBuiltinCodeReviewShortcut(_ context.Context, c *Ctx) ([]string, error) {
	v, configPath, err := loadConfigHJSON(c)
	if err != nil || v == nil {
		return nil, err
	}
	rootObj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("parsing %s: expected JSON object at top level", configPath)
	}

	existing := arrayMemberStringValues(rootObj, "prompt_shortcuts", "name")
	if _, present := existing[builtinCodeReviewShortcutName]; present {
		return nil, nil
	}

	def := map[string]any{
		"name":        builtinCodeReviewShortcutName,
		"description": "Run Claude Code's built-in /code-review slash command",
		"prompt":      "/code-review",
	}
	if err := appendOrCreateArrayMember(v, "prompt_shortcuts", def); err != nil {
		return nil, fmt.Errorf("patching %s: %w", configPath, err)
	}
	if err := c.Sys.WriteFile(configPath, v.Pack(), 0644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", configPath, err)
	}
	return []string{builtinCodeReviewShortcutName}, nil
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
//
// Mutations go through the hujson AST so the user's HJSON comments and key
// ordering survive. The previous implementation round-tripped through
// json.Unmarshal + json.MarshalIndent, which silently stripped every comment
// and re-sorted keys alphabetically — a real problem now that
// /api/builtins/restore lets the user trigger this on-demand from Settings.
func seedReviewLoopWorkflows(_ context.Context, c *Ctx) ([]string, error) {
	v, configPath, err := loadConfigHJSON(c)
	if err != nil || v == nil {
		return nil, err
	}
	rootObj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("parsing %s: expected JSON object at top level", configPath)
	}

	existing := arrayMemberStringValues(rootObj, "workflows", "name")

	type seed struct {
		name string
		def  map[string]any
	}
	seeds := []seed{
		{seededReviewLoopName, builtinReviewLoopDef()},
		{seededReviewFixLoopName, builtinReviewFixLoopDef()},
	}
	var added []string
	for _, s := range seeds {
		if _, present := existing[s.name]; present {
			continue
		}
		if err := appendOrCreateArrayMember(v, "workflows", s.def); err != nil {
			return nil, fmt.Errorf("patching %s: %w", configPath, err)
		}
		added = append(added, s.name)
	}
	if len(added) == 0 {
		return nil, nil
	}
	if err := c.Sys.WriteFile(configPath, v.Pack(), 0644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", configPath, err)
	}
	return added, nil
}

// patchReviewFixVerifyScript walks ~/.loop/config.json, finds the
// `review-fix-loop` workflow, and rewrites its `verify` bash node's script
// to the current `reviewFixVerifyScript` value when the existing script
// matches one of the known-replaceable old versions (original no-op
// `git rev-parse HEAD` or the buggy interim `git add -A` variant). Any
// other script value is treated as a user customization and left alone.
// No-ops when the config file is missing or the workflow is absent.
//
// Operates on the hujson AST and mutates only the script literal in place,
// so the user's surrounding comments, key ordering, and formatting survive.
func patchReviewFixVerifyScript(ctx context.Context, c *Ctx) error {
	_, err := patchReviewFixVerifyScriptReport(ctx, c)
	return err
}

// patchReviewFixVerifyScriptReport is the (bool, error) variant of
// patchReviewFixVerifyScript used by RestoreBuiltinWorkflows to surface
// patched-but-not-added workflows to the FE. The bool is true iff the
// patcher wrote to disk.
func patchReviewFixVerifyScriptReport(_ context.Context, c *Ctx) (bool, error) {
	v, configPath, err := loadConfigHJSON(c)
	if err != nil || v == nil {
		return false, err
	}
	rootObj, ok := v.Value.(*hujson.Object)
	if !ok {
		return false, fmt.Errorf("parsing %s: expected JSON object at top level", configPath)
	}

	replaceable := map[string]struct{}{
		reviewFixVerifyScriptOld:         {},
		reviewFixVerifyScriptBuggyAddAll: {},
	}
	patched := false
	forEachReviewFixLoopBodyChild(rootObj, func(childObj *hujson.Object) {
		id, _ := memberString(childObj, "id")
		if id != "verify" {
			return
		}
		scriptVal := findObjectMember(childObj, "script")
		if scriptVal == nil {
			return
		}
		scriptLit, ok := scriptVal.Value.(hujson.Literal)
		if !ok {
			return
		}
		if _, ok := replaceable[scriptLit.String()]; !ok {
			return
		}
		scriptVal.Value = hujson.String(reviewFixVerifyScript)
		patched = true
	})
	if !patched {
		return false, nil
	}
	if err := c.Sys.WriteFile(configPath, v.Pack(), 0644); err != nil {
		return false, fmt.Errorf("writing %s: %w", configPath, err)
	}
	return true, nil
}

// patchReviewFixLoopBodyDeps walks ~/.loop/config.json, finds the
// `review-fix-loop` workflow, and adds explicit `depends_on` to its `fix`
// and `verify` body children (fix→review, verify→fix) when they're absent.
// Only fills in missing deps — never overwrites a user-set value — so a
// customized workflow with deliberate parallel siblings is preserved.
// No-ops when the config file is missing or the workflow is absent.
//
// Operates on the hujson AST and patches each body child individually via
// JSON Patch ops so user comments and key ordering survive.
func patchReviewFixLoopBodyDeps(ctx context.Context, c *Ctx) error {
	_, err := patchReviewFixLoopBodyDepsReport(ctx, c)
	return err
}

// patchReviewFixLoopBodyDepsReport is the (bool, error) variant used by
// RestoreBuiltinWorkflows. The bool is true iff the patcher wrote to disk.
func patchReviewFixLoopBodyDepsReport(_ context.Context, c *Ctx) (bool, error) {
	v, configPath, err := loadConfigHJSON(c)
	if err != nil || v == nil {
		return false, err
	}
	rootObj, ok := v.Value.(*hujson.Object)
	if !ok {
		return false, fmt.Errorf("parsing %s: expected JSON object at top level", configPath)
	}

	wantDeps := map[string]string{"fix": "review", "verify": "fix"}
	patched := false
	forEachReviewFixLoopBodyChild(rootObj, func(childObj *hujson.Object) {
		id, _ := memberString(childObj, "id")
		dep, want := wantDeps[id]
		if !want {
			return
		}
		if findObjectMember(childObj, "depends_on") != nil {
			return
		}
		childObj.Members = append(childObj.Members, hujson.ObjectMember{
			Name:  hujson.Value{Value: hujson.String("depends_on")},
			Value: hujson.Value{Value: parseJSONValue([]any{dep})},
		})
		patched = true
	})
	if !patched {
		return false, nil
	}
	if err := c.Sys.WriteFile(configPath, v.Pack(), 0644); err != nil {
		return false, fmt.Errorf("writing %s: %w", configPath, err)
	}
	return true, nil
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
				"default":     "1",
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
				"id":         "fix",
				"type":       "prompt",
				"when":       "{{ not .Review.NoComments }}",
				"prompt":     "Fix the following review comments and commit your changes:\n\n{{.Review.CommentsJSON}}",
				"depends_on": []any{"review"},
			},
			map[string]any{
				"id":         "verify",
				"type":       "bash",
				"when":       "{{ not .Review.NoComments }}",
				"script":     reviewFixVerifyScript,
				"depends_on": []any{"fix"},
			},
		},
	)
}

// loadConfigHJSON reads ~/.loop/config.json and parses it via hujson so the
// returned AST retains every comment and key ordering from the source. A
// missing file returns (nil, path, nil) so callers can treat it as a no-op
// — the caller usually short-circuits with `if v == nil { return ..., err }`.
func loadConfigHJSON(c *Ctx) (*hujson.Value, string, error) {
	configPath := filepath.Join(c.LoopDir, "config.json")
	data, err := c.Sys.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, configPath, nil
		}
		return nil, configPath, fmt.Errorf("reading %s: %w", configPath, err)
	}
	v, err := hujson.Parse(data)
	if err != nil {
		return nil, configPath, fmt.Errorf("parsing %s: %w", configPath, err)
	}
	return &v, configPath, nil
}

// findObjectMember returns a pointer to the named member's Value, or nil if
// no such member exists. Pointer return lets callers mutate the value in
// place (e.g. swap a script literal) without rebuilding the parent object.
func findObjectMember(obj *hujson.Object, name string) *hujson.Value {
	for i := range obj.Members {
		nameLit, ok := obj.Members[i].Name.Value.(hujson.Literal)
		if !ok {
			continue
		}
		if nameLit.String() == name {
			return &obj.Members[i].Value
		}
	}
	return nil
}

// memberString returns the string value of the named member when it is
// present and is a JSON string literal. Anything else (missing, non-string,
// nested object) returns "", false so the caller's `if` chain keeps walking.
func memberString(obj *hujson.Object, name string) (string, bool) {
	v := findObjectMember(obj, name)
	if v == nil {
		return "", false
	}
	lit, ok := v.Value.(hujson.Literal)
	if !ok || lit.Kind() != '"' {
		return "", false
	}
	return lit.String(), true
}

// arrayMemberStringValues returns the set of string values found under
// memberKey across each object element of rootObj[arrayKey]. Used to dedupe
// seeded entries by name: if the user already has a workflow / shortcut with
// the same name, we leave it alone.
func arrayMemberStringValues(rootObj *hujson.Object, arrayKey, memberKey string) map[string]struct{} {
	out := map[string]struct{}{}
	arrVal := findObjectMember(rootObj, arrayKey)
	if arrVal == nil {
		return out
	}
	arr, ok := arrVal.Value.(*hujson.Array)
	if !ok {
		return out
	}
	for i := range arr.Elements {
		elemObj, ok := arr.Elements[i].Value.(*hujson.Object)
		if !ok {
			continue
		}
		if s, present := memberString(elemObj, memberKey); present {
			out[s] = struct{}{}
		}
	}
	return out
}

// appendOrCreateArrayMember appends item to the array at rootObj[key], or
// creates a new array with item as its only element when the key is absent.
// Implemented via hujson.Value.Patch (RFC 6902) so the user's surrounding
// comments and key ordering survive the rewrite — round-tripping through
// json.Unmarshal would strip both.
func appendOrCreateArrayMember(v *hujson.Value, key string, item map[string]any) error {
	rootObj, ok := v.Value.(*hujson.Object)
	if !ok {
		return fmt.Errorf("expected JSON object at top level")
	}
	itemBytes, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshaling item: %w", err)
	}
	pointer := jsonPointerEscape(key)
	var ops string
	existing := findObjectMember(rootObj, key)
	switch {
	case existing == nil:
		ops = fmt.Sprintf(`[{"op":"add","path":"/%s","value":[%s]}]`, pointer, itemBytes)
	case isArrayValue(existing):
		ops = fmt.Sprintf(`[{"op":"add","path":"/%s/-","value":%s}]`, pointer, itemBytes)
	default:
		// User-edited config has a non-array (null, object, scalar) at this
		// key. Patching `/key/-` would emit RFC6902 "path not an array" and
		// surface as a HTTP 500 to the FE. Refuse explicitly so the
		// Restore-built-ins handler returns a clear 4xx-shaped error instead.
		return fmt.Errorf("cannot append %q: existing value is not a JSON array", key)
	}
	return v.Patch([]byte(ops))
}

// isArrayValue reports whether the hujson value is an array. Used by
// appendOrCreateArrayMember to refuse patching when the user clobbered a
// canonical array key with null/object/scalar.
func isArrayValue(v *hujson.Value) bool {
	if v == nil {
		return false
	}
	_, ok := v.Value.(*hujson.Array)
	return ok
}

// jsonPointerEscape encodes a JSON Pointer reference token per RFC 6901
// section 4 ("~" → "~0", "/" → "~1"). Our keys are simple identifiers but
// kept defensive — a stray "/" in a future key would silently produce a
// malformed path otherwise.
func jsonPointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// forEachReviewFixLoopBodyChild walks rootObj.workflows looking for the
// review-fix-loop workflow, then for each top-level loop node within it
// invokes fn on every object-typed body child. Non-object entries at any
// level are skipped silently so a malformed user config can't panic the
// migration.
func forEachReviewFixLoopBodyChild(rootObj *hujson.Object, fn func(*hujson.Object)) {
	wfsVal := findObjectMember(rootObj, "workflows")
	if wfsVal == nil {
		return
	}
	wfsArr, ok := wfsVal.Value.(*hujson.Array)
	if !ok {
		return
	}
	for i := range wfsArr.Elements {
		wfObj, ok := wfsArr.Elements[i].Value.(*hujson.Object)
		if !ok {
			continue
		}
		if name, _ := memberString(wfObj, "name"); name != seededReviewFixLoopName {
			continue
		}
		nodesVal := findObjectMember(wfObj, "nodes")
		if nodesVal == nil {
			continue
		}
		nodesArr, ok := nodesVal.Value.(*hujson.Array)
		if !ok {
			continue
		}
		for j := range nodesArr.Elements {
			nodeObj, ok := nodesArr.Elements[j].Value.(*hujson.Object)
			if !ok {
				continue
			}
			if t, _ := memberString(nodeObj, "type"); t != "loop" {
				continue
			}
			bodyVal := findObjectMember(nodeObj, "body")
			if bodyVal == nil {
				continue
			}
			bodyArr, ok := bodyVal.Value.(*hujson.Array)
			if !ok {
				continue
			}
			for k := range bodyArr.Elements {
				childObj, ok := bodyArr.Elements[k].Value.(*hujson.Object)
				if !ok {
					continue
				}
				fn(childObj)
			}
		}
	}
}

// parseJSONValue marshals v (typically a Go literal like []any{"dep"}) to
// JSON then re-parses it as a hujson AST node so it can be spliced into a
// parent Value. Used when appending fresh ObjectMembers — building the
// hujson.Array literal-by-hand would be brittle. hujson.Parse is not
// re-checked for error: its input is the output of json.Marshal, which is
// always valid JSON by construction.
func parseJSONValue(v any) hujson.ValueTrimmed {
	b, err := json.Marshal(v)
	if err != nil {
		return hujson.Literal("null")
	}
	parsed, _ := hujson.Parse(b)
	return parsed.Value
}
