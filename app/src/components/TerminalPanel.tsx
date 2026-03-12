import { useCallback, useRef, useState } from "react";
import { colors, fonts } from "../theme";
import { TerminalPanes } from "./TerminalPanes";
import type { TerminalPanesRef } from "./TerminalPanes";
import { killAgentContainer } from "../api/loopApi";

const MIN_WIDTH = 280;
const MAX_WIDTH_PERCENT = 0.6;
const WIDTH_STORAGE_KEY = "loop-terminal-panel-width";
const TREE_STORAGE_KEY = "loop-terminal-panes";

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

interface TerminalPanelProps {
  channelId: string | null;
  dirPath?: string;
  branch?: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  tabBar?: React.ReactNode;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
  onStatusChange?: () => void;
}

export function TerminalPanel({ channelId, dirPath, branch, maximized, sidebarOpen, tabBar, onToggleSidebar, onOpenPalette, onToggleMaximize, onClose, onStatusChange }: TerminalPanelProps) {
  const [width, setWidth] = useState(loadWidth);
  const [resizing, setResizing] = useState(false);
  const panesRef = useRef<TerminalPanesRef>(null);
  const [agentState, setAgentState] = useState<"running" | "stopped" | "none">("none");

  // Close all sessions before unmounting the panel.
  const handleClose = useCallback(() => {
    panesRef.current?.closeAllSessions();
    onClose();
  }, [onClose]);

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

  return (
    <div
      style={{
        width: maximized ? "100%" : width,
        minWidth: maximized ? 0 : MIN_WIDTH,
        maxWidth: maximized ? "none" : `${MAX_WIDTH_PERCENT * 100}vw`,
        flex: maximized ? 1 : undefined,
        flexShrink: maximized ? undefined : 1,
        backgroundColor: colors.sidebar,
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
              fontFamily: fonts.mono,
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
              marginLeft: 12,
            }}
          >
            {dirPath}
            {branch && (
              <>
                <span style={{ color: colors.border, flexShrink: 0 }}>|</span>
                <span style={{ fontSize: 11, color: colors.active, fontFamily: fonts.mono, flexShrink: 0 }}>
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
        <div style={{ flex: 1 }} />
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
          height: 35,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, overflow: "hidden" }}>
          {maximized && tabBar}
          {!maximized && (
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
              Terminal
            </span>
          )}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 4, position: "relative" }}>
          {maximized && (
            <span
              style={{
                fontSize: 10,
                fontWeight: 700,
                color: colors.textDim,
                textTransform: "uppercase",
                letterSpacing: 1,
                marginRight: 8,
              }}
            >
              Terminal
            </span>
          )}
          {agentState === "running" && (
            <button
              onClick={() => { if (channelId) killAgentContainer(channelId); }}
              title="Kill agent container"
              style={{
                padding: "1px 8px",
                borderRadius: 4,
                border: `1px solid ${colors.error}`,
                backgroundColor: "transparent",
                color: colors.error,
                cursor: "pointer",
                fontSize: 10,
              }}
            >
              Kill
            </button>
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
            onClick={handleClose}
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
        key={`term-${channelId}`}
        channelId={channelId}
        storageKey={TREE_STORAGE_KEY}
        onStatusChange={onStatusChange}
        onAgentStateChange={setAgentState}
        onLastAgentRemoved={() => { if (channelId) killAgentContainer(channelId); }}
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

