import { describe, expect, it } from "vitest";
import type { LeafNode, PaneNode } from "../../types/panels";
import { initIdCounter, insertOppositeHorizontal, leafIdForPanel, shownPluginTiles, withDefaultFileTreeForEditor } from "./workspaceTree";

const leaf = (id: string, panel: LeafNode["panel"], flex = 1): LeafNode => ({ type: "leaf", id, panel, flex });

describe("leafIdForPanel", () => {
  it.each(["chat", "editor", "memory", "git", "sessions", "notes", "audit"] as const)("names a %s leaf after its panel", (panel) => {
    expect(leafIdForPanel("ch-singleton", panel)).toBe(panel);
  });

  it("numbers other panels per channel", () => {
    expect(leafIdForPanel("ch-count-a", "host-shell")).toBe("host-shell-0");
    expect(leafIdForPanel("ch-count-a", "docker-agent")).toBe("docker-agent-1");
    expect(leafIdForPanel("ch-count-b", "host-shell")).toBe("host-shell-0");
  });
});

describe("initIdCounter", () => {
  it("raises the counter past the highest numbered leaf, ignoring unnumbered ones", () => {
    const tree: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [leaf("chat", "chat"), leaf("host-shell-7", "host-shell"), leaf("docker-agent-3", "docker-agent")] };
    initIdCounter("ch-init", tree);
    expect(leafIdForPanel("ch-init", "host-shell")).toBe("host-shell-8");
  });

  it("never lowers the counter", () => {
    initIdCounter("ch-keep", leaf("host-shell-5", "host-shell"));
    initIdCounter("ch-keep", leaf("host-shell-1", "host-shell"));
    expect(leafIdForPanel("ch-keep", "host-shell")).toBe("host-shell-6");
  });
});

describe("shownPluginTiles", () => {
  it("keys the plugin tiles that name their panel", () => {
    const shown = shownPluginTiles([
      { id: "p1", panel: "plugin", plugin: { id: "acme", panel: "board", label: "Board" }, x: 0, y: 0, width: 1, height: 1, zIndex: 0 },
      { id: "p2", panel: "plugin", x: 0, y: 0, width: 1, height: 1, zIndex: 0 },
      { id: "chat", panel: "chat", x: 0, y: 0, width: 1, height: 1, zIndex: 0 },
    ]);
    expect([...shown]).toEqual(["acme/board"]);
  });
});

describe("insertOppositeHorizontal", () => {
  const editor = leaf("editor", "editor");

  it("splits a lone anchor leaf with the new leaf on its right", () => {
    expect(insertOppositeHorizontal(leaf("chat", "chat", 5), "chat", editor)).toEqual({
      type: "split",
      direction: "horizontal",
      flex: 5,
      children: [leaf("chat", "chat", 1), leaf("editor", "editor", 1)],
    });
  });

  it("leaves a tree without the anchor alone", () => {
    const tree = leaf("git", "git");
    expect(insertOppositeHorizontal(tree, "chat", editor)).toBe(tree);
  });

  it("appends at the far end of a horizontal split, at the siblings' average flex", () => {
    const left: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [leaf("chat", "chat", 60), leaf("git", "git", 40)] };
    expect(insertOppositeHorizontal(left, "chat", editor)).toMatchObject({ children: [{ id: "chat" }, { id: "git" }, { id: "editor", flex: 50 }] });
    const right: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [leaf("git", "git", 40), leaf("chat", "chat", 60)] };
    expect(insertOppositeHorizontal(right, "chat", editor)).toMatchObject({ children: [{ id: "editor", flex: 50 }, { id: "git" }, { id: "chat" }] });
  });

  it("wraps an anchor in a vertical split into a horizontal one", () => {
    const tree: PaneNode = { type: "split", direction: "vertical", flex: 1, children: [leaf("chat", "chat", 70), leaf("git", "git", 30)] };
    expect(insertOppositeHorizontal(tree, "chat", editor)).toEqual({
      type: "split",
      direction: "vertical",
      flex: 1,
      children: [{ type: "split", direction: "horizontal", flex: 70, children: [leaf("chat", "chat", 1), leaf("editor", "editor", 1)] }, leaf("git", "git", 30)],
    });
  });

  it("finds an anchor deeper down", () => {
    const tree: PaneNode = {
      type: "split",
      direction: "horizontal",
      flex: 1,
      children: [leaf("git", "git"), { type: "split", direction: "vertical", flex: 1, children: [leaf("chat", "chat"), leaf("notes", "notes")] }],
    };
    const got = insertOppositeHorizontal(tree, "chat", editor);
    expect(got).toMatchObject({ children: [{ id: "git" }, { children: [{ direction: "horizontal", children: [{ id: "chat" }, { id: "editor" }] }, { id: "notes" }] }] });
  });
});

describe("withDefaultFileTreeForEditor", () => {
  it("puts a file tree left of the editor", () => {
    const tree: PaneNode = { type: "split", direction: "vertical", flex: 1, children: [leaf("chat", "chat"), leaf("editor", "editor", 4)] };
    expect(withDefaultFileTreeForEditor(tree, "editor", "file-tree-9")).toEqual({
      type: "split",
      direction: "vertical",
      flex: 1,
      children: [leaf("chat", "chat"), { type: "split", direction: "horizontal", flex: 4, children: [leaf("file-tree-9", "file-tree", 25), leaf("editor", "editor", 75)] }],
    });
  });

  it("adds none to a tree that has a file tree", () => {
    const tree: PaneNode = { type: "split", direction: "horizontal", flex: 1, children: [leaf("file-tree", "file-tree"), leaf("editor", "editor")] };
    expect(withDefaultFileTreeForEditor(tree, "editor", "file-tree-1")).toBe(tree);
  });
});
