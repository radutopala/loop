import { LoopInfinityIcon } from "../components/LoopInfinityIcon";
import { SIDE_VIEW_LABEL, type SideView } from "../components/layout/LearnSplit";
import { useTheme } from "../ThemeContext";
import { MIN_HEIGHT, MIN_WIDTH } from "./CanvasTile";
import type { CanvasTile as CanvasTileType } from "./types";

// World units between the chat's tile and the Learn pane docked to it.
export const LEARN_DOCK_GAP = 24;

interface CanvasLearnDockProps {
  /** The chat's tile, which the Learn pane docks to. */
  chat: CanvasTileType;
  zoom: number;
  /** Fades the dock in; false fades it out, and the parent unmounts it once
   * that ends. */
  shown: boolean;
  /** Animation length in ms; 0 skips it. */
  ms: number;
  /** The docked pane: Learn's, or Explain's. */
  view?: SideView;
  /** A learn pass (or an explanation) is running: the logo on the seam
   * animates. */
  running: boolean;
  pane: React.ReactNode;
  onMove: (id: string, dx: number, dy: number) => void;
  onResize: (id: string, width: number, height: number) => void;
  onBringToFront: (id: string) => void;
  ref?: React.Ref<HTMLDivElement>;
}

/**
 * The Learn view on a canvas: the Learn pane docked to the right of the
 * chat's tile, the same size, joined to it by the Loop logo on the seam. The
 * two go together: dragging the Learn pane's header moves the chat's tile,
 * the dock's corner resizes both, and a click in it brings both to the front.
 * The dock isn't a tile of the canvas: it isn't saved with it, and goes when
 * the view closes.
 */
export function CanvasLearnDock({ chat, zoom, shown, ms, view = "learn", running, pane, onMove, onResize, onBringToFront, ref }: CanvasLearnDockProps) {
  const { colors } = useTheme();
  const fade: React.CSSProperties = { opacity: shown ? 1 : 0, transition: ms ? `opacity ${ms}ms ease` : "none" };

  const handleMouseDown = (e: React.MouseEvent) => {
    onBringToFront(chat.id);
    const target = e.target as HTMLElement;
    if (e.button !== 0 || !target.closest("[data-learn-pane-header]") || target.closest("button")) return;
    e.preventDefault();
    trackMouse(e, zoom, (dx, dy) => onMove(chat.id, dx, dy));
  };

  // The corner moves with the pair's right edge, which a width change moves
  // twice (the dock's own width, and its left edge with the chat's).
  const handleResizeStart = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    onBringToFront(chat.id);
    const { width, height } = chat;
    let dw = 0;
    let dh = 0;
    trackMouse(e, zoom, (dx, dy) => {
      dw += dx;
      dh += dy;
      onResize(chat.id, Math.max(MIN_WIDTH, width + dw / 2), Math.max(MIN_HEIGHT, height + dh));
    });
  };

  return (
    <>
      <div
        ref={ref}
        data-canvas-tile
        data-testid="canvas-learn-dock"
        data-view={view}
        role="region"
        aria-label={SIDE_VIEW_LABEL[view]}
        onMouseDown={handleMouseDown}
        style={{
          position: "absolute",
          left: chat.x + chat.width + LEARN_DOCK_GAP,
          top: chat.y,
          width: chat.width,
          height: chat.height,
          zIndex: chat.zIndex,
          display: "flex",
          flexDirection: "column",
          border: `1px solid ${colors.border}`,
          borderRadius: 6,
          overflow: "hidden",
          backgroundColor: colors.bg,
          boxShadow: "0 2px 8px rgba(0,0,0,0.15)",
          ...fade,
        }}
      >
        {pane}
        <div onMouseDown={handleResizeStart} style={{ position: "absolute", right: 0, bottom: 0, width: 12, height: 12, cursor: "nwse-resize" }} />
      </div>
      <div
        data-testid="canvas-learn-dock-logo"
        style={{
          position: "absolute",
          left: chat.x + chat.width + LEARN_DOCK_GAP / 2,
          top: chat.y + chat.height / 2,
          width: 40,
          height: 40,
          transform: "translate(-50%, -50%)",
          zIndex: chat.zIndex,
          ...fade,
          borderRadius: "50%",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: colors.sidebar,
          border: `1px solid ${colors.border}`,
          boxShadow: "0 2px 12px rgba(0, 0, 0, 0.35)",
          pointerEvents: "none",
        }}
      >
        <div style={{ zoom: 1.5, display: "flex" }}>
          <LoopInfinityIcon color={running ? undefined : colors.textDim} animated={running} isDark={colors.isDark} />
        </div>
      </div>
    </>
  );
}

/** Follows the mouse from a mousedown until it's released, reporting each
 * move in world units (screen pixels over zoom). */
export function trackMouse(start: { clientX: number; clientY: number }, zoom: number, onDelta: (dx: number, dy: number) => void): void {
  let lastX = start.clientX;
  let lastY = start.clientY;
  const onMouseMove = (ev: MouseEvent) => {
    onDelta((ev.clientX - lastX) / zoom, (ev.clientY - lastY) / zoom);
    lastX = ev.clientX;
    lastY = ev.clientY;
  };
  const onMouseUp = () => {
    document.removeEventListener("mousemove", onMouseMove);
    document.removeEventListener("mouseup", onMouseUp);
  };
  document.addEventListener("mousemove", onMouseMove);
  document.addEventListener("mouseup", onMouseUp);
}
