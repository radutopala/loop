import { useCallback, useEffect, useRef, useState } from "react";
import { browserAction, getWsUrl } from "../api/loopApi";

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
  target_id?: string;
}

export interface TabInfo {
  target_id: string;
  url: string;
  title: string;
}

interface BrowserWSResponse {
  type: string;
  url?: string;
  title?: string;
  message?: string;
  tabs?: TabInfo[];
  active_target_id?: string;
  target_id?: string;
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
  const [tabs, setTabs] = useState<TabInfo[]>([]);
  const [activeTargetId, setActiveTargetId] = useState("");

  const activeTargetIdRef = useRef(activeTargetId);
  activeTargetIdRef.current = activeTargetId;

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
              // Request tab list via HTTP (WS no longer handles list_tabs).
              if (channelId) {
                browserAction(channelId, "list_tabs").then((resp) => {
                  if (resp.tabs) {
                    setTabs(resp.tabs);
                    const active = resp.tabs.find((t) => t.active);
                    if (active) setActiveTargetId(active.target_id);
                  }
                });
              }
              onStartedRef.current?.();
              break;
            case "stopped":
              setStarted(false);
              onStoppedRef.current?.();
              break;
            case "page_info":
              onPageInfoRef.current?.(msg.url || "", msg.title || "");
              // Update the active tab's title/URL in the tab bar.
              setTabs((prev) =>
                prev.map((t) =>
                  t.target_id === activeTargetIdRef.current
                    ? { ...t, url: msg.url || t.url, title: msg.title || t.title }
                    : t,
                ),
              );
              break;
            case "error":
              onErrorRef.current?.(msg.message || "Unknown error");
              break;
            case "tabs":
              if (msg.tabs) setTabs(msg.tabs);
              if (msg.active_target_id) setActiveTargetId(msg.active_target_id);
              break;
            case "tab_switched":
              if (msg.target_id) setActiveTargetId(msg.target_id);
              break;
            case "tab_created":
              if (msg.target_id) {
                setTabs((prev) => {
                  // Deduplicate — the tab may already exist from a "tabs" response.
                  if (prev.some((t) => t.target_id === msg.target_id)) return prev;
                  return [
                    ...prev,
                    {
                      target_id: msg.target_id!,
                      url: msg.url || "",
                      title: msg.title || "",
                    },
                  ];
                });
              }
              break;
            case "tab_closed":
              if (msg.target_id) {
                setTabs((prev) =>
                  prev.filter((t) => t.target_id !== msg.target_id),
                );
              }
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
      if (!channelId) return;
      browserAction(channelId, "navigate", { url }).then((resp) => {
        if (resp.page_info) {
          onPageInfoRef.current?.(resp.page_info.url, resp.page_info.title);
          setTabs((prev) =>
            prev.map((t) =>
              t.target_id === activeTargetIdRef.current
                ? { ...t, url: resp.page_info!.url, title: resp.page_info!.title }
                : t,
            ),
          );
        }
        // Refresh tab list after page loads to get the final title
        // (Chrome may not have the title ready immediately after navigate).
        setTimeout(() => {
          if (channelId) {
            browserAction(channelId, "list_tabs").then((r) => {
              if (r.tabs) setTabs(r.tabs);
            });
          }
        }, 1000);
      });
    },
    [channelId],
  );

  const reload = useCallback(() => {
    if (channelId) browserAction(channelId, "reload");
  }, [channelId]);
  const goBack = useCallback(() => {
    if (channelId) browserAction(channelId, "go_back");
  }, [channelId]);
  const goForward = useCallback(() => {
    if (channelId) browserAction(channelId, "go_forward");
  }, [channelId]);

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

  const listTabs = useCallback(() => {
    if (!channelId) return;
    browserAction(channelId, "list_tabs").then((resp) => {
      if (resp.tabs) {
        setTabs(resp.tabs);
        const active = resp.tabs.find((t) => t.active);
        if (active) setActiveTargetId(active.target_id);
      }
    });
  }, [channelId]);

  const switchTab = useCallback(
    (targetId: string) => {
      if (!channelId) return;
      setActiveTargetId(targetId);
      // HTTP action — backend sends tab_switched over WS to trigger screencast switch.
      browserAction(channelId, "switch_tab", { target_id: targetId });
    },
    [channelId],
  );

  const newTab = useCallback(
    (url?: string) => {
      if (!channelId) return;
      // HTTP action — backend sends tab_created + tab_switched over WS.
      browserAction(channelId, "new_tab", { url }).then(() => {
        // Refresh tab list after page loads to get the title.
        setTimeout(() => {
          browserAction(channelId, "list_tabs").then((r) => {
            if (r.tabs) setTabs(r.tabs);
          });
        }, 1000);
      });
    },
    [channelId],
  );

  const closeTab = useCallback(
    (targetId: string) => {
      if (!channelId) return;
      // HTTP action — backend sends tab_closed over WS + switches to next tab.
      browserAction(channelId, "close_tab", { target_id: targetId });
    },
    [channelId],
  );

  return {
    connected,
    started,
    tabs,
    activeTargetId,
    startBrowser,
    stopBrowser,
    startStreaming,
    navigate,
    reload,
    goBack,
    goForward,
    sendInput,
    listTabs,
    switchTab,
    newTab,
    closeTab,
  };
}
