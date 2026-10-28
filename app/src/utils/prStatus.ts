import type { PRStatus } from "../api/pr";

/** The author of the messages that send a CI failure to the agent. */
export const PR_AUTHOR_ID = "loop-pr";

/** How the PR/MR chip reads: its color follows the tone. */
export type PRTone = "ok" | "failed" | "running" | "merging" | "merged" | "closed";

/** "PR #342" or, for a GitLab merge request, "MR !57". */
export function prLabel(d: Pick<PRStatus, "kind" | "number">): string {
  return d.kind === "mr" ? `MR !${d.number}` : `PR #${d.number}`;
}

/**
 * The PR/MR chip: merged or closed once the PR/MR is, else a merge conflict
 * with its base, its failures, then the checks still running, an armed
 * merge, and finally the checks passing.
 */
export function prChip(d: PRStatus): { text: string; tone: PRTone } {
  if (d.merged) return { text: "merged", tone: "merged" };
  if (d.closed) return { text: "closed", tone: "closed" };
  if (d.merge_state === "conflict") return { text: "⚠ conflicts", tone: "failed" };
  const { total, failed, pending } = d.checks;
  if (failed > 0) return { text: `✗ ${failed} failing`, tone: "failed" };
  if (pending > 0) return { text: `◌ ${total - pending}/${total} running`, tone: "running" };
  if (d.merge_armed || d.auto_merge) return { text: "merging when ready", tone: "merging" };
  return { text: total > 0 ? "✓ checks" : "no checks", tone: "ok" };
}

/**
 * The merge buttons a PR/MR offers: none once it's merged or closed. Only a
 * merge Loop armed can be cancelled here; an auto-merge turned on outside
 * Loop offers neither arming nor cancelling.
 */
export function prMergeActions(d: PRStatus): { whenReady: boolean; cancel: boolean; now: boolean } {
  if (d.merged || d.closed) return { whenReady: false, cancel: false, now: false };
  const armed = !!d.merge_armed;
  const green = d.checks.failed === 0 && d.checks.pending === 0;
  return { whenReady: !armed && !d.auto_merge, cancel: armed, now: green && d.merge_state === "clean" };
}

/** The merge method shown: the armed one, else the one a merge would use. */
export function prMergeMethod(d: PRStatus): string {
  return d.merge_armed || d.merge_method || "";
}

/**
 * The Git panel's PR/MR tab badge: the chip's state in a few characters, so the
 * tab reads it from any other tab.
 */
export function prTabBadge(d: PRStatus): { text: string; tone: PRTone } {
  const chip = prChip(d);
  const { total, failed, pending } = d.checks;
  switch (chip.tone) {
    case "failed":
      return { text: d.merge_state === "conflict" ? "⚠" : `✗ ${failed}`, tone: chip.tone };
    case "running":
      return { text: `◌ ${total - pending}/${total}`, tone: chip.tone };
    case "merged":
    case "closed":
      return chip;
    default:
      return { text: total > 0 ? "✓" : "", tone: chip.tone };
  }
}

/** The merge conflict an open PR/MR has with its base, in a sentence; empty without one. */
export function prConflictText(d: PRStatus): string {
  if (d.merged || d.closed || d.merge_state !== "conflict") return "";
  return `Merge conflicts with ${d.base_ref || "its base"}.`;
}

/** Why the watcher last acted as it did on a failure, in a sentence. */
export function prReasonText(d: PRStatus): string {
  switch (d.reason) {
    case "not_agent_commit":
      return "The head commit isn't one this thread's agent made, so auto-fix leaves it to you.";
    case "auto_fix_off":
      return "Auto-fix is off for this thread.";
    case "max_attempts":
      return `Auto-fix stopped after ${d.attempts} attempts.`;
    case "no_new_commit":
      return "The last fix pushed no new commit.";
    case "same_failure":
      return "The same failure came back after a fix.";
    case "infra_rerun":
      return "The checks failed for runner reasons, so Loop re-ran them.";
    case "infra_again":
      return "The checks failed for runner reasons again after a re-run.";
    case "fix_sent":
      return "Auto-fix sent the failure to the agent.";
    case "fix_asked":
      return "Fix it sent the failure to the agent.";
    default:
      return "";
  }
}

/** Whether the thread's checkout is at another commit than the PR/MR's head. */
export function prStaleCheckout(d: PRStatus): boolean {
  return !!d.checkout_sha && !!d.head_sha && d.checkout_sha !== d.head_sha;
}

/** Whether "Re-run failed" has failed checks to re-run. */
export function prCanRerun(d: PRStatus): boolean {
  return !d.merged && !d.closed && d.checks.failed > 0;
}

/** Whether "Fix it" has a failure to send; a merge conflict is yours to resolve. */
export function prCanFix(d: PRStatus): boolean {
  return !d.merged && !d.closed && d.checks.failed > 0 && d.pr_attention !== "fixing";
}

/** The auto-fix toggle's label: the effective setting, and the config's when the thread overrides it. */
export function prAutoFixLabel(d: PRStatus): string {
  const on = (v: boolean) => (v ? "on" : "off");
  return d.auto_fix === d.auto_fix_default ? `Auto-fix failed checks: ${on(d.auto_fix)}` : `Auto-fix failed checks: ${on(d.auto_fix)} (default ${on(d.auto_fix_default)})`;
}

/**
 * The auto-fix override that flips the toggle: back to the config's (null)
 * when that's what flipping gives, else the flipped value.
 */
export function prAutoFixToggle(d: PRStatus): boolean | null {
  const next = !d.auto_fix;
  return next === d.auto_fix_default ? null : next;
}

/** Whether a chat message is one the PR watcher sent the agent. */
export function isPRMessage(m: { author_id: string; is_bot: boolean }): boolean {
  return !m.is_bot && m.author_id === PR_AUTHOR_ID;
}
