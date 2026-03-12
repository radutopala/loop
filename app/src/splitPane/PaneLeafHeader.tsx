import { useCallback } from "react";
import type { PanelType, DropPosition } from "./types";
import { emitLayoutDragStart, emitLayoutDragEnd, DRAG_MIME } from "./DropZoneOverlay";
import { colors } from "../theme";

const PANEL_LABELS: Record<PanelType, string> = {
  chat: "Chat",
  editor: "Editor",
  memory: "Memory",
  diff: "Diff",
  agent: "Agent",
  shell: "Shell",
};

interface PaneLeafHeaderProps {
  leafId: string;
  panel: PanelType;
  onRemove: () => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
}

export function PaneLeafHeader({ leafId, panel, onRemove, onDrop }: PaneLeafHeaderProps) {
  const label = PANEL_LABELS[panel];
  const isAgent = panel === "agent";

  const handleDragStart = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    e.dataTransfer.setData(DRAG_MIME, leafId);
    e.dataTransfer.effectAllowed = "move";
    emitLayoutDragStart();
  }, [leafId]);

  const handleDragEnd = useCallback(() => {
    emitLayoutDragEnd();
  }, []);

  const handleDragOverHeader = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    if (!e.dataTransfer.types.includes(DRAG_MIME)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  }, []);

  const handleDropOnHeader = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    const dragId = e.dataTransfer.getData(DRAG_MIME);
    if (dragId && dragId !== leafId) {
      onDrop(dragId, leafId, "center");
    }
  }, [leafId, onDrop]);

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
      <span
        style={{
          padding: "1px 4px",
          borderRadius: 3,
          fontSize: 10,
          fontWeight: 500,
          color: isAgent ? colors.active : panel === "shell" ? colors.textDim : colors.textLight,
          backgroundColor: isAgent ? "rgba(96, 165, 250, 0.1)" : "rgba(255,255,255,0.05)",
        }}
      >
        {label}
      </span>
      <div style={{ flex: 1 }} />
      <button
        onClick={onRemove}
        title="Close pane"
        style={btnStyle}
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

const btnStyle: React.CSSProperties = {
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
