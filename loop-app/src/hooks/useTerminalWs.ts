import { useCallback, useEffect, useRef, useState } from "react";
import type { ClientMessage, ServerMessage, SessionStatus } from "../types";
import { getWsUrl } from "../api/loopApi";

const RECONNECT_INTERVAL_MS = 10_000;

interface UseTerminalWsOptions {
  channelId: string | null;
  onData: (data: ArrayBuffer) => void;
  onStatus: (status: SessionStatus) => void;
}

export function useTerminalWs({
  channelId,
  onData,
  onStatus,
}: UseTerminalWsOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>();
  const [connected, setConnected] = useState(false);

  const connect = useCallback(() => {
    if (!channelId) return;

    const wsUrl = `${getWsUrl()}/api/ws/terminal?channel_id=${channelId}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setConnected(true);
    };

    ws.onmessage = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        onData(event.data);
      } else {
        const msg = JSON.parse(event.data as string) as ServerMessage;
        if (msg.type === "status") {
          onStatus(msg.status);
        }
      }
    };

    ws.onclose = () => {
      setConnected(false);
      wsRef.current = null;
      reconnectTimer.current = setTimeout(connect, RECONNECT_INTERVAL_MS);
    };

    ws.onerror = () => {
      ws.close();
    };
  }, [channelId, onData, onStatus]);

  useEffect(() => {
    connect();
    return () => {
      clearTimeout(reconnectTimer.current);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [connect]);

  const send = useCallback((msg: ClientMessage) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(msg));
    }
  }, []);

  return { connected, send };
}
