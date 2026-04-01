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
  created_at: string;
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
}

export interface MessagesProcessedData {
  msg_ids: string[];
}

export interface MessageStreamingData {
  content: string;
}

export interface AgentStatusData {
  status: "running" | "completed" | "error";
  error?: string;
  duration_ms?: number;
  num_turns?: number;
  stop_reason?: string;
  model?: string;
  trigger_content?: string;
  thread_id?: string;
}

export interface ToolUseData {
  tool_name: string;
  input: string;
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
    };
  }
}
