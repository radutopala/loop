// Panel types: chat/editor/memory/diff are singletons (max 1 each).
// agent/shell are multi-instance (multiple allowed, distinguished by id).
export type PanelType = "chat" | "editor" | "memory" | "diff" | "agent" | "shell" | "browser";
export const SINGLETON_PANELS: PanelType[] = ["chat", "editor", "memory", "diff", "browser"];

export type SplitDirection = "vertical" | "horizontal";
export type DropPosition = "top" | "bottom" | "left" | "right" | "center";

export interface LeafNode {
  type: "leaf";
  id: string;       // unique per leaf (e.g. "chat", "editor", "agent-0", "shell-1")
  panel: PanelType; // determines what component to render
  flex: number;
}

export interface SplitNode {
  type: "split";
  direction: SplitDirection;
  children: PaneNode[];
  flex: number;
}

export type PaneNode = LeafNode | SplitNode;
