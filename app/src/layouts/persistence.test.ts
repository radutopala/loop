import { beforeEach, describe, expect, it, vi } from "vitest";
import { makeLeaf } from "../splitPane/treeOps";
import { deleteLayout, loadChannelLayouts, renameLayout, saveLayout, saveLayoutType } from "./persistence";

beforeEach(() => {
  const items = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => items.set(k, v),
  });
  return () => vi.unstubAllGlobals();
});

describe("renameLayout", () => {
  it("renames a tab without panes yet", () => {
    saveLayoutType("c1", "Layout 1", "split");
    renameLayout("c1", "Layout 1", "Review 2");
    expect(loadChannelLayouts("c1")).toMatchObject({ active: "Review 2", order: ["Review 2"], types: { "Review 2": "split" } });
  });

  it("keeps a canvas tab a canvas", () => {
    saveLayoutType("c1", "Canvas 1", "canvas");
    saveLayout("c1", "Canvas 1", makeLeaf("notes", "notes"));
    renameLayout("c1", "Canvas 1", "Board");
    const ch = loadChannelLayouts("c1");
    expect(ch?.types).toEqual({ Board: "canvas" });
    expect(Object.keys(ch?.layouts ?? {})).toEqual(["Board"]);
    expect(ch?.order).toEqual(["Board"]);
  });

  it("leaves a channel without the tab as it is", () => {
    saveLayoutType("c1", "Layout 1", "split");
    renameLayout("c1", "Nope", "Other");
    renameLayout("c2", "Nope", "Other");
    expect(loadChannelLayouts("c1")?.order).toEqual(["Layout 1"]);
    expect(loadChannelLayouts("c2")).toBeNull();
  });
});

describe("loadChannelLayouts", () => {
  it("knows a canvas saved without its type", () => {
    const canvas = { type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] };
    const chat = makeLeaf("chat", "chat");
    localStorage.setItem(
      "loop-workspace-layout",
      JSON.stringify({ c1: { active: "Chat", order: ["Chat", "Canvas"], types: {}, layouts: { Chat: chat, Canvas: canvas } }, c2: { active: "Board", order: ["Board"], layouts: { Board: canvas } } }),
    );
    expect(loadChannelLayouts("c1")?.types).toEqual({ Canvas: "canvas" });
    expect(loadChannelLayouts("c2")?.types).toEqual({ Board: "canvas" });
  });
});

describe("deleteLayout", () => {
  it("forgets the tab's type", () => {
    saveLayoutType("c1", "Layout 1", "split");
    saveLayoutType("c1", "Canvas 1", "canvas");
    deleteLayout("c1", "Canvas 1");
    expect(loadChannelLayouts("c1")).toMatchObject({ order: ["Layout 1"], types: { "Layout 1": "split" } });
  });
});
