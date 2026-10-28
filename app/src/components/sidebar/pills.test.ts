import { describe, expect, it } from "vitest";
import { CI_FIXING_PILL, ciPillIds, pluginBadgeTitle, REVIEWING_PILL, SIDEBAR_PILLS, trustPillIds } from "./pills";

describe("REVIEWING_PILL", () => {
  it("stays out of the pills that mean the session needs you", () => {
    expect(SIDEBAR_PILLS.map((p) => p.label)).not.toContain(REVIEWING_PILL.label);
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

describe("ci pills", () => {
  it("counts a failure for the needs-you filter, not one being fixed", () => {
    const ci = SIDEBAR_PILLS.find((p) => p.kind === "ci");
    expect(ci).toMatchObject({ label: "ci", color: "error" });
    expect(SIDEBAR_PILLS.map((p) => p.label)).not.toContain(CI_FIXING_PILL.label);
  });

  it("lights the rows with a failure no agent is fixing", () => {
    const ids = ciPillIds([{ id: "failed", ci_attention: "failed" }, { id: "fixing", ci_attention: "fixing" }, { id: "none" }]);
    expect([...ids]).toEqual(["failed"]);
  });
});
