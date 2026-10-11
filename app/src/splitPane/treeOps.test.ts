import { describe, expect, it } from "vitest";
import { addBeside, findLeafById, flexPercents, makeLeaf, moveLeaf } from "./treeOps";
import type { PaneNode } from "./types";

describe("flexPercents", () => {
  it.each([
    { flexes: [1, 1], want: [50, 50] },
    { flexes: [0.6, 0.4], want: [60, 40] },
    { flexes: [1, 1, 1], want: [33, 33, 33] },
    { flexes: [1.25, 0.75], want: [63, 38] },
    { flexes: [0.5, 0, 0.5], want: [50, 0, 50] },
    { flexes: [0, 0], want: [0, 0] },
    { flexes: [], want: [] },
  ])("$flexes → $want", ({ flexes, want }) => {
    expect(flexPercents(flexes)).toEqual(want);
  });
});

describe("addBeside", () => {
  const a = makeLeaf("a", "chat");
  const b = makeLeaf("b", "git");
  const n = makeLeaf("n", "notes");
  const row: PaneNode = { type: "split", direction: "horizontal", children: [a, { ...b, flex: 3 }], flex: 1 };
  const shape = (node: PaneNode): unknown => (node.type === "leaf" ? `${node.id}:${node.flex}` : { [node.direction]: node.children.map(shape) });

  it.each<{ name: string; tree: PaneNode; anchor: string | null; direction: "horizontal" | "vertical"; before: boolean; want: unknown }>([
    { name: "joins a row after the anchor", tree: row, anchor: "a", direction: "horizontal", before: false, want: { horizontal: ["a:1", "n:2", "b:3"] } },
    { name: "joins a row before the anchor", tree: row, anchor: "a", direction: "horizontal", before: true, want: { horizontal: ["n:2", "a:1", "b:3"] } },
    { name: "splits the anchor across the row", tree: row, anchor: "b", direction: "vertical", before: true, want: { horizontal: ["a:1", { vertical: ["n:1", "b:1"] }] } },
    { name: "splits a lone pane", tree: a, anchor: "a", direction: "horizontal", before: false, want: { horizontal: ["a:1", "n:1"] } },
    { name: "ends the row with a column", tree: row, anchor: null, direction: "horizontal", before: false, want: { horizontal: ["a:1", "b:3", "n:2"] } },
    { name: "starts the row with a column", tree: row, anchor: null, direction: "horizontal", before: true, want: { horizontal: ["n:2", "a:1", "b:3"] } },
    { name: "puts a full-width row under the tab", tree: row, anchor: null, direction: "vertical", before: false, want: { vertical: [{ horizontal: ["a:1", "b:3"] }, "n:1"] } },
    {
      name: "joins a nested column",
      tree: { type: "split", direction: "horizontal", children: [a, { type: "split", direction: "vertical", children: [b], flex: 1 }], flex: 1 },
      anchor: "b",
      direction: "vertical",
      before: false,
      want: { horizontal: ["a:1", { vertical: ["b:1", "n:1"] }] },
    },
  ])("$name", ({ tree, anchor, direction, before, want }) => {
    expect(shape(addBeside(tree, anchor, direction, n, before))).toEqual(want);
  });

  it("leaves a tree without the anchor as it is", () => {
    expect(addBeside(row, "zz", "horizontal", n, false)).toEqual(row);
  });
});

describe("moveLeaf", () => {
  it("keeps the moved pane's plugin and open mode", () => {
    const plugin = { id: "cogtest", panel: "quiz", label: "Cognitive test" };
    const tree: PaneNode = {
      type: "split",
      direction: "horizontal",
      flex: 1,
      children: [makeLeaf("a", "chat"), makeLeaf("b", "plugin", 2, undefined, plugin), makeLeaf("c", "docker-agent", 1, "fork")],
    };
    let moved = moveLeaf(tree, "b", "a", "left");
    expect(findLeafById(moved, "b")).toEqual({ type: "leaf", id: "b", panel: "plugin", flex: 1, plugin });
    moved = moveLeaf(moved, "c", "a", "bottom");
    expect(findLeafById(moved, "c")).toEqual({ type: "leaf", id: "c", panel: "docker-agent", flex: 1, openMode: "fork" });
  });
});
