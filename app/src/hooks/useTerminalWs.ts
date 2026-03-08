import { useCallback, useEffect, useRef, useState } from "react";
import type { ClientMessage, ServerMessage, SessionStatus } from "../types";
import { getWsUrl } from "../api/loopApi";

const RECONNECT_DELAY_MS = 3_000;

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
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const sessionIdRef = useRef<string | null>(null);
  const [connected, setConnected] = useState(false);

  const sendRaw = useCallback((msg: ClientMessage) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg));
    }
  }, []);

  const connect = useCallback(() => {
    if (!channelId || !containerId) return;

    const wsUrl = `${getWsUrl()}/api/ws/terminal`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setConnected(true);

      // Re-attach to existing session or create a new one.
      if (sessionIdRef.current) {
        ws.send(
          JSON.stringify({ type: "attach", session_id: sessionIdRef.current }),
        );
      } else {
        ws.send(
          JSON.stringify({ type: "create", container_id: containerId }),
        );
      }
    };

    ws.onmessage = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        onData(event.data);
        return;
      }

      const msg = JSON.parse(event.data as string) as ServerMessage;
      switch (msg.type) {
        case "created":
          sessionIdRef.current = msg.session_id ?? null;
          onStatus("running");
          break;
        case "attached":
          sessionIdRef.current = msg.session_id ?? null;
          onStatus("running");
          break;
        case "stopped":
          sessionIdRef.current = null;
          onStatus("completed");
          break;
        case "closed":
          sessionIdRef.current = null;
          onStatus("completed");
          break;
        case "error":
          onError(msg.message);
          // Session-related errors clear the session.
          if (
            msg.error_code === "no_session" ||
            msg.error_code === "session_failed"
          ) {
            sessionIdRef.current = null;
            onStatus("failed");
          }
          break;
      }
    };

    ws.onclose = () => {
      setConnected(false);
      wsRef.current = null;
      reconnectTimer.current = setTimeout(connect, RECONNECT_DELAY_MS);
    };

    ws.onerror = () => {
      ws.close();
    };
  }, [channelId, containerId, onData, onStatus, onError]);

  useEffect(() => {
    connect();
    return () => {
      clearTimeout(reconnectTimer.current);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [connect]);

  // sendInput base64-encodes the data before sending.
  const sendInput = useCallback(
    (data: string) => {
      sendRaw({ type: "input", data: btoa(data) });
    },
    [sendRaw],
  );

  const sendResize = useCallback(
    (cols: number, rows: number) => {
      sendRaw({ type: "resize", cols, rows });
    },
    [sendRaw],
  );

  const sendStop = useCallback(() => {
    sendRaw({ type: "stop" });
  }, [sendRaw]);

  return { connected, sendInput, sendResize, sendStop };
}
