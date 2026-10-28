import type { CIStatus } from "../api/ci";

/** The author of the messages that send a CI failure to the agent. */
export const CI_AUTHOR_ID = "loop-ci";

/** How the CI chip reads: its color follows the tone. */
export type CITone = "ok" | "failed" | "running" | "merging" | "merged" | "closed";

/** "PR #342" or, for a GitLab merge request, "MR !57". */
export function ciLabel(d: Pick<CIStatus, "kind" | "number">): string {
  return d.kind === "mr" ? `MR !${d.number}` : `PR #${d.number}`;
}

/**
 * The CI chip: merged or closed once the PR/MR is, else its failures first,
 * then the checks still running, an armed merge, and finally the checks
 * passing.
 */
export function ciChip(d: CIStatus): { text: string; tone: CITone } {
  if (d.merged) return { text: "merged", tone: "merged" };
  if (d.closed) return { text: "closed", tone: "closed" };
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
export function ciMergeActions(d: CIStatus): { whenReady: boolean; cancel: boolean; now: boolean } {
  if (d.merged || d.closed) return { whenReady: false, cancel: false, now: false };
  const armed = !!d.merge_armed;
  const green = d.checks.failed === 0 && d.checks.pending === 0;
  return { whenReady: !armed && !d.auto_merge, cancel: armed, now: green && d.merge_state === "clean" };
}

/** The merge method shown: the armed one, else the one a merge would use. */
export function ciMergeMethod(d: CIStatus): string {
  return d.merge_armed || d.merge_method || "";
}

/** Whether "Fix it" has a failure to send. */
export function ciCanFix(d: CIStatus): boolean {
  return !d.merged && !d.closed && d.checks.failed > 0 && d.ci_attention !== "fixing";
}

/** The auto-fix toggle's label: the effective setting, and the config's when the thread overrides it. */
export function ciAutoFixLabel(d: CIStatus): string {
  const on = (v: boolean) => (v ? "on" : "off");
  return d.auto_fix === d.auto_fix_default ? `Auto-fix CI: ${on(d.auto_fix)}` : `Auto-fix CI: ${on(d.auto_fix)} (default ${on(d.auto_fix_default)})`;
}

/**
 * The auto-fix override that flips the toggle: back to the config's (null)
 * when that's what flipping gives, else the flipped value.
 */
export function ciAutoFixToggle(d: CIStatus): boolean | null {
  const next = !d.auto_fix;
  return next === d.auto_fix_default ? null : next;
}

/** Whether a chat message is one the CI watcher sent the agent. */
export function isCIMessage(m: { author_id: string; is_bot: boolean }): boolean {
  return !m.is_bot && m.author_id === CI_AUTHOR_ID;
}
