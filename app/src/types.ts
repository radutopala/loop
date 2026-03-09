export interface Channel {
  id: string;
  name: string;
  parent_id: string;
  dir_path: string;
  active: boolean;
  running: boolean;
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
}

// UI-level session status (mapped from server message types).
export type SessionStatus = "connecting" | "running" | "completed" | "failed";

// View mode per channel: interactive terminal or chat transcript.
export type ViewMode = "terminal" | "chat";

// --- Client → Server messages ---

export interface CreateMessage {
  type: "create";
  channel_id: string;
  cmd?: string[];
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
  type: "created" | "attached" | "detached" | "stopped" | "closed";
  session_id?: string;
  message?: string;
}

export interface ServerErrorMessage {
  type: "error";
  message: string;
  error_code?: string;
}

export type ServerMessage = ServerStatusMessage | ServerErrorMessage;

declare global {
  interface Window {
    loopAPI: {
      getApiUrl: () => Promise<string>;
      onNavigateChannel: (callback: (channelId: string) => void) => void;
    };
  }
}
