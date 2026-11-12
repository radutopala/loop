import { beforeEach, describe, expect, it, vi } from "vitest";
import { loadPreviewModes, savePreviewModes } from "./editorPreviewModes";

const items = new Map<string, string>();
beforeEach(() => {
  items.clear();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => items.set(k, v),
  });
  return () => vi.unstubAllGlobals();
});

describe("preview modes", () => {
  it("defaults to split Markdown and rendered HTML", () => {
    expect(loadPreviewModes("c1")).toEqual({ md: "both", html: "preview" });
  });

  it("keeps each channel's modes", () => {
    savePreviewModes("c1", { md: "preview", html: "editor" });
    savePreviewModes("c2", { md: "editor", html: "preview" });
    expect(loadPreviewModes("c1")).toEqual({ md: "preview", html: "editor" });
    expect(loadPreviewModes("c2")).toEqual({ md: "editor", html: "preview" });
  });

  it("drops a channel back at the defaults", () => {
    savePreviewModes("c1", { md: "preview", html: "preview" });
    savePreviewModes("c1", { md: "both", html: "preview" });
    expect(JSON.parse(items.get("loop-editor-preview-modes") ?? "")).toEqual({});
  });

  it("ignores unknown saved values", () => {
    items.set("loop-editor-preview-modes", JSON.stringify({ c1: { md: "split", html: 3 } }));
    expect(loadPreviewModes("c1")).toEqual({ md: "both", html: "preview" });
  });
});
