export interface Channel {
  id: string;
  name: string;
  parent_id: string;
  dir_path: string;
  active: boolean;
  container_id?: string;
}

export interface Message {
  id: string;
  channel_id: string;
  content: string;
  author: string;
  bot: boolean;
  created_at: string;
}

// UI-level session status (mapped from server message types).
export type SessionStatus = "connecting" | "running" | "completed" | "failed";

// --- Client → Server messages ---

export interface CreateMessage {
  type: "create";
  container_id: string;
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

declare global {
  interface Window {
    loopAPI: {
      getApiUrl: () => Promise<string>;
    };
  }
}
