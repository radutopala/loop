import { Fragment, useCallback, useRef } from "react";
import type { AgentInfo } from "../hooks/useAgentRegistry";
import type { ContainerStatsByType } from "../hooks/useContainerStats";
import { useTheme } from "../ThemeContext";
import type { PanelType } from "../types/panels";
import { DropZoneOverlay } from "./DropZoneOverlay";
import { PaneLeafHeader } from "./PaneLeafHeader";
import { collectPanelTypes } from "./treeOps";
import type { DropPosition, LeafNode, PaneNode, SplitDirection } from "./types";

const HEADER_HEIGHT = 22;

/** Check if every leaf in a subtree is minimized. */
function isSubtreeMinimized(node: PaneNode, minimizedLeaves?: Set<string>): boolean {
  if (node.type === "leaf") return minimizedLeaves?.has(node.id) ?? false;
  return node.children.every((child) => isSubtreeMinimized(child, minimizedLeaves));
}

interface SplitPaneLayoutProps {
  tree: PaneNode;
  renderLeaf: (leaf: LeafNode) => React.ReactNode;
  agentInfoMap?: Map<string, AgentInfo>;
  containerStats?: ContainerStatsByType;
  minimizedLeaves?: Set<string>;
  onUpdateFlex: (parentPath: number[], dividerIdx: number, flexA: number, flexB: number) => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
  onRemoveLeaf: (id: string) => void;
  onSplitLeaf: (leafId: string, panel: PanelType, direction: SplitDirection, meta?: { openMode?: import("../types/panels").AgentOpenMode }) => void;
  onMaximize?: (leafId: string) => void;
  onToggleMinimize?: (leafId: string) => void;
  hiddenPanels?: PanelType[];
}

export function SplitPaneLayout({
  tree,
  renderLeaf,
  agentInfoMap,
  containerStats,
  minimizedLeaves,
  onUpdateFlex,
  onDrop,
  onRemoveLeaf,
  onSplitLeaf,
  onMaximize,
  onToggleMinimize,
  hiddenPanels,
}: SplitPaneLayoutProps) {
  const usedSingletons = collectPanelTypes(tree);
  return (
    <PaneTree
      node={tree}
      path={[]}
      usedSingletons={usedSingletons}
      renderLeaf={renderLeaf}
      agentInfoMap={agentInfoMap}
      containerStats={containerStats}
      minimizedLeaves={minimizedLeaves}
      onUpdateFlex={onUpdateFlex}
      onDrop={onDrop}
      onRemoveLeaf={onRemoveLeaf}
      onSplitLeaf={onSplitLeaf}
      onMaximize={onMaximize}
      onToggleMinimize={onToggleMinimize}
      hiddenPanels={hiddenPanels}
    />
  );
}

interface PaneTreeProps {
  node: PaneNode;
  path: number[];
  usedSingletons: Set<PanelType>;
  renderLeaf: (leaf: LeafNode) => React.ReactNode;
  agentInfoMap?: Map<string, AgentInfo>;
  containerStats?: ContainerStatsByType;
  minimizedLeaves?: Set<string>;
  /** Override node.flex — used by parent splits to scale flex when minimized siblings cause sum < 1. */
  flexOverride?: number;
  /** Parent split direction — controls whether an all-minimized subtree can collapse its flex. */
  parentDirection?: "vertical" | "horizontal";
  onUpdateFlex: (parentPath: number[], dividerIdx: number, flexA: number, flexB: number) => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
  onRemoveLeaf: (id: string) => void;
  onSplitLeaf: (leafId: string, panel: PanelType, direction: SplitDirection, meta?: { openMode?: import("../types/panels").AgentOpenMode }) => void;
  onMaximize?: (leafId: string) => void;
  onToggleMinimize?: (leafId: string) => void;
  hiddenPanels?: PanelType[];
}

function PaneTree({
  node,
  path,
  usedSingletons,
  renderLeaf,
  agentInfoMap,
  containerStats,
  minimizedLeaves,
  flexOverride,
  parentDirection,
  onUpdateFlex,
  onDrop,
  onRemoveLeaf,
  onSplitLeaf,
  onMaximize,
  onToggleMinimize,
  hiddenPanels,
}: PaneTreeProps) {
  const { colors } = useTheme();
  const containerRef = useRef<HTMLDivElement>(null);

  const handleDivider = useCallback(
    (e: React.MouseEvent, dividerIndex: number, direction: "vertical" | "horizontal") => {
      e.preventDefault();
      const container = containerRef.current;
      if (!container || node.type !== "split") return;

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
        document.querySelectorAll("iframe").forEach((f) => ((f as HTMLElement).style.pointerEvents = ""));
      };

      document.body.style.cursor = direction === "vertical" ? "row-resize" : "col-resize";
      document.body.style.userSelect = "none";
      // Prevent iframes from capturing mouse events during divider drag.
      document.querySelectorAll("iframe").forEach((f) => ((f as HTMLElement).style.pointerEvents = "none"));
      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [node, path, onUpdateFlex],
  );

  if (node.type === "leaf") {
    const isMinimized = minimizedLeaves?.has(node.id) ?? false;
    const effectiveFlex = flexOverride ?? node.flex;
    return (
      <div
        style={{
          flex: isMinimized ? "0 0 auto" : `${effectiveFlex} 1 0%`,
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          minHeight: 0,
          minWidth: 0,
          position: "relative",
          borderRadius: colors.islandRadius,
          boxShadow: colors.islandShadow,
          border: colors.islandBorder,
          backgroundColor: colors.sidebar,
        }}
      >
        <PaneLeafHeader
          leafId={node.id}
          panel={node.panel}
          usedSingletons={usedSingletons}
          isMinimized={isMinimized}
          hiddenPanels={hiddenPanels}
          agentInfo={node.panel === "docker-agent" ? agentInfoMap?.get(node.id) : undefined}
          containerStats={containerStats}
          onRemove={() => onRemoveLeaf(node.id)}
          onDrop={onDrop}
          onSplitLeaf={onSplitLeaf}
          onToggleMaximize={onMaximize ? () => onMaximize(node.id) : undefined}
          onToggleMinimize={onToggleMinimize ? () => onToggleMinimize(node.id) : undefined}
        />
        <DropZoneOverlay leafId={node.id} headerHeight={HEADER_HEIGHT} onDrop={onDrop} />
        {!isMinimized && <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>{renderLeaf(node)}</div>}
      </div>
    );
  }

  const isVertical = node.direction === "vertical";

  // CSS spec: when sum of flex-grow < 1, only that fraction of free space is distributed.
  // Scale non-minimized children's flex so their sum >= 1 to avoid wasted space.
  // Uses recursive check so collapsed split subtrees also count as minimized.
  const childFlexes = node.children.map((child) => {
    return isSubtreeMinimized(child, minimizedLeaves) ? 0 : child.flex;
  });
  const totalActiveFlex = childFlexes.reduce((s, f) => s + f, 0);
  const flexScale = totalActiveFlex > 0 && totalActiveFlex < 1 ? 1 / totalActiveFlex : 1;

  // If every child in this split is minimized, collapse height — but only when
  // this node sits inside a vertical parent.  Collapsing inside a horizontal
  // parent would shrink the column width, which is not the intended behavior.
  const allMinimized = totalActiveFlex === 0 && parentDirection === "vertical";

  return (
    <div
      ref={containerRef}
      style={{
        flex: allMinimized ? "0 0 auto" : (flexOverride ?? node.flex),
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
                [isVertical ? "height" : "width"]: colors.islandGap || 4,
                flexShrink: 0,
                cursor: isVertical ? "row-resize" : "col-resize",
                backgroundColor: colors.islandGap ? "transparent" : colors.border,
                position: "relative",
              }}
            >
              {!colors.islandGap && (
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
              )}
            </div>
          )}
          <PaneTree
            node={child}
            path={[...path, i]}
            usedSingletons={usedSingletons}
            renderLeaf={renderLeaf}
            agentInfoMap={agentInfoMap}
            containerStats={containerStats}
            minimizedLeaves={minimizedLeaves}
            flexOverride={childFlexes[i]! > 0 && flexScale !== 1 ? childFlexes[i]! * flexScale : undefined}
            parentDirection={node.direction}
            onUpdateFlex={onUpdateFlex}
            onDrop={onDrop}
            onRemoveLeaf={onRemoveLeaf}
            onSplitLeaf={onSplitLeaf}
            onMaximize={onMaximize}
            onToggleMinimize={onToggleMinimize}
            hiddenPanels={hiddenPanels}
          />
        </Fragment>
      ))}
    </div>
  );
}
