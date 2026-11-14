import { useCallback, useEffect, useRef, useState } from "react";
import { browserAction, getWsUrl, wsProtocols } from "../api/loopApi";
import type { PageContextMenu } from "../components/panels/browserContextMenu";
import { type PageDialog, type PageDialogs, withDialog, withoutDialog } from "../components/panels/browserDialog";
import type { DownloadedFile, DownloadList, PageDownload } from "../components/panels/browserDownloads";
import { type ChosenFile, type PageFileChooser, type PageFileChoosers, withFileChooser, withoutFileChooser } from "../components/panels/browserFileChooser";
import type { PageSelectMenu } from "../components/panels/browserSelectMenu";

interface BrowserWSMessage {
  type: string;
  channel_id?: string;
  mode?: string;
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
  modifiers?: number;
  index?: number;
  target_id?: string;
  accept?: boolean;
  files?: ChosenFile[];
}

export interface TabInfo {
  target_id: string;
  url: string;
  title: string;
  // Absent when Chrome has no icon for the tab yet, or would not say.
  favicon_url?: string;
}

interface BrowserWSResponse {
  type: string;
  url?: string;
  title?: string;
  message?: string;
  tabs?: TabInfo[];
  active_target_id?: string;
  target_id?: string;
  text?: string;
  menu?: PageContextMenu;
  dialog?: PageDialog;
  file_chooser?: PageFileChooser;
  select?: PageSelectMenu;
  download?: PageDownload;
  downloads_dir?: string;
  files?: DownloadedFile[];
}

interface UseBrowserWsOptions {
  channelId: string | null;
  onFrame?: (data: ArrayBuffer) => void;
  onPageInfo?: (url: string, title: string) => void;
  onError?: (message: string) => void;
  onStarted?: () => void;
  onStopped?: () => void;
  /** A right-click in the page that wants the pane's menu. */
  onContextMenu?: (menu: PageContextMenu) => void;
  /** A click on a <select>, whose options the pane shows instead of Chrome. */
  onSelectMenu?: (menu: PageSelectMenu) => void;
  /** A download, from any tab, starting or finishing. */
  onDownload?: (download: PageDownload) => void;
  /** The channel's downloads dir, answering listDownloads. */
  onDownloadList?: (list: DownloadList) => void;
}

export function useBrowserWs({ channelId, onFrame, onPageInfo, onError, onStarted, onStopped, onContextMenu, onSelectMenu, onDownload, onDownloadList }: UseBrowserWsOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const onFrameRef = useRef(onFrame);
  const onPageInfoRef = useRef(onPageInfo);
  const onErrorRef = useRef(onError);
  const onStartedRef = useRef(onStarted);
  const onStoppedRef = useRef(onStopped);
  const onContextMenuRef = useRef(onContextMenu);
  const onSelectMenuRef = useRef(onSelectMenu);
  const onDownloadRef = useRef(onDownload);
  const onDownloadListRef = useRef(onDownloadList);
  const [connected, setConnected] = useState(false);
  const [started, setStarted] = useState(false);
  const [tabs, setTabs] = useState<TabInfo[]>([]);
  const [activeTargetId, setActiveTargetId] = useState("");
  const [dialogs, setDialogs] = useState<PageDialogs>({});
  const [fileChoosers, setFileChoosers] = useState<PageFileChoosers>({});

  const activeTargetIdRef = useRef(activeTargetId);
  activeTargetIdRef.current = activeTargetId;

  onFrameRef.current = onFrame;
  onPageInfoRef.current = onPageInfo;
  onErrorRef.current = onError;
  onStartedRef.current = onStarted;
  onStoppedRef.current = onStopped;
  onContextMenuRef.current = onContextMenu;
  onSelectMenuRef.current = onSelectMenu;
  onDownloadRef.current = onDownload;
  onDownloadListRef.current = onDownloadList;

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
      const ws = new WebSocket(wsUrl, wsProtocols());
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
              setTabs((prev) => prev.map((t) => (t.target_id === activeTargetIdRef.current ? { ...t, url: msg.url || t.url, title: msg.title || t.title } : t)));
              break;
            case "clipboard":
              // The sidecar's clipboard is unreachable from here, so a copy in
              // the page comes back as text and is written to the host clipboard.
              if (msg.text) void navigator.clipboard?.writeText(msg.text).catch(() => {});
              break;
            case "context_menu":
              if (msg.menu) onContextMenuRef.current?.(msg.menu);
              break;
            case "select_menu":
              if (msg.select) onSelectMenuRef.current?.(msg.select);
              break;
            case "download":
              if (msg.download) onDownloadRef.current?.(msg.download);
              break;
            case "downloads":
              onDownloadListRef.current?.({ dir: msg.downloads_dir ?? "", files: msg.files ?? [] });
              break;
            case "dialog":
              if (msg.dialog) setDialogs((prev) => withDialog(prev, msg.dialog!));
              break;
            case "dialog_closed":
              if (msg.target_id) setDialogs((prev) => withoutDialog(prev, msg.target_id!));
              break;
            case "file_chooser":
              if (msg.file_chooser) setFileChoosers((prev) => withFileChooser(prev, msg.file_chooser!));
              break;
            case "file_chooser_closed":
              if (msg.target_id) setFileChoosers((prev) => withoutFileChooser(prev, msg.target_id!));
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
                // The backend makes a newly opened tab active as soon as it is
                // created, so mark it here too — waiting for "tab_switched"
                // (sent once the pane has attached) leaves the strip pointing at
                // the previous tab while actions already go to the new one.
                setActiveTargetId(msg.target_id);
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
            case "tab_updated":
              // A tab navigated or retitled itself — the agent's navigations
              // and the page's own, which the pane would otherwise never hear.
              if (msg.target_id) {
                const id = msg.target_id;
                const url = msg.url || "";
                const title = msg.title || "";
                setTabs((prev) => prev.map((t) => (t.target_id === id ? { ...t, url: url || t.url, title } : t)));
                if (id === activeTargetIdRef.current) onPageInfoRef.current?.(url, title);
              }
              break;
            case "tab_closed":
              if (msg.target_id) {
                setTabs((prev) => prev.filter((t) => t.target_id !== msg.target_id));
                setDialogs((prev) => withoutDialog(prev, msg.target_id!));
                setFileChoosers((prev) => withoutFileChooser(prev, msg.target_id!));
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
        // The next start announces whatever is still open.
        setDialogs({});
        setFileChoosers({});
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

  const startBrowser = useCallback(
    (mode?: "docker" | "host") => {
      if (!channelId) return;
      send({ type: "start", channel_id: channelId, mode });
    },
    [channelId, send],
  );

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
          setTabs((prev) => prev.map((t) => (t.target_id === activeTargetIdRef.current ? { ...t, url: resp.page_info!.url, title: resp.page_info!.title } : t)));
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

  const startStreaming = useCallback(
    (width?: number, height?: number) => {
      send({ type: "screencast", width, height });
    },
    [send],
  );

  const sendInput = useCallback(
    (input: { type: string; x?: number; y?: number; button?: string; clickCount?: number; deltaX?: number; deltaY?: number; key?: string; text?: string; modifiers?: number }) => {
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
        modifiers: input.modifiers,
      });
    },
    [send],
  );

  /** Picks option index of the <select> the last select_menu came from. */
  const chooseSelectOption = useCallback(
    (index: number) => {
      send({ type: "input", input_type: "select_choose", index });
    },
    [send],
  );

  /** Asks for the channel's downloads dir; the answer comes to onDownloadList. */
  const listDownloads = useCallback(() => {
    if (channelId) send({ type: "list_downloads", channel_id: channelId });
  }, [send, channelId]);

  /** Answers the dialog blocking targetId: OK or Cancel, with a prompt's text. */
  const answerDialog = useCallback(
    (targetId: string, accept: boolean, text?: string) => {
      send({ type: "dialog_answer", target_id: targetId, accept, text });
    },
    [send],
  );

  /** Gives the file chooser open on targetId the picked files; null cancels it. */
  const answerFileChooser = useCallback(
    (targetId: string, files: ChosenFile[] | null) => {
      send({ type: "file_chooser_answer", target_id: targetId, files: files ?? [] });
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
    // The dialog blocking the tab on screen, if any.
    dialog: dialogs[activeTargetId] as PageDialog | undefined,
    // Every tab's open dialog, so the strip can flag tabs that wait on one.
    dialogs,
    answerDialog,
    // The file chooser the tab on screen opened, if any.
    fileChooser: fileChoosers[activeTargetId] as PageFileChooser | undefined,
    answerFileChooser,
    chooseSelectOption,
    listDownloads,
    listTabs,
    switchTab,
    newTab,
    closeTab,
  };
}
