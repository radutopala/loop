import type { PanelType } from "../types/panels";
import type { CanvasNode, CanvasTile } from "./types";

const GAP = 20;

/** Default tile sizes per panel type. Editor and Memory get more space. */
export const DEFAULT_TILE_SIZES: Partial<Record<PanelType, { w: number; h: number }>> = {
  editor: { w: 900, h: 900 },
  memory: { w: 900, h: 900 },
  "docker-browser": { w: 700, h: 500 },
  "host-browser": { w: 700, h: 500 },
};

/** Find a position that doesn't overlap existing tiles. Tries the given position
 *  first, then shifts right, then wraps below. */
export function findNonOverlappingPosition(x: number, y: number, w: number, h: number, tiles: CanvasTile[]): { x: number; y: number } {
  const overlaps = (px: number, py: number) => tiles.some((t) => px < t.x + t.width + GAP && px + w + GAP > t.x && py < t.y + t.height + GAP && py + h + GAP > t.y);

  if (!overlaps(x, y)) return { x, y };

  // Try placing to the right of the rightmost tile.
  const maxRight = Math.max(...tiles.map((t) => t.x + t.width), 0);
  const rightPos = { x: maxRight + GAP, y };
  if (!overlaps(rightPos.x, rightPos.y)) return rightPos;

  // Try below the bottommost tile.
  const maxBottom = Math.max(...tiles.map((t) => t.y + t.height), 0);
  return { x, y: maxBottom + GAP };
}

/**
 * The canvas with a tile for panel: as it is when it has one already, else
 * with a new one, on top, to the right of the anchor panel's tile when
 * there's one (else at the top left), nudged off the other tiles.
 */
export function withPanelTile(canvas: CanvasNode, panel: PanelType, anchorPanel: PanelType | undefined, id: string): CanvasNode {
  if (canvas.tiles.some((t) => t.panel === panel)) return canvas;
  const anchor = anchorPanel ? canvas.tiles.find((t) => t.panel === anchorPanel) : undefined;
  const { w, h } = DEFAULT_TILE_SIZES[panel] ?? { w: 500, h: 400 };
  const at = anchor ? { x: anchor.x + anchor.width + GAP, y: anchor.y } : { x: GAP, y: GAP };
  const { x, y } = findNonOverlappingPosition(at.x, at.y, w, h, canvas.tiles);
  const zIndex = Math.max(...canvas.tiles.map((t) => t.zIndex), 0) + 1;
  return { ...canvas, tiles: [...canvas.tiles, { id, panel, x, y, width: w, height: h, zIndex }] };
}

/** A tile's place on the canvas. */
type Bounds = Pick<CanvasTile, "x" | "y" | "width" | "height">;

/** What a new tile is, before it has a place. */
export type NewTile = Omit<CanvasTile, "x" | "y" | "width" | "height" | "zIndex">;

/**
 * The canvas with tile added, on top, beside the tile nextTo, or, without
 * one, beside all the tiles: right of it (horizontal) or below it (vertical),
 * or left of or above it with before; nudged off the other tiles. An editor
 * gets a file tree left of it, as one added from a menu does, unless there's
 * one; fileTreeId is that tile's id.
 */
export function withTileBeside(canvas: CanvasNode, tile: NewTile, nextTo: string | undefined, direction: "horizontal" | "vertical", before: boolean, fileTreeId: string): CanvasNode {
  const { w, h } = DEFAULT_TILE_SIZES[tile.panel] ?? { w: 500, h: 400 };
  const anchor = nextTo ? canvas.tiles.find((t) => t.id === nextTo) : boundsOf(canvas.tiles);
  let at = { x: GAP, y: GAP };
  if (anchor) {
    if (direction === "horizontal") at = { x: before ? anchor.x - w - GAP : anchor.x + anchor.width + GAP, y: anchor.y };
    else at = { x: anchor.x, y: before ? anchor.y - h - GAP : anchor.y + anchor.height + GAP };
  }
  const { x, y } = findNonOverlappingPosition(at.x, at.y, w, h, canvas.tiles);
  const zIndex = Math.max(...canvas.tiles.map((t) => t.zIndex), 0) + 1;
  const added: CanvasTile = { ...tile, x, y, width: w, height: h, zIndex };
  const tiles = [...canvas.tiles, added];
  if (tile.panel === "editor" && !canvas.tiles.some((t) => t.panel === "file-tree")) {
    const { w: ftW, h: ftH } = DEFAULT_TILE_SIZES["file-tree"] ?? { w: 500, h: 400 };
    const ft = findNonOverlappingPosition(x - ftW - GAP, y, ftW, ftH, tiles);
    tiles.push({ id: fileTreeId, panel: "file-tree", x: ft.x, y: ft.y, width: ftW, height: ftH, zIndex: zIndex + 1 });
  }
  return { ...canvas, tiles };
}

/** The box around tiles, or undefined with none. */
function boundsOf(tiles: CanvasTile[]): Bounds | undefined {
  if (tiles.length === 0) return undefined;
  const x = Math.min(...tiles.map((t) => t.x));
  const y = Math.min(...tiles.map((t) => t.y));
  return { x, y, width: Math.max(...tiles.map((t) => t.x + t.width)) - x, height: Math.max(...tiles.map((t) => t.y + t.height)) - y };
}

/** The canvas with the tile id replaced by tile, in its place and size. */
export function withTileReplaced(canvas: CanvasNode, id: string, tile: NewTile): CanvasNode {
  return { ...canvas, tiles: canvas.tiles.map((t) => (t.id === id ? { ...tile, x: t.x, y: t.y, width: t.width, height: t.height, zIndex: t.zIndex } : t)) };
}

/** A tile id for panel no tile of the canvas has: panel-now, counting up. */
export function freshTileId(canvas: CanvasNode, panel: string, now: number): string {
  let n = now;
  while (canvas.tiles.some((t) => t.id === `${panel}-${n}`)) n++;
  return `${panel}-${n}`;
}
