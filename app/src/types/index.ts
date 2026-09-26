export type { PanelType } from "./panels";
export { EXCLUSIVE_PANELS, SINGLETON_PANELS } from "./panels";

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
  /** The commit's subject line. */
  subject?: string;
  /** The branch's tracking branch, e.g. origin/main, and the commits it's ahead of and behind it. */
  upstream?: string;
  ahead?: number;
  behind?: number;
  /** Inside a worktree chain: the branch the worktree was cut from, set while it still exists, and the commits it's ahead of and behind it. */
  sync_base?: string;
  base_ahead?: number;
  base_behind?: number;
  worktree: boolean;
  /** For worktree threads: the branch this worktree was created from. */
  base_branch?: string;
  /** Inside a worktree chain (the worktree thread or a thread under it): the dir of the checkout it was cut from. */
  root_dir_path?: string;
  locked: boolean;
  diff_additions: number;
  diff_deletions: number;
  review_enabled: boolean;
  /** The model this channel's agent runs with instead of the config's; unset inherits it. */
  model_override?: string;
  /** The reasoning effort this channel's agent runs with instead of the config's; unset inherits it. */
  effort_override?: string;
  /** When the channel's newest message was written (ms since the epoch); unset when it has none. */
  last_activity_at?: number;
  /** For a thread a scheduled task created for its output: that task's id. */
  task_id?: number;
  /** What the thread is for, in a line or two; unset when it has none. */
  description?: string;
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
  // Unix-seconds timestamp before which a delayed (queue_message with a delay)
  // message runs. Missing/0 = immediate. While in the future the UI shows a
  // live countdown until it fires.
  not_before?: number;
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
  // The message's row id in the database. Missing when the row isn't stored
  // by this backend.
  id?: number;
  msg_id: string;
  author_id: string;
  author_name: string;
  content: string;
  is_bot: boolean;
  is_processed: boolean;
  priority?: number;
  trigger_msg_id?: string;
  not_before?: number;
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
  activity: "model" | "subagent_started" | "subagent_progress" | "compacting" | "thinking" | "rate_limited" | "tool_progress" | "task_notification" | "api_retry" | "image_build" | "image_ready";
  model?: string;
  description?: string;
}

export interface AskUserOption {
  label: string;
  description?: string;
  /** Optional mockup / code snippet / visual comparison shown when the option is focused. */
  preview?: string;
}

export interface AskUserQuestion {
  question: string;
  header?: string;
  options?: AskUserOption[];
  /** When true the question is a checkbox list (multiple options selectable). */
  multiSelect?: boolean;
}

export interface AskUserQuestionData {
  questions: AskUserQuestion[];
}

export interface ExitPlanModeData {
  plan: string;
  planFilePath?: string;
}

export interface TaskItem {
  id: string;
  subject: string;
  description?: string;
  activeForm?: string;
  status: "pending" | "in_progress" | "completed";
  blocks?: string[];
  blockedBy?: string[];
}

export interface AgentTasksData {
  tasks: TaskItem[];
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
  subject?: string;
  upstream?: string;
  ahead?: number;
  behind?: number;
  sync_base?: string;
  base_ahead?: number;
  base_behind?: number;
  /**
   * Set by a rename or a description change, which carry nothing else: the
   * git fields are then empty and must be left alone. An empty description
   * clears it.
   */
  name?: string;
  description?: string;
}

/** A channel's model/effort overrides after they change; empty clears one. */
export interface ChannelAgentConfigData {
  model_override: string;
  effort_override: string;
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
  /** Gate deadline (RFC3339). After it the request auto-denies daemon-side
   * and the card renders as expired even if the resolved event was missed. */
  expires_at?: string;
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

export type ClientMessage = CreateMessage | AttachMessage | InputMessage | ResizeMessage | StopMessage;

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

export interface DockerReclaimResult {
  build_cache_reclaimed: number;
  images_reclaimed: number;
  total_reclaimed: number;
}

export interface DaemonInfo {
  running: boolean;
  binaryPath: string | null;
}

declare global {
  interface Window {
    /** Test-observability flag (see loop:test-event): false while the events
     *  WS is down or its onOpen rehydrates are still in flight, true once
     *  they have settled. The BDD harness waits on this before dispatching
     *  synthetic events — rehydrateGateApprovals & co. reconcile against the
     *  backend and would otherwise wipe injected cards that raced the
     *  initial connect on slow runners. */
    __loopWsRehydrated?: boolean;
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
