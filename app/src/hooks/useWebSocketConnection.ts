import { useCallback, useEffect, useRef, useState } from "react";
import { getWsUrl } from "../api/loopApi";

const DEFAULT_RECONNECT_DELAY_MS = 3_000;

interface UseWebSocketConnectionOptions {
  /** URL path appended to the WS base URL (e.g. "/api/ws/terminal"). */
  path: string;
  /** Whether the connection should be active. */
  enabled: boolean;
  /** Called when the socket opens. */
  onOpen?: (ws: WebSocket) => void;
  /** Called for each incoming message. */
  onMessage?: (event: MessageEvent) => void;
  /** Reconnection delay in ms (default 3000). */
  reconnectDelay?: number;
}

/**
 * Low-level hook that manages a single WebSocket connection with
 * automatic reconnection.
 */
export function useWebSocketConnection({
  path,
  enabled,
  onOpen,
  onMessage,
  reconnectDelay = DEFAULT_RECONNECT_DELAY_MS,
}: UseWebSocketConnectionOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const [connected, setConnected] = useState(false);

  // Stable refs so the connect callback doesn't re-create on every render.
  const onOpenRef = useRef(onOpen);
  onOpenRef.current = onOpen;
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  const connect = useCallback(() => {
    const ws = new WebSocket(`${getWsUrl()}${path}`);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setConnected(true);
      onOpenRef.current?.(ws);
    };

    ws.onmessage = (event: MessageEvent) => {
      onMessageRef.current?.(event);
    };

    ws.onclose = () => {
      setConnected(false);
      wsRef.current = null;
      reconnectTimer.current = setTimeout(connect, reconnectDelay);
    };

    ws.onerror = () => {
      ws.close();
    };
  }, [path]);

  useEffect(() => {
    if (!enabled) return;
    connect();
    return () => {
      clearTimeout(reconnectTimer.current);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [enabled, connect]);

  const send = useCallback((data: string) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(data);
    }
  }, []);

  return { connected, send };
}
