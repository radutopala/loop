import type { AgentOpenMode, PanelType, PaneNode, PluginPaneRef } from "../types/panels";

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
  /** For docker-agent and chat-side tiles: how to boot the Claude session. Undefined → "fork" (legacy default). */
  openMode?: AgentOpenMode;
  /** For plugin tiles: the plugin panel shown. */
  plugin?: PluginPaneRef;
}

/** Union type for both layout modes. */
export type LayoutRoot = PaneNode | CanvasNode;

export function isCanvasNode(node: LayoutRoot): node is CanvasNode {
  return node.type === "canvas";
}
