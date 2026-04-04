import { useCallback, useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { switchBrowserMode } from "../../api/loopApi";
import { useBrowserWs, type TabInfo } from "../../hooks/useBrowserWs";
import { storageGet, storageSet } from "../../utils/storage";

interface BrowserPanelProps {
  channelId: string;
  /** When set, locks the browser to this mode and hides the Docker|Host pill. */
  fixedMode?: "docker" | "host";
}

export function BrowserPanel({ channelId, fixedMode }: BrowserPanelProps) {
  const { colors } = useTheme();
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const canvasContainerRef = useRef<HTMLDivElement>(null);
  const urlInputRef = useRef<HTMLInputElement>(null);
  const [url, setUrl] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [hoveredTab, setHoveredTab] = useState<string | null>(null);
  const [browserMode, setBrowserMode] = useState<"docker" | "host">(() => {
    if (fixedMode) return fixedMode;
    const saved = storageGet(`browserMode:${channelId}`);
    return saved === "host" ? "host" : "docker";
  });

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
  } = useBrowserWs({
    channelId,
    onFrame: useCallback((data: ArrayBuffer) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const ctx = canvas.getContext("2d");
      if (!ctx) return;

      const blob = new Blob([data], { type: "image/jpeg" });
      const img = new Image();
      img.onload = () => {
        canvas.width = img.width;
        canvas.height = img.height;
        ctx.drawImage(img, 0, 0);
        URL.revokeObjectURL(img.src);
      };
      img.src = URL.createObjectURL(blob);
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
  });

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

  const handleNavigate = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault();
      const input = urlInputRef.current;
      if (!input) return;
      let targetUrl = input.value.trim();
      if (!targetUrl) return;
      if (!/^https?:\/\//i.test(targetUrl)) {
        targetUrl = "https://" + targetUrl;
      }
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
          sendInput({ type: "click", x, y, button: "left", clickCount: 1 });
          break;
        case "dblclick":
          sendInput({ type: "click", x, y, button: "left", clickCount: 2 });
          break;
        case "contextmenu":
          e.preventDefault();
          sendInput({ type: "click", x, y, button: "right", clickCount: 1 });
          break;
        case "mousemove":
          sendInput({ type: "mousemove", x, y });
          break;
      }
    },
    [sendInput],
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
      sendInput({ type: "scroll", x, y, deltaX: e.deltaX, deltaY: e.deltaY });
    },
    [sendInput],
  );

  const handleCanvasKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLCanvasElement>) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
        sendInput({ type: "typetext", text: e.key });
      } else {
        sendInput({ type: "keypress", key: e.key });
      }
    },
    [sendInput],
  );

  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        backgroundColor: colors.sidebar,
        overflow: "hidden",
      }}
    >
      {/* Tab strip — Chrome-style rounded tabs */}
      <TabStrip
        tabs={tabs}
        activeTargetId={activeTargetId}
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
      </div>

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

      {/* Screencast canvas */}
      <div
        ref={canvasContainerRef}
        style={{
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
            onClick={(e) => { handleCanvasMouseEvent(e); canvasRef.current?.focus(); }}
            onDoubleClick={handleCanvasMouseEvent}
            onContextMenu={handleCanvasMouseEvent}
            onMouseMove={handleCanvasMouseEvent}
            onWheel={handleCanvasWheel}
            onKeyDown={handleCanvasKeyDown}
          />
        ) : (
          <div style={{ color: colors.textDim, fontSize: 13 }}>
            {connected ? "Starting browser..." : "Connecting..."}
          </div>
        )}
      </div>
    </div>
  );
}

/* ---------- sub-components ---------- */

function ModePill({
  mode, onToggle, colors,
}: {
  mode: "docker" | "host";
  onToggle: () => void;
  colors: { textDim: string; textLight: string; active: string };
}) {
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

function NavButton({
  onClick, title, colors, children,
}: {
  onClick: () => void;
  title: string;
  colors: { textDim: string };
  children: React.ReactNode;
}) {
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

function TabStrip({
  tabs, activeTargetId, hoveredTab, colors,
  onTabClick, onTabClose, onNewTab, onHover,
}: {
  tabs: TabInfo[];
  activeTargetId: string;
  hoveredTab: string | null;
  colors: {
    sidebar: string; surface: string; border: string;
    textLight: string; textDim: string; textMuted: string;
    isDark: boolean;
  };
  onTabClick: (id: string) => void;
  onTabClose: (id: string) => void;
  onNewTab: () => void;
  onHover: (id: string | null) => void;
}) {
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

        return (
          <div
            key={tab.target_id}
            onMouseEnter={() => onHover(tab.target_id)}
            onMouseLeave={() => onHover(null)}
            onClick={() => { if (!isActive) onTabClick(tab.target_id); }}
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
            {/* Favicon placeholder dot */}
            <span
              style={{
                width: 6,
                height: 6,
                borderRadius: "50%",
                backgroundColor: isActive ? colors.textLight : colors.textDim,
                flexShrink: 0,
                opacity: 0.5,
              }}
            />
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
              onClick={(e) => { e.stopPropagation(); onTabClose(tab.target_id); }}
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
              onMouseEnter={(e) => { (e.target as HTMLElement).style.opacity = "1"; (e.target as HTMLElement).style.backgroundColor = colors.isDark ? "rgba(255,255,255,0.1)" : "rgba(0,0,0,0.08)"; }}
              onMouseLeave={(e) => { (e.target as HTMLElement).style.opacity = isActive ? "0.6" : "0"; (e.target as HTMLElement).style.backgroundColor = "transparent"; }}
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
