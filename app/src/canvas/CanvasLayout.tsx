import { useCallback, useEffect, useRef, useState } from "react";
import type { CanvasNode, CanvasTile as CanvasTileType } from "./types";
import { SINGLETON_PANELS, EXCLUSIVE_PANELS, PANEL_OPTIONS, type LeafNode, type PanelType } from "../types/panels";
import { CanvasTile } from "./CanvasTile";
import { EmptyLayoutPicker } from "../splitPane/AddPanelButton";
import { useTheme } from "../ThemeContext";
import type { AgentInfo } from "../hooks/useAgentRegistry";

const DOT_SPACING = 20;
const MIN_ZOOM = 0.25;
const MAX_ZOOM = 2;

interface CanvasLayoutProps {
  canvas: CanvasNode;
  renderLeaf: (leaf: LeafNode) => React.ReactNode;
  agentInfoMap?: Map<string, AgentInfo>;
  onCanvasChange: (canvas: CanvasNode) => void;
  hiddenPanels?: PanelType[];
}

/** Free-form canvas layout with draggable/resizable tiles, pan & zoom. */
export function CanvasLayout({ canvas, renderLeaf, agentInfoMap, onCanvasChange, hiddenPanels }: CanvasLayoutProps) {
  const { colors } = useTheme();
  const [showAddMenu, setShowAddMenu] = useState<{ x: number; y: number } | null>(null);
  const [maximizedId, setMaximizedId] = useState<string | null>(null);
  const savedBoundsRef = useRef<{ x: number; y: number; width: number; height: number } | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const isPanning = useRef(false);

  const vp = canvas.viewport;

  const setViewport = useCallback((x: number, y: number, zoom: number) => {
    onCanvasChange({ ...canvas, viewport: { x, y, zoom } });
  }, [canvas, onCanvasChange]);

  // Auto-center tiles when viewport is at the default {0,0,1} (initial load or after reset).
  const canvasForCenter = canvas; // capture for effect
  useEffect(() => {
    if (canvasForCenter.tiles.length === 0) return;
    if (vp.x !== 0 || vp.y !== 0 || vp.zoom !== 1) return;
    const rect = containerRef.current?.getBoundingClientRect();
    if (!rect) return;
    const minX = Math.min(...canvasForCenter.tiles.map((t) => t.x));
    const minY = Math.min(...canvasForCenter.tiles.map((t) => t.y));
    const maxX = Math.max(...canvasForCenter.tiles.map((t) => t.x + t.width));
    const maxY = Math.max(...canvasForCenter.tiles.map((t) => t.y + t.height));
    const contentW = maxX - minX;
    const contentH = maxY - minY;
    const zoom = Math.min(1, rect.width * 0.9 / contentW, rect.height * 0.9 / contentH);
    const offsetX = (rect.width - contentW * zoom) / 2 - minX * zoom;
    const offsetY = (rect.height - contentH * zoom) / 2 - minY * zoom;
    onCanvasChange({ ...canvasForCenter, viewport: { x: offsetX, y: offsetY, zoom } });
  }, [vp.x, vp.y, vp.zoom]); // eslint-disable-line react-hooks/exhaustive-deps

  const canvasRef = useRef(canvas);
  canvasRef.current = canvas;

  const handleMoveTile = useCallback((id: string, dx: number, dy: number) => {
    const c = canvasRef.current;
    onCanvasChange({
      ...c,
      tiles: c.tiles.map((t) => (t.id === id ? { ...t, x: t.x + dx, y: t.y + dy } : t)),
    });
  }, [onCanvasChange]);

  const handleResizeTile = useCallback((id: string, width: number, height: number) => {
    const c = canvasRef.current;
    onCanvasChange({
      ...c,
      tiles: c.tiles.map((t) => (t.id === id ? { ...t, width, height } : t)),
    });
  }, [onCanvasChange]);

  const handleBringToFront = useCallback((id: string) => {
    const maxZ = Math.max(...canvas.tiles.map((t) => t.zIndex), 0);
    const tile = canvas.tiles.find((t) => t.id === id);
    if (tile && tile.zIndex < maxZ) {
      onCanvasChange({
        ...canvas,
        tiles: canvas.tiles.map((t) => (t.id === id ? { ...t, zIndex: maxZ + 1 } : t)),
      });
    }
  }, [canvas, onCanvasChange]);

  const handleCloseTile = useCallback((id: string) => {
    onCanvasChange({
      ...canvas,
      tiles: canvas.tiles.filter((t) => t.id !== id),
    });
  }, [canvas, onCanvasChange]);

  const handleToggleMaximize = useCallback((id: string) => {
    const c = canvasRef.current;
    const tile = c.tiles.find((t) => t.id === id);
    if (!tile) return;

    if (maximizedId === id) {
      // Restore original bounds.
      const saved = savedBoundsRef.current;
      if (saved) {
        onCanvasChange({
          ...c,
          tiles: c.tiles.map((t) => (t.id === id ? { ...t, ...saved } : t)),
        });
      }
      savedBoundsRef.current = null;
      setMaximizedId(null);
    } else {
      // Save current bounds, then fill the visible viewport.
      savedBoundsRef.current = { x: tile.x, y: tile.y, width: tile.width, height: tile.height };
      const rect = containerRef.current?.getBoundingClientRect();
      const w = (rect?.width ?? 1200) / vp.zoom;
      const h = (rect?.height ?? 800) / vp.zoom;
      const worldX = -vp.x / vp.zoom;
      const worldY = -vp.y / vp.zoom;
      const maxZ = Math.max(...c.tiles.map((t) => t.zIndex), 0);
      onCanvasChange({
        ...c,
        tiles: c.tiles.map((t) => (t.id === id ? { ...t, x: worldX + 10 / vp.zoom, y: worldY + 10 / vp.zoom, width: w - 20 / vp.zoom, height: h - 20 / vp.zoom, zIndex: maxZ + 1 } : t)),
      });
      setMaximizedId(id);
    }
  }, [maximizedId, vp, onCanvasChange]);

  /** Convert screen coordinates to world coordinates. */
  const screenToWorld = useCallback((screenX: number, screenY: number) => {
    const rect = containerRef.current?.getBoundingClientRect();
    if (!rect) return { x: 0, y: 0 };
    return {
      x: (screenX - rect.left - vp.x) / vp.zoom,
      y: (screenY - rect.top - vp.y) / vp.zoom,
    };
  }, [vp]);

  const handleAddTile = useCallback((panel: PanelType, position?: { x: number; y: number }) => {
    // Default position: center of the visible viewport.
    let worldPos: { x: number; y: number };
    if (position) {
      worldPos = position;
    } else if (showAddMenu) {
      worldPos = screenToWorld(
        (containerRef.current?.getBoundingClientRect().left ?? 0) + showAddMenu.x,
        (containerRef.current?.getBoundingClientRect().top ?? 0) + showAddMenu.y,
      );
    } else {
      const rect = containerRef.current?.getBoundingClientRect();
      worldPos = rect ? screenToWorld(rect.left + rect.width / 2 - 250, rect.top + rect.height / 2 - 200) : { x: 20, y: 20 };
    }
    const maxZ = Math.max(...canvas.tiles.map((t) => t.zIndex), 0);
    const id = `${panel}-${Date.now()}`;
    const { w: tileW, h: tileH } = DEFAULT_TILE_SIZES[panel] ?? { w: 500, h: 400 };
    // Nudge position to avoid overlapping existing tiles.
    const adjusted = findNonOverlappingPosition(worldPos.x, worldPos.y, tileW, tileH, canvas.tiles);
    const newTile: CanvasTileType = {
      id,
      panel,
      x: adjusted.x,
      y: adjusted.y,
      width: tileW,
      height: tileH,
      zIndex: maxZ + 1,
    };
    onCanvasChange({
      ...canvas,
      tiles: [...canvas.tiles, newTile],
    });
    setShowAddMenu(null);
  }, [canvas, showAddMenu, onCanvasChange, screenToWorld]);

  const handleDoubleClick = useCallback((e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest("[data-canvas-tile]")) return;
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setShowAddMenu({ x: e.clientX - rect.left, y: e.clientY - rect.top });
  }, []);

  // --- Pan: middle-click drag or space+left-click ---
  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    // Middle-click pan.
    if (e.button !== 1) return;
    e.preventDefault();
    isPanning.current = true;
    const startX = e.clientX - vp.x;
    const startY = e.clientY - vp.y;
    const onMouseMove = (ev: MouseEvent) => {
      setViewport(ev.clientX - startX, ev.clientY - startY, vp.zoom);
    };
    const onMouseUp = () => {
      isPanning.current = false;
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    };
    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
  }, [vp, setViewport]);

  // --- Zoom: Ctrl+scroll or pinch (non-passive to allow preventDefault) ---
  const vpRef = useRef(vp);
  vpRef.current = vp;
  const setViewportRef = useRef(setViewport);
  setViewportRef.current = setViewport;

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const handler = (e: WheelEvent) => {
      const v = vpRef.current;
      const insideTile = (e.target as HTMLElement).closest?.("[data-canvas-tile]");
      if (e.ctrlKey || e.metaKey) {
        e.preventDefault();
        const rect = el.getBoundingClientRect();
        const cursorX = e.clientX - rect.left;
        const cursorY = e.clientY - rect.top;
        const delta = -e.deltaY * 0.002;
        const newZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v.zoom * (1 + delta)));
        const scale = newZoom / v.zoom;
        setViewportRef.current(
          cursorX - (cursorX - v.x) * scale,
          cursorY - (cursorY - v.y) * scale,
          newZoom,
        );
      } else if (!insideTile) {
        // Only pan when scrolling on the canvas background, not inside tiles.
        setViewportRef.current(v.x - e.deltaX, v.y - e.deltaY, v.zoom);
      }
    };
    el.addEventListener("wheel", handler, { passive: false });
    return () => el.removeEventListener("wheel", handler);
  }, []);

  // Singletons and exclusive groups already in use — grey out in add menus.
  const usedSingletons = new Set(
    canvas.tiles.map((t) => t.panel).filter((p) => (SINGLETON_PANELS as string[]).includes(p)),
  );
  // If any panel in an exclusive group is used, mark all others in that group as used.
  for (const group of EXCLUSIVE_PANELS) {
    if (group.some((p) => usedSingletons.has(p))) {
      for (const p of group) usedSingletons.add(p);
    }
  }
  // Dot grid: scale spacing with zoom, offset with pan.
  const dotSpacing = DOT_SPACING * vp.zoom;
  const dotSize = Math.max(1, vp.zoom) * 1.5;
  const dotColor = colors.isDark ? "rgba(255,255,255,0.12)" : "rgba(0,0,0,0.12)";

  return (
    <div
      ref={containerRef}
      onDoubleClick={handleDoubleClick}
      onMouseDown={handleMouseDown}
      onClick={() => showAddMenu && setShowAddMenu(null)}
      style={{
        position: "relative",
        width: "100%",
        height: "100%",
        overflow: "hidden",
        backgroundColor: colors.isDark ? "rgba(0,0,0,0.3)" : "rgba(0,0,0,0.05)",
        backgroundImage: `radial-gradient(circle, ${dotColor} ${dotSize}px, transparent ${dotSize}px)`,
        backgroundSize: `${dotSpacing}px ${dotSpacing}px`,
        backgroundPosition: `${vp.x % dotSpacing}px ${vp.y % dotSpacing}px`,
        cursor: isPanning.current ? "grabbing" : "default",
      }}
    >
      {/* Empty state */}
      {canvas.tiles.length === 0 && !showAddMenu && (
        <div style={{ position: "absolute", inset: 0, display: "flex" }}>
          <EmptyLayoutPicker onAdd={(panel) => handleAddTile(panel)} hiddenPanels={hiddenPanels} />
        </div>
      )}

      {/* Transformed content layer */}
      <div
        style={{
          position: "absolute",
          left: 0,
          top: 0,
          transformOrigin: "0 0",
          transform: `translate(${vp.x}px, ${vp.y}px) scale(${vp.zoom})`,
        }}
      >
        {canvas.tiles.map((tile) => (
          <div key={tile.id} data-canvas-tile>
            <CanvasTile
              tile={tile}
              renderLeaf={renderLeaf}
              agentInfo={tile.panel === "docker-agent" ? agentInfoMap?.get(tile.id) : undefined}
              onMove={handleMoveTile}
              onResize={handleResizeTile}
              onBringToFront={handleBringToFront}
              onClose={handleCloseTile}
              onAddTile={handleAddTile}
              usedSingletons={usedSingletons}
              onToggleMaximize={handleToggleMaximize}
              isMaximized={maximizedId === tile.id}
              zoom={vp.zoom}
              hiddenPanels={hiddenPanels}
            />
          </div>
        ))}
      </div>

      {/* Add tile menu */}
      {showAddMenu && (
        <div
          style={{
            position: "absolute",
            left: showAddMenu.x,
            top: showAddMenu.y,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.border}`,
            borderRadius: 6,
            padding: 4,
            zIndex: 99999,
            boxShadow: "0 4px 12px rgba(0,0,0,0.2)",
          }}
        >
          {PANEL_OPTIONS.filter((opt) => !hiddenPanels?.includes(opt.panel)).map((opt) => {
            const disabled = usedSingletons.has(opt.panel);
            return (
              <button
                key={opt.panel}
                onClick={(e) => { e.stopPropagation(); if (!disabled) handleAddTile(opt.panel); }}
                disabled={disabled}
                style={{
                  display: "block",
                  width: "100%",
                  padding: "4px 12px",
                  background: "none",
                  border: "none",
                  color: disabled ? colors.textDim : colors.textLight,
                  opacity: disabled ? 0.4 : 1,
                  fontSize: 12,
                  textAlign: "left",
                  cursor: disabled ? "default" : "pointer",
                  borderRadius: 3,
                }}
                onMouseEnter={(e) => { if (!disabled) e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
              >
                {opt.label}
              </button>
            );
          })}
        </div>
      )}

      {/* Minimap + zoom controls */}
      {canvas.tiles.length > 0 && (
        <CanvasMinimap
          tiles={canvas.tiles}
          viewport={vp}
          containerRef={containerRef}
          onPan={(x, y) => setViewport(x, y, vp.zoom)}
          onZoom={(newZoom) => {
            const rect = containerRef.current?.getBoundingClientRect();
            if (!rect) return;
            const cx = rect.width / 2;
            const cy = rect.height / 2;
            const scale = newZoom / vp.zoom;
            setViewport(cx - (cx - vp.x) * scale, cy - (cy - vp.y) * scale, newZoom);
          }}
        />
      )}
    </div>
  );
}

// --- Minimap ---

const MINIMAP_WIDTH = 160;
const MINIMAP_HEIGHT = 100;
const MINIMAP_PADDING = 8;

const PANEL_COLORS: Record<PanelType, string> = {
  chat: "#4a9eff",
  editor: "#a78bfa",
  memory: "#34d399",
  git: "#fbbf24",
  "docker-agent": "#f87171",
  "host-shell": "#94a3b8",
  "docker-shell": "#64748b",
  "docker-browser": "#fb923c",
  "host-browser": "#38bdf8",
  sessions: "#c084fc",
  playground: "#10b981",
  notes: "#f9a8d4",
  tasks: "#f59e0b",
  kanban: "#8b5cf6",
  workflows: "#818cf8",
};

function CanvasMinimap({ tiles, viewport: vp, containerRef, onPan, onZoom }: {
  tiles: CanvasTileType[];
  viewport: { x: number; y: number; zoom: number };
  containerRef: React.RefObject<HTMLDivElement | null>;
  onPan: (x: number, y: number) => void;
  onZoom: (zoom: number) => void;
}) {
  const { colors } = useTheme();
  const minimapRef = useRef<HTMLDivElement>(null);

  // Compute world bounds of all tiles.
  const minX = Math.min(...tiles.map((t) => t.x));
  const minY = Math.min(...tiles.map((t) => t.y));
  const maxX = Math.max(...tiles.map((t) => t.x + t.width));
  const maxY = Math.max(...tiles.map((t) => t.y + t.height));

  // Include the visible viewport area in bounds.
  const rect = containerRef.current?.getBoundingClientRect();
  const vpWorldX = -vp.x / vp.zoom;
  const vpWorldY = -vp.y / vp.zoom;
  const vpWorldW = (rect?.width ?? 800) / vp.zoom;
  const vpWorldH = (rect?.height ?? 600) / vp.zoom;

  const worldMinX = Math.min(minX, vpWorldX);
  const worldMinY = Math.min(minY, vpWorldY);
  const worldMaxX = Math.max(maxX, vpWorldX + vpWorldW);
  const worldMaxY = Math.max(maxY, vpWorldY + vpWorldH);
  const worldW = worldMaxX - worldMinX || 1;
  const worldH = worldMaxY - worldMinY || 1;

  // Scale to fit minimap.
  const scale = Math.min(MINIMAP_WIDTH / worldW, MINIMAP_HEIGHT / worldH);
  const mapW = worldW * scale;
  const mapH = worldH * scale;

  // Map world coords to minimap coords.
  const toMinimap = (wx: number, wy: number) => ({
    x: (wx - worldMinX) * scale,
    y: (wy - worldMinY) * scale,
  });

  const vpMini = toMinimap(vpWorldX, vpWorldY);
  const vpMiniW = vpWorldW * scale;
  const vpMiniH = vpWorldH * scale;

  // Drag viewport rectangle to pan.
  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const startX = e.clientX;
    const startY = e.clientY;
    const startVpX = vp.x;
    const startVpY = vp.y;

    const onMouseMove = (ev: MouseEvent) => {
      const dx = (ev.clientX - startX) / scale;
      const dy = (ev.clientY - startY) / scale;
      onPan(startVpX - dx * vp.zoom, startVpY - dy * vp.zoom);
    };
    const onMouseUp = () => {
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    };
    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
  }, [vp.x, vp.y, vp.zoom, scale, onPan]);

  // Click on minimap to jump viewport center.
  const handleClick = useCallback((e: React.MouseEvent) => {
    const mmRect = minimapRef.current?.getBoundingClientRect();
    if (!mmRect) return;
    const clickX = e.clientX - mmRect.left;
    const clickY = e.clientY - mmRect.top;
    const worldClickX = clickX / scale + worldMinX;
    const worldClickY = clickY / scale + worldMinY;
    onPan(-(worldClickX - vpWorldW / 2) * vp.zoom, -(worldClickY - vpWorldH / 2) * vp.zoom);
  }, [scale, worldMinX, worldMinY, vpWorldW, vpWorldH, vp.zoom, onPan]);

  const zoomBtnStyle: React.CSSProperties = {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: "2px 6px",
    fontSize: 12,
    lineHeight: 1,
    fontWeight: 700,
  };

  return (
    <div style={{ position: "absolute", bottom: MINIMAP_PADDING, right: MINIMAP_PADDING, display: "flex", flexDirection: "column", alignItems: "flex-end", gap: 4 }}>
      {/* Minimap */}
      <div
        ref={minimapRef}
        onClick={handleClick}
        style={{
          width: mapW,
          height: mapH,
          backgroundColor: colors.isDark ? "rgba(0,0,0,0.6)" : "rgba(255,255,255,0.8)",
          border: `1px solid ${colors.border}`,
          borderRadius: 4,
          overflow: "hidden",
          cursor: "pointer",
          position: "relative",
        }}
      >
        {tiles.map((t) => {
          const pos = toMinimap(t.x, t.y);
          return (
            <div
              key={t.id}
              style={{
                position: "absolute",
                left: pos.x,
                top: pos.y,
                width: t.width * scale,
                height: t.height * scale,
                backgroundColor: PANEL_COLORS[t.panel] ?? colors.textDim,
                opacity: 0.6,
                borderRadius: 1,
              }}
            />
          );
        })}
        <div
          onMouseDown={handleMouseDown}
          onClick={(e) => e.stopPropagation()}
          style={{
            position: "absolute",
            left: vpMini.x,
            top: vpMini.y,
            width: vpMiniW,
            height: vpMiniH,
            border: `1.5px solid ${colors.active}`,
            backgroundColor: `${colors.active}22`,
            borderRadius: 2,
            cursor: "grab",
          }}
        />
      </div>
      {/* Zoom controls */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 2,
          backgroundColor: colors.isDark ? "rgba(0,0,0,0.6)" : "rgba(255,255,255,0.8)",
          border: `1px solid ${colors.border}`,
          borderRadius: 4,
          padding: "1px 2px",
        }}
      >
        <button
          onClick={(e) => { e.stopPropagation(); onZoom(Math.max(MIN_ZOOM, vp.zoom / 1.2)); }}
          title="Zoom out"
          style={zoomBtnStyle}
          onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
        >
          −
        </button>
        <span
          onClick={(e) => { e.stopPropagation(); onZoom(1); }}
          title="Reset zoom"
          style={{ fontSize: 9, color: colors.textDim, cursor: "pointer", padding: "0 4px", minWidth: 28, textAlign: "center" }}
        >
          {Math.round(vp.zoom * 100)}%
        </span>
        <button
          onClick={(e) => { e.stopPropagation(); onZoom(Math.min(MAX_ZOOM, vp.zoom * 1.2)); }}
          title="Zoom in"
          style={zoomBtnStyle}
          onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
        >
          +
        </button>
      </div>
    </div>
  );
}

/** Find a position that doesn't overlap existing tiles. Tries the given position
 *  first, then shifts right, then wraps below. */
function findNonOverlappingPosition(x: number, y: number, w: number, h: number, tiles: CanvasTileType[]): { x: number; y: number } {
  const GAP = 20;
  const overlaps = (px: number, py: number) =>
    tiles.some((t) => px < t.x + t.width + GAP && px + w + GAP > t.x && py < t.y + t.height + GAP && py + h + GAP > t.y);

  if (!overlaps(x, y)) return { x, y };

  // Try placing to the right of the rightmost tile.
  const maxRight = Math.max(...tiles.map((t) => t.x + t.width), 0);
  const rightPos = { x: maxRight + GAP, y };
  if (!overlaps(rightPos.x, rightPos.y)) return rightPos;

  // Try below the bottommost tile.
  const maxBottom = Math.max(...tiles.map((t) => t.y + t.height), 0);
  return { x, y: maxBottom + GAP };
}

/** Default tile sizes per panel type. Editor and Memory get more space. */
const DEFAULT_TILE_SIZES: Partial<Record<PanelType, { w: number; h: number }>> = {
  editor: { w: 900, h: 900 },
  memory: { w: 900, h: 900 },
  "docker-browser": { w: 700, h: 500 },
  "host-browser": { w: 700, h: 500 },
};
