import { useCallback } from "react";
import type { ServerMessage, SessionStatus } from "../types";

interface UseTerminalMessageDispatcherOptions {
  onData: (data: ArrayBuffer) => void;
  onStatus: (status: SessionStatus) => void;
  onError: (message: string) => void;
  onSessionChange: (sessionId: string | null) => void;
  /** Called when an attach/create fails so the caller can retry with create. */
  onSessionFailed?: () => void;
}

/**
 * Returns a message handler that dispatches incoming WebSocket messages
 * to the appropriate callback: binary PTY data, status updates, or errors.
 */
export function useTerminalMessageDispatcher({ onData, onStatus, onError, onSessionChange, onSessionFailed }: UseTerminalMessageDispatcherOptions) {
  const handleMessage = useCallback(
    (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        onData(event.data);
        return;
      }

      const msg = JSON.parse(event.data as string) as ServerMessage;
      switch (msg.type) {
        case "created":
        case "attached":
          onSessionChange(msg.session_id ?? null);
          onStatus("running");
          break;
        case "stopped":
        case "closed":
          onSessionChange(null);
          onStatus("completed");
          break;
        case "error":
          onError(msg.message);
          if (msg.error_code === "no_session" || msg.error_code === "session_failed") {
            onSessionChange(null);
            onSessionFailed?.();
          }
          break;
      }
    },
    [onData, onStatus, onError, onSessionChange],
  );

  return { handleMessage };
}
