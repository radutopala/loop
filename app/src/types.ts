export interface Channel {
  id: string;
  name: string;
  parent_id: string;
  dir_path: string;
  active: boolean;
  container_running: boolean;
  agent_running: boolean;
  branch: string;
}

export interface Message {
  id: number;
  channel_id: string;
  msg_id: string;
  author_id: string;
  author_name: string;
  content: string;
  is_bot: boolean;
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
}

export interface ToolUseData {
  tool_name: string;
  input: string;
}

export interface AgentActivityData {
  activity: "model" | "subagent_started" | "subagent_progress";
  model?: string;
  description?: string;
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

export interface AppSettings {
  stopDaemonOnQuit: boolean;
  autoSaveOnBlur: boolean;
}

export interface DaemonInfo {
  running: boolean;
  binaryPath: string | null;
}

export interface ConfigInfo {
  path: string;
  content: string | null;
}

declare global {
  interface Window {
    loopAPI: {
      getApiUrl: () => Promise<string>;
      showOpenDirectoryDialog?: () => Promise<string | null>;
      onboardLocal?: (dirPath: string) => Promise<{ ok: boolean; output?: string; error?: string }>;
      onNavigateChannel: (callback: (channelId: string) => void) => void;
      getSettings: () => Promise<AppSettings>;
      saveSettings: (settings: AppSettings) => Promise<void>;
      getDaemonInfo: () => Promise<DaemonInfo>;
      getConfig: () => Promise<ConfigInfo>;
      getProjectConfig: (dirPath: string) => Promise<ConfigInfo>;
      saveConfig: (filePath: string, content: string) => Promise<{ ok: boolean; error?: string }>;
      restartDaemon: () => Promise<DaemonInfo>;
      onOpenSettings: (callback: () => void) => void;
      getUpdateStatus?: () => Promise<UpdateStatus>;
      downloadUpdate?: () => Promise<void>;
      installUpdate?: () => Promise<void>;
      onUpdateStatus?: (callback: (status: UpdateStatus) => void) => void;
    };
  }
}
