import { useCallback, useRef } from "react";

/**
 * Tracks the active terminal session ID across WebSocket reconnects.
 * On open, sends either an "attach" (existing session) or "create" (new session).
 */
export function useSessionPersistence(containerId: string | null) {
  const sessionIdRef = useRef<string | null>(null);

  /** Called from the message dispatcher when the server confirms or clears a session. */
  const setSessionId = useCallback((id: string | null) => {
    sessionIdRef.current = id;
  }, []);

  /** Sends the correct handshake message when the WebSocket opens. */
  const handleOpen = useCallback(
    (ws: WebSocket) => {
      if (sessionIdRef.current) {
        ws.send(
          JSON.stringify({ type: "attach", session_id: sessionIdRef.current }),
        );
      } else if (containerId) {
        ws.send(
          JSON.stringify({ type: "create", container_id: containerId }),
        );
      }
    },
    [containerId],
  );

  return { sessionIdRef, setSessionId, handleOpen };
}
