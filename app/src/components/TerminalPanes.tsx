import { forwardRef, Fragment, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { SessionStatus, TerminalTarget } from "../types";
import { colors } from "../theme";
import { Terminal, getCloseForInstance } from "./Terminal";

// ── Global drag state ──
// Custom events let DropZoneOverlay know when a pane drag is in progress,
// so overlays can switch from pointer-events:none to pointer-events:auto.
const DRAG_START_EVENT = "pane-drag-start";
const DRAG_END_EVENT = "pane-drag-end";

function emitPaneDragStart() { document.dispatchEvent(new Event(DRAG_START_EVENT)); }
function emitPaneDragEnd() { document.dispatchEvent(new Event(DRAG_END_EVENT)); }

// ── Split tree types ──

type SplitDirection = "vertical" | "horizontal";
type DropPosition = "top" | "bottom" | "left" | "right" | "center";

interface LeafNode {
  type: "leaf";
  id: string;
  target: TerminalTarget;
  flex: number;
}

interface SplitNode {
  type: "split";
  direction: SplitDirection;
  children: PaneNode[];
  flex: number;
}

type PaneNode = LeafNode | SplitNode;

function makeLeaf(id: string, target: TerminalTarget = "agent", flex = 1): LeafNode {
  return { type: "leaf", id, target, flex };
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

function hasAgentLeaf(node: PaneNode): boolean {
  if (node.type === "leaf") return node.target === "agent";
  return node.children.some(hasAgentLeaf);
}

function findLeafById(node: PaneNode, id: string): LeafNode | null {
  if (node.type === "leaf") return node.id === id ? node : null;
  for (const child of node.children) {
    const found = findLeafById(child, id);
    if (found) return found;
  }
  return null;
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

function splitLeaf(node: PaneNode, id: string, direction: SplitDirection, storageKey: string, target?: TerminalTarget): PaneNode {
  if (node.type === "leaf") {
    if (node.id !== id) return node;
    const newId = nextId(storageKey);
    const newTarget = target ?? node.target;
    return {
      type: "split",
      direction,
      children: [{ ...node, flex: 1 }, makeLeaf(newId, newTarget)],
      flex: node.flex,
    };
  }
  return {
    ...node,
    children: node.children.map((c) => splitLeaf(c, id, direction, storageKey, target)),
  };
}

function changeLeafTarget(node: PaneNode, id: string, target: TerminalTarget): PaneNode {
  if (node.type === "leaf") {
    return node.id === id ? { ...node, target } : node;
  }
  return {
    ...node,
    children: node.children.map((c) => changeLeafTarget(c, id, target)),
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

function findLastLeaf(node: PaneNode): LeafNode | null {
  if (node.type === "leaf") return node;
  for (let i = node.children.length - 1; i >= 0; i--) {
    const child = node.children[i];
    if (!child) continue;
    const found = findLastLeaf(child);
    if (found) return found;
  }
  return null;
}

// ── Drag-and-drop tree operations ──

function swapLeavesInTree(tree: PaneNode, idA: string, idB: string): PaneNode {
  const leafA = findLeafById(tree, idA);
  const leafB = findLeafById(tree, idB);
  if (!leafA || !leafB) return tree;
  // Swap target and id by replacing in two passes
  const replaceLeaf = (node: PaneNode, targetId: string, replacement: LeafNode): PaneNode => {
    if (node.type === "leaf") {
      return node.id === targetId ? { ...replacement, flex: node.flex } : node;
    }
    return { ...node, children: node.children.map((c) => replaceLeaf(c, targetId, replacement)) };
  };
  // Use a temp ID to avoid collision
  const tempId = `__swap_temp_${Date.now()}`;
  let result = replaceLeaf(tree, idA, { ...leafA, id: tempId });
  result = replaceLeaf(result, idB, { ...leafB, id: idA, target: leafB.target });
  result = replaceLeaf(result, tempId, { ...leafA, id: idB, target: leafA.target });
  return result;
}

function moveLeaf(tree: PaneNode, dragId: string, dropId: string, position: Exclude<DropPosition, "center">): PaneNode {
  const dragLeaf = findLeafById(tree, dragId);
  if (!dragLeaf) return tree;
  // Remove the dragged leaf
  let result = removeLeaf(tree, dragId);
  if (!result) return tree;
  // Insert at target position
  const direction: SplitDirection = position === "top" || position === "bottom" ? "vertical" : "horizontal";
  const insertBefore = position === "top" || position === "left";
  const insertLeafAt = (node: PaneNode): PaneNode => {
    if (node.type === "leaf") {
      if (node.id !== dropId) return node;
      const children = insertBefore
        ? [makeLeaf(dragId, dragLeaf.target), { ...node, flex: 1 }]
        : [{ ...node, flex: 1 }, makeLeaf(dragId, dragLeaf.target)];
      return { type: "split", direction, children, flex: node.flex };
    }
    return { ...node, children: node.children.map((c) => insertLeafAt(c)) };
  };
  result = insertLeafAt(result);
  return result;
}

// ── Persistence ──

function migrateTree(tree: PaneNode): PaneNode {
  // Add `target` field to legacy leaf nodes that don't have it.
  if (tree.type === "leaf") {
    if (!tree.target) return { ...tree, target: "agent" };
    return tree;
  }
  return { ...tree, children: tree.children.map(migrateTree) };
}

function loadTree(storageKey: string, channelId: string | null): PaneNode | null {
  if (!channelId) return null;
  try {
    const stored = localStorage.getItem(storageKey);
    if (stored) {
      const all = JSON.parse(stored);
      if (typeof all === "object" && all !== null && all[channelId]) {
        const tree = migrateTree(all[channelId] as PaneNode);
        initId(storageKey, tree);
        return tree;
      }
    }
  } catch { /* ignore */ }
  return null;
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

type AgentState = "running" | "stopped" | "none";

interface TerminalPanesProps {
  channelId: string | null;
  storageKey: string;
  onStatusChange?: () => void;
  /** Reports aggregate agent pane state: "running" (alive), "stopped" (all dead), "none" (no agent panes). */
  onAgentStateChange?: (state: AgentState) => void;
  /** Called when the last agent pane is removed from the tree. */
  onLastAgentRemoved?: () => void;
}

export interface TerminalPanesRef {
  splitLast: (dir: SplitDirection, target?: TerminalTarget) => void;
  addPane: (target: TerminalTarget, dir: SplitDirection) => void;
}

export const TerminalPanes = forwardRef<TerminalPanesRef, TerminalPanesProps>(function TerminalPanes({ channelId, storageKey, onStatusChange, onAgentStateChange, onLastAgentRemoved }, ref) {
  const [tree, setTree] = useState<PaneNode | null>(() => loadTree(storageKey, channelId));
  const treeRef = useRef(tree);
  treeRef.current = tree;

  // Track per-pane session status so we can report aggregate running state.
  const statusMapRef = useRef(new Map<string, SessionStatus>());
  const onAgentStateChangeRef = useRef(onAgentStateChange);
  onAgentStateChangeRef.current = onAgentStateChange;
  const onLastAgentRemovedRef = useRef(onLastAgentRemoved);
  onLastAgentRemovedRef.current = onLastAgentRemoved;

  const computeAgentState = useCallback((): AgentState => {
    const current = treeRef.current;
    if (!current || !hasAgentLeaf(current)) return "none";
    const agentStatuses = [...statusMapRef.current.entries()]
      .filter(([leafId]) => findLeafById(current, leafId)?.target === "agent");
    if (agentStatuses.length === 0) return "running"; // agent leaves exist but no status yet → treat as running
    const allDead = agentStatuses.every(([, s]) => s === "completed" || s === "failed");
    return allDead ? "stopped" : "running";
  }, []);

  const handlePaneStatus = useCallback((id: string, status: SessionStatus) => {
    statusMapRef.current.set(id, status);
    onAgentStateChangeRef.current?.(computeAgentState());
  }, [computeAgentState]);

  // Reload tree when channelId changes.
  const prevChannelRef = useRef(channelId);
  useEffect(() => {
    if (channelId !== prevChannelRef.current) {
      prevChannelRef.current = channelId;
      statusMapRef.current.clear();
      setTree(loadTree(storageKey, channelId));
    }
  }, [channelId, storageKey]);

  // Save tree whenever it changes.
  useEffect(() => {
    if (channelId && tree) saveTree(storageKey, channelId, tree);
  }, [channelId, storageKey, tree]);

  const startPane = useCallback(
    (target: TerminalTarget) => {
      idCounters.set(storageKey, 1);
      setTree(makeLeaf("0", target));
    },
    [storageKey],
  );

  const handleRemoveLeaf = useCallback(
    (id: string) => {
      const current = treeRef.current;
      if (!current) return;
      const leaf = findLeafById(current, id);
      const wasAgent = leaf?.target === "agent";
      const target = leaf?.target ?? "agent";
      const closeKey = `${target}:${channelId}:${id}`;
      getCloseForInstance(closeKey)?.();
      statusMapRef.current.delete(id);
      setTree((prev) => {
        if (!prev) return prev;
        if (leafCount(prev) <= 1) {
          // Last pane — return to picker.
          if (channelId) {
            try {
              const stored = localStorage.getItem(storageKey);
              const all: Record<string, unknown> = stored ? JSON.parse(stored) : {};
              delete all[channelId];
              localStorage.setItem(storageKey, JSON.stringify(all));
            } catch { /* ignore */ }
          }
          if (wasAgent) onLastAgentRemovedRef.current?.();
          onAgentStateChangeRef.current?.("none");
          return null;
        }
        const newTree = removeLeaf(prev, id) ?? makeLeaf("0");
        // If the removed leaf was an agent and no agent leaves remain, fire callback.
        if (wasAgent && !hasAgentLeaf(newTree)) {
          onLastAgentRemovedRef.current?.();
          onAgentStateChangeRef.current?.("none");
        }
        return newTree;
      });
    },
    [channelId, storageKey],
  );

  const handleChangeTarget = useCallback(
    (id: string, target: TerminalTarget) => {
      // Close existing session before switching target type.
      const current = treeRef.current;
      if (!current) return;
      const leaf = findLeafById(current, id);
      if (leaf) {
        const closeKey = `${leaf.target}:${channelId}:${id}`;
        getCloseForInstance(closeKey)?.();
      }
      statusMapRef.current.delete(id);
      setTree((prev) => {
        if (!prev) return prev;
        const newTree = changeLeafTarget(prev, id, target);
        // If switching away from agent and no agent leaves remain, fire callback.
        if (leaf?.target === "agent" && !hasAgentLeaf(newTree)) {
          onLastAgentRemovedRef.current?.();
        }
        return newTree;
      });
      // Defer state report to after tree update.
      setTimeout(() => onAgentStateChangeRef.current?.(computeAgentState()), 0);
    },
    [channelId, computeAgentState],
  );

  const handleSplit = useCallback(
    (id: string, dir: SplitDirection, target?: TerminalTarget) => {
      setTree((prev) => prev ? splitLeaf(prev, id, dir, storageKey, target) : prev);
    },
    [storageKey],
  );

  const handleSplitLast = useCallback(
    (dir: SplitDirection, target?: TerminalTarget) => {
      setTree((prev) => {
        if (!prev) {
          // No tree yet — create the first leaf.
          idCounters.set(storageKey, 1);
          return makeLeaf("0", target ?? "host");
        }
        const last = findLastLeaf(prev);
        if (!last) return prev;
        return splitLeaf(prev, last.id, dir, storageKey, target);
      });
    },
    [storageKey],
  );

  const handleAddPane = useCallback(
    (target: TerminalTarget, dir: SplitDirection) => {
      setTree((prev) => {
        if (!prev) {
          idCounters.set(storageKey, 1);
          return makeLeaf("0", target);
        }
        const last = findLastLeaf(prev);
        if (!last) return prev;
        return splitLeaf(prev, last.id, dir, storageKey, target);
      });
    },
    [storageKey],
  );

  const handleDrop = useCallback(
    (dragId: string, dropId: string, position: DropPosition) => {
      if (dragId === dropId) return;
      setTree((prev) => {
        if (!prev) return prev;
        if (position === "center") {
          return swapLeavesInTree(prev, dragId, dropId);
        }
        return moveLeaf(prev, dragId, dropId, position);
      });
    },
    [],
  );

  const handleUpdateFlex = useCallback((parentPath: number[], dividerIndex: number, flexA: number, flexB: number) => {
    setTree((prev) => prev ? updateFlex(prev, parentPath, dividerIndex, flexA, flexB) : prev);
  }, []);

  useImperativeHandle(ref, () => ({ splitLast: handleSplitLast, addPane: handleAddPane }), [handleSplitLast, handleAddPane]);

  if (!tree) {
    return (
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
        <PanePicker onPick={startPane} />
      </div>
    );
  }

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
      <PaneTree
        node={tree}
        channelId={channelId}
        path={[]}
        showHeaders
        onStatusChange={onStatusChange}
        onPaneStatus={handlePaneStatus}
        onRemove={handleRemoveLeaf}
        onChangeTarget={handleChangeTarget}
        onSplit={handleSplit}
        onDrop={handleDrop}
        onUpdateFlex={handleUpdateFlex}
        treeRef={treeRef}
      />
    </div>
  );
});

/** Expose splitLast for external callers (e.g. panel header buttons). */
export { findLastLeaf, splitLeaf, leafCount, loadTree, saveTree, findLeafById };
export type { PaneNode, SplitDirection, LeafNode };

// ── Recursive pane tree renderer ──

interface PaneTreeProps {
  node: PaneNode;
  channelId: string | null;
  path: number[];
  showHeaders: boolean;
  onStatusChange?: () => void;
  onPaneStatus?: (id: string, status: SessionStatus) => void;
  onRemove: (id: string) => void;
  onChangeTarget: (id: string, target: TerminalTarget) => void;
  onSplit: (id: string, dir: SplitDirection, target?: TerminalTarget) => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
  onUpdateFlex: (parentPath: number[], dividerIndex: number, flexA: number, flexB: number) => void;
  treeRef: React.RefObject<PaneNode | null>;
}

function PaneTree({ node, channelId, path, showHeaders, onStatusChange, onPaneStatus, onRemove, onChangeTarget, onSplit, onDrop, onUpdateFlex, treeRef }: PaneTreeProps) {
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
    const isAgent = node.target === "agent";
    const label = isAgent ? "Agent" : "Shell";
    return (
      <div style={{ flex: node.flex, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, minWidth: 0, position: "relative" }}>
        {showHeaders && (
          <PaneHeader
            label={label}
            target={node.target}
            leafId={node.id}
            onToggleTarget={() => onChangeTarget(node.id, isAgent ? "host" : "agent")}
            onRemove={() => onRemove(node.id)}
            onSplit={(dir, t) => onSplit(node.id, dir, t)}
            onDrop={onDrop}
          />
        )}
        <DropZoneOverlay leafId={node.id} onDrop={onDrop} showHeaders={showHeaders} />
        <Terminal
          key={`${node.target}-${channelId}-${node.id}`}
          channelId={channelId}
          target={node.target}
          instanceId={node.id}
          hideActions={isAgent}
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
            path={[...path, i]}
            showHeaders={showHeaders}
            onStatusChange={onStatusChange}
            onPaneStatus={onPaneStatus}
            onRemove={onRemove}
            onChangeTarget={onChangeTarget}
            onSplit={onSplit}
            onDrop={onDrop}
            onUpdateFlex={onUpdateFlex}
            treeRef={treeRef}
          />
        </Fragment>
      ))}
    </div>
  );
}

// ── Drop zone overlay ──

function DropZoneOverlay({ leafId, onDrop, showHeaders }: {
  leafId: string;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
  showHeaders: boolean;
}) {
  const [activeZone, setActiveZone] = useState<DropPosition | null>(null);
  const [isDragOver, setIsDragOver] = useState(false);
  // Track whether ANY pane is being dragged (global drag state).
  const [isDragging, setIsDragging] = useState(false);

  useEffect(() => {
    const onStart = () => setIsDragging(true);
    const onEnd = () => { setIsDragging(false); setIsDragOver(false); setActiveZone(null); };
    document.addEventListener(DRAG_START_EVENT, onStart);
    document.addEventListener(DRAG_END_EVENT, onEnd);
    return () => {
      document.removeEventListener(DRAG_START_EVENT, onStart);
      document.removeEventListener(DRAG_END_EVENT, onEnd);
    };
  }, []);

  const getDropPosition = useCallback((e: React.DragEvent<HTMLDivElement>): DropPosition => {
    const rect = e.currentTarget.getBoundingClientRect();
    const x = (e.clientX - rect.left) / rect.width;
    const y = (e.clientY - rect.top) / rect.height;

    // Edge zones: 25% from each edge
    if (y < 0.25) return "top";
    if (y > 0.75) return "bottom";
    if (x < 0.25) return "left";
    if (x > 0.75) return "right";
    return "center";
  }, []);

  const handleDragOver = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    setIsDragOver(true);
    setActiveZone(getDropPosition(e));
  }, [getDropPosition]);

  const handleDragLeave = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    // Only reset if we're truly leaving the overlay (not entering a child).
    if (e.currentTarget.contains(e.relatedTarget as Node)) return;
    setIsDragOver(false);
    setActiveZone(null);
  }, []);

  const handleDrop = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    const dragId = e.dataTransfer.getData("text/plain");
    if (dragId && dragId !== leafId) {
      onDrop(dragId, leafId, getDropPosition(e));
    }
    setIsDragOver(false);
    setActiveZone(null);
  }, [leafId, onDrop, getDropPosition]);

  if (!showHeaders) return null;

  // The overlay is only interactive during a drag operation.
  const active = isDragging;

  return (
    <div
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      style={{
        position: "absolute",
        top: 22, // below PaneHeader
        left: 0,
        right: 0,
        bottom: 0,
        zIndex: active ? 10 : -1,
        pointerEvents: active ? "auto" : "none",
      }}
    >
      {isDragOver && activeZone && (
        <div style={{
          position: "absolute",
          ...(activeZone === "top" ? { top: 0, left: 0, right: 0, height: "50%" } :
            activeZone === "bottom" ? { bottom: 0, left: 0, right: 0, height: "50%" } :
            activeZone === "left" ? { top: 0, left: 0, bottom: 0, width: "50%" } :
            activeZone === "right" ? { top: 0, right: 0, bottom: 0, width: "50%" } :
            { top: 0, left: 0, right: 0, bottom: 0 }),
          backgroundColor: "rgba(96, 165, 250, 0.15)",
          border: "2px solid rgba(96, 165, 250, 0.5)",
          borderRadius: 4,
          pointerEvents: "none",
        }} />
      )}
    </div>
  );
}

// ── Pane header ──

function PaneHeader({ label, target, leafId, onToggleTarget, onRemove, onSplit, onDrop }: {
  label: string;
  target: TerminalTarget;
  leafId: string;
  onToggleTarget: () => void;
  onRemove: () => void;
  onSplit: (dir: SplitDirection, target: TerminalTarget) => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
}) {
  const handleDragStart = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    e.dataTransfer.setData("text/plain", leafId);
    e.dataTransfer.effectAllowed = "move";
    emitPaneDragStart();
  }, [leafId]);

  const handleDragEnd = useCallback(() => {
    emitPaneDragEnd();
  }, []);

  const handleDragOverHeader = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  }, []);

  const handleDropOnHeader = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    const dragId = e.dataTransfer.getData("text/plain");
    if (dragId && dragId !== leafId) {
      onDrop(dragId, leafId, "center");
    }
  }, [leafId, onDrop]);

  const [splitMenu, setSplitMenu] = useState(false);

  return (
    <div
      draggable
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragOver={handleDragOverHeader}
      onDrop={handleDropOnHeader}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 4,
        padding: "2px 8px",
        backgroundColor: colors.surface,
        borderBottom: `1px solid ${colors.border}`,
        flexShrink: 0,
        height: 22,
        cursor: "grab",
      }}
    >
      <button
        onClick={onToggleTarget}
        title={`Switch to ${target === "agent" ? "Shell" : "Agent"}`}
        style={{
          background: "none",
          border: "none",
          padding: "1px 4px",
          borderRadius: 3,
          fontSize: 10,
          fontWeight: 500,
          cursor: "pointer",
          color: target === "agent" ? colors.active : colors.textDim,
          backgroundColor: target === "agent" ? "rgba(96, 165, 250, 0.1)" : "rgba(255,255,255,0.05)",
        }}
        onMouseEnter={(e) => { e.currentTarget.style.opacity = "0.8"; }}
        onMouseLeave={(e) => { e.currentTarget.style.opacity = "1"; }}
      >
        {label}
      </button>
      <div style={{ flex: 1 }} />
      <div style={{ position: "relative" }}>
        <button
          onClick={() => setSplitMenu((v) => !v)}
          title="Split pane"
          style={paneHeaderBtnStyle}
          onMouseEnter={hoverIn}
          onMouseLeave={hoverOut}
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
        </button>
        {splitMenu && (
          <PaneSplitMenu
            onSplit={(dir, t) => { setSplitMenu(false); onSplit(dir, t); }}
            onClose={() => setSplitMenu(false)}
          />
        )}
      </div>
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

function PaneSplitMenu({ onSplit, onClose }: {
  onSplit: (dir: SplitDirection, target: TerminalTarget) => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [onClose]);

  const itemStyle: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: 6,
    padding: "4px 10px",
    background: "none",
    border: "none",
    color: colors.text,
    cursor: "pointer",
    fontSize: 11,
    width: "100%",
    textAlign: "left",
    borderRadius: 3,
  };

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
        minWidth: 140,
        boxShadow: "0 4px 12px rgba(0,0,0,0.3)",
      }}
    >
      <div style={{ fontSize: 9, color: colors.textDim, padding: "2px 10px", textTransform: "uppercase", letterSpacing: 0.5 }}>Agent Terminal</div>
      <button style={itemStyle} onClick={() => onSplit("vertical", "agent")} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="3" y1="12" x2="21" y2="12" /></svg>
        Split Down
      </button>
      <button style={itemStyle} onClick={() => onSplit("horizontal", "agent")} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="12" y1="3" x2="12" y2="21" /></svg>
        Split Right
      </button>
      <div style={{ height: 1, backgroundColor: colors.border, margin: "4px 0" }} />
      <div style={{ fontSize: 9, color: colors.textDim, padding: "2px 10px", textTransform: "uppercase", letterSpacing: 0.5 }}>Host Shell</div>
      <button style={itemStyle} onClick={() => onSplit("vertical", "host")} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="3" y1="12" x2="21" y2="12" /></svg>
        Split Down
      </button>
      <button style={itemStyle} onClick={() => onSplit("horizontal", "host")} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="12" y1="3" x2="12" y2="21" /></svg>
        Split Right
      </button>
    </div>
  );
}

// ── Pane picker (initial empty state) ──

function PanePicker({ onPick }: { onPick: (target: TerminalTarget) => void }) {
  return (
    <div style={{
      flex: 1,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      gap: 16,
      padding: 24,
    }}>
      <button
        onClick={() => onPick("agent")}
        style={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          gap: 8,
          padding: "20px 28px",
          borderRadius: 8,
          border: `1px solid ${colors.border}`,
          backgroundColor: colors.surface,
          color: colors.text,
          cursor: "pointer",
          fontSize: 13,
          fontWeight: 500,
          minWidth: 140,
        }}
        onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.active; e.currentTarget.style.backgroundColor = colors.hoverBg; }}
        onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; e.currentTarget.style.backgroundColor = colors.surface; }}
      >
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke={colors.active} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <rect x="4" y="4" width="16" height="16" rx="2" />
          <path d="M9 9h6M9 13h4" />
        </svg>
        Agent Terminal
        <span style={{ fontSize: 10, color: colors.textDim, fontWeight: 400 }}>Docker container</span>
      </button>
      <button
        onClick={() => onPick("host")}
        style={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          gap: 8,
          padding: "20px 28px",
          borderRadius: 8,
          border: `1px solid ${colors.border}`,
          backgroundColor: colors.surface,
          color: colors.text,
          cursor: "pointer",
          fontSize: 13,
          fontWeight: 500,
          minWidth: 140,
        }}
        onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.active; e.currentTarget.style.backgroundColor = colors.hoverBg; }}
        onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; e.currentTarget.style.backgroundColor = colors.surface; }}
      >
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke={colors.active} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="4 17 10 11 4 5" />
          <line x1="12" y1="19" x2="20" y2="19" />
        </svg>
        Host Shell
        <span style={{ fontSize: 10, color: colors.textDim, fontWeight: 400 }}>Local terminal</span>
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

function hoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
  e.currentTarget.style.color = colors.textLight;
}

function hoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
  e.currentTarget.style.color = colors.textDim;
}
