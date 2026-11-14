import { type CSSProperties, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { resetBrowserProfile, switchBrowserMode } from "../../api/loopApi";
import { type TabInfo, useBrowserWs } from "../../hooks/useBrowserWs";
import { useTheme } from "../../ThemeContext";
import { storageGet, storageSet } from "../../utils/storage";
import { ContextMenu } from "../shared/ContextMenu";
import { type PageContextMenu, pageMenuItems } from "./browserContextMenu";
import { dialogButtons, dialogHeading, type PageDialog, type PageDialogs } from "./browserDialog";
import { downloadLabel, type PageDownload, withDownload, withoutDownload } from "./browserDownloads";
import { type PageSelectMenu, selectMenuItems } from "./browserSelectMenu";
import { type BrowserInput, createFrameRenderer, createInputCoalescer } from "./browserStream";
import { normalizeNavigateUrl, safeFaviconUrl } from "./browserUrl";
import { CookieImportDialog } from "./CookieImportDialog";

interface BrowserPanelProps {
  channelId: string;
  /** When set, locks the browser to this mode and hides the Docker|Host pill. */
  fixedMode?: "docker" | "host";
}

/**
 * cdpModifiers builds Chrome's modifier bitmask (Alt=1, Ctrl=2, Meta=4,
 * Shift=8). The sidecar runs Linux, where shortcuts are Ctrl-based, so a macOS
 * Command press is sent as Ctrl.
 */
function cdpModifiers(e: React.KeyboardEvent): number {
  return (e.altKey ? 1 : 0) | (e.ctrlKey || e.metaKey ? 2 : 0) | (e.shiftKey ? 8 : 0);
}

export function BrowserPanel({ channelId, fixedMode }: BrowserPanelProps) {
  const { colors } = useTheme();
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const canvasContainerRef = useRef<HTMLDivElement>(null);
  const urlInputRef = useRef<HTMLInputElement>(null);
  const [url, setUrl] = useState("");
  // Headless Chrome draws no context menu, so the pane draws one where the
  // user right-clicked once the backend says what was under the pointer.
  const [pageMenu, setPageMenu] = useState<{ x: number; y: number; menu: PageContextMenu } | null>(null);
  const menuAnchorRef = useRef({ x: 0, y: 0 });
  // Its dropdowns open in a popup the screencast never shows, so a click on a
  // <select> brings up the options here, under the click.
  const [selectMenu, setSelectMenu] = useState<{ x: number; y: number; menu: PageSelectMenu } | null>(null);
  const clickAnchorRef = useRef({ x: 0, y: 0 });
  // Downloads save into the channel's downloads dir with nothing on screen,
  // so the pane says when one starts and how it ended.
  const [downloads, setDownloads] = useState<readonly PageDownload[]>([]);
  const dismissDownload = useCallback((guid: string) => setDownloads((prev) => withoutDownload(prev, guid)), []);
  const [error, setError] = useState<string | null>(null);
  const [hoveredTab, setHoveredTab] = useState<string | null>(null);
  const [showCookieImport, setShowCookieImport] = useState(false);
  const [importNotice, setImportNotice] = useState<string | null>(null);
  const [browserMode, setBrowserMode] = useState<"docker" | "host">(() => {
    if (fixedMode) return fixedMode;
    const saved = storageGet(`browserMode:${channelId}`);
    return saved === "host" ? "host" : "docker";
  });

  // Decodes off the main thread and skips frames that a slower decode has
  // already made stale.
  const drawFrameRef = useRef(createFrameRenderer(() => canvasRef.current));
  // Set right after useBrowserWs returns; the coalescer outlives any single
  // sendInput identity, so it reads the current one through this ref.
  const sendInputRef = useRef<(ev: BrowserInput) => void>(() => {});
  const inputQueueRef = useRef(createInputCoalescer((ev) => sendInputRef.current(ev)));

  const {
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
    switchTab,
    newTab,
    closeTab,
    dialog,
    dialogs,
    answerDialog,
    chooseSelectOption,
  } = useBrowserWs({
    channelId,
    onFrame: useCallback((data: ArrayBuffer) => {
      drawFrameRef.current(data);
    }, []),
    onPageInfo: useCallback((pageUrl: string, _pageTitle: string) => {
      setUrl(pageUrl);
    }, []),
    onError: useCallback((msg: string) => {
      setError(msg);
    }, []),
    onStarted: useCallback(() => {
      setError(null);
      setUrl("");
    }, []),
    onContextMenu: useCallback((menu: PageContextMenu) => {
      setPageMenu({ ...menuAnchorRef.current, menu });
    }, []),
    onSelectMenu: useCallback((menu: PageSelectMenu) => {
      setSelectMenu({ ...clickAnchorRef.current, menu });
    }, []),
    onDownload: useCallback((d: PageDownload) => {
      setDownloads((prev) => withDownload(prev, d));
    }, []),
  });
  sendInputRef.current = sendInput;

  // Every canvas gesture goes through the coalescer, so a burst of moves or
  // wheel ticks becomes one dispatch per frame while clicks and keys still go
  // out immediately, in the order the user made them.
  const pushInput = useCallback((ev: BrowserInput) => {
    inputQueueRef.current.push(ev);
  }, []);

  // Auto-start browser when connected. Pass current mode so the server
  // restores it after daemon restart (server loses in-memory mode state).
  useEffect(() => {
    if (connected && !started) {
      startBrowser(browserMode);
    }
  }, [connected, started, startBrowser, browserMode]);

  // Start streaming once browser is started, and re-request when the panel
  // becomes visible again (e.g. after a layout switch) to ensure frames flow.
  useEffect(() => {
    if (!started) return;
    startStreaming(1920, 1080);
    reload();
  }, [started, startStreaming, reload]);

  // Update URL input when page info changes or active tab changes.
  // Prefer activeTab URL (always matches active tab) over url state
  // (which may be stale from a previous tab's page_info).
  const activeTab = tabs.find((t) => t.target_id === activeTargetId);
  const displayUrl = activeTab?.url || url || "";
  useEffect(() => {
    if (urlInputRef.current && document.activeElement !== urlInputRef.current) {
      urlInputRef.current.value = displayUrl;
    }
  }, [displayUrl]);

  const handleModeToggle = useCallback(() => {
    const newMode = browserMode === "docker" ? "host" : "docker";
    // Stop current session, switch mode on server, then restart.
    stopBrowser();
    switchBrowserMode(channelId, newMode).then((res) => {
      if (res.mode) {
        const mode = res.mode as "docker" | "host";
        setBrowserMode(mode);
        storageSet(`browserMode:${channelId}`, mode);
        // Restart browser with new provider after a brief delay for cleanup.
        setTimeout(() => startBrowser(newMode), 500);
      }
    });
  }, [channelId, browserMode, stopBrowser, startBrowser]);

  const handleCookiesImported = useCallback(
    (count: number) => {
      setShowCookieImport(false);
      // Cookies only take effect on the next load of the page that owns them.
      reload();
      setImportNotice(`Imported ${count} cookie${count === 1 ? "" : "s"}`);
      setTimeout(() => setImportNotice(null), 4000);
    },
    [reload],
  );

  const handleProfileReset = useCallback(() => {
    if (!window.confirm("Reset the browser profile? The agent will be signed out of every site it has logged into.")) return;
    stopBrowser();
    resetBrowserProfile(channelId).then(() => {
      setTimeout(() => startBrowser(browserMode), 500);
    });
  }, [channelId, browserMode, stopBrowser, startBrowser]);

  const handleNavigate = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault();
      const input = urlInputRef.current;
      if (!input) return;
      const targetUrl = normalizeNavigateUrl(input.value);
      if (!targetUrl) return;
      navigate(targetUrl);
    },
    [navigate],
  );

  const handleCanvasMouseEvent = useCallback(
    (e: React.MouseEvent<HTMLCanvasElement>) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const rect = canvas.getBoundingClientRect();
      const scaleX = canvas.width / rect.width;
      const scaleY = canvas.height / rect.height;
      const x = (e.clientX - rect.left) * scaleX;
      const y = (e.clientY - rect.top) * scaleY;
      switch (e.type) {
        case "click":
          clickAnchorRef.current = { x: e.clientX, y: e.clientY };
          pushInput({ type: "click", x, y, button: "left", clickCount: 1 });
          break;
        case "dblclick":
          pushInput({ type: "click", x, y, button: "left", clickCount: 2 });
          break;
        case "contextmenu":
          e.preventDefault();
          menuAnchorRef.current = { x: e.clientX, y: e.clientY };
          pushInput({ type: "contextmenu", x, y });
          break;
        case "mousemove":
          pushInput({ type: "mousemove", x, y });
          break;
      }
    },
    [pushInput],
  );

  const handleCanvasWheel = useCallback(
    (e: React.WheelEvent<HTMLCanvasElement>) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const rect = canvas.getBoundingClientRect();
      const scaleX = canvas.width / rect.width;
      const scaleY = canvas.height / rect.height;
      const x = (e.clientX - rect.left) * scaleX;
      const y = (e.clientY - rect.top) * scaleY;
      pushInput({ type: "scroll", x, y, deltaX: e.deltaX, deltaY: e.deltaY });
    },
    [pushInput],
  );

  const pasteClipboard = useCallback(() => {
    void navigator.clipboard
      ?.readText()
      .then((text) => {
        if (text) pushInput({ type: "paste", text });
      })
      .catch(() => setError("Clipboard read was blocked, so paste is unavailable."));
  }, [pushInput]);

  const handleCanvasKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLCanvasElement>) => {
      e.preventDefault();
      e.stopPropagation();
      const accel = e.ctrlKey || e.metaKey;
      if (accel && !e.altKey) {
        const k = e.key.toLowerCase();
        if (k === "c" || k === "x") {
          // Read the selection first — a cut clears it — then let the page
          // perform the cut itself.
          pushInput({ type: "copy" });
          if (k === "x") pushInput({ type: "keypress", key: k, modifiers: cdpModifiers(e) });
          return;
        }
        if (k === "v") {
          pasteClipboard();
          return;
        }
      }
      if (e.key.length === 1 && !accel && !e.altKey) {
        pushInput({ type: "typetext", text: e.key });
      } else {
        pushInput({ type: "keypress", key: e.key, modifiers: cdpModifiers(e) });
      }
    },
    [pushInput, pasteClipboard],
  );

  const pageMenuActions = useMemo(
    () => ({
      openInNewTab: (u: string) => newTab(u),
      copy: (text: string) => {
        void navigator.clipboard?.writeText(text).catch(() => setError("Clipboard write was blocked, so copy is unavailable."));
      },
      paste: pasteClipboard,
      back: goBack,
      forward: goForward,
      reload,
    }),
    [newTab, pasteClipboard, goBack, goForward, reload],
  );

  return (
    <div
      data-testid="browser-panel"
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        backgroundColor: colors.sidebar,
        overflow: "hidden",
        // Containing block for the cookie import overlay, which is absolute
        // inset:0 — without it the overlay resolves to an ancestor and the
        // dialog centres itself over the whole window.
        position: "relative",
      }}
    >
      {/* Tab strip — Chrome-style rounded tabs */}
      <TabStrip
        tabs={tabs}
        activeTargetId={activeTargetId}
        dialogs={dialogs}
        hoveredTab={hoveredTab}
        colors={colors}
        onTabClick={switchTab}
        onTabClose={closeTab}
        onNewTab={() => newTab()}
        onHover={setHoveredTab}
      />

      {/* Toolbar — back/forward/reload + URL bar */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 2,
          padding: "4px 8px",
          backgroundColor: colors.surface,
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
        }}
      >
        <NavButton onClick={goBack} title="Back" colors={colors}>
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
            <path d="M10 3L5 8L10 13" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </NavButton>
        <NavButton onClick={goForward} title="Forward" colors={colors}>
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
            <path d="M6 3L11 8L6 13" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </NavButton>
        <NavButton onClick={reload} title="Reload" colors={colors}>
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
            <path d="M13.5 8A5.5 5.5 0 1 1 8 2.5M13.5 2.5V6H10" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </NavButton>

        <form onSubmit={handleNavigate} style={{ flex: 1, display: "flex", marginLeft: 4 }}>
          <input
            ref={urlInputRef}
            type="text"
            defaultValue={url}
            placeholder="Search or enter URL"
            onFocus={(e) => e.target.select()}
            style={{
              flex: 1,
              padding: "5px 10px",
              backgroundColor: colors.isDark ? "rgba(255,255,255,0.06)" : "rgba(0,0,0,0.04)",
              border: "none",
              borderRadius: 16,
              color: colors.textLight,
              fontSize: 12,
              outline: "none",
            }}
          />
        </form>

        {/* Docker / Host mode toggle pill — hidden when fixedMode is set */}
        {!fixedMode && <ModePill mode={browserMode} onToggle={handleModeToggle} colors={colors} />}

        {/* Cookie import and profile reset — docker mode only; host mode
            already runs on the user's own Chrome profile, cookies included */}
        {browserMode === "docker" && fixedMode !== "host" && (
          <>
            <ToolbarButton onClick={() => setShowCookieImport(true)} title="Import cookies from your own browser" label="Import cookies" colors={colors} />
            <ToolbarButton onClick={handleProfileReset} title="Reset browser profile (signs the agent out everywhere)" label="Reset profile" colors={colors} />
          </>
        )}
      </div>

      {showCookieImport && <CookieImportDialog channelId={channelId} onClose={() => setShowCookieImport(false)} onImported={handleCookiesImported} />}

      {importNotice && <div style={{ padding: "4px 12px", fontSize: 11, color: colors.textDim, borderBottom: `1px solid ${colors.border}` }}>{importNotice}</div>}

      {/* Error bar */}
      {error && (
        <div
          style={{
            padding: "4px 12px",
            fontSize: 11,
            color: colors.error,
            backgroundColor: colors.errorBannerBg,
            borderBottom: `1px solid ${colors.border}`,
          }}
        >
          {error}
        </div>
      )}

      {dialog && <DialogBar key={`${dialog.target_id}:${dialog.type}:${dialog.message}`} dialog={dialog} colors={colors} onAnswer={(accept, text) => answerDialog(dialog.target_id, accept, text)} />}

      {/* Screencast canvas */}
      <div
        ref={canvasContainerRef}
        style={{
          position: "relative",
          flex: 1,
          overflow: "hidden",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: colors.bg,
        }}
      >
        {started ? (
          <canvas
            ref={canvasRef}
            tabIndex={0}
            style={{
              maxWidth: "100%",
              maxHeight: "100%",
              outline: "none",
              cursor: "default",
            }}
            onClick={(e) => {
              handleCanvasMouseEvent(e);
              canvasRef.current?.focus();
            }}
            onDoubleClick={handleCanvasMouseEvent}
            onContextMenu={handleCanvasMouseEvent}
            onMouseMove={handleCanvasMouseEvent}
            onWheel={handleCanvasWheel}
            onKeyDown={handleCanvasKeyDown}
          />
        ) : (
          <div style={{ color: colors.textDim, fontSize: 13 }}>{connected ? "Starting browser..." : "Connecting..."}</div>
        )}
        <DownloadToasts downloads={downloads} colors={colors} onDismiss={dismissDownload} />
      </div>
      {selectMenu && <ContextMenu x={selectMenu.x} y={selectMenu.y} items={selectMenuItems(selectMenu.menu, chooseSelectOption)} onClose={() => setSelectMenu(null)} />}
      {pageMenu && <ContextMenu x={pageMenu.x} y={pageMenu.y} items={pageMenuItems(pageMenu.menu, pageMenuActions)} onClose={() => setPageMenu(null)} />}
    </div>
  );
}

/* ---------- sub-components ---------- */

/** How long a finished download's toast stays up. */
const DOWNLOAD_TOAST_MS = 6000;

/** Toasts for the page's downloads, in the pane's bottom-right corner. */
function DownloadToasts({
  downloads,
  colors,
  onDismiss,
}: {
  downloads: readonly PageDownload[];
  colors: { surface: string; border: string; textLight: string; textDim: string };
  onDismiss: (guid: string) => void;
}) {
  if (downloads.length === 0) return null;
  return (
    <div data-testid="browser-downloads" style={{ position: "absolute", right: 12, bottom: 12, display: "flex", flexDirection: "column", gap: 6, zIndex: 2 }}>
      {downloads.map((d) => (
        <DownloadToast key={d.guid} download={d} colors={colors} onDismiss={onDismiss} />
      ))}
    </div>
  );
}

function DownloadToast({
  download,
  colors,
  onDismiss,
}: {
  download: PageDownload;
  colors: { surface: string; border: string; textLight: string; textDim: string };
  onDismiss: (guid: string) => void;
}) {
  const { guid, state } = download;
  // One that is still running stays until it ends.
  useEffect(() => {
    if (state === "started") return;
    const t = setTimeout(() => onDismiss(guid), DOWNLOAD_TOAST_MS);
    return () => clearTimeout(t);
  }, [guid, state, onDismiss]);
  return (
    <div
      role="status"
      title={download.url}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        maxWidth: 320,
        padding: "6px 8px 6px 10px",
        fontSize: 12,
        color: colors.textLight,
        backgroundColor: colors.surface,
        border: `1px solid ${colors.border}`,
        borderRadius: 6,
        boxShadow: "0 2px 8px rgba(0,0,0,0.25)",
      }}
    >
      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>{downloadLabel(download)}</span>
      <button
        type="button"
        aria-label="Dismiss"
        onClick={() => onDismiss(guid)}
        style={{ background: "none", border: "none", padding: 0, cursor: "pointer", color: colors.textDim, fontSize: 14, lineHeight: 1 }}
      >
        ×
      </button>
    </div>
  );
}

/**
 * The JavaScript dialog the tab is blocked on, which headless Chrome never
 * draws: the page stays frozen until it is answered here or by the agent.
 */
function DialogBar({
  dialog,
  colors,
  onAnswer,
}: {
  dialog: PageDialog;
  colors: { surface: string; border: string; textLight: string; textDim: string; active: string; white: string; isDark: boolean };
  onAnswer: (accept: boolean, text?: string) => void;
}) {
  const [text, setText] = useState(dialog.default_prompt ?? "");
  const isPrompt = dialog.type === "prompt";
  const buttons = dialogButtons(dialog);
  const accept = () => onAnswer(true, isPrompt ? text : undefined);
  const button = (primary: boolean): CSSProperties => ({
    padding: "3px 12px",
    fontSize: 12,
    borderRadius: 4,
    cursor: "pointer",
    border: `1px solid ${primary ? colors.active : colors.border}`,
    backgroundColor: primary ? colors.active : "transparent",
    color: primary ? colors.white : colors.textLight,
    fontFamily: "inherit",
  });
  return (
    <div
      role="alertdialog"
      aria-label="Page dialog"
      data-testid="browser-dialog"
      onKeyDown={(e) => {
        if (e.key === "Escape") onAnswer(false);
      }}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        flexWrap: "wrap",
        padding: "6px 12px",
        fontSize: 12,
        color: colors.textLight,
        backgroundColor: colors.surface,
        borderBottom: `1px solid ${colors.border}`,
        flexShrink: 0,
      }}
    >
      <span style={{ color: colors.textDim }}>{dialogHeading(dialog)}</span>
      {dialog.message && <span style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", minWidth: 0 }}>{dialog.message}</span>}
      {isPrompt && (
        <input
          autoFocus
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") accept();
          }}
          style={{
            flex: "1 1 160px",
            padding: "3px 8px",
            fontSize: 12,
            border: `1px solid ${colors.border}`,
            borderRadius: 4,
            backgroundColor: colors.isDark ? "rgba(255,255,255,0.06)" : "rgba(0,0,0,0.04)",
            color: colors.textLight,
            outline: "none",
          }}
        />
      )}
      <span style={{ display: "flex", gap: 6, marginLeft: "auto" }}>
        <button type="button" onClick={() => onAnswer(false)} style={button(false)}>
          {buttons.dismiss}
        </button>
        <button type="button" onClick={accept} style={button(true)} autoFocus={!isPrompt}>
          {buttons.accept}
        </button>
      </span>
    </div>
  );
}

function ToolbarButton({ onClick, title, label, colors }: { onClick: () => void; title: string; label: string; colors: { textDim: string } }) {
  return (
    <button
      onClick={onClick}
      title={title}
      aria-label={label}
      style={{
        display: "flex",
        alignItems: "center",
        padding: "3px 8px",
        marginLeft: 4,
        background: "none",
        border: `1px solid ${colors.textDim}33`,
        borderRadius: 10,
        color: colors.textDim,
        cursor: "pointer",
        fontSize: 10,
        lineHeight: "14px",
        whiteSpace: "nowrap",
        flexShrink: 0,
      }}
    >
      {label}
    </button>
  );
}

function ModePill({ mode, onToggle, colors }: { mode: "docker" | "host"; onToggle: () => void; colors: { textDim: string; textLight: string; active: string } }) {
  const activeStyle = { color: colors.active, fontWeight: 600 as const };
  const inactiveStyle = { color: colors.textDim };
  return (
    <button
      onClick={onToggle}
      title={`Switch to ${mode === "docker" ? "host" : "docker"} browser`}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 2,
        padding: "3px 8px",
        marginLeft: 4,
        background: "none",
        border: `1px solid ${colors.textDim}33`,
        borderRadius: 10,
        cursor: "pointer",
        fontSize: 10,
        lineHeight: "14px",
        whiteSpace: "nowrap",
        flexShrink: 0,
      }}
    >
      <span style={mode === "docker" ? activeStyle : inactiveStyle}>Docker</span>
      <span style={{ color: colors.textDim, fontSize: 9 }}>|</span>
      <span style={mode === "host" ? activeStyle : inactiveStyle}>Host</span>
    </button>
  );
}

function NavButton({ onClick, title, colors, children }: { onClick: () => void; title: string; colors: { textDim: string }; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      title={title}
      style={{
        background: "none",
        border: "none",
        color: colors.textDim,
        cursor: "pointer",
        padding: 4,
        borderRadius: 4,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        lineHeight: 0,
      }}
    >
      {children}
    </button>
  );
}

/** Marks a tab whose page waits on a confirm, prompt or leave-page dialog. */
function DialogBadge({ dialog, color }: { dialog: PageDialog; color: string }) {
  return (
    <span
      data-testid="browser-tab-dialog"
      title={`Waiting for an answer: ${dialog.message || dialogHeading(dialog)}`}
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        width: 14,
        height: 14,
        borderRadius: 7,
        flexShrink: 0,
        backgroundColor: color,
        color: "#fff",
        fontSize: 10,
        fontWeight: 700,
        lineHeight: 1,
      }}
    >
      !
    </span>
  );
}

function TabStrip({
  tabs,
  activeTargetId,
  dialogs,
  hoveredTab,
  colors,
  onTabClick,
  onTabClose,
  onNewTab,
  onHover,
}: {
  tabs: TabInfo[];
  activeTargetId: string;
  /** Tabs blocked on a page dialog, flagged so one in the background is not missed. */
  dialogs: PageDialogs;
  hoveredTab: string | null;
  colors: {
    warning: string;
    sidebar: string;
    surface: string;
    border: string;
    textLight: string;
    textDim: string;
    textMuted: string;
    isDark: boolean;
  };
  onTabClick: (id: string) => void;
  onTabClose: (id: string) => void;
  onNewTab: () => void;
  onHover: (id: string | null) => void;
}) {
  // An icon Chrome knows about can still fail to load here — it may be behind
  // a login, or on a network only the sidecar can reach. Remembering the ones
  // that failed keeps the row from retrying the same 404 on every render.
  const [brokenIcons, setBrokenIcons] = useState<Set<string>>(new Set());
  const markIconBroken = useCallback((url: string) => {
    setBrokenIcons((prev) => (prev.has(url) ? prev : new Set(prev).add(url)));
  }, []);

  // Darker strip behind tabs, like Chrome's tab strip.
  const stripBg = colors.isDark ? "#1a1a1a" : "#e0e0e0";
  const activeBg = colors.surface;
  const inactiveBg = "transparent";
  const hoverBg = colors.isDark ? "rgba(255,255,255,0.06)" : "rgba(0,0,0,0.04)";

  return (
    <div
      style={{
        display: "flex",
        alignItems: "flex-end",
        backgroundColor: stripBg,
        paddingLeft: 4,
        paddingTop: 4,
        flexShrink: 0,
        overflow: "hidden",
        gap: 1,
      }}
    >
      {tabs.map((tab) => {
        const isActive = tab.target_id === activeTargetId;
        const isHovered = tab.target_id === hoveredTab;
        const icon = safeFaviconUrl(tab.favicon_url);

        return (
          <div
            key={tab.target_id}
            onMouseEnter={() => onHover(tab.target_id)}
            onMouseLeave={() => onHover(null)}
            onClick={() => {
              if (!isActive) onTabClick(tab.target_id);
            }}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 6,
              padding: "5px 10px",
              paddingRight: 6,
              fontSize: 11,
              color: isActive ? colors.textLight : colors.textMuted,
              backgroundColor: isActive ? activeBg : isHovered ? hoverBg : inactiveBg,
              cursor: isActive ? "default" : "pointer",
              borderTopLeftRadius: 8,
              borderTopRightRadius: 8,
              maxWidth: 200,
              minWidth: 60,
              whiteSpace: "nowrap",
              overflow: "hidden",
              flexShrink: 1,
              position: "relative",
              // Active tab blends into the toolbar below.
              borderBottom: isActive ? `1px solid ${activeBg}` : `1px solid ${colors.border}`,
              marginBottom: -1,
              zIndex: isActive ? 1 : 0,
            }}
          >
            <TabIcon url={icon} broken={brokenIcons.has(icon ?? "")} isActive={isActive} colors={colors} onBroken={markIconBroken} />
            {dialogs[tab.target_id] && <DialogBadge dialog={dialogs[tab.target_id]!} color={colors.warning} />}
            <span
              style={{
                overflow: "hidden",
                textOverflow: "ellipsis",
                flex: 1,
                minWidth: 0,
              }}
            >
              {tab.title || tab.url || "New Tab"}
            </span>
            {/* Close button */}
            <span
              onClick={(e) => {
                e.stopPropagation();
                onTabClose(tab.target_id);
              }}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                width: 16,
                height: 16,
                borderRadius: 4,
                cursor: "pointer",
                opacity: isHovered || isActive ? 0.6 : 0,
                fontSize: 11,
                flexShrink: 0,
                lineHeight: 1,
                transition: "opacity 0.1s",
              }}
              onMouseEnter={(e) => {
                (e.target as HTMLElement).style.opacity = "1";
                (e.target as HTMLElement).style.backgroundColor = colors.isDark ? "rgba(255,255,255,0.1)" : "rgba(0,0,0,0.08)";
              }}
              onMouseLeave={(e) => {
                (e.target as HTMLElement).style.opacity = isActive ? "0.6" : "0";
                (e.target as HTMLElement).style.backgroundColor = "transparent";
              }}
            >
              ✕
            </span>
          </div>
        );
      })}

      {/* New tab button */}
      <button
        onClick={onNewTab}
        title="New tab"
        style={{
          background: "none",
          border: "none",
          color: colors.textDim,
          cursor: "pointer",
          padding: "4px 8px",
          marginBottom: -1,
          borderRadius: 4,
          fontSize: 16,
          lineHeight: 1,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
        }}
      >
        +
      </button>

      {/* Spacer — absorbs remaining width */}
      <div style={{ flex: 1 }} />
    </div>
  );
}

/**
 * The tab's own favicon, falling back to the dot the strip used to draw.
 *
 * The box keeps its 12px either way, so a strip whose icons arrive late does
 * not shuffle its titles sideways as they land. `url` is expected to have come
 * through safeFaviconUrl — the site chooses it, so nothing else may reach the
 * img.
 */
function TabIcon({
  url,
  broken,
  isActive,
  colors,
  onBroken,
}: {
  url: string | undefined;
  broken: boolean;
  isActive: boolean;
  colors: { textLight: string; textDim: string };
  onBroken: (url: string) => void;
}) {
  return (
    <span style={{ width: 12, height: 12, flexShrink: 0, display: "flex", alignItems: "center", justifyContent: "center" }}>
      {url && !broken ? (
        <img src={url} alt="" width={12} height={12} style={{ borderRadius: 2, opacity: isActive ? 1 : 0.8 }} onError={() => onBroken(url)} />
      ) : (
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            backgroundColor: isActive ? colors.textLight : colors.textDim,
            opacity: 0.5,
          }}
        />
      )}
    </span>
  );
}
