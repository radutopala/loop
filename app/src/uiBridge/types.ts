import type { PluginPaneOption } from "../plugins/PluginPanelsContext";
import type { SessionStatus } from "../types";
import type { AgentOpenMode, PanelType, PluginPaneRef } from "../types/panels";

/** A step of a command from the daemon (POST /api/ui/commands). */
export interface UiStep {
  op: string;
  channel_id?: string;
  tab?: string;
  /** rename_tab: the tab's new name. */
  name?: string;
  /** create_tab: a canvas tab instead of a split one. */
  canvas?: boolean;
  pane?: string;
  panel?: string;
  open_mode?: string;
  next_to?: string;
  direction?: string;
  /** add_pane: "before" puts the pane left of or above next_to. */
  side?: string;
  text?: string;
  submit?: boolean;
  /** read_output, wait_for: how many of the terminal's (or chat's) last
   *  lines. */
  lines?: number;
  /** wait_for: a regular expression the terminal's output must match. */
  match?: string;
  /** wait_for: how long the terminal must be quiet. */
  quiet_ms?: number;
  /** open_file: a path in one of the channel's roots. */
  path?: string;
  line?: number;
  /** show_message: a message of the open channel's chat. */
  message_id?: number;
  /** replace_pane, add_pane: the playground a playground pane shows, or
   *  the panel a plugin pane shows, as plugin/panel or the plugin's id. */
  item?: string;
  scope?: string;
}

/** What a step did: the pane it made or typed into, or why it failed. */
export interface StepResult {
  op: string;
  ok: boolean;
  error?: string;
  pane?: string;
  /** create_tab, rename_tab: the tab's name. */
  tab?: string;
  /** read_output, wait_for: the terminal's (or chat's) last lines. */
  output?: string;
}

/** A pane in the open tab, as a window reports it. */
export interface PaneInfo {
  id: string;
  panel: PanelType;
  open_mode?: AgentOpenMode;
  /** A plugin pane's panel, as plugin/panel. */
  plugin?: string;
  status?: SessionStatus;
  /** An agent terminal that's printing, e.g. the Claude TUI at work. */
  busy?: boolean;
}

/** The open channel's layout tabs and the panes of the open one. */
export interface WorkspaceView {
  tab: string;
  tabs: string[];
  /** A canvas tab's panes are its tiles. */
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
  plugin?: PluginPaneRef;
}

/** What a mounted WorkspaceLayout lets steps do. Pane ops take a pane id and
 *  throw an Error that says why they can't. */
export interface WorkspaceController {
  view(): WorkspaceView;
  setTab(name: string): void;
  /** Opens a new split tab, or a canvas one with canvas set, named name or
   *  the next "Layout N" ("Canvas N"); returns its name. */
  createTab(name?: string, canvas?: boolean): string;
  renameTab(name: string, newName: string): void;
  /** Removes a tab, closing its terminals if it's the open one. */
  removeTab(name: string): void;
  /** Replaces the pane with a new one of panel; returns its id. */
  replacePane(id: string, panel: PanelType, opts?: PaneOptions): string;
  /** Adds a pane of panel after nextTo (right of or below it), or before it
   *  with before set; without nextTo, at that edge of the tab. A pane that
   *  sits in a row (or column) the pane joins gets a share of it. On a
   *  canvas, the tile goes there beside nextTo's, or beside all the tiles.
   *  Returns its id. */
  addPane(panel: PanelType, nextTo: string | undefined, direction: "horizontal" | "vertical", before: boolean, opts?: PaneOptions): string;
  removePane(id: string): void;
  /** Makes the pane fill the tab (a tile, the canvas's view), or, with
   *  null, puts it back. */
  maximize(id: string | null): void;
  /** The plugin panels a pane can show, as the add-pane menus offer them,
   *  with the pane replacing taken out; null until the plugins are listed. */
  pluginPanes(replacing?: string): PluginPaneOption[] | null;
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

/** A message of a chat pane's chat, as read_output shows it. */
export interface ChatLine {
  id: number;
  author: string;
  bot: boolean;
  content: string;
}

/** A chat pane's chat: the channel's (a chat pane), or a side chat's (a
 *  chat-side pane). send_input queues a message in it, as its composer does. */
export interface ChatPaneInput {
  /** Whether it's working on a message, or has one queued to run. */
  busy(): boolean;
  /** What it waits on an answer to before it takes a message (a question,
   *  a plan or an approval card), or null. */
  blocked(): string | null;
  /** Its loaded messages, oldest first. */
  messages(): ChatLine[];
  send(text: string): Promise<void>;
}

/** What the executor needs from the app. */
export interface UiHost {
  selectedChannelId(): string | null;
  /** Opens a channel, thread or worktree thread; rejects if there's no such
   *  channel. */
  selectChannel(id: string): Promise<void>;
  workspace(channelId: string): WorkspaceController | undefined;
  terminal(channelId: string, paneId: string): TerminalInput | undefined;
  chat(channelId: string, paneId: string): ChatPaneInput | undefined;
  /** Opens a file of the channel's roots in its editor; rejects if there's
   *  no such file. */
  openFile(channelId: string, path: string, line?: number): Promise<void>;
  /** Scrolls channelId's chat to a message and highlights it; false when
   *  the chat has no such message. */
  showMessage(channelId: string, messageId: number): Promise<boolean>;
  /** The playgrounds a channel's playground panes can show. */
  playgrounds(channelId: string): Promise<PlaygroundPick[]>;
  now(): number;
  sleep(ms: number): Promise<void>;
}
