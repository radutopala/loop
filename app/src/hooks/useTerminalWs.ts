import { useCallback, useRef } from "react";
import type { SessionStatus, TerminalTarget } from "../types";
import { useWebSocketConnection } from "./useWebSocketConnection";
import { useTerminalMessageDispatcher } from "./useTerminalMessageDispatcher";
import { useSessionPersistence } from "./useSessionPersistence";

interface UseTerminalWsOptions {
  channelId: string | null;
  target?: TerminalTarget;
  instanceId?: string;
  /** Claude Code session ID to resume (overrides the channel's stored session). */
  claudeSessionId?: string;
  /** Start a fresh session, ignoring the channel's stored session. */
  newSession?: boolean;
  /** Explicit command to run instead of the interactive Claude bootstrap. */
  cmd?: string[];
  onData: (data: ArrayBuffer) => void;
  onStatus: (status: SessionStatus) => void;
  onError: (message: string) => void;
  /** Returns current terminal dimensions for include in create messages. */
  getTerminalSize?: () => { cols: number; rows: number } | null;
}

export function useTerminalWs({
  channelId,
  target = "agent",
  instanceId,
  claudeSessionId,
  newSession,
  cmd,
  onData,
  onStatus,
  onError,
  getTerminalSize,
}: UseTerminalWsOptions) {
  const getTerminalSizeRef = useRef(getTerminalSize);
  getTerminalSizeRef.current = getTerminalSize;

  const { sessionIdRef, setSessionId, killedRef, handleOpen, markKilled, getStartTime } = useSessionPersistence(channelId, target, getTerminalSizeRef, instanceId, claudeSessionId, newSession, cmd);

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
        JSON.stringify({ type: "create", channel_id: channelId, target, ...(target === "agent" && instanceId ? { agent_id: instanceId } : {}), ...(claudeSessionId ? { session_id: claudeSessionId } : {}), ...(newSession ? { new_session: true } : {}), ...(cmd && cmd.length > 0 ? { cmd } : {}), ...size }),
      );
    }
  }, [channelId, target, instanceId, claudeSessionId, newSession, cmd, killedRef]);

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
    setSessionId(null);
    send(JSON.stringify({ type: "stop", ...(sid ? { session_id: sid } : {}) }));
  }, [send, markKilled, setSessionId]);

  /** Create a new session (used to restart after kill). */
  const sendCreate = useCallback(() => {
    if (!channelId) return;
    killedRef.current = false;
    const size = getTerminalSizeRef.current?.();
    send(JSON.stringify({ type: "create", channel_id: channelId, target, ...(target === "agent" && instanceId ? { agent_id: instanceId } : {}), ...(claudeSessionId ? { session_id: claudeSessionId } : {}), ...(newSession ? { new_session: true } : {}), ...(cmd && cmd.length > 0 ? { cmd } : {}), ...size }));
  }, [channelId, target, instanceId, claudeSessionId, newSession, cmd, send, killedRef]);

  /** Close: stop the exec session but keep the container alive.
   *  Does NOT set killedRef — only explicit Kill should prevent auto-create. */
  const sendClose = useCallback(() => {
    const sid = sessionIdRef.current;
    setSessionId(null);
    send(JSON.stringify({ type: "close", ...(sid ? { session_id: sid } : {}) }));
  }, [send, setSessionId]);

  return { connected, sendInput, sendResize, sendKill, sendClose, sendCreate, getStartTime };
}
