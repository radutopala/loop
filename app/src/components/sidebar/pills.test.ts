import { describe, expect, it } from "vitest";
import { REVIEWING_PILL, SIDEBAR_PILLS, trustPillIds } from "./pills";

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
