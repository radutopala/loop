export type { PanelType } from "./panels";
export { SINGLETON_PANELS, EXCLUSIVE_PANELS } from "./panels";

export interface Channel {
  id: string;
  name: string;
  parent_id: string;
  dir_path: string;
  session_id: string;
  active: boolean;
  container_running: boolean;
  agent_running: boolean;
  branch: string;
  commit: string;
  worktree: boolean;
  locked: boolean;
  diff_additions: number;
  diff_deletions: number;
  review_enabled: boolean;
}

export interface Message {
  id: number;
  channel_id: string;
  msg_id: string;
  author_id: string;
  author_name: string;
  content: string;
  is_bot: boolean;
  is_processed: boolean;
  // Priority governs processing order (higher first). Missing/0 = default.
  // Used by ChatMessages to render queue position ("1/3").
  priority?: number;
  // For bot replies, the msg_id of the user message whose agent run produced
  // this reply. Empty for user messages and pre-feature rows. Used to group
  // agent events with their triggering user message at reload time.
  trigger_msg_id?: string;
  created_at: string;
}

// TimelineItem is the discriminated union returned by /api/channels/{id}/timeline.
// Real chat messages and agent events (thinking, tool_use, tool_result,
// compacting) are interleaved by chain_position so reload renders the same
// canonical order the user saw live. The `compacting` kind is a marker
// (no payload fields) emitted whenever the runner reports a /compact pass.
export type TimelineItem =
  | { kind: "message"; position: number; id: number; data: Message; trigger_msg_id?: string }
  | { kind: "thinking"; position: number; id: number; text: string; truncated?: boolean; trigger_msg_id?: string }
  | { kind: "tool_use"; position: number; id: number; tool_use_id: string; tool_name: string; tool_input: string; truncated?: boolean; trigger_msg_id?: string }
  | { kind: "tool_result"; position: number; id: number; tool_use_id: string; text: string; is_error?: boolean; truncated?: boolean; trigger_msg_id?: string }
  | { kind: "compacting"; position: number; id: number; trigger_msg_id?: string };

export interface TimelineCursor {
  position: number;
  id: number;
}

export interface TimelineResponse {
  items: TimelineItem[];
  next_cursor: TimelineCursor | null;
}

// Event stream types from /api/ws
export interface WSEvent {
  type: string;
  channel_id: string;
  data: unknown;
  timestamp: number;
}

export interface MessageCreatedData {
  msg_id: string;
  author_id: string;
  author_name: string;
  content: string;
  is_bot: boolean;
  is_processed: boolean;
  priority?: number;
  trigger_msg_id?: string;
}

export interface MessagesProcessedData {
  msg_ids: string[];
}

export interface MessageStreamingData {
  content: string;
}

export interface AgentStatusData {
  status: "running" | "completed" | "error";
  run_id?: string;
  error?: string;
  duration_ms?: number;
  num_turns?: number;
  stop_reason?: string;
  model?: string;
  trigger_content?: string;
  thread_id?: string;
  trigger?: string;
  msg_id?: string;
}

export interface ToolUseData {
  tool_use_id?: string;
  tool_name: string;
  input: string;
}

export interface AgentThinkingData {
  text: string;
}

export interface ToolResultData {
  tool_use_id?: string;
  output: string;
  is_error?: boolean;
}

export interface AgentActivityData {
  activity: "model" | "subagent_started" | "subagent_progress" | "compacting";
  model?: string;
  description?: string;
}

export interface AskUserOption {
  label: string;
  description?: string;
}

export interface AskUserQuestion {
  question: string;
  header?: string;
  options?: AskUserOption[];
  multi_select?: boolean;
}

export interface AskUserQuestionData {
  questions: AskUserQuestion[];
}

export interface ExitPlanModeData {
  plan: string;
  planFilePath?: string;
}

export interface TodoItem {
  content: string;
  status: "completed" | "in_progress" | "pending";
  activeForm: string;
}

export interface TodoWriteData {
  todos: TodoItem[];
}

export interface PRInfo {
  number: number;
  url: string;
  base_ref: string;
  head_ref: string;
  state: string;
  title?: string;
  is_draft?: boolean;
}

export interface PRResponse {
  present: boolean;
  pr?: PRInfo;
}

export interface ChannelUpdatedData {
  channel_id: string;
  branch: string;
  commit: string;
  diff_additions: number;
  diff_deletions: number;
}

export interface GateApprovalRequestedData {
  req_id: string;
  kind: string;
  target: string;
  /** Where the prompt originated inside the agent container. The desktop
   * uses it verbatim to decide which UI surface should render the card:
   *   "chat"               — the chat agent (container entrypoint).
   *   "terminal:<leafId>"  — a specific terminal pane (leaf id from the
   *                          layout tree, stamped via LOOP_TERMINAL_LEAF
   *                          on the exec). */
  source?: string;
  message?: string;
  details?: Record<string, string>;
}

export interface GateApprovalResolvedData {
  req_id: string;
  decision?: string;
  actor?: string;
}

// UI-level session status (mapped from server message types).
export type SessionStatus = "connecting" | "running" | "completed" | "failed";

// Terminal target: Docker agent or host shell.
export type TerminalTarget = "agent" | "host";

// --- Client → Server messages ---

export interface CreateMessage {
  type: "create";
  channel_id: string;
  cmd?: string[];
  target?: "host" | "agent";
}

export interface AttachMessage {
  type: "attach";
  session_id: string;
}

export interface InputMessage {
  type: "input";
  data: string; // base64-encoded
}

export interface ResizeMessage {
  type: "resize";
  rows: number;
  cols: number;
}

export interface StopMessage {
  type: "stop";
}

export type ClientMessage =
  | CreateMessage
  | AttachMessage
  | InputMessage
  | ResizeMessage
  | StopMessage;

// --- Server → Client messages ---

export interface ServerStatusMessage {
  type: "created" | "attached" | "stopped" | "closed";
  session_id?: string;
  message?: string;
}

export interface ServerErrorMessage {
  type: "error";
  message: string;
  error_code?: string;
}

export type ServerMessage = ServerStatusMessage | ServerErrorMessage;

export interface UpdateStatus {
  available: boolean;
  version?: string;
  downloading: boolean;
  downloaded: boolean;
  error?: string;
}

export interface ImageBuildStatusData {
  state: "idle" | "building" | "completed" | "failed";
  phase?: string;
  error?: string;
}

export interface ImageUpdateAvailableData {
  current_version: string;
  latest_version: string;
  component: string;
}

export interface ImageStatusResponse {
  status: ImageBuildStatusData;
  versions: {
    loop_version: string;
    claude_version: string;
    built_at: string;
  };
  update_available?: ImageUpdateAvailableData;
}

export interface DaemonInfo {
  running: boolean;
  binaryPath: string | null;
}

declare global {
  interface Window {
    loopAPI: {
      getApiUrl: () => Promise<string>;
      showOpenDirectoryDialog?: () => Promise<string | null>;
      onboardLocal?: (dirPath: string) => Promise<{ ok: boolean; output?: string; error?: string }>;
      onNavigateChannel: (callback: (channelId: string) => void) => void;
      getDaemonInfo: () => Promise<DaemonInfo>;
      restartDaemon: () => Promise<DaemonInfo>;
      onOpenSettings: (callback: () => void) => void;
      getUpdateStatus?: () => Promise<UpdateStatus>;
      downloadUpdate?: () => Promise<void>;
      installUpdate?: () => Promise<void>;
      onUpdateStatus?: (callback: (status: UpdateStatus) => void) => void;
      notifyTurnEnd?: () => void;
      notifyApprovalNeeded?: (reqId?: string) => void;
      notifyApprovalResolved?: (reqId?: string) => void;
      /** Replace the dock-bouncer's pending-approval set with this canonical
       *  list of req_ids. Called on WS reconnect so the renderer's view of
       *  reality wins over any stale entries left over from a prior session. */
      reconcileApprovals?: (reqIds: string[]) => void;
      setTheme?: (name: string) => void;
      onThemeChanged?: (callback: (name: string) => void) => void;
      openExternal?: (url: string) => Promise<void>;
    };
  }
}
