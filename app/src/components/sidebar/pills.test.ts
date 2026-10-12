import { describe, expect, it } from "vitest";
import { pluginBadgeTitle, REVIEWING_PILL, SIDEBAR_PILLS, trustPillIds } from "./pills";

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
