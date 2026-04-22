import { useCallback, useEffect, useRef, useState } from "react";
import type { CanvasTile as CanvasTileType } from "./types";
import { PANEL_OPTIONS, PANEL_LABELS, type LeafNode, type PanelType } from "../types/panels";
import { useTheme } from "../ThemeContext";
import type { AgentInfo } from "../hooks/useAgentRegistry";

const HEADER_HEIGHT = 24;
const MIN_WIDTH = 200;
const MIN_HEIGHT = 120;

interface CanvasTileProps {
  tile: CanvasTileType;
  renderLeaf: (leaf: LeafNode) => React.ReactNode;
  agentInfo?: AgentInfo;
  /** Report movement delta (in world coordinates). */
  onMove: (id: string, dx: number, dy: number) => void;
  onResize: (id: string, width: number, height: number) => void;
  onBringToFront: (id: string) => void;
  onClose: (id: string) => void;
  /** Add a new tile at a world position. */
  onAddTile: (panel: PanelType, position: { x: number; y: number }) => void;
  /** Singleton panels already in use. */
  usedSingletons?: Set<PanelType>;
  /** Toggle maximize — tile fills viewport. */
  onToggleMaximize?: (id: string) => void;
  isMaximized?: boolean;
  /** Current viewport zoom — used to scale mouse deltas. */
  zoom?: number;
  hiddenPanels?: PanelType[];
}

export function CanvasTile({ tile, renderLeaf, agentInfo, onMove, onResize, onBringToFront, onClose, onAddTile, usedSingletons, onToggleMaximize, isMaximized, zoom = 1, hiddenPanels }: CanvasTileProps) {
  const { colors } = useTheme();
  const containerRef = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState(false);
  const [resizing, setResizing] = useState(false);
  const [showAddMenu, setShowAddMenu] = useState(false);
  const addMenuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!showAddMenu) return;
    const handler = (e: MouseEvent) => {
      if (addMenuRef.current && !addMenuRef.current.contains(e.target as Node)) setShowAddMenu(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [showAddMenu]);

  const isAgent = tile.panel === "docker-agent";
  const label = isAgent ? (agentInfo?.name || tile.id) : PANEL_LABELS[tile.panel];

  // --- Drag (reports deltas in world coords) ---
  const handleDragStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    onBringToFront(tile.id);
    let lastX = e.clientX;
    let lastY = e.clientY;
    setDragging(true);

    const onMouseMove = (ev: MouseEvent) => {
      const dx = (ev.clientX - lastX) / zoom;
      const dy = (ev.clientY - lastY) / zoom;
      lastX = ev.clientX;
      lastY = ev.clientY;
      onMove(tile.id, dx, dy);
    };
    const onMouseUp = () => {
      setDragging(false);
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    };
    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
  }, [tile.id, zoom, onMove, onBringToFront]);

  // --- Resize ---
  const handleResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    onBringToFront(tile.id);
    const startX = e.clientX;
    const startY = e.clientY;
    const startW = tile.width;
    const startH = tile.height;
    setResizing(true);

    const onMouseMove = (ev: MouseEvent) => {
      const newW = Math.max(MIN_WIDTH, startW + (ev.clientX - startX) / zoom);
      const newH = Math.max(MIN_HEIGHT, startH + (ev.clientY - startY) / zoom);
      onResize(tile.id, newW, newH);
    };
    const onMouseUp = () => {
      setResizing(false);
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    };
    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
  }, [tile.id, tile.width, tile.height, zoom, onResize, onBringToFront]);

  // Create a synthetic LeafNode for renderLeaf.
  const leafNode: LeafNode = {
    type: "leaf",
    id: tile.id,
    panel: tile.panel,
    flex: 1,
  };

  return (
    <div
      ref={containerRef}
      onMouseDown={() => onBringToFront(tile.id)}
      style={{
        position: "absolute",
        left: tile.x,
        top: tile.y,
        width: tile.width,
        height: tile.height,
        zIndex: tile.zIndex,
        display: "flex",
        flexDirection: "column",
        border: `1px solid ${colors.border}`,
        borderRadius: 6,
        overflow: "hidden",
        backgroundColor: colors.bg,
        boxShadow: dragging || resizing ? "0 4px 16px rgba(0,0,0,0.3)" : "0 2px 8px rgba(0,0,0,0.15)",
        transition: dragging || resizing ? "none" : "box-shadow 0.15s ease",
      }}
    >
      {/* Header — draggable */}
      <div
        onMouseDown={handleDragStart}
        style={{
          height: HEADER_HEIGHT,
          display: "flex",
          alignItems: "center",
          gap: 4,
          padding: "0 6px",
          backgroundColor: colors.surface,
          borderBottom: `1px solid ${colors.border}`,
          cursor: "grab",
          flexShrink: 0,
          userSelect: "none",
        }}
      >
        {isAgent && agentInfo && (
          <span
            style={{
              width: 6,
              height: 6,
              borderRadius: "50%",
              backgroundColor: agentInfo.status === "running" ? colors.active : agentInfo.status === "error" ? colors.error : colors.textDim,
              flexShrink: 0,
            }}
          />
        )}
        <span style={{ fontSize: 10, fontWeight: 500, color: colors.textLight, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {label}
        </span>
        <span style={{ width: 1, height: 10, backgroundColor: colors.border, flexShrink: 0, marginLeft: 2, marginRight: 2 }} />
        {/* Add panel button */}
        <div ref={addMenuRef} style={{ position: "relative", flexShrink: 0 }}>
          <button
            onClick={(e) => { e.stopPropagation(); setShowAddMenu((v) => !v); }}
            title="Add panel"
            style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: "0 2px", lineHeight: 0 }}
            onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
            onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
          </button>
          {showAddMenu && (
            <div
              style={{
                position: "absolute",
                top: "100%",
                left: 0,
                marginTop: 4,
                backgroundColor: colors.surface,
                border: `1px solid ${colors.border}`,
                borderRadius: 6,
                padding: 4,
                zIndex: 99999,
                minWidth: 100,
                boxShadow: "0 4px 12px rgba(0,0,0,0.2)",
              }}
              onMouseDown={(e) => e.stopPropagation()}
            >
              {PANEL_OPTIONS.filter((opt) => !hiddenPanels?.includes(opt.panel)).map((opt) => {
                const disabled = !!usedSingletons?.has(opt.panel);
                return (
                  <button
                    key={opt.panel}
                    disabled={disabled}
                    onClick={(e) => {
                      e.stopPropagation();
                      if (!disabled) {
                        onAddTile(opt.panel, { x: tile.x + tile.width + 20, y: tile.y });
                        setShowAddMenu(false);
                      }
                    }}
                    style={{ display: "block", width: "100%", padding: "4px 12px", background: "none", border: "none", color: disabled ? colors.textDim : colors.textLight, opacity: disabled ? 0.4 : 1, fontSize: 12, textAlign: "left", cursor: disabled ? "default" : "pointer", borderRadius: 3 }}
                    onMouseEnter={(e) => { if (!disabled) e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; }}
                    onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
                  >
                    {opt.label}
                  </button>
                );
              })}
            </div>
          )}
        </div>
        {onToggleMaximize && (
          <button
            onClick={(e) => { e.stopPropagation(); onToggleMaximize(tile.id); }}
            title={isMaximized ? "Restore tile" : "Maximize tile"}
            style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: "0 2px", lineHeight: 0 }}
            onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
            onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
          >
            {isMaximized ? (
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="4 14 10 14 10 20" />
                <polyline points="20 10 14 10 14 4" />
                <line x1="14" y1="10" x2="21" y2="3" />
                <line x1="3" y1="21" x2="10" y2="14" />
              </svg>
            ) : (
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="15 3 21 3 21 9" />
                <polyline points="9 21 3 21 3 15" />
                <line x1="21" y1="3" x2="14" y2="10" />
                <line x1="3" y1="21" x2="10" y2="14" />
              </svg>
            )}
          </button>
        )}
        <button
          onClick={(e) => { e.stopPropagation(); onClose(tile.id); }}
          style={{
            background: "none",
            border: "none",
            color: colors.textDim,
            cursor: "pointer",
            padding: 0,
            lineHeight: 0,
            fontSize: 14,
          }}
          title="Close tile"
        >
          ×
        </button>
        <div style={{ flex: 1 }} />
        <div id={`pane-header-slot-${tile.id}`} style={{ display: "flex", alignItems: "center", gap: 4, flexShrink: 0 }} />
      </div>

      {/* Content */}
      <div style={{ flex: 1, overflow: "hidden", minHeight: 0, display: "flex", flexDirection: "column" }}>
        {renderLeaf(leafNode)}
      </div>

      {/* Resize handle (bottom-right corner) */}
      <div
        onMouseDown={handleResizeStart}
        style={{
          position: "absolute",
          right: 0,
          bottom: 0,
          width: 12,
          height: 12,
          cursor: "nwse-resize",
        }}
      />
    </div>
  );
}
