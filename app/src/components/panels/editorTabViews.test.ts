import { beforeEach, describe, expect, it, vi } from "vitest";
import { clampTabView, loadTabViews, saveTabViews, type TabView } from "./editorTabViews";

const items = new Map<string, string>();
beforeEach(() => {
  items.clear();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => items.set(k, v),
  });
  return () => vi.unstubAllGlobals();
});

const view = (anchor: number, head: number, top: number): TabView => ({ anchor, head, top });

describe("tab views", () => {
  it("is empty for a channel with nothing saved", () => {
    expect(loadTabViews("c1").size).toBe(0);
  });

  it("keeps each channel's views of its open tabs", () => {
    saveTabViews("c1", new Map([["0:a.go", view(3, 7, 120)]]), ["0:a.go"]);
    saveTabViews("c2", new Map([["1:b.md", view(0, 0, 40)]]), ["1:b.md"]);
    expect([...loadTabViews("c1")]).toEqual([["0:a.go", view(3, 7, 120)]]);
    expect([...loadTabViews("c2")]).toEqual([["1:b.md", view(0, 0, 40)]]);
  });

  it("drops closed tabs, and the channel once none is left", () => {
    const views = new Map([
      ["0:a.go", view(1, 1, 0)],
      ["0:b.go", view(2, 2, 10)],
    ]);
    saveTabViews("c1", views, ["0:b.go"]);
    expect([...loadTabViews("c1").keys()]).toEqual(["0:b.go"]);
    saveTabViews("c1", views, []);
    expect(JSON.parse(items.get("loop-editor-tab-views") ?? "")).toEqual({});
  });

  it.each([
    ["not an object", "x"],
    ["a negative offset", { anchor: -1, head: 0, top: 0 }],
    ["a missing field", { anchor: 1, head: 1 }],
    ["a string offset", { anchor: "1", head: 1, top: 0 }],
    ["null", null],
  ])("skips a saved view that is %s", (_, saved) => {
    items.set("loop-editor-tab-views", JSON.stringify({ c1: { "0:bad": saved, "0:ok": view(1, 2, 3) } }));
    expect([...loadTabViews("c1")]).toEqual([["0:ok", view(1, 2, 3)]]);
  });

  it("ignores a channel entry that isn't an object", () => {
    items.set("loop-editor-tab-views", JSON.stringify({ c1: 5 }));
    expect(loadTabViews("c1").size).toBe(0);
  });

  it.each([
    ["inside the document", view(4, 9, 12), 20, view(4, 9, 12)],
    ["past a shorter document", view(30, 40, 50), 25, view(25, 25, 25)],
  ])("clamps the offsets %s", (_, saved, length, want) => {
    expect(clampTabView(saved, length)).toEqual(want);
  });
});
