import { forwardRef, Fragment, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { SessionStatus, TerminalTarget } from "../types";
import { colors } from "../theme";
import { Terminal, getCloseForInstance } from "./Terminal";

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

/** Per-storageKey counters so multiple TerminalPanes instances don't collide. */
const idCounters = new Map<string, number>();

function nextId(storageKey: string): string {
  const n = idCounters.get(storageKey) ?? 0;
  idCounters.set(storageKey, n + 1);
  return String(n);
}

function initId(storageKey: string, tree: PaneNode) {
  const max = maxLeafId(tree);
  idCounters.set(storageKey, max + 1);
}

function maxLeafId(node: PaneNode): number {
  if (node.type === "leaf") return parseInt(node.id, 10) || 0;
  return node.children.reduce((m, c) => Math.max(m, maxLeafId(c)), 0);
}

function leafCount(node: PaneNode): number {
  if (node.type === "leaf") return 1;
  return node.children.reduce((s, c) => s + leafCount(c), 0);
}

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

function splitLeaf(node: PaneNode, id: string, direction: SplitDirection, storageKey: string): PaneNode {
  if (node.type === "leaf") {
    if (node.id !== id) return node;
    const newId = nextId(storageKey);
    return {
      type: "split",
      direction,
      children: [makeLeaf(node.id), makeLeaf(newId)],
      flex: node.flex,
    };
  }
  return {
    ...node,
    children: node.children.map((c) => splitLeaf(c, id, direction, storageKey)),
  };
}

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

// ── Persistence ──

function loadTree(storageKey: string, channelId: string | null): PaneNode {
  if (!channelId) return makeLeaf("0");
  try {
    const stored = localStorage.getItem(storageKey);
    if (stored) {
      const all = JSON.parse(stored);
      if (typeof all === "object" && all !== null && all[channelId]) {
        const tree = all[channelId] as PaneNode;
        initId(storageKey, tree);
        return tree;
      }
    }
  } catch { /* ignore */ }
  idCounters.set(storageKey, 1);
  return makeLeaf("0");
}

function saveTree(storageKey: string, channelId: string, tree: PaneNode) {
  try {
    const stored = localStorage.getItem(storageKey);
    const parsed = stored ? JSON.parse(stored) : null;
    const all: Record<string, PaneNode> = typeof parsed === "object" && parsed !== null ? parsed : {};
    all[channelId] = tree;
    localStorage.setItem(storageKey, JSON.stringify(all));
  } catch { /* ignore */ }
}

// ── Main component ──

interface TerminalPanesProps {
  channelId: string | null;
  target: TerminalTarget;
  storageKey: string;
  /** Bumping mountKey forces re-evaluation (e.g. after kill + re-click). */
  mountKey?: number;
  /** Hide Kill/Restart from individual terminal toolbars (parent provides these). */
  hideActions?: boolean;
  /** Incrementing this triggers sendKill on all terminals. */
  killSignal?: number;
  onStatusChange?: () => void;
  /** Reports whether any terminal pane is still alive (not completed/failed). */
  onRunningChange?: (anyRunning: boolean) => void;
  /** Called when the last pane is removed. */
  onEmpty?: () => void;
}

export interface TerminalPanesRef {
  splitLast: (dir: SplitDirection) => void;
}

export const TerminalPanes = forwardRef<TerminalPanesRef, TerminalPanesProps>(function TerminalPanes({ channelId, target, storageKey, mountKey, hideActions, killSignal, onStatusChange, onRunningChange, onEmpty }, ref) {
  const [tree, setTree] = useState<PaneNode>(() => loadTree(storageKey, channelId));
  const treeRef = useRef(tree);
  treeRef.current = tree;

  // Track per-pane session status so we can report aggregate running state.
  const statusMapRef = useRef(new Map<string, SessionStatus>());
  const onRunningChangeRef = useRef(onRunningChange);
  onRunningChangeRef.current = onRunningChange;

  const handlePaneStatus = useCallback((id: string, status: SessionStatus) => {
    statusMapRef.current.set(id, status);
    const allDead = statusMapRef.current.size > 0 &&
      [...statusMapRef.current.values()].every((s) => s === "completed" || s === "failed");
    onRunningChangeRef.current?.(!allDead);
  }, []);

  // Reload tree when channelId changes.
  const prevChannelRef = useRef(channelId);
  useEffect(() => {
    if (channelId !== prevChannelRef.current) {
      prevChannelRef.current = channelId;
      setTree(loadTree(storageKey, channelId));
    }
  }, [channelId, storageKey]);

  // Save tree whenever it changes.
  useEffect(() => {
    if (channelId) saveTree(storageKey, channelId, tree);
  }, [channelId, storageKey, tree]);

  const handleRemoveLeaf = useCallback(
    (id: string) => {
      // Close the exec session before removing the pane from the tree.
      const closeKey = `${target}:${channelId}:${id}`;
      getCloseForInstance(closeKey)?.();
      statusMapRef.current.delete(id);
      setTree((prev) => {
        if (leafCount(prev) <= 1) {
          onEmpty?.();
          return prev;
        }
        return removeLeaf(prev, id) ?? makeLeaf("0");
      });
    },
    [onEmpty, target, channelId],
  );

  const handleSplit = useCallback(
    (id: string, dir: SplitDirection) => {
      setTree((prev) => splitLeaf(prev, id, dir, storageKey));
    },
    [storageKey],
  );

  const handleSplitLast = useCallback(
    (dir: SplitDirection) => {
      setTree((prev) => {
        const last = findLastLeaf(prev);
        if (!last) return prev;
        return splitLeaf(prev, last, dir, storageKey);
      });
    },
    [storageKey],
  );

  const handleUpdateFlex = useCallback((parentPath: number[], dividerIndex: number, flexA: number, flexB: number) => {
    setTree((prev) => updateFlex(prev, parentPath, dividerIndex, flexA, flexB));
  }, []);

  useImperativeHandle(ref, () => ({ splitLast: handleSplitLast }), [handleSplitLast]);

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
      <PaneTree
        node={tree}
        channelId={channelId}
        target={target}
        mountKey={mountKey}
        hideActions={hideActions}
        killSignal={killSignal}
        path={[]}
        showHeaders={leafCount(tree) > 1}
        onStatusChange={onStatusChange}
        onPaneStatus={handlePaneStatus}
        onRemove={handleRemoveLeaf}
        onSplit={handleSplit}
        onUpdateFlex={handleUpdateFlex}
        treeRef={treeRef}
      />
    </div>
  );
});

/** Expose splitLast for external callers (e.g. panel header buttons). */
export { findLastLeaf, splitLeaf, leafCount, loadTree, saveTree };
export type { PaneNode, SplitDirection };

// ── Recursive pane tree renderer ──

interface PaneTreeProps {
  node: PaneNode;
  channelId: string | null;
  target: TerminalTarget;
  mountKey?: number;
  hideActions?: boolean;
  killSignal?: number;
  path: number[];
  showHeaders: boolean;
  onStatusChange?: () => void;
  onPaneStatus?: (id: string, status: SessionStatus) => void;
  onRemove: (id: string) => void;
  onSplit: (id: string, dir: SplitDirection) => void;
  onUpdateFlex: (parentPath: number[], dividerIndex: number, flexA: number, flexB: number) => void;
  treeRef: React.RefObject<PaneNode>;
}

function PaneTree({ node, channelId, target, mountKey, hideActions, killSignal, path, showHeaders, onStatusChange, onPaneStatus, onRemove, onSplit, onUpdateFlex, treeRef }: PaneTreeProps) {
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
    const label = target === "host" ? "Shell" : "Terminal";
    return (
      <div style={{ flex: node.flex, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, minWidth: 0 }}>
        {showHeaders && (
          <PaneHeader
            label={label}
            onRemove={() => onRemove(node.id)}
            onSplitDown={() => onSplit(node.id, "vertical")}
            onSplitRight={() => onSplit(node.id, "horizontal")}
          />
        )}
        <Terminal
          key={`${target}-${channelId}-${node.id}-${mountKey ?? 0}`}
          channelId={channelId}
          target={target}
          instanceId={node.id}
          hideActions={hideActions}
          killSignal={killSignal}
          onStatusChange={onStatusChange}
          onPaneStatus={onPaneStatus ? (status) => onPaneStatus(node.id, status) : undefined}
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
            target={target}
            mountKey={mountKey}
            hideActions={hideActions}
            killSignal={killSignal}
            path={[...path, i]}
            showHeaders={showHeaders}
            onStatusChange={onStatusChange}
            onPaneStatus={onPaneStatus}
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

// ── Pane header ──

function PaneHeader({ label, onRemove, onSplitDown, onSplitRight }: {
  label: string;
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
        {label}
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

// ── Split menu (reusable dropdown) ──

export function SplitMenu({ onSplit, onClose }: { onSplit: (dir: SplitDirection) => void; onClose: () => void }) {
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

// ── Styles ──

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

function hoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
  e.currentTarget.style.color = colors.textLight;
}

function hoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
  e.currentTarget.style.color = colors.textDim;
}
