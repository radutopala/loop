import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { colors } from "../theme";
import { Terminal } from "./Terminal";

const MIN_WIDTH = 280;
const MAX_WIDTH_PERCENT = 0.45;
const WIDTH_STORAGE_KEY = "loop-shell-panel-width";
const TREE_STORAGE_KEY = "loop-shell-tree";

// ── Split tree types ──

type SplitDirection = "vertical" | "horizontal";

interface LeafNode {
  type: "leaf";
  id: string;
  flex: number;
}

interface SplitNode {
  type: "split";
  direction: SplitDirection;
  children: PaneNode[];
  flex: number;
}

type PaneNode = LeafNode | SplitNode;

function makeLeaf(id: string, flex = 1): LeafNode {
  return { type: "leaf", id, flex };
}

let _nextId = 0;
function nextId(): string {
  return String(_nextId++);
}

function initId(tree: PaneNode) {
  const max = maxLeafId(tree);
  _nextId = max + 1;
}

function maxLeafId(node: PaneNode): number {
  if (node.type === "leaf") return parseInt(node.id, 10) || 0;
  return node.children.reduce((m, c) => Math.max(m, maxLeafId(c)), 0);
}

function leafCount(node: PaneNode): number {
  if (node.type === "leaf") return 1;
  return node.children.reduce((s, c) => s + leafCount(c), 0);
}

/** Remove a leaf by id. Returns null if the entire tree is removed. */
function removeLeaf(node: PaneNode, id: string): PaneNode | null {
  if (node.type === "leaf") {
    return node.id === id ? null : node;
  }
  const remaining = node.children
    .map((c) => removeLeaf(c, id))
    .filter((c): c is PaneNode => c !== null);
  if (remaining.length === 0) return null;
  if (remaining.length === 1) return remaining[0]!;
  return { ...node, children: remaining };
}

/** Split a leaf into two panes in the given direction. */
function splitLeaf(node: PaneNode, id: string, direction: SplitDirection): PaneNode {
  if (node.type === "leaf") {
    if (node.id !== id) return node;
    const newId = nextId();
    return {
      type: "split",
      direction,
      children: [makeLeaf(node.id), makeLeaf(newId)],
      flex: node.flex,
    };
  }
  return {
    ...node,
    children: node.children.map((c) => splitLeaf(c, id, direction)),
  };
}

/** Update flex values for children at a divider index inside a specific split node. */
function updateFlex(node: PaneNode, parentPath: number[], dividerIndex: number, newFlexA: number, newFlexB: number): PaneNode {
  if (parentPath.length === 0) {
    if (node.type !== "split") return node;
    return {
      ...node,
      children: node.children.map((c, i) => {
        if (i === dividerIndex) return { ...c, flex: newFlexA };
        if (i === dividerIndex + 1) return { ...c, flex: newFlexB };
        return c;
      }),
    };
  }
  if (node.type !== "split") return node;
  const [first, ...rest] = parentPath;
  return {
    ...node,
    children: node.children.map((c, i) =>
      i === first ? updateFlex(c, rest, dividerIndex, newFlexA, newFlexB) : c,
    ),
  };
}

// ── Persistence ──

function loadTree(channelId: string | null): PaneNode {
  if (!channelId) return makeLeaf("0");
  try {
    const stored = localStorage.getItem(TREE_STORAGE_KEY);
    if (stored) {
      const all = JSON.parse(stored);
      if (typeof all === "object" && all !== null && all[channelId]) {
        const tree = all[channelId] as PaneNode;
        initId(tree);
        return tree;
      }
    }
  } catch { /* ignore */ }
  _nextId = 1;
  return makeLeaf("0");
}

function saveTree(channelId: string, tree: PaneNode) {
  try {
    const stored = localStorage.getItem(TREE_STORAGE_KEY);
    const parsed = stored ? JSON.parse(stored) : null;
    const all: Record<string, PaneNode> = typeof parsed === "object" && parsed !== null ? parsed : {};
    all[channelId] = tree;
    localStorage.setItem(TREE_STORAGE_KEY, JSON.stringify(all));
  } catch { /* ignore */ }
}

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
  maximized?: boolean;
  sidebarOpen?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

export function ShellPanel({ channelId, maximized, sidebarOpen, onToggleSidebar, onOpenPalette, onToggleMaximize, onClose }: ShellPanelProps) {
  const [width, setWidth] = useState(loadWidth);
  const [resizing, setResizing] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);
  const [tree, setTree] = useState<PaneNode>(() => loadTree(channelId));
  const [splitMenu, setSplitMenu] = useState(false);
  const treeRef = useRef(tree);
  treeRef.current = tree;

  // Reload tree when channelId changes.
  const prevChannelRef = useRef(channelId);
  useEffect(() => {
    if (channelId !== prevChannelRef.current) {
      prevChannelRef.current = channelId;
      setTree(loadTree(channelId));
    }
  }, [channelId]);

  // Save tree whenever it changes.
  useEffect(() => {
    if (channelId) saveTree(channelId, tree);
  }, [channelId, tree]);

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
    setTree((prev) => {
      // Find the last leaf and split it.
      const lastLeaf = findLastLeaf(prev);
      if (!lastLeaf) return prev;
      return splitLeaf(prev, lastLeaf, direction);
    });
  }, []);

  const handleRemoveLeaf = useCallback(
    (id: string) => {
      setTree((prev) => {
        if (leafCount(prev) <= 1) {
          onClose();
          return prev;
        }
        return removeLeaf(prev, id) ?? makeLeaf("0");
      });
    },
    [onClose],
  );

  const handleUpdateFlex = useCallback((parentPath: number[], dividerIndex: number, flexA: number, flexB: number) => {
    setTree((prev) => updateFlex(prev, parentPath, dividerIndex, flexA, flexB));
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
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
          }}
        >
          Host Shell
        </span>
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
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
        <PaneTree
          node={tree}
          channelId={channelId}
          path={[]}
          showHeaders={leafCount(tree) > 1}
          onRemove={handleRemoveLeaf}
          onSplit={(id, dir) => setTree((prev) => splitLeaf(prev, id, dir))}
          onUpdateFlex={handleUpdateFlex}
          treeRef={treeRef}
        />
      </div>
    </div>
  );
}

// ── Split menu dropdown ──

function SplitMenu({ onSplit, onClose }: { onSplit: (dir: SplitDirection) => void; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [onClose]);

  return (
    <div
      ref={ref}
      style={{
        position: "absolute",
        top: "100%",
        right: 0,
        zIndex: 100,
        backgroundColor: colors.surface,
        border: `1px solid ${colors.border}`,
        borderRadius: 6,
        padding: 4,
        display: "flex",
        flexDirection: "column",
        gap: 2,
        minWidth: 130,
      }}
    >
      <button
        onClick={() => onSplit("vertical")}
        style={menuItemStyle}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <line x1="3" y1="12" x2="21" y2="12" />
        </svg>
        Split Down
      </button>
      <button
        onClick={() => onSplit("horizontal")}
        style={menuItemStyle}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <line x1="12" y1="3" x2="12" y2="21" />
        </svg>
        Split Right
      </button>
    </div>
  );
}

const menuItemStyle: React.CSSProperties = {
  background: "none",
  border: "none",
  color: colors.text,
  cursor: "pointer",
  padding: "4px 8px",
  fontSize: 12,
  borderRadius: 4,
  display: "flex",
  alignItems: "center",
  gap: 6,
  width: "100%",
  textAlign: "left",
};

// ── Recursive pane tree renderer ──

interface PaneTreeProps {
  node: PaneNode;
  channelId: string | null;
  path: number[];
  showHeaders: boolean;
  onRemove: (id: string) => void;
  onSplit: (id: string, dir: SplitDirection) => void;
  onUpdateFlex: (parentPath: number[], dividerIndex: number, flexA: number, flexB: number) => void;
  treeRef: React.RefObject<PaneNode>;
}

function PaneTree({ node, channelId, path, showHeaders, onRemove, onSplit, onUpdateFlex, treeRef }: PaneTreeProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  const handleDivider = useCallback(
    (e: React.MouseEvent, dividerIndex: number, direction: SplitDirection) => {
      e.preventDefault();
      const container = containerRef.current;
      if (!container) return;
      if (node.type !== "split") return;

      const startPos = direction === "vertical" ? e.clientY : e.clientX;
      const above = node.children[dividerIndex];
      const below = node.children[dividerIndex + 1];
      if (!above || !below) return;
      const aboveFlex = above.flex;
      const belowFlex = below.flex;
      const totalFlex = aboveFlex + belowFlex;
      const allFlex = node.children.reduce((s, c) => s + c.flex, 0);
      const rect = container.getBoundingClientRect();
      const containerSize = direction === "vertical" ? rect.height : rect.width;
      const pixelsPerFlex = containerSize / allFlex;

      const onMouseMove = (ev: MouseEvent) => {
        const currentPos = direction === "vertical" ? ev.clientY : ev.clientX;
        const delta = currentPos - startPos;
        const deltaFlex = delta / pixelsPerFlex;
        const newA = Math.max(0.15, Math.min(totalFlex - 0.15, aboveFlex + deltaFlex));
        const newB = totalFlex - newA;
        onUpdateFlex(path, dividerIndex, newA, newB);
      };

      const onMouseUp = () => {
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };

      document.body.style.cursor = direction === "vertical" ? "row-resize" : "col-resize";
      document.body.style.userSelect = "none";
      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [node, path, onUpdateFlex],
  );

  if (node.type === "leaf") {
    return (
      <div style={{ flex: node.flex, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, minWidth: 0 }}>
        {showHeaders && (
          <PaneHeader
            onRemove={() => onRemove(node.id)}
            onSplitDown={() => onSplit(node.id, "vertical")}
            onSplitRight={() => onSplit(node.id, "horizontal")}
          />
        )}
        <Terminal
          key={`shell-${channelId}-${node.id}`}
          channelId={channelId}
          target="host"
          instanceId={node.id}
        />
      </div>
    );
  }

  const isVertical = node.direction === "vertical";

  return (
    <div
      ref={containerRef}
      style={{
        flex: node.flex,
        display: "flex",
        flexDirection: isVertical ? "column" : "row",
        overflow: "hidden",
        minHeight: 0,
        minWidth: 0,
      }}
    >
      {node.children.map((child, i) => (
        <Fragment key={child.type === "leaf" ? child.id : `split-${i}`}>
          {i > 0 && (
            <div
              onMouseDown={(e) => handleDivider(e, i - 1, node.direction)}
              style={{
                [isVertical ? "height" : "width"]: 4,
                flexShrink: 0,
                cursor: isVertical ? "row-resize" : "col-resize",
                backgroundColor: colors.border,
                position: "relative",
              }}
            >
              <div
                style={{
                  position: "absolute",
                  left: "50%",
                  top: "50%",
                  transform: "translate(-50%, -50%)",
                  [isVertical ? "width" : "height"]: 24,
                  [isVertical ? "height" : "width"]: 2,
                  borderRadius: 1,
                  backgroundColor: colors.textDim,
                  opacity: 0.5,
                }}
              />
            </div>
          )}
          <PaneTree
            node={child}
            channelId={channelId}
            path={[...path, i]}
            showHeaders={showHeaders}
            onRemove={onRemove}
            onSplit={onSplit}
            onUpdateFlex={onUpdateFlex}
            treeRef={treeRef}
          />
        </Fragment>
      ))}
    </div>
  );
}

// ── Pane header with grip, split buttons, close ──

function PaneHeader({ onRemove, onSplitDown, onSplitRight }: {
  onRemove: () => void;
  onSplitDown: () => void;
  onSplitRight: () => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 4,
        padding: "2px 8px",
        backgroundColor: colors.surface,
        borderBottom: `1px solid ${colors.border}`,
        flexShrink: 0,
        height: 22,
      }}
    >
      <span style={{ fontSize: 10, color: colors.textDim, fontWeight: 500 }}>
        Shell
      </span>
      <div style={{ flex: 1 }} />
      <button
        onClick={onSplitDown}
        title="Split down"
        style={paneHeaderBtnStyle}
        onMouseEnter={hoverIn}
        onMouseLeave={hoverOut}
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <line x1="3" y1="12" x2="21" y2="12" />
        </svg>
      </button>
      <button
        onClick={onSplitRight}
        title="Split right"
        style={paneHeaderBtnStyle}
        onMouseEnter={hoverIn}
        onMouseLeave={hoverOut}
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <line x1="12" y1="3" x2="12" y2="21" />
        </svg>
      </button>
      <button
        onClick={onRemove}
        title="Close pane"
        style={paneHeaderBtnStyle}
        onMouseEnter={hoverIn}
        onMouseLeave={hoverOut}
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <line x1="18" y1="6" x2="6" y2="18" />
          <line x1="6" y1="6" x2="18" y2="18" />
        </svg>
      </button>
    </div>
  );
}

// ── Helpers ──

function findLastLeaf(node: PaneNode): string | null {
  if (node.type === "leaf") return node.id;
  for (let i = node.children.length - 1; i >= 0; i--) {
    const child = node.children[i];
    if (!child) continue;
    const found = findLastLeaf(child);
    if (found) return found;
  }
  return null;
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

const paneHeaderBtnStyle: React.CSSProperties = {
  background: "none",
  border: "none",
  color: colors.textDim,
  cursor: "pointer",
  padding: "0 2px",
  lineHeight: 1,
  fontSize: 12,
  display: "flex",
  alignItems: "center",
  borderRadius: 2,
};

function hoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
  e.currentTarget.style.color = colors.textLight;
}

function hoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
  e.currentTarget.style.color = colors.textDim;
}
