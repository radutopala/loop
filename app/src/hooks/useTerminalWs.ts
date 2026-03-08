import { useCallback } from "react";
import type { SessionStatus } from "../types";
import { useWebSocketConnection } from "./useWebSocketConnection";
import { useTerminalMessageDispatcher } from "./useTerminalMessageDispatcher";
import { useSessionPersistence } from "./useSessionPersistence";

interface UseTerminalWsOptions {
  channelId: string | null;
  containerId: string | null;
  onData: (data: ArrayBuffer) => void;
  onStatus: (status: SessionStatus) => void;
  onError: (message: string) => void;
}

export function useTerminalWs({
  channelId,
  containerId,
  onData,
  onStatus,
  onError,
}: UseTerminalWsOptions) {
  const { setSessionId, handleOpen } = useSessionPersistence(containerId);

  const { handleMessage } = useTerminalMessageDispatcher({
    onData,
    onStatus,
    onError,
    onSessionChange: setSessionId,
  });

  const { connected, send } = useWebSocketConnection({
    path: "/api/ws/terminal",
    enabled: Boolean(channelId && containerId),
    onOpen: handleOpen,
    onMessage: handleMessage,
  });

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

  const sendStop = useCallback(() => {
    send(JSON.stringify({ type: "stop" }));
  }, [send]);

  return { connected, sendInput, sendResize, sendStop };
}
