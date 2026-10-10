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

describe("deleteLayout", () => {
  it("forgets the tab's type", () => {
    saveLayoutType("c1", "Layout 1", "split");
    saveLayoutType("c1", "Canvas 1", "canvas");
    deleteLayout("c1", "Canvas 1");
    expect(loadChannelLayouts("c1")).toMatchObject({ order: ["Layout 1"], types: { "Layout 1": "split" } });
  });
});
