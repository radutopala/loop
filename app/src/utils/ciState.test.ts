import { describe, expect, it } from "vitest";
import type { CIStatus } from "../api/ci";
import { ciAutoFixLabel, ciAutoFixToggle, ciCanFix, ciCanRerun, ciChip, ciLabel, ciMergeActions, ciMergeMethod, ciReasonText, ciStaleCheckout, ciTabBadge, isCIMessage } from "./ciState";

const base: CIStatus = {
  channel_id: "t1",
  kind: "pr",
  number: 342,
  head_sha: "abc",
  checks: { total: 7, passed: 7, failed: 0, pending: 0 },
  merge_state: "clean",
  auto_merge: false,
  ci_attention: "",
  auto_fix: true,
  auto_fix_default: true,
  attempts: 0,
  max_attempts: 3,
};
const ci = (over: Partial<CIStatus>): CIStatus => ({ ...base, ...over });

describe("ciLabel", () => {
  it.each([
    [ci({}), "PR #342"],
    [ci({ kind: "mr", number: 57 }), "MR !57"],
  ])("%#", (d, want) => {
    expect(ciLabel(d)).toBe(want);
  });
});

describe("ciChip", () => {
  it.each([
    { name: "passing", d: ci({}), text: "✓ checks", tone: "ok" },
    { name: "no checks", d: ci({ checks: { total: 0, passed: 0, failed: 0, pending: 0 } }), text: "no checks", tone: "ok" },
    { name: "failing", d: ci({ checks: { total: 7, passed: 5, failed: 2, pending: 0 } }), text: "✗ 2 failing", tone: "failed" },
    { name: "failing beats running", d: ci({ checks: { total: 7, passed: 2, failed: 1, pending: 4 } }), text: "✗ 1 failing", tone: "failed" },
    { name: "running", d: ci({ checks: { total: 7, passed: 3, failed: 0, pending: 4 } }), text: "◌ 3/7 running", tone: "running" },
    { name: "armed by Loop", d: ci({ merge_armed: "squash" }), text: "merging when ready", tone: "merging" },
    { name: "the forge's auto-merge", d: ci({ auto_merge: true }), text: "merging when ready", tone: "merging" },
    { name: "merged", d: ci({ merged: true, checks: { total: 7, passed: 6, failed: 1, pending: 0 } }), text: "merged", tone: "merged" },
    { name: "closed", d: ci({ closed: true }), text: "closed", tone: "closed" },
  ])("$name", ({ d, text, tone }) => {
    expect(ciChip(d)).toEqual({ text, tone });
  });
});

describe("ciMergeActions", () => {
  it.each([
    { name: "green and clean", d: ci({}), want: { whenReady: true, cancel: false, now: true } },
    { name: "running", d: ci({ checks: { total: 2, passed: 1, failed: 0, pending: 1 } }), want: { whenReady: true, cancel: false, now: false } },
    { name: "failing", d: ci({ checks: { total: 2, passed: 1, failed: 1, pending: 0 } }), want: { whenReady: true, cancel: false, now: false } },
    { name: "blocked", d: ci({ merge_state: "blocked" }), want: { whenReady: true, cancel: false, now: false } },
    { name: "armed", d: ci({ merge_armed: "merge", checks: { total: 2, passed: 1, failed: 0, pending: 1 } }), want: { whenReady: false, cancel: true, now: false } },
    { name: "armed through the forge", d: ci({ merge_armed: "squash", auto_merge: true }), want: { whenReady: false, cancel: true, now: true } },
    { name: "auto-merge on outside Loop", d: ci({ auto_merge: true }), want: { whenReady: false, cancel: false, now: true } },
    { name: "merged", d: ci({ merged: true }), want: { whenReady: false, cancel: false, now: false } },
    { name: "closed", d: ci({ closed: true, merge_armed: "squash" }), want: { whenReady: false, cancel: false, now: false } },
  ])("$name", ({ d, want }) => {
    expect(ciMergeActions(d)).toEqual(want);
  });
});

describe("ciMergeMethod", () => {
  it.each([
    [ci({ merge_armed: "rebase", merge_method: "squash" }), "rebase"],
    [ci({ merge_method: "squash" }), "squash"],
    [ci({}), ""],
  ])("%#", (d, want) => {
    expect(ciMergeMethod(d)).toBe(want);
  });
});

describe("ciTabBadge", () => {
  it.each([
    { name: "failing", d: ci({ checks: { total: 7, passed: 6, failed: 1, pending: 0 } }), want: { text: "✗ 1", tone: "failed" } },
    { name: "running", d: ci({ checks: { total: 7, passed: 3, failed: 0, pending: 4 } }), want: { text: "◌ 3/7", tone: "running" } },
    { name: "passing", d: ci({}), want: { text: "✓", tone: "ok" } },
    { name: "merging", d: ci({ auto_merge: true }), want: { text: "✓", tone: "merging" } },
    { name: "no checks", d: ci({ checks: { total: 0, passed: 0, failed: 0, pending: 0 } }), want: { text: "", tone: "ok" } },
    { name: "merged", d: ci({ merged: true }), want: { text: "merged", tone: "merged" } },
    { name: "closed", d: ci({ closed: true }), want: { text: "closed", tone: "closed" } },
  ])("$name", ({ d, want }) => {
    expect(ciTabBadge(d)).toEqual(want);
  });
});

describe("ciReasonText", () => {
  it.each([
    [ci({ reason: "not_agent_commit" }), "The head commit isn't one this thread's agent made, so auto-fix leaves it to you."],
    [ci({ reason: "auto_fix_off" }), "Auto-fix is off for this thread."],
    [ci({ reason: "max_attempts", attempts: 3 }), "Auto-fix stopped after 3 attempts."],
    [ci({ reason: "no_new_commit" }), "The last fix pushed no new commit."],
    [ci({ reason: "same_failure" }), "The same failure came back after a fix."],
    [ci({ reason: "infra_rerun" }), "The checks failed for runner reasons, so Loop re-ran them."],
    [ci({ reason: "infra_again" }), "The checks failed for runner reasons again after a re-run."],
    [ci({ reason: "fix_sent" }), "Auto-fix sent the failure to the agent."],
    [ci({ reason: "fix_asked" }), "Fix it sent the failure to the agent."],
    [ci({}), ""],
  ])("%#", (d, want) => {
    expect(ciReasonText(d)).toBe(want);
  });
});

describe("ciStaleCheckout", () => {
  it.each([
    { name: "at the head", d: ci({ checkout_sha: "abc" }), want: false },
    { name: "behind the head", d: ci({ checkout_sha: "def" }), want: true },
    { name: "checkout unknown", d: ci({}), want: false },
  ])("$name", ({ d, want }) => {
    expect(ciStaleCheckout(d)).toBe(want);
  });
});

describe("ciCanRerun", () => {
  const failing = { total: 1, passed: 0, failed: 1, pending: 0 };
  it.each([
    { name: "a failure", d: ci({ checks: failing }), want: true },
    { name: "nothing failed", d: ci({}), want: false },
    { name: "merged", d: ci({ checks: failing, merged: true }), want: false },
    { name: "closed", d: ci({ checks: failing, closed: true }), want: false },
  ])("$name", ({ d, want }) => {
    expect(ciCanRerun(d)).toBe(want);
  });
});

describe("ciCanFix", () => {
  const failing = { total: 1, passed: 0, failed: 1, pending: 0 };
  it.each([
    { name: "a failure", d: ci({ checks: failing }), want: true },
    { name: "already fixing", d: ci({ checks: failing, ci_attention: "fixing" }), want: false },
    { name: "nothing failed", d: ci({}), want: false },
    { name: "merged", d: ci({ checks: failing, merged: true }), want: false },
    { name: "closed", d: ci({ checks: failing, closed: true }), want: false },
  ])("$name", ({ d, want }) => {
    expect(ciCanFix(d)).toBe(want);
  });
});

describe("auto-fix toggle", () => {
  it.each([
    { name: "on, as configured", d: ci({}), label: "Auto-fix CI: on", flip: false },
    { name: "off, as configured", d: ci({ auto_fix: false, auto_fix_default: false }), label: "Auto-fix CI: off", flip: true },
    { name: "turned off", d: ci({ auto_fix: false }), label: "Auto-fix CI: off (default on)", flip: null },
    { name: "turned on", d: ci({ auto_fix_default: false }), label: "Auto-fix CI: on (default off)", flip: null },
  ])("$name", ({ d, label, flip }) => {
    expect(ciAutoFixLabel(d)).toBe(label);
    expect(ciAutoFixToggle(d)).toBe(flip);
  });
});

describe("isCIMessage", () => {
  it.each([
    [{ author_id: "loop-ci", is_bot: false }, true],
    [{ author_id: "loop-ci", is_bot: true }, false],
    [{ author_id: "desktop", is_bot: false }, false],
  ])("%o → %s", (m, want) => {
    expect(isCIMessage(m)).toBe(want);
  });
});
