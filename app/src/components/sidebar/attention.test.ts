import { describe, expect, it } from "vitest";
import { pruneToLit } from "./attention";

const t = (id: string) => ({ id });

describe("pruneToLit", () => {
  const tree = {
    proj: [t("a"), t("b"), t("c")],
    a: [t("a1"), t("a2")],
    a1: [t("a1x")],
    b: [t("b1")],
  };

  it.each([
    { name: "nothing lit drops every parent", lit: [] as string[], want: {} },
    { name: "a lit leaf keeps its ancestors only", lit: ["a1x"], want: { proj: [t("a")], a: [t("a1")], a1: [t("a1x")] } },
    { name: "a lit thread doesn't keep its unlit children", lit: ["b"], want: { proj: [t("b")] } },
    { name: "siblings are each judged", lit: ["a2", "c"], want: { proj: [t("a"), t("c")], a: [t("a2")] } },
    { name: "a lit top-level row alone keeps no threads", lit: ["proj"], want: {} },
  ])("$name", ({ lit, want }) => {
    expect(pruneToLit(tree, (id) => lit.includes(id))).toEqual(want);
  });

  it("survives a parent cycle", () => {
    const cyclic = { x: [t("y")], y: [t("x")] };
    expect(pruneToLit(cyclic, (id) => id === "y").x).toEqual([t("y")]);
  });
});
