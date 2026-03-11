import { useCallback, useRef, useState } from "react";
import { colors } from "../theme";
import { TerminalPanes, SplitMenu } from "./TerminalPanes";
import type { TerminalPanesRef, SplitDirection } from "./TerminalPanes";

const MIN_WIDTH = 280;
const MAX_WIDTH_PERCENT = 0.45;
const WIDTH_STORAGE_KEY = "loop-shell-panel-width";
const TREE_STORAGE_KEY = "loop-shell-tree";

function loadWidth(): number {
  try {
    const stored = localStorage.getItem(WIDTH_STORAGE_KEY);
    if (stored) {
      const w = parseInt(stored, 10);
      if (w >= MIN_WIDTH) return w;
    }
  } catch { /* ignore */ }
  return Math.floor(window.innerWidth * MAX_WIDTH_PERCENT);
}

function saveWidth(w: number) {
  try {
    localStorage.setItem(WIDTH_STORAGE_KEY, String(w));
  } catch { /* ignore */ }
}

// ── Components ──

interface ShellPanelProps {
  channelId: string | null;
  dirPath?: string;
  branch?: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

export function ShellPanel({ channelId, dirPath, branch, maximized, sidebarOpen, onToggleSidebar, onOpenPalette, onToggleMaximize, onClose }: ShellPanelProps) {
  const [width, setWidth] = useState(loadWidth);
  const [resizing, setResizing] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);
  const panesRef = useRef<TerminalPanesRef>(null);
  const [splitMenu, setSplitMenu] = useState(false);

  // Panel width resize (left edge).
  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      setResizing(true);
      const startX = e.clientX;
      const startWidth = width;

      let lastWidth = startWidth;
      const onMouseMove = (ev: MouseEvent) => {
        const maxWidth = window.innerWidth * MAX_WIDTH_PERCENT;
        const newWidth = Math.min(maxWidth, Math.max(MIN_WIDTH, startWidth - (ev.clientX - startX)));
        lastWidth = newWidth;
        setWidth(newWidth);
      };

      const onMouseUp = () => {
        setResizing(false);
        saveWidth(lastWidth);
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [width],
  );

  const handleSplit = useCallback((direction: SplitDirection) => {
    setSplitMenu(false);
    panesRef.current?.splitLast(direction);
  }, []);

  return (
    <div
      ref={panelRef}
      style={{
        width: maximized ? "100%" : width,
        minWidth: maximized ? 0 : MIN_WIDTH,
        maxWidth: maximized ? "none" : `${MAX_WIDTH_PERCENT * 100}vw`,
        flex: maximized ? 1 : undefined,
        flexShrink: maximized ? undefined : 1,
        backgroundColor: colors.bg,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        position: "relative",
        userSelect: resizing ? "none" : undefined,
        borderLeft: maximized ? "none" : `1px solid ${colors.border}`,
      }}
    >
      {/* Resize handle (left edge) — hidden when maximized */}
      {!maximized && (
        <div
          onMouseDown={handleMouseDown}
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            width: 4,
            height: "100%",
            cursor: "col-resize",
            backgroundColor: resizing ? colors.textDim : "transparent",
            zIndex: 1,
          }}
          onMouseEnter={(e) => {
            (e.currentTarget as HTMLDivElement).style.backgroundColor = colors.textDim;
          }}
          onMouseLeave={(e) => {
            if (!resizing) (e.currentTarget as HTMLDivElement).style.backgroundColor = "transparent";
          }}
        />
      )}

      {/* Drag region for macOS title bar alignment */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          paddingLeft: maximized && !sidebarOpen ? 76 : maximized ? 4 : 0,
          // @ts-expect-error: WebKit-specific CSS property for Electron drag region
          WebkitAppRegion: "drag",
        }}
      >
        {maximized && onToggleSidebar && (
          <button
            onClick={onToggleSidebar}
            title="Toggle sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              // @ts-expect-error: WebKit-specific CSS property
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              {sidebarOpen
                ? <polyline points="15,9 12,12 15,15" />
                : <polyline points="13,9 16,12 13,15" />
              }
            </svg>
          </button>
        )}
        {maximized && onOpenPalette && (
          <button
            onClick={onOpenPalette}
            title="Search messages (Cmd+K)"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 8px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontFamily: "'SF Mono', Menlo, Monaco, 'Courier New', monospace",
              marginLeft: 6,
              // @ts-expect-error: WebKit-specific CSS property
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
          </button>
        )}
      </div>

      {/* Panel header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "3px 12px",
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
          boxSizing: "border-box",
          height: 39,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, overflow: "hidden" }}>
          <span
            style={{
              fontSize: 10,
              fontWeight: 700,
              color: colors.textDim,
              textTransform: "uppercase",
              letterSpacing: 1,
              flexShrink: 0,
            }}
          >
            Host Shell
          </span>
          {maximized && dirPath && (
            <span
              style={{
                fontSize: 12,
                color: colors.textDim,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
                minWidth: 0,
                display: "flex",
                alignItems: "center",
                gap: 6,
              }}
            >
              {dirPath}
              {branch && (
                <>
                  <span style={{ color: colors.border, flexShrink: 0 }}>|</span>
                  <span style={{ fontSize: 11, color: colors.active, fontFamily: "'SF Mono', Menlo, Monaco, 'Courier New', monospace", flexShrink: 0 }}>
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ marginRight: 2, verticalAlign: -1 }}>
                      <line x1="6" y1="3" x2="6" y2="15" />
                      <circle cx="18" cy="6" r="3" />
                      <circle cx="6" cy="18" r="3" />
                      <path d="M18 9a9 9 0 0 1-9 9" />
                    </svg>
                    {branch}
                  </span>
                </>
              )}
            </span>
          )}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 4, position: "relative" }}>
          <button
            onClick={() => setSplitMenu((v) => !v)}
            title="Split pane"
            style={headerBtnStyle}
            onMouseEnter={hoverIn}
            onMouseLeave={hoverOut}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
          </button>
          {splitMenu && (
            <SplitMenu
              onSplit={handleSplit}
              onClose={() => setSplitMenu(false)}
            />
          )}
          {onToggleMaximize && (
            <button
              onClick={onToggleMaximize}
              title={maximized ? "Restore panel" : "Maximize panel"}
              style={headerBtnStyle}
              onMouseEnter={hoverIn}
              onMouseLeave={hoverOut}
            >
              {maximized ? (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="4,14 10,14 10,20" />
                  <polyline points="20,10 14,10 14,4" />
                  <line x1="14" y1="10" x2="21" y2="3" />
                  <line x1="3" y1="21" x2="10" y2="14" />
                </svg>
              ) : (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="15,3 21,3 21,9" />
                  <polyline points="9,21 3,21 3,15" />
                  <line x1="21" y1="3" x2="14" y2="10" />
                  <line x1="3" y1="21" x2="10" y2="14" />
                </svg>
              )}
            </button>
          )}
          <button
            onClick={onClose}
            title="Close panel"
            style={headerBtnStyle}
            onMouseEnter={hoverIn}
            onMouseLeave={hoverOut}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>

      {/* Pane tree */}
      <TerminalPanes
        ref={panesRef}
        channelId={channelId}
        target="host"
        storageKey={TREE_STORAGE_KEY}
        onEmpty={onClose}
      />
    </div>
  );
}

// ── Styles ──

const headerBtnStyle: React.CSSProperties = {
  background: "none",
  border: "none",
  color: colors.textDim,
  cursor: "pointer",
  padding: 4,
  lineHeight: 1,
  borderRadius: 4,
  display: "flex",
  alignItems: "center",
};

function hoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
  e.currentTarget.style.color = colors.textLight;
}

function hoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
  e.currentTarget.style.color = colors.textDim;
}
