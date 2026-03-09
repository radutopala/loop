import { useCallback, useRef } from "react";
import type { RefObject } from "react";

/** Module-level map so session IDs survive component remounts. */
const sessionsByChannel = new Map<string, string>();

type GetTerminalSize = (() => { cols: number; rows: number } | null) | undefined;

/**
 * Tracks the active terminal session ID across WebSocket reconnects
 * and component remounts (channel switching).
 * On open, sends either an "attach" (existing session) or "create" (new session).
 */
export function useSessionPersistence(
  channelId: string | null,
  getTerminalSizeRef?: RefObject<GetTerminalSize>,
) {
  const sessionIdRef = useRef<string | null>(
    channelId ? (sessionsByChannel.get(channelId) ?? null) : null,
  );
  /** Set to true after kill to prevent auto-creating a new session on reconnect. */
  const killedRef = useRef(false);

  /** Called from the message dispatcher when the server confirms or clears a session. */
  const setSessionId = useCallback(
    (id: string | null) => {
      sessionIdRef.current = id;
      if (channelId) {
        if (id) {
          sessionsByChannel.set(channelId, id);
        } else {
          sessionsByChannel.delete(channelId);
        }
      }
    },
    [channelId],
  );

  /** Mark the session as killed so reconnect doesn't auto-create. */
  const markKilled = useCallback(() => {
    killedRef.current = true;
  }, []);

  /** Sends the correct handshake message when the WebSocket opens. */
  const handleOpen = useCallback(
    (ws: WebSocket) => {
      if (sessionIdRef.current) {
        ws.send(
          JSON.stringify({ type: "attach", session_id: sessionIdRef.current }),
        );
      } else if (channelId && !killedRef.current) {
        const size = getTerminalSizeRef?.current?.();
        ws.send(JSON.stringify({ type: "create", channel_id: channelId, ...size }));
      }
    },
    [channelId, getTerminalSizeRef],
  );

  return { sessionIdRef, killedRef, setSessionId, handleOpen, markKilled };
}
