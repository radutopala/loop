import { describe, expect, it } from "vitest";
import { findNonOverlappingPosition, withPanelTile } from "./tilePlacement";
import type { CanvasNode, CanvasTile } from "./types";

function tile(id: string, panel: CanvasTile["panel"], x: number, y: number, w = 500, h = 400, zIndex = 1): CanvasTile {
  return { id, panel, x, y, width: w, height: h, zIndex };
}

function canvas(tiles: CanvasTile[]): CanvasNode {
  return { type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles };
}

describe("findNonOverlappingPosition", () => {
  it("keeps a free spot", () => {
    expect(findNonOverlappingPosition(0, 0, 100, 100, [])).toEqual({ x: 0, y: 0 });
  });

  it("moves right of the rightmost tile, else below them all", () => {
    expect(findNonOverlappingPosition(0, 0, 100, 100, [tile("a", "chat", 0, 0, 200, 200)])).toEqual({ x: 220, y: 0 });
  });
});

describe("withPanelTile", () => {
  it("leaves a canvas that has the panel", () => {
    const c = canvas([tile("e", "editor", 0, 0)]);
    expect(withPanelTile(c, "editor", "chat", "editor-1")).toBe(c);
  });

  it("puts the tile right of the anchor, on top", () => {
    const c = canvas([tile("chat", "chat", 100, 50, 500, 400, 3)]);
    const got = withPanelTile(c, "editor", "chat", "editor-1");
    expect(got.tiles[1]).toEqual({ id: "editor-1", panel: "editor", x: 620, y: 50, width: 900, height: 900, zIndex: 4 });
  });

  it("starts at the top left without an anchor", () => {
    const got = withPanelTile(canvas([]), "notes", undefined, "notes-1");
    expect(got.tiles).toEqual([{ id: "notes-1", panel: "notes", x: 20, y: 20, width: 500, height: 400, zIndex: 1 }]);
  });
});
