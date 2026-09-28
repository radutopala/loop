import type { PanelType } from "../types/panels";
import type { CanvasNode, CanvasTile } from "./types";

const GAP = 20;

/** Default tile sizes per panel type. Editor and Memory get more space. */
export const DEFAULT_TILE_SIZES: Partial<Record<PanelType, { w: number; h: number }>> = {
  editor: { w: 900, h: 900 },
  memory: { w: 900, h: 900 },
  "docker-browser": { w: 700, h: 500 },
  "host-browser": { w: 700, h: 500 },
  explain: { w: 600, h: 700 },
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
