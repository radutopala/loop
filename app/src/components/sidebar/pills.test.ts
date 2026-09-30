import { describe, expect, it } from "vitest";
import { trustPillIds } from "./pills";

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
