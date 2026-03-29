// Panel types: chat/editor/memory/diff are singletons (max 1 each).
// docker-browser/host-browser are mutually exclusive (only one browser type per layout).
// agent/shell/playground are multi-instance (multiple allowed, distinguished by id).
export type PanelType = "chat" | "editor" | "memory" | "diff" | "agent" | "shell" | "docker-browser" | "host-browser" | "sessions" | "playground";
export const SINGLETON_PANELS: PanelType[] = ["chat", "editor", "memory", "diff", "docker-browser", "host-browser", "sessions"];

/** Panels that exclude each other -- if one is present, the others in the same group are blocked. */
export const EXCLUSIVE_PANELS: PanelType[][] = [
  ["docker-browser", "host-browser"],
];

export interface LeafNode {
  type: "leaf";
  id: string;       // unique per leaf (e.g. "chat", "editor", "agent-0", "shell-1")
  panel: PanelType; // determines what component to render
  flex: number;
}

export interface SplitNode {
  type: "split";
  direction: "vertical" | "horizontal";
  children: PaneNode[];
  flex: number;
}

export type PaneNode = LeafNode | SplitNode;
