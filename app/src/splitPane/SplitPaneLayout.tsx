import { Fragment, useCallback, useRef } from "react";
import type { PaneNode, LeafNode, DropPosition } from "./types";
import { PaneLeafHeader } from "./PaneLeafHeader";
import { DropZoneOverlay } from "./DropZoneOverlay";
import { colors } from "../theme";

const HEADER_HEIGHT = 22;

interface SplitPaneLayoutProps {
  tree: PaneNode;
  renderLeaf: (leaf: LeafNode) => React.ReactNode;
  onUpdateFlex: (parentPath: number[], dividerIdx: number, flexA: number, flexB: number) => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
  onRemoveLeaf: (id: string) => void;
}

export function SplitPaneLayout({ tree, renderLeaf, onUpdateFlex, onDrop, onRemoveLeaf }: SplitPaneLayoutProps) {
  return (
    <PaneTree
      node={tree}
      path={[]}
      renderLeaf={renderLeaf}
      onUpdateFlex={onUpdateFlex}
      onDrop={onDrop}
      onRemoveLeaf={onRemoveLeaf}
    />
  );
}

interface PaneTreeProps {
  node: PaneNode;
  path: number[];
  renderLeaf: (leaf: LeafNode) => React.ReactNode;
  onUpdateFlex: (parentPath: number[], dividerIdx: number, flexA: number, flexB: number) => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
  onRemoveLeaf: (id: string) => void;
}

function PaneTree({ node, path, renderLeaf, onUpdateFlex, onDrop, onRemoveLeaf }: PaneTreeProps) {
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
      <div style={{ flex: node.flex, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, minWidth: 0, position: "relative" }}>
        <PaneLeafHeader
          leafId={node.id}
          panel={node.panel}
          onRemove={() => onRemoveLeaf(node.id)}
          onDrop={onDrop}
        />
        <DropZoneOverlay leafId={node.id} headerHeight={HEADER_HEIGHT} onDrop={onDrop} />
        <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
          {renderLeaf(node)}
        </div>
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
            path={[...path, i]}
            renderLeaf={renderLeaf}
            onUpdateFlex={onUpdateFlex}
            onDrop={onDrop}
            onRemoveLeaf={onRemoveLeaf}
          />
        </Fragment>
      ))}
    </div>
  );
}
