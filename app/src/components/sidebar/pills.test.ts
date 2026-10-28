import { describe, expect, it } from "vitest";
import { ciPillIds, commentPillIds, conflictPillIds, fixingPill, pluginBadgeTitle, REVIEWING_PILL, SIDEBAR_PILLS, shownPills, trustPillIds } from "./pills";

describe("REVIEWING_PILL", () => {
  it("stays out of the pills that mean the session needs you", () => {
    expect(SIDEBAR_PILLS.map((p) => p.label)).not.toContain(REVIEWING_PILL.label);
  });
});

describe("shownPills", () => {
  it("leaves out the kinds turned off, in render order", () => {
    expect(shownPills([]).map((p) => p.kind)).toEqual(SIDEBAR_PILLS.map((p) => p.kind));
    expect(shownPills(["comment", "ci", "conflict"]).map((p) => p.label)).toEqual(["gate", "rev", "ask", "plan", "trust"]);
  });
});

describe("trustPillIds", () => {
  it("lights a project's top-level row only", () => {
    const ids = trustPillIds([
      { id: "proj", parent_id: "", trust_pending: true },
      { id: "thread", parent_id: "proj", trust_pending: true },
      { id: "wt", parent_id: "proj", trust_pending: true },
      { id: "ok", parent_id: "", trust_pending: false },
      { id: "unset", parent_id: "" },
    ]);
    expect([...ids]).toEqual(["proj"]);
  });
});

describe("pluginBadgeTitle", () => {
  it.each([
    [{ plugin_id: "quiz", text: "3", title: "3 quizzes due" }, "quiz: 3 quizzes due"],
    [{ plugin_id: "quiz", text: "3" }, "quiz: 3"],
    [{ plugin_id: "quiz", text: "3", title: "" }, "quiz: 3"],
  ])("%o → %s", (b, want) => {
    expect(pluginBadgeTitle(b)).toBe(want);
  });
});

describe("PR/MR pills", () => {
  it("has one needs-you pill per issue, none for one being handled", () => {
    expect(SIDEBAR_PILLS.find((p) => p.kind === "ci")).toMatchObject({ label: "ci", color: "error" });
    expect(SIDEBAR_PILLS.find((p) => p.kind === "conflict")).toMatchObject({ label: "cnf", color: "error" });
    expect(SIDEBAR_PILLS.find((p) => p.kind === "comment")).toMatchObject({ label: "cmt", color: "warning" });
    const labels = SIDEBAR_PILLS.map((p) => p.label);
    expect(labels).not.toContain(fixingPill("fixing", SIDEBAR_PILLS)?.label);
  });

  it.each([
    ["fixing", "ci…"],
    ["failed", undefined],
    ["conflict", undefined],
    [undefined, undefined],
  ] as const)("fixingPill(%s) → %s", (attention, want) => {
    expect(fixingPill(attention, SIDEBAR_PILLS)?.label).toBe(want);
  });

  it("drops ci… with the ci pill turned off", () => {
    expect(fixingPill("fixing", shownPills(["ci"]))).toBeUndefined();
    expect(fixingPill("fixing", shownPills(["comment"]))?.label).toBe("ci…");
  });

  const rows = [
    { id: "failed", pr_attention: "failed" as const },
    { id: "fixing", pr_attention: "fixing" as const },
    { id: "conflict", pr_attention: "conflict" as const, unresolved_threads: 1 },
    { id: "none", unresolved_threads: 0 },
  ];

  it("lights ci on a failure no agent is fixing", () => {
    expect([...ciPillIds(rows)]).toEqual(["failed"]);
  });

  it("lights conflict on a merge conflict", () => {
    expect([...conflictPillIds(rows)]).toEqual(["conflict"]);
  });

  it("lights cmt on unresolved review threads", () => {
    expect([...commentPillIds(rows)]).toEqual(["conflict"]);
  });
});
