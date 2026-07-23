package fsmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	{
		// Upgrade path: installs that seeded the old "builtin code review"
		// shortcut (bare /code-review) get the new review-panel-derived
		// prompt. Skip-if-present seeding can't do this; the patcher rewrites
		// only an unmodified entry, never a user-edited one.
		Description: "patch builtin code review shortcut to the review-panel-derived prompt",
		Apply:       patchBuiltinCodeReviewShortcutPrompt,
	},
	{
		Description: "seed builtin simplify prompt shortcut",
		Apply: func(ctx context.Context, c *Ctx) error {
			_, err := seedBuiltinSimplifyShortcut(ctx, c)
			return err
		},
	},
	{
		Description: "refresh container/ files: Node 24 via nodesource (Debian's Node 20 is EOL)",
		Apply:       refreshContainerFiles,
	},
	{
		// Upgrade path: existing review-loop / review-fix-loop installs get the
		// env-based review command (`loop review run --pr {{.Inputs.pr}} --wait`,
		// reading CHANNEL_ID/API_URL from the container env) + a `pr` input.
		// Only rewrites an unmodified review script; ships with the new binary
		// so the container's `loop` understands the new flags.
		Description: "patch review-loop/review-fix-loop to env-based review command + pr input",
		Apply:       patchReviewLoopEnvAndPRInput,
	},
	{
		// Claude Code ships the code-review/simplify skills with
		// disable-model-invocation, so the Skill-tool prompts these
		// shortcuts carried always error and degrade to the fallback
		// self-review. Rewrite unmodified entries to slash-command form —
		// a message that IS the slash command counts as a user invocation
		// and runs the full skill.
		Description: "patch builtin code review shortcut back to slash-command form",
		Apply:       patchBuiltinCodeReviewShortcutPrompt,
	},
	{
		Description: "patch builtin simplify shortcut back to slash-command form",
		Apply:       patchBuiltinSimplifyShortcutPrompt,
	},
	{
		// Ships the entrypoint.sh chown-skip guard: `chown -R` over the
		// multi-GB named cache volumes (/go, ~/.npm, ~/.cache) ran on every
		// container start and dominated cold-spawn latency (~15s observed);
		// the refreshed entrypoint stats the volume root and skips the walk
		// when it is already agent-owned.
		Description: "refresh container/ files: skip redundant chown -R over cache volumes",
		Apply:       refreshContainerFiles,
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

// builtinCodeReviewShortcutPrompt is the prompt seeded for the "builtin code
// review" shortcut. It leads with the bare /code-review slash command — the
// built-in skill ships with disable-model-invocation, so a Skill-tool call
// from the model is rejected, but a message that starts with the slash
// command counts as a user invocation and runs the full skill (trailing
// lines ride along as extra instructions). Adds an importance grouping plus
// a triage question so the user decides what to fix.
// Kept in sync with the same entry in config.global.example.json.
const builtinCodeReviewShortcutPrompt = `/code-review

When the review completes, present the findings grouped by importance — Critical, High, Medium, Low — each as a short bullet with file:line, the bug, and the concrete trigger and resulting wrong behavior. Report them as regular text — do not call or emulate the ReportFindings tool. Don't fix anything yet. After listing them, ask me which findings to address (for example: all Critical and High, specific items by number, or none).`

// builtinCodeReviewShortcutDescription is the stock description seeded with the
// shortcut. Kept in sync with config.global.example.json.
const builtinCodeReviewShortcutDescription = "Run /code-review, group findings by importance, and ask what to address"

// oldBuiltinCodeReviewShortcut{Prompt,Description} are the original seeded
// values — a bare slash command. The upgrade patcher rewrites only entries
// still holding these exact values (i.e. unmodified by the user); a
// user-edited prompt is left untouched.
const (
	oldBuiltinCodeReviewShortcutPrompt      = "/code-review"
	oldBuiltinCodeReviewShortcutDescription = "Run Claude Code's built-in /code-review slash command"
)

// skillToolBuiltin*ShortcutPrompt are the interim prompts that told the model
// to invoke the skills via the Skill tool. Claude Code now ships those skills
// with disable-model-invocation, so the Skill call always errors and the run
// degrades to the fallback self-review. The patchers rewrite entries still
// holding these exact values back to slash-command form; user edits are left
// untouched.
const skillToolBuiltinCodeReviewShortcutPrompt = `Run the built-in code-review skill (via the Skill tool with skill="code-review") — it runs the full multi-angle find / verify / sweep pass and returns findings, each with at least a file, line, and description. If the skill is unavailable, fall back to a recall-focused review yourself: read the diff (git diff @{upstream}...HEAD plus any working-tree changes) and surface every real bug you can confirm or reasonably suspect.

When the review completes, present the findings grouped by importance — Critical, High, Medium, Low — each as a short bullet with file:line, the bug, and the concrete trigger and resulting wrong behavior. Don't fix anything yet. After listing them, ask me which findings to address (for example: all Critical and High, specific items by number, or none).`

const skillToolBuiltinSimplifyShortcutPrompt = `Run the built-in simplify skill (via the Skill tool with skill="simplify") — it reviews the changed code for reuse, simplification, efficiency, and altitude cleanups and applies the fixes (quality only — it does not hunt for bugs; use the code-review skill for that). If the skill is unavailable, do the cleanup review yourself on the diff (git diff @{upstream}...HEAD plus any working-tree changes) and apply the safe cleanups.

When it completes, summarize the cleanups grouped by category — reuse, simplification, efficiency, altitude — each as a short bullet with file:line and what changed.`

// builtinSimplifyShortcutName is the unique name for the simplify shortcut, as
// it renders in the # picker.
const builtinSimplifyShortcutName = "builtin simplify"

// builtinSimplifyShortcutPrompt leads with the bare /simplify slash command
// (user-invoked skill — same reasoning as builtinCodeReviewShortcutPrompt).
// Unlike code-review, simplify applies the cleanups, so the closing step
// summarizes what changed rather than asking what to address. Kept in sync
// with config.global.example.json.
const builtinSimplifyShortcutPrompt = `/simplify

When it completes, summarize the cleanups grouped by category — reuse, simplification, efficiency, altitude — each as a short bullet with file:line and what changed.`

// builtinSimplifyShortcutDescription is the stock description seeded with the
// shortcut. Kept in sync with config.global.example.json.
const builtinSimplifyShortcutDescription = "Run /simplify — apply reuse/simplification/efficiency/altitude cleanups to the changed code"

// seedBuiltinShortcut appends a built-in prompt shortcut to the user's
// existing ~/.loop/config.json. Fresh installs get the same entries via
// config.global.example.json on first onboard; these migrations cover the
// upgrade path for installs that already have a config.
//
// No-ops when the file doesn't exist (onboard will handle it) or when an
// entry with the same name is already present (user may have added it
// themselves; never duplicate). Mutations go through the hujson AST so the
// user's HJSON comments and key ordering survive — round-tripping through
// json.Unmarshal would silently strip both. Returns the seeded name, or nil
// when nothing was written.
func seedBuiltinShortcut(c *Ctx, name, description, prompt string) ([]string, error) {
	v, configPath, err := loadConfigHJSON(c)
	if err != nil || v == nil {
		return nil, err
	}
	rootObj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("parsing %s: expected JSON object at top level", configPath)
	}

	existing := arrayMemberStringValues(rootObj, "prompt_shortcuts", "name")
	if _, present := existing[name]; present {
		return nil, nil
	}

	def := map[string]any{
		"name":        name,
		"description": description,
		"prompt":      prompt,
	}
	if err := appendOrCreateArrayMember(v, "prompt_shortcuts", def); err != nil {
		return nil, fmt.Errorf("patching %s: %w", configPath, err)
	}
	if err := atomicWriteConfig(c.Sys, configPath, v.Pack(), 0644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", configPath, err)
	}
	return []string{name}, nil
}

func seedBuiltinCodeReviewShortcut(_ context.Context, c *Ctx) ([]string, error) {
	return seedBuiltinShortcut(c, builtinCodeReviewShortcutName, builtinCodeReviewShortcutDescription, builtinCodeReviewShortcutPrompt)
}

func seedBuiltinSimplifyShortcut(_ context.Context, c *Ctx) ([]string, error) {
	return seedBuiltinShortcut(c, builtinSimplifyShortcutName, builtinSimplifyShortcutDescription, builtinSimplifyShortcutPrompt)
}

// seededReviewLoopName / seededReviewFixLoopName are the canonical names of
// the two seeded review workflows. The FE's split button refers to these by
// name when starting a run, so the names must stay stable across releases.
const (
	seededReviewLoopName    = "review-loop"
	seededReviewFixLoopName = "review-fix-loop"
)

// reviewFixWhenExpr gates the seeded fix and verify body children. It fires
// when the latest review produced findings AND the bash node's output
// parsed cleanly into a review envelope. ParseFailed=true (unparseable
// output, daemon-side `status:error` envelope, or a failed bash child)
// would otherwise let the fix prompt run with empty CommentsJSON, asking
// the agent to fix nothing — wasting an iteration and confusing the agent.
const reviewFixWhenExpr = "{{ and (not .Review.NoComments) (not .Review.ParseFailed) }}"

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

// patchBuiltinCodeReviewShortcutPrompt upgrades an unmodified "builtin code
// review" prompt shortcut (prompt still the bare "/code-review") to the
// current review-panel-derived prompt + description. A user-edited prompt is
// left untouched. Covers the upgrade path for installs that seeded the old
// shortcut before this prompt existed — seedBuiltinCodeReviewShortcut only
// ever adds a missing entry, never rewrites a present one.
func patchBuiltinCodeReviewShortcutPrompt(ctx context.Context, c *Ctx) error {
	_, err := patchBuiltinCodeReviewShortcutPromptReport(ctx, c)
	return err
}

// patchBuiltinCodeReviewShortcutPromptReport is the (bool, error) variant used
// by RestoreBuiltinShortcuts to surface a patched-but-not-added shortcut to
// the FE. The bool is true iff the patcher wrote to disk.
func patchBuiltinCodeReviewShortcutPromptReport(_ context.Context, c *Ctx) (bool, error) {
	return patchBuiltinShortcutPrompt(c, builtinCodeReviewShortcutName,
		[]string{oldBuiltinCodeReviewShortcutPrompt, skillToolBuiltinCodeReviewShortcutPrompt},
		builtinCodeReviewShortcutPrompt,
		oldBuiltinCodeReviewShortcutDescription, builtinCodeReviewShortcutDescription)
}

// patchBuiltinSimplifyShortcutPrompt rewrites an unmodified "builtin
// simplify" shortcut from the Skill-tool-era prompt back to slash-command
// form. A user-edited prompt is left untouched.
func patchBuiltinSimplifyShortcutPrompt(ctx context.Context, c *Ctx) error {
	_, err := patchBuiltinSimplifyShortcutPromptReport(ctx, c)
	return err
}

// patchBuiltinSimplifyShortcutPromptReport is the (bool, error) variant used
// by RestoreBuiltinShortcuts.
func patchBuiltinSimplifyShortcutPromptReport(_ context.Context, c *Ctx) (bool, error) {
	return patchBuiltinShortcutPrompt(c, builtinSimplifyShortcutName,
		[]string{skillToolBuiltinSimplifyShortcutPrompt},
		builtinSimplifyShortcutPrompt,
		builtinSimplifyShortcutDescription, builtinSimplifyShortcutDescription)
}

// patchBuiltinShortcutPrompt rewrites the named prompt shortcut's prompt to
// newPrompt when the on-disk value exactly matches one of oldPrompts, and
// refreshes the stock description the same way. Entries the user has edited
// (any other value) are never overwritten. Operates on the hujson AST so the
// user's HJSON comments and key ordering survive.
func patchBuiltinShortcutPrompt(c *Ctx, name string, oldPrompts []string, newPrompt, oldDesc, newDesc string) (bool, error) {
	v, configPath, err := loadConfigHJSON(c)
	if err != nil || v == nil {
		return false, err
	}
	rootObj, ok := v.Value.(*hujson.Object)
	if !ok {
		return false, fmt.Errorf("parsing %s: expected JSON object at top level", configPath)
	}
	scsVal := findObjectMember(rootObj, "prompt_shortcuts")
	if scsVal == nil {
		return false, nil
	}
	scsArr, ok := scsVal.Value.(*hujson.Array)
	if !ok {
		return false, nil
	}
	patched := false
	for i := range scsArr.Elements {
		elemObj, ok := scsArr.Elements[i].Value.(*hujson.Object)
		if !ok {
			continue
		}
		if n, _ := memberString(elemObj, "name"); n != name {
			continue
		}
		promptVal := findObjectMember(elemObj, "prompt")
		if promptVal == nil {
			continue
		}
		lit, ok := promptVal.Value.(hujson.Literal)
		if !ok || !slices.Contains(oldPrompts, lit.String()) {
			continue // user-edited (or unexpected shape) — never overwrite
		}
		promptVal.Value = hujson.String(newPrompt)
		// Refresh the stock description too, but only when it's unmodified.
		if descVal := findObjectMember(elemObj, "description"); descVal != nil {
			if dlit, ok := descVal.Value.(hujson.Literal); ok && dlit.String() == oldDesc {
				descVal.Value = hujson.String(newDesc)
			}
		}
		patched = true
	}
	if !patched {
		return false, nil
	}
	if err := atomicWriteConfig(c.Sys, configPath, v.Pack(), 0644); err != nil {
		return false, fmt.Errorf("writing %s: %w", configPath, err)
	}
	return true, nil
}

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
	if err := atomicWriteConfig(c.Sys, configPath, v.Pack(), 0644); err != nil {
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
	if err := atomicWriteConfig(c.Sys, configPath, v.Pack(), 0644); err != nil {
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
//
// Patches in two cases:
//   - depends_on is absent → add it
//   - depends_on is null or an empty array → replace it
//
// Leaves the field alone when it's any non-empty array (treated as a
// deliberate user customization) so a workflow with intentional parallel
// siblings survives. Patches are applied via v.Patch (RFC 6902) rather than
// raw AST mutation so the user's surrounding comments and key ordering
// survive the rewrite.
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
	type pendingOp struct {
		pointer string // JSON Pointer to the body child object
		op      string // "add" (key absent) or "replace" (key present but value unusable)
		dep     string // the dependency id to write
	}
	var pending []pendingOp
	forEachReviewFixLoopBodyChildWithPath(rootObj, func(childObj *hujson.Object, pointer string) {
		id, _ := memberString(childObj, "id")
		dep, want := wantDeps[id]
		if !want {
			return
		}
		existing := findObjectMember(childObj, "depends_on")
		if existing == nil {
			pending = append(pending, pendingOp{pointer: pointer, op: "add", dep: dep})
			return
		}
		// Only patch null or empty array. A non-empty array — even with a
		// stale id — is treated as user customization. Strings, objects,
		// numbers are pathological shapes the user wrote on purpose; leave
		// them alone to avoid silently rewriting non-canonical configs.
		if isNullLiteral(existing) {
			pending = append(pending, pendingOp{pointer: pointer, op: "replace", dep: dep})
			return
		}
		if arr, ok := existing.Value.(*hujson.Array); ok && len(arr.Elements) == 0 {
			pending = append(pending, pendingOp{pointer: pointer, op: "replace", dep: dep})
			return
		}
	})
	if len(pending) == 0 {
		return false, nil
	}

	// The patch is built deterministically from a fixed set of `op`s
	// ("add"/"replace"), pointers we just walked in the AST, and a string
	// dep that json.Marshal cannot fail on. A v.Patch error here would
	// represent a programmer mistake, not a runtime condition — discard.
	var ops []string
	for _, p := range pending {
		b, _ := json.Marshal([]string{p.dep})
		ops = append(ops, fmt.Sprintf(`{"op":%q,"path":"%s/depends_on","value":%s}`, p.op, p.pointer, b))
	}
	patch := "[" + strings.Join(ops, ",") + "]"
	_ = v.Patch([]byte(patch))
	if err := atomicWriteConfig(c.Sys, configPath, v.Pack(), 0644); err != nil {
		return false, fmt.Errorf("writing %s: %w", configPath, err)
	}
	return true, nil
}

// patchReviewLoopEnvAndPRInput walks ~/.loop/config.json and upgrades the
// seeded review-loop / review-fix-loop workflows to the env-based review
// command: it rewrites each `review` body child's script from the old
// `--channel-id {{.ChannelID}} --api-url $API_URL` form to `reviewRunScript`
// (only when the script is unmodified) and adds the `pr` input when absent.
// Never touches a user-customized review script. No-ops when the config file
// is missing or the workflows are absent.
func patchReviewLoopEnvAndPRInput(ctx context.Context, c *Ctx) error {
	_, err := patchReviewLoopEnvAndPRInputReport(ctx, c)
	return err
}

// patchReviewLoopEnvAndPRInputReport is the ([]patchedNames, error) variant
// used by RestoreBuiltinWorkflows. Patches are applied via v.Patch (RFC 6902)
// so the user's surrounding comments and key ordering survive.
func patchReviewLoopEnvAndPRInputReport(_ context.Context, c *Ctx) ([]string, error) {
	v, configPath, err := loadConfigHJSON(c)
	if err != nil || v == nil {
		return nil, err
	}
	rootObj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("parsing %s: expected JSON object at top level", configPath)
	}
	wfsVal := findObjectMember(rootObj, "workflows")
	if wfsVal == nil {
		return nil, nil
	}
	wfsArr, ok := wfsVal.Value.(*hujson.Array)
	if !ok {
		return nil, nil
	}

	var ops []jsonPatchOp
	patchedSet := map[string]struct{}{}
	for i := range wfsArr.Elements {
		wfObj, ok := wfsArr.Elements[i].Value.(*hujson.Object)
		if !ok {
			continue
		}
		name, _ := memberString(wfObj, "name")
		if name != seededReviewLoopName && name != seededReviewFixLoopName {
			continue
		}

		// (1) Upgrade the review node's script when it's the known old form.
		nodesVal := findObjectMember(wfObj, "nodes")
		if nodesArr, ok := arrayValue(nodesVal); ok {
			for j := range nodesArr.Elements {
				nodeObj, ok := nodesArr.Elements[j].Value.(*hujson.Object)
				if !ok {
					continue
				}
				if t, _ := memberString(nodeObj, "type"); t != "loop" {
					continue
				}
				bodyArr, ok := arrayValue(findObjectMember(nodeObj, "body"))
				if !ok {
					continue
				}
				for k := range bodyArr.Elements {
					childObj, ok := bodyArr.Elements[k].Value.(*hujson.Object)
					if !ok {
						continue
					}
					if id, _ := memberString(childObj, "id"); id != "review" {
						continue
					}
					if sc, ok := memberString(childObj, "script"); ok && sc == reviewRunScriptOld {
						ops = append(ops, jsonPatchOp{
							Op:    "replace",
							Path:  fmt.Sprintf("/workflows/%d/nodes/%d/body/%d/script", i, j, k),
							Value: reviewRunScript,
						})
						patchedSet[name] = struct{}{}
					}
				}
			}
		}

		// (2) Add the `pr` input when the workflow declares inputs but lacks it.
		if inputsVal := findObjectMember(wfObj, "inputs"); inputsVal != nil {
			if inputsObj, ok := inputsVal.Value.(*hujson.Object); ok && findObjectMember(inputsObj, "pr") == nil {
				ops = append(ops, jsonPatchOp{
					Op:    "add",
					Path:  fmt.Sprintf("/workflows/%d/inputs/pr", i),
					Value: workflowInputValue{Description: reviewPRInputDesc, Default: ""},
				})
				patchedSet[name] = struct{}{}
			}
		}
	}
	if len(ops) == 0 {
		return nil, nil
	}
	// Marshal the whole RFC 6902 op list at once so every string value
	// (script, description) is JSON-escaped by the encoder — never by hand-
	// rolled string formatting — which keeps the patch injection-safe. The
	// ops hold only strings and a fixed struct, so marshaling cannot fail.
	patch, _ := json.Marshal(ops)
	_ = v.Patch(patch)
	if err := atomicWriteConfig(c.Sys, configPath, v.Pack(), 0644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", configPath, err)
	}
	var patched []string
	for n := range patchedSet {
		patched = append(patched, n)
	}
	return patched, nil
}

// jsonPatchOp is a single RFC 6902 operation. Building ops as typed values and
// marshaling them (rather than fmt.Sprintf-ing JSON fragments) guarantees every
// string value is properly escaped, so a value containing a quote can't break
// out of the patch document.
type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// workflowInputValue is the shape of a workflow input definition, used as a
// patch `value` when adding the `pr` input.
type workflowInputValue struct {
	Description string `json:"description"`
	Default     string `json:"default"`
}

// arrayValue returns v's underlying hujson.Array when v is non-nil and holds
// an array. A small guard used by the review-loop patcher's node/body walk.
func arrayValue(v *hujson.Value) (*hujson.Array, bool) {
	if v == nil {
		return nil, false
	}
	arr, ok := v.Value.(*hujson.Array)
	return arr, ok
}

// isNullLiteral reports whether v holds a JSON null literal. Used by the
// depends_on patcher to recognize `"depends_on": null` as "missing" so it
// gets replaced with the canonical [dep] array. Caller must pass non-nil.
func isNullLiteral(v *hujson.Value) bool {
	lit, ok := v.Value.(hujson.Literal)
	if !ok {
		return false
	}
	return lit.Kind() == 'n'
}

// reviewRunScript is the current review-node bash: channel-id / api-url come
// from the container's injected env (CHANNEL_ID / API_URL), and an optional
// `pr` input reviews a specific PR (blank = the channel's already-loaded
// review). reviewRunScriptOld is the pre-env form the patcher upgrades.
const (
	reviewRunScript    = "loop review run --pr {{.Inputs.pr}} --wait"
	reviewRunScriptOld = "loop review run --channel-id {{.ChannelID}} --api-url $API_URL --wait"
	reviewPRInputDesc  = "PR number or URL to review (blank = the channel's already-loaded review)."
)

// reviewBashBodyChild is the bash node every seeded review loop pins as its
// first body child. The loop body parser keys off `id == "review"` to
// populate runCtx.Review with the CLI's JSON output (see
// internal/workflow/dag.go reviewBodyNodeID).
func reviewBashBodyChild() map[string]any {
	return map[string]any{
		"id":     "review",
		"type":   "bash",
		"script": reviewRunScript,
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
			"pr": map[string]any{
				"description": reviewPRInputDesc,
				"default":     "",
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
				"when":       reviewFixWhenExpr,
				"prompt":     "Fix the following review comments and commit your changes:\n\n{{.Review.CommentsJSON}}",
				"depends_on": []any{"review"},
			},
			map[string]any{
				"id":         "verify",
				"type":       "bash",
				"when":       reviewFixWhenExpr,
				"script":     reviewFixVerifyScript,
				"depends_on": []any{"fix"},
			},
		},
	)
}

// atomicWriteConfig writes data to a sibling temp file and renames it over
// path so the swap is durable against SIGKILL / OOM / power loss. The plain
// WriteFile opens with O_TRUNC, which leaves the user's config.json empty if
// the daemon dies between the truncate and the bytes landing — a rare-but-
// observed failure mode on memory-pressured machines, and irreversible since
// fsmigrate is the only piece that knows the canonical content.
func atomicWriteConfig(sys System, path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := sys.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := sys.Rename(tmp, path); err != nil {
		// Best-effort cleanup of the orphaned tmp file so a successful
		// retry doesn't trip over it. Ignore the Remove error: if the
		// rename failed because the source is gone, the cleanup will
		// also fail and there's nothing useful to do with the result.
		_ = sys.Remove(tmp)
		return err
	}
	return nil
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
	forEachReviewFixLoopBodyChildWithPath(rootObj, func(childObj *hujson.Object, _ string) {
		fn(childObj)
	})
}

// forEachReviewFixLoopBodyChildWithPath is like forEachReviewFixLoopBodyChild
// but also passes the JSON Pointer (RFC 6901) path to the child object. Used
// by patchers that need to apply RFC 6902 Patch operations rather than
// mutating the AST in place. The path starts with `/workflows/<i>/...`.
func forEachReviewFixLoopBodyChildWithPath(rootObj *hujson.Object, fn func(*hujson.Object, string)) {
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
				path := fmt.Sprintf("/workflows/%d/nodes/%d/body/%d", i, j, k)
				fn(childObj, path)
			}
		}
	}
}
