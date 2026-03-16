import { useCallback, useEffect, useRef, useState } from "react";
import { getWsUrl } from "../api/loopApi";

interface BrowserWSMessage {
  type: string;
  channel_id?: string;
  url?: string;
  width?: number;
  height?: number;
  input_type?: string;
  x?: number;
  y?: number;
  button?: string;
  click_count?: number;
  delta_x?: number;
  delta_y?: number;
  key?: string;
  text?: string;
}

interface BrowserWSResponse {
  type: string;
  url?: string;
  title?: string;
  message?: string;
}

interface UseBrowserWsOptions {
  channelId: string | null;
  onFrame?: (data: ArrayBuffer) => void;
  onPageInfo?: (url: string, title: string) => void;
  onError?: (message: string) => void;
  onStarted?: () => void;
  onStopped?: () => void;
}

export function useBrowserWs({
  channelId,
  onFrame,
  onPageInfo,
  onError,
  onStarted,
  onStopped,
}: UseBrowserWsOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const onFrameRef = useRef(onFrame);
  const onPageInfoRef = useRef(onPageInfo);
  const onErrorRef = useRef(onError);
  const onStartedRef = useRef(onStarted);
  const onStoppedRef = useRef(onStopped);
  const [connected, setConnected] = useState(false);
  const [started, setStarted] = useState(false);

  onFrameRef.current = onFrame;
  onPageInfoRef.current = onPageInfo;
  onErrorRef.current = onError;
  onStartedRef.current = onStarted;
  onStoppedRef.current = onStopped;

  // Connect WebSocket with auto-reconnect.
  useEffect(() => {
    if (!channelId) return;

    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;
    let stopped = false;

    // Reset state in case of StrictMode remount (state persists but WS was closed).
    setConnected(false);
    setStarted(false);
    function connect() {
      if (stopped) return;

      const wsUrl = `${getWsUrl()}/api/ws/browser`;
      const ws = new WebSocket(wsUrl);
      wsRef.current = ws;

      ws.onopen = () => {
        reconnectDelay = 1000; // reset backoff
        setConnected(true);
      };

      ws.binaryType = "arraybuffer";

      ws.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) {
          onFrameRef.current?.(event.data);
          return;
        }
        try {
          const msg: BrowserWSResponse = JSON.parse(event.data);
          switch (msg.type) {
            case "started":
              setStarted(true);
              // Auto-start screencast immediately — don't rely on React effects
              // which may not fire reliably across StrictMode remounts.
              ws.send(JSON.stringify({ type: "screencast", width: 1920, height: 1080 }));
              onStartedRef.current?.();
              break;
            case "stopped":
              setStarted(false);
              onStoppedRef.current?.();
              break;
            case "page_info":
              onPageInfoRef.current?.(msg.url || "", msg.title || "");
              break;
            case "error":
              onErrorRef.current?.(msg.message || "Unknown error");
              break;
          }
        } catch {
          // Ignore parse errors.
        }
      };

      ws.onclose = () => {
        // Don't touch state if this WS was replaced by cleanup (StrictMode remount).
        if (stopped) return;
        wsRef.current = null;
        setConnected(false);
        setStarted(false);
        reconnectTimer = setTimeout(() => {
          reconnectDelay = Math.min(reconnectDelay * 2, 10000);
          connect();
        }, reconnectDelay);
      };
    }

    connect();

    return () => {
      stopped = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [channelId]);

  const send = useCallback((msg: BrowserWSMessage) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }, []);

  const startBrowser = useCallback(() => {
    if (!channelId) return;
    send({ type: "start", channel_id: channelId });
  }, [channelId, send]);

  const stopBrowser = useCallback(() => {
    if (!channelId) return;
    send({ type: "stop", channel_id: channelId });
  }, [channelId, send]);

  const navigate = useCallback(
    (url: string) => {
      send({ type: "navigate", url });
    },
    [send],
  );

  const reload = useCallback(() => send({ type: "reload" }), [send]);
  const goBack = useCallback(() => send({ type: "back" }), [send]);
  const goForward = useCallback(() => send({ type: "forward" }), [send]);
  const requestPageInfo = useCallback(
    () => send({ type: "page_info" }),
    [send],
  );

  const startStreaming = useCallback((width?: number, height?: number) => {
    send({ type: "screencast", width, height });
  }, [send]);

  const sendInput = useCallback(
    (input: {
      type: string;
      x?: number;
      y?: number;
      button?: string;
      clickCount?: number;
      deltaX?: number;
      deltaY?: number;
      key?: string;
      text?: string;
    }) => {
      send({
        type: "input",
        input_type: input.type,
        x: input.x,
        y: input.y,
        button: input.button,
        click_count: input.clickCount,
        delta_x: input.deltaX,
        delta_y: input.deltaY,
        key: input.key,
        text: input.text,
      });
    },
    [send],
  );

  return {
    connected,
    started,
    startBrowser,
    stopBrowser,
    startStreaming,
    navigate,
    reload,
    goBack,
    goForward,
    requestPageInfo,
    sendInput,
  };
}
