import { useCallback } from "react";
import type { WSEvent } from "../types";
import { useWebSocketConnection } from "./useWebSocketConnection";

interface UseEventStreamOptions {
  channelId: string | null;
  onEvent: (event: WSEvent) => void;
}

export function useEventStream({ channelId, onEvent }: UseEventStreamOptions) {
  const handleMessage = useCallback(
    (event: MessageEvent) => {
      try {
        const wsEvent: WSEvent = JSON.parse(event.data);
        onEvent(wsEvent);
      } catch {
        /* ignore malformed messages */
      }
    },
    [onEvent],
  );

  useWebSocketConnection({
    path: `/api/ws?channels=${encodeURIComponent(channelId ?? "")}`,
    enabled: !!channelId,
    onMessage: handleMessage,
  });
}
