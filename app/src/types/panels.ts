// Panel types: chat/editor/memory/git are singletons (max 1 each).
// docker-browser/host-browser are mutually exclusive (only one browser type per layout).
// agent/shell/playground are multi-instance (multiple allowed, distinguished by id).
export type PanelType = "chat" | "editor" | "memory" | "git" | "agent" | "shell" | "docker-browser" | "host-browser" | "sessions" | "playground" | "notes";
export const SINGLETON_PANELS: PanelType[] = ["chat", "editor", "memory", "git", "docker-browser", "host-browser", "sessions", "notes"];

/** Panels that exclude each other -- if one is present, the others in the same group are blocked. */
export const EXCLUSIVE_PANELS: PanelType[][] = [
  ["docker-browser", "host-browser"],
];

/** Canonical panel list with display metadata. Order determines UI ordering. */
export const PANEL_OPTIONS: { panel: PanelType; label: string }[] = [
  { panel: "chat", label: "Chat" },
  { panel: "editor", label: "Editor" },
  { panel: "memory", label: "Memory" },
  { panel: "git", label: "Git" },
  { panel: "agent", label: "Agent" },
  { panel: "shell", label: "Shell" },
  { panel: "docker-browser", label: "Docker Browser" },
  { panel: "host-browser", label: "Host Browser" },
  { panel: "sessions", label: "Sessions" },
  { panel: "playground", label: "Playground" },
  { panel: "notes", label: "Notes" },
];

/** Display labels for panel headers and tiles. */
export const PANEL_LABELS: Record<PanelType, string> = Object.fromEntries(
  PANEL_OPTIONS.map(({ panel, label }) => [panel, label]),
) as Record<PanelType, string>;

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
