import { describe, expect, it } from "vitest";
import { findNonOverlappingPosition, freshTileId, withPanelTile, withTileBeside, withTileReplaced } from "./tilePlacement";
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

describe("withTileBeside", () => {
  const a = tile("a", "chat", 100, 100, 500, 400, 2);
  it.each([
    { name: "right of next_to", direction: "horizontal", before: false, want: { x: 620, y: 100 } },
    { name: "left of next_to", direction: "horizontal", before: true, want: { x: -420, y: 100 } },
    { name: "below next_to", direction: "vertical", before: false, want: { x: 100, y: 520 } },
    { name: "above next_to", direction: "vertical", before: true, want: { x: 100, y: -320 } },
  ] as const)("puts the tile $name, on top", ({ direction, before, want }) => {
    const got = withTileBeside(canvas([a]), { id: "n", panel: "notes" }, "a", direction, before, "ft");
    expect(got.tiles[1]).toEqual({ id: "n", panel: "notes", ...want, width: 500, height: 400, zIndex: 3 });
  });

  it("goes beside all the tiles without next_to", () => {
    const got = withTileBeside(canvas([a, tile("b", "git", 0, 600, 300, 300)]), { id: "n", panel: "notes" }, undefined, "horizontal", false, "ft");
    expect(got.tiles[2]).toMatchObject({ x: 620, y: 100 });
  });

  it("starts at the top left of an empty canvas", () => {
    const got = withTileBeside(canvas([]), { id: "n", panel: "notes", openMode: "fresh" }, undefined, "vertical", true, "ft");
    expect(got.tiles).toEqual([{ id: "n", panel: "notes", openMode: "fresh", x: 20, y: 20, width: 500, height: 400, zIndex: 1 }]);
  });

  it("gives an editor a file tree left of it, unless there's one", () => {
    const got = withTileBeside(canvas([a]), { id: "e", panel: "editor" }, "a", "horizontal", false, "ft");
    expect(got.tiles.map((t) => t.id)).toEqual(["a", "e", "ft"]);
    expect(got.tiles[2]).toMatchObject({ panel: "file-tree", zIndex: 4 });
    const again = withTileBeside(canvas([a, tile("t", "file-tree", 0, 900)]), { id: "e", panel: "editor" }, "a", "horizontal", false, "ft");
    expect(again.tiles.map((t) => t.id)).toEqual(["a", "t", "e"]);
  });
});

describe("withTileReplaced", () => {
  it("puts the new tile in the old one's place and size", () => {
    const got = withTileReplaced(canvas([tile("a", "chat", 10, 20, 300, 200, 5), tile("b", "git", 400, 0)]), "a", { id: "n", panel: "notes" });
    expect(got.tiles).toEqual([{ id: "n", panel: "notes", x: 10, y: 20, width: 300, height: 200, zIndex: 5 }, tile("b", "git", 400, 0)]);
  });
});

describe("freshTileId", () => {
  it("counts up past the ids the canvas has", () => {
    expect(freshTileId(canvas([]), "notes", 7)).toBe("notes-7");
    expect(freshTileId(canvas([tile("notes-7", "notes", 0, 0), tile("notes-8", "notes", 0, 0)]), "notes", 7)).toBe("notes-9");
  });
});
