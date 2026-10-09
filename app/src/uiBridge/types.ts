import type { SessionStatus } from "../types";
import type { AgentOpenMode, PanelType } from "../types/panels";

/** A step of a command from the daemon (POST /api/ui/commands). */
export interface UiStep {
  op: string;
  channel_id?: string;
  tab?: string;
  pane?: string;
  panel?: string;
  open_mode?: string;
  next_to?: string;
  direction?: string;
  text?: string;
  submit?: boolean;
  /** read_output, wait_for: how many of the terminal's last lines. */
  lines?: number;
  /** wait_for: a regular expression the terminal's output must match. */
  match?: string;
  /** wait_for: how long the terminal must be quiet. */
  quiet_ms?: number;
  /** open_file: a path in one of the channel's roots. */
  path?: string;
  line?: number;
  /** replace_pane, add_pane: the playground a playground pane shows. */
  item?: string;
  scope?: string;
}

/** What a step did: the pane it made or typed into, or why it failed. */
export interface StepResult {
  op: string;
  ok: boolean;
  error?: string;
  pane?: string;
  /** read_output, wait_for: the terminal's last lines. */
  output?: string;
}

/** A pane in the open tab, as a window reports it. */
export interface PaneInfo {
  id: string;
  panel: PanelType;
  open_mode?: AgentOpenMode;
  status?: SessionStatus;
  /** An agent terminal that's printing, e.g. the Claude TUI at work. */
  busy?: boolean;
}

/** The open channel's layout tabs and the panes of the open one. */
export interface WorkspaceView {
  tab: string;
  tabs: string[];
  /** A canvas tab has tiles, not panes. */
  canvas: boolean;
  panes: PaneInfo[];
  /** The pane that fills the tab, if one does. */
  maximized?: string;
}

/** The playground a playground pane shows. */
export interface PlaygroundPick {
  name: string;
  scope: "global" | "project";
}

/** How a pane a step makes starts. */
export interface PaneOptions {
  openMode?: AgentOpenMode;
  playground?: PlaygroundPick;
}

/** What a mounted WorkspaceLayout lets steps do. Pane ops take a pane id and
 *  throw an Error that says why they can't. */
export interface WorkspaceController {
  view(): WorkspaceView;
  setTab(name: string): void;
  /** Replaces the pane with a new one of panel; returns its id. */
  replacePane(id: string, panel: PanelType, opts?: PaneOptions): string;
  /** Adds a pane of panel beside nextTo (or the last pane); returns its id. */
  addPane(panel: PanelType, nextTo: string | undefined, direction: "horizontal" | "vertical", opts?: PaneOptions): string;
  removePane(id: string): void;
  /** Makes the pane fill the tab, or, with null, puts it back. */
  maximize(id: string | null): void;
}

/** An agent terminal's input. Only docker-agent and docker-shell panes
 *  register one: a host shell runs on the host, so steps never type there. */
export interface TerminalInput {
  /** "agent" is the Claude TUI, "shell" a shell in the agent's container. */
  kind: "agent" | "shell";
  status(): SessionStatus;
  /** When the terminal last printed anything, in ms (0 if it hasn't). */
  lastOutputAt(): number;
  /** Whether it's printing now. */
  busy(): boolean;
  /** Its last lines of text, the screen's and the scrollback's. */
  read(lines: number): string;
  send(data: string): void;
}

/** What the executor needs from the app. */
export interface UiHost {
  selectedChannelId(): string | null;
  /** Opens a channel, thread or worktree thread; rejects if there's no such
   *  channel. */
  selectChannel(id: string): Promise<void>;
  workspace(channelId: string): WorkspaceController | undefined;
  terminal(channelId: string, paneId: string): TerminalInput | undefined;
  /** Opens a file of the channel's roots in its editor; rejects if there's
   *  no such file. */
  openFile(channelId: string, path: string, line?: number): Promise<void>;
  /** The playgrounds a channel's playground panes can show. */
  playgrounds(channelId: string): Promise<PlaygroundPick[]>;
  now(): number;
  sleep(ms: number): Promise<void>;
}
