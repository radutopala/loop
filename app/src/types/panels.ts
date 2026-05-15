// Panel types: chat/editor/file-tree/memory/git are singletons (max 1 each).
// docker-browser/host-browser are mutually exclusive (only one browser type per layout).
// docker-agent/host-shell/docker-shell/playground are multi-instance (multiple allowed, distinguished by id).
export type PanelType = "chat" | "editor" | "file-tree" | "memory" | "git" | "docker-agent" | "host-shell" | "docker-shell" | "docker-browser" | "host-browser" | "sessions" | "playground" | "notes" | "tasks" | "kanban" | "workflows" | "audit" | "quality";
export const SINGLETON_PANELS: PanelType[] = ["chat", "editor", "file-tree", "memory", "git", "docker-browser", "host-browser", "sessions", "notes", "tasks", "kanban", "workflows", "audit", "quality"];

/** Panels that exclude each other -- if one is present, the others in the same group are blocked. */
export const EXCLUSIVE_PANELS: PanelType[][] = [
  ["docker-browser", "host-browser"],
];

/** Panels only available in top-level channels (not threads or worktrees). */
export const CHANNEL_ONLY_PANELS: PanelType[] = ["kanban"];

/** Canonical panel list with display metadata. Order determines UI ordering. */
export const PANEL_OPTIONS: { panel: PanelType; label: string }[] = [
  { panel: "chat", label: "Chat" },
  { panel: "editor", label: "Editor" },
  { panel: "file-tree", label: "Files" },
  { panel: "memory", label: "Memory" },
  { panel: "git", label: "Git" },
  { panel: "docker-agent", label: "Docker Agent" },
  { panel: "docker-shell", label: "Docker Shell" },
  { panel: "host-shell", label: "Host Shell" },
  { panel: "docker-browser", label: "Docker Browser" },
  { panel: "host-browser", label: "Host Browser" },
  { panel: "sessions", label: "Sessions" },
  { panel: "playground", label: "Playground" },
  { panel: "notes", label: "Notes" },
  { panel: "tasks", label: "Tasks" },
  { panel: "kanban", label: "Kanban" },
  { panel: "workflows", label: "Workflows" },
  { panel: "audit", label: "Audit" },
  { panel: "quality", label: "Quality" },
];

/** Display labels for panel headers and tiles. */
export const PANEL_LABELS: Record<PanelType, string> = Object.fromEntries(
  PANEL_OPTIONS.map(({ panel, label }) => [panel, label]),
) as Record<PanelType, string>;

/** How a docker-agent terminal should boot Claude relative to the channel's stored session.
 *  - "resume": reuse the channel session in place (writes back to it).
 *  - "fork":   resume the channel session and immediately fork to a new session id.
 *  - "fresh":  start with no session at all.
 *  Only meaningful when panel === "docker-agent". */
export type AgentOpenMode = "resume" | "fork" | "fresh";

export const AGENT_OPEN_MODE_OPTIONS: { mode: AgentOpenMode; label: string; description: string }[] = [
  { mode: "resume", label: "Resume", description: "Continue the channel's session in place" },
  { mode: "fork", label: "Resume with fork", description: "Branch a new session from the channel's" },
  { mode: "fresh", label: "Fresh session", description: "Start with no prior context" },
];

export interface LeafNode {
  type: "leaf";
  id: string;       // unique per leaf (e.g. "chat", "editor", "docker-agent-0", "host-shell-1")
  panel: PanelType; // determines what component to render
  flex: number;
  /** For docker-agent panes: how to handle the Claude session at boot.
   *  Undefined on legacy persisted layouts → treated as "fork" (the historical default). */
  openMode?: AgentOpenMode;
}

export interface SplitNode {
  type: "split";
  direction: "vertical" | "horizontal";
  children: PaneNode[];
  flex: number;
}

export type PaneNode = LeafNode | SplitNode;
