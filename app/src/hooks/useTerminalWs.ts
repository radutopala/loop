import { useCallback, useRef } from "react";
import type { SessionStatus } from "../types";
import { useWebSocketConnection } from "./useWebSocketConnection";
import { useTerminalMessageDispatcher } from "./useTerminalMessageDispatcher";
import { useSessionPersistence } from "./useSessionPersistence";

interface UseTerminalWsOptions {
  channelId: string | null;
  onData: (data: ArrayBuffer) => void;
  onStatus: (status: SessionStatus) => void;
  onError: (message: string) => void;
  /** Returns current terminal dimensions for include in create messages. */
  getTerminalSize?: () => { cols: number; rows: number } | null;
}

export function useTerminalWs({
  channelId,
  onData,
  onStatus,
  onError,
  getTerminalSize,
}: UseTerminalWsOptions) {
  const getTerminalSizeRef = useRef(getTerminalSize);
  getTerminalSizeRef.current = getTerminalSize;

  const { sessionIdRef, setSessionId, killedRef, handleOpen, markKilled } = useSessionPersistence(channelId, getTerminalSizeRef);

  // Use a ref for send to break the circular dependency between
  // handleMessage (needs onSessionFailed) and send (needs handleMessage).
  const sendRef = useRef<(data: string) => void>(() => {});
  /** Guards against infinite retry loops: only retry once after a failed attach. */
  const retriedRef = useRef(false);

  /** When an attach fails (stale session), clear it and send a fresh create. */
  const onSessionFailed = useCallback(() => {
    if (channelId && !killedRef.current && !retriedRef.current) {
      retriedRef.current = true;
      const size = getTerminalSizeRef.current?.();
      sendRef.current(
        JSON.stringify({ type: "create", channel_id: channelId, ...size }),
      );
    }
  }, [channelId, killedRef]);

  /** When a session is confirmed, send the current terminal dimensions.
   *  This handles the race where xterm wasn't ready when the create message was sent. */
  const onSessionChange = useCallback(
    (id: string | null) => {
      setSessionId(id);
      if (id) {
        retriedRef.current = false;
        const size = getTerminalSizeRef.current?.();
        if (size) {
          sendRef.current(
            JSON.stringify({ type: "resize", cols: size.cols, rows: size.rows }),
          );
        }
      }
    },
    [setSessionId],
  );

  const { handleMessage } = useTerminalMessageDispatcher({
    onData,
    onStatus,
    onError,
    onSessionChange,
    onSessionFailed,
  });

  const { connected, send } = useWebSocketConnection({
    path: "/api/ws/terminal",
    enabled: Boolean(channelId),
    onOpen: handleOpen,
    onMessage: handleMessage,
  });

  sendRef.current = send;

  const sendInput = useCallback(
    (data: string) => {
      send(JSON.stringify({ type: "input", data: btoa(data) }));
    },
    [send],
  );

  const sendResize = useCallback(
    (cols: number, rows: number) => {
      send(JSON.stringify({ type: "resize", cols, rows }));
    },
    [send],
  );

  /** Kill: stop the session and remove the container. */
  const sendKill = useCallback(() => {
    markKilled();
    const sid = sessionIdRef.current;
    send(JSON.stringify({ type: "stop", ...(sid ? { session_id: sid } : {}) }));
  }, [send, markKilled]);

  return { connected, sendInput, sendResize, sendKill };
}
