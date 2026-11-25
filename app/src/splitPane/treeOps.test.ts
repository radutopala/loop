import { describe, expect, it } from "vitest";
import type { PanelType } from "../types/panels";
import {
  addBeside,
  canAddPanel,
  canAddPanelBeside,
  collectLeaves,
  collectPanelTypes,
  exclusiveRivals,
  findLastLeaf,
  findLeafById,
  flexPercents,
  hasAgentLeaf,
  leafCount,
  makeLeaf,
  moveLeaf,
  removeLeaf,
  splitLeaf,
  swapLeavesInTree,
  updateFlex,
} from "./treeOps";
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

describe("exclusiveRivals", () => {
  it.each([
    ["docker-browser", ["host-browser"]],
    ["host-browser", ["docker-browser"]],
    ["chat", []],
  ] as const)("%s", (panel, want) => {
    expect(exclusiveRivals(panel)).toEqual(want);
  });
});

describe("tree queries", () => {
  const a = makeLeaf("a", "chat");
  const b = makeLeaf("b", "docker-shell");
  const c = makeLeaf("c", "host-shell");
  const tree: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [a, { type: "split", direction: "vertical", flex: 2, children: [b, c] }] };

  it("finds leaves by id", () => {
    expect(findLeafById(tree, "c")).toBe(c);
    expect(findLeafById(tree, "zz")).toBeNull();
  });

  it("collects leaves in order and counts them", () => {
    expect(collectLeaves(tree).map((l) => l.id)).toEqual(["a", "b", "c"]);
    expect(leafCount(tree)).toBe(3);
    expect(leafCount(a)).toBe(1);
    expect([...collectPanelTypes(tree)]).toEqual(["chat", "docker-shell", "host-shell"]);
  });

  it("finds the last leaf, skipping empty splits", () => {
    expect(findLastLeaf(tree)).toBe(c);
    expect(findLastLeaf(a)).toBe(a);
    const empty: PaneNode = { type: "split", direction: "vertical", flex: 1, children: [] };
    expect(findLastLeaf(empty)).toBeNull();
    expect(findLastLeaf({ type: "split", direction: "vertical", flex: 1, children: [a, empty] })).toBe(a);
    const sparse = { type: "split", direction: "vertical", flex: 1, children: [a, undefined] } as unknown as PaneNode;
    expect(findLastLeaf(sparse)).toBe(a);
  });

  it.each<{ name: string; node: PaneNode; want: boolean }>([
    { name: "a docker shell", node: tree, want: true },
    { name: "a docker agent", node: makeLeaf("x", "docker-agent"), want: true },
    { name: "no agent pane", node: { type: "split", direction: "vertical", flex: 1, children: [a, c] }, want: false },
  ])("hasAgentLeaf: $name", ({ node, want }) => {
    expect(hasAgentLeaf(node)).toBe(want);
  });

  it("builds leaves with optional open mode and plugin", () => {
    expect(makeLeaf("x", "chat")).toEqual({ type: "leaf", id: "x", panel: "chat", flex: 1 });
    expect(makeLeaf("x", "docker-agent", 2, "fresh")).toEqual({ type: "leaf", id: "x", panel: "docker-agent", flex: 2, openMode: "fresh" });
  });
});

describe("removeLeaf", () => {
  const a = makeLeaf("a", "chat");
  const b = makeLeaf("b", "git");

  it("drops the leaf but keeps a single-child split", () => {
    const tree: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [a, b] };
    expect(removeLeaf(tree, "a")).toEqual({ ...tree, children: [b] });
  });

  it("returns null when nothing is left", () => {
    expect(removeLeaf(a, "a")).toBeNull();
    expect(removeLeaf({ type: "split", direction: "horizontal", flex: 1, children: [a] }, "a")).toBeNull();
  });

  it("keeps an unrelated leaf", () => {
    expect(removeLeaf(a, "b")).toBe(a);
  });
});

describe("splitLeaf", () => {
  const n = makeLeaf("n", "notes");

  it("splits the matching leaf, keeping its flex on the new split", () => {
    const tree: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [makeLeaf("a", "chat", 2), makeLeaf("b", "git")] };
    expect(splitLeaf(tree, "a", "vertical", n)).toEqual({
      ...tree,
      children: [{ type: "split", direction: "vertical", flex: 2, children: [makeLeaf("a", "chat", 1), n] }, makeLeaf("b", "git")],
    });
  });

  it("leaves a different leaf alone", () => {
    const a = makeLeaf("a", "chat");
    expect(splitLeaf(a, "zz", "vertical", n)).toBe(a);
  });
});

describe("addBeside edge cases", () => {
  it("gives a joined pane a share of 1 when the split has no panes", () => {
    const empty: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [] };
    expect(addBeside(empty, null, "horizontal", makeLeaf("n", "notes"), false)).toEqual({ ...empty, children: [makeLeaf("n", "notes")] });
  });
});

describe("updateFlex", () => {
  const tree: PaneNode = {
    type: "split",
    direction: "horizontal",
    flex: 1,
    children: [makeLeaf("a", "chat"), { type: "split", direction: "vertical", flex: 1, children: [makeLeaf("b", "git"), makeLeaf("c", "notes"), makeLeaf("d", "tasks")] }],
  };

  it("sets the flex of the two panes beside the divider at the root", () => {
    const out = updateFlex(tree, [], 0, 0.4, 1.6);
    expect(out.type === "split" && out.children.map((c) => c.flex)).toEqual([0.4, 1.6]);
  });

  it("follows the path into a nested split", () => {
    const out = updateFlex(tree, [1], 1, 0.3, 1.7);
    expect(collectLeaves(out).map((l) => l.flex)).toEqual([1, 1, 0.3, 1.7]);
  });

  it("leaves a leaf as it is", () => {
    const a = makeLeaf("a", "chat");
    expect(updateFlex(a, [], 0, 2, 2)).toBe(a);
    expect(updateFlex(tree, [0, 1], 0, 2, 2)).toEqual(tree);
  });
});

describe("swapLeavesInTree", () => {
  const tree: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [makeLeaf("a", "chat", 2), makeLeaf("b", "git", 3)] };

  it("swaps the two leaf ids, each slot keeping its panel and flex", () => {
    expect(swapLeavesInTree(tree, "a", "b")).toEqual({ ...tree, children: [makeLeaf("b", "chat", 2), makeLeaf("a", "git", 3)] });
  });

  it("does nothing when a leaf is missing", () => {
    expect(swapLeavesInTree(tree, "a", "zz")).toBe(tree);
    expect(swapLeavesInTree(tree, "zz", "a")).toBe(tree);
  });
});

describe("moveLeaf edge cases", () => {
  const tree: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [makeLeaf("a", "chat"), makeLeaf("b", "git")] };

  it.each([
    { position: "top", want: { vertical: ["b", "a"] } },
    { position: "right", want: { horizontal: ["a", "b"] } },
  ] as const)("drops on the $position edge", ({ position, want }) => {
    const out = moveLeaf(tree, "b", "a", position);
    const shape = (n: PaneNode): unknown => (n.type === "leaf" ? n.id : { [n.direction]: n.children.map(shape) });
    expect(out.type === "split" && shape(out.children[0]!)).toEqual(want);
  });

  it("does nothing for an unknown pane or the only pane", () => {
    expect(moveLeaf(tree, "zz", "a", "left")).toBe(tree);
    const a = makeLeaf("a", "chat");
    expect(moveLeaf(a, "a", "a", "left")).toBe(a);
  });
});

describe("canAddPanel", () => {
  it.each<{ name: string; tree: PaneNode | null; panel: PanelType; want: boolean }>([
    { name: "anything to an empty layout", tree: null, panel: "chat", want: true },
    { name: "a singleton already shown", tree: makeLeaf("a", "chat"), panel: "chat", want: false },
    { name: "a rival of the exclusive group", tree: makeLeaf("a", "docker-browser"), panel: "host-browser", want: false },
    { name: "an exclusive panel without its rival", tree: makeLeaf("a", "host-shell"), panel: "docker-browser", want: true },
    { name: "a repeatable panel", tree: makeLeaf("a", "host-shell"), panel: "host-shell", want: true },
  ])("$name", ({ tree, panel, want }) => {
    expect(canAddPanel(tree, panel)).toBe(want);
    if (tree) expect(canAddPanelBeside(collectPanelTypes(tree), panel)).toBe(want);
  });
});
