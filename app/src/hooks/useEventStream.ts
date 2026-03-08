import { useEffect, useRef } from "react";
import type { WSEvent } from "../types";
import { getWsUrl } from "../api/loopApi";

interface UseEventStreamOptions {
  channelId: string | null;
  onEvent: (event: WSEvent) => void;
}

const RECONNECT_DELAY = 5_000;

export function useEventStream({ channelId, onEvent }: UseEventStreamOptions) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    if (!channelId) return;

    let ws: WebSocket | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout>;
    let cancelled = false;

    function connect() {
      if (cancelled) return;

      const url = `${getWsUrl()}/api/ws?channels=${encodeURIComponent(channelId!)}`;
      ws = new WebSocket(url);

      ws.onmessage = (e) => {
        try {
          const event: WSEvent = JSON.parse(e.data);
          onEventRef.current(event);
        } catch {
          /* ignore malformed messages */
        }
      };

      ws.onclose = () => {
        if (!cancelled) {
          reconnectTimer = setTimeout(connect, RECONNECT_DELAY);
        }
      };

      ws.onerror = () => {
        ws?.close();
      };
    }

    connect();

    return () => {
      cancelled = true;
      clearTimeout(reconnectTimer);
      ws?.close();
    };
  }, [channelId]);
}
