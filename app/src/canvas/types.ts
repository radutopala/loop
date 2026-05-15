import type { PanelType, PaneNode, AgentOpenMode } from "../types/panels";

export interface CanvasNode {
  type: "canvas";
  tiles: CanvasTile[];
  viewport: { x: number; y: number; zoom: number };
}

export interface CanvasTile {
  id: string;
  panel: PanelType;
  x: number;
  y: number;
  width: number;
  height: number;
  zIndex: number;
  /** For docker-agent tiles: how to boot the Claude session. Undefined → "fork" (legacy default). */
  openMode?: AgentOpenMode;
}

/** Union type for both layout modes. */
export type LayoutRoot = PaneNode | CanvasNode;

export function isCanvasNode(node: LayoutRoot): node is CanvasNode {
  return node.type === "canvas";
}
