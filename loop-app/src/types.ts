export interface Channel {
  id: string;
  name: string;
  parent_id: string;
  dir_path: string;
  active: boolean;
}

export interface Message {
  id: string;
  channel_id: string;
  content: string;
  author: string;
  bot: boolean;
  created_at: string;
}

export type SessionStatus = "connecting" | "running" | "completed" | "failed";

export interface StatusMessage {
  type: "status";
  status: SessionStatus;
}

export interface ErrorMessage {
  type: "error";
  message: string;
}

export interface InputMessage {
  type: "input";
  data: string;
}

export interface ResizeMessage {
  type: "resize";
  cols: number;
  rows: number;
}

export interface StopMessage {
  type: "stop";
}

export type ClientMessage = InputMessage | ResizeMessage | StopMessage;
export type ServerMessage = StatusMessage | ErrorMessage;

declare global {
  interface Window {
    loopAPI: {
      getApiUrl: () => Promise<string>;
    };
  }
}
