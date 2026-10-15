// Panel types: chat/editor/file-tree/memory/git are singletons (max 1 each).
// docker-browser/host-browser are mutually exclusive (only one browser type per layout).
// chat-side/docker-agent/host-shell/docker-shell/playground are multi-instance (multiple allowed, distinguished by id).
export type PanelType =
  | "chat"
  | "chat-side"
  | "editor"
  | "file-tree"
  | "memory"
  | "git"
  | "docker-agent"
  | "host-shell"
  | "docker-shell"
  | "docker-browser"
  | "host-browser"
  | "sessions"
  | "playground"
  | "notes"
  | "tasks"
  | "kanban"
  | "workflows"
  | "audit"
  | "quality"
  | "review"
  | "plugin";
export const SINGLETON_PANELS: PanelType[] = [
  "chat",
  "editor",
  "file-tree",
  "memory",
  "git",
  "docker-browser",
  "host-browser",
  "sessions",
  "notes",
  "tasks",
  "kanban",
  "workflows",
  "audit",
  "quality",
  "review",
];

/** Panels that exclude each other -- if one is present, the others in the same group are blocked. */
export const EXCLUSIVE_PANELS: PanelType[][] = [["docker-browser", "host-browser"]];

/** Canonical panel list with display metadata. Order determines UI ordering. */
export const PANEL_OPTIONS: { panel: PanelType; label: string }[] = [
  { panel: "chat", label: "Chat" },
  { panel: "chat-side", label: "Chat" },
  { panel: "editor", label: "Editor" },
  { panel: "file-tree", label: "Files" },
  { panel: "memory", label: "Memory" },
  { panel: "git", label: "Git" },
  { panel: "docker-agent", label: "Chat Terminal" },
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
  { panel: "review", label: "Review" },
];

/** Display labels for panel headers and tiles. Plugin panes aren't in
 *  PANEL_OPTIONS: the installed plugins' panels are offered instead. */
export const PANEL_LABELS: Record<PanelType, string> = { ...Object.fromEntries(PANEL_OPTIONS.map(({ panel, label }) => [panel, label])), plugin: "Plugin" } as Record<PanelType, string>;

/** The plugin panel a "plugin" pane shows. */
export interface PluginPaneRef {
  /** The plugin's id. */
  id: string;
  /** The panel's id in the plugin's manifest. */
  panel: string;
  label: string;
}

/** What a new pane needs besides its panel type. */
export interface PaneMeta {
  openMode?: AgentOpenMode;
  plugin?: PluginPaneRef;
}

/** The header label of a pane: a plugin pane's own label, a side chat's
 *  mode ("Chat · fork"), else its panel's. */
export function paneLabel(panel: PanelType, plugin?: PluginPaneRef, openMode?: AgentOpenMode): string {
  if (panel === "chat-side") return `Chat · ${openMode ?? "fork"}`;
  return (panel === "plugin" && plugin?.label) || PANEL_LABELS[panel];
}

/** How a docker-agent terminal, or a chat-side pane's side chat, should boot Claude relative to the channel's stored session.
 *  - "resume": reuse the channel session in place (writes back to it).
 *  - "fork":   resume the channel session and immediately fork to a new session id.
 *  - "fresh":  start with no session at all.
 *  Only meaningful for the panels takesOpenMode accepts. */
export type AgentOpenMode = "resume" | "fork" | "fresh";

/** Whether a pane of panel starts Claude in an AgentOpenMode. */
export function takesOpenMode(panel: PanelType): boolean {
  return panel === "docker-agent" || panel === "chat-side";
}

/** The open modes a pane of panel is offered in: a side chat is a fork or a
 *  fresh session (resuming is the Chat pane's). */
export function openModesFor(panel: PanelType): AgentOpenMode[] {
  if (panel === "chat-side") return ["fork", "fresh"];
  return takesOpenMode(panel) ? ["resume", "fork", "fresh"] : [];
}

export const AGENT_OPEN_MODE_OPTIONS: { mode: AgentOpenMode; label: string; description: string }[] = [
  { mode: "resume", label: "Resume", description: "Continue the channel's session in place" },
  { mode: "fork", label: "Resume with fork", description: "Branch a new session from the channel's" },
  { mode: "fresh", label: "Fresh session", description: "Start with no prior context" },
];

export interface LeafNode {
  type: "leaf";
  id: string; // unique per leaf (e.g. "chat", "editor", "docker-agent-0", "host-shell-1")
  panel: PanelType; // determines what component to render
  flex: number;
  /** For docker-agent and chat-side panes: how to handle the Claude session at boot.
   *  Undefined on legacy persisted layouts → treated as "fork" (the historical default). */
  openMode?: AgentOpenMode;
  /** For plugin panes: the plugin panel shown. */
  plugin?: PluginPaneRef;
}

export interface SplitNode {
  type: "split";
  direction: "vertical" | "horizontal";
  children: PaneNode[];
  flex: number;
}

export type PaneNode = LeafNode | SplitNode;
