import { useCallback, useEffect, useRef, useState } from "react";
import type { PanelType, SplitDirection, DropPosition } from "./types";
import { SINGLETON_PANELS } from "./types";
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

const PANEL_OPTIONS: { panel: PanelType; label: string }[] = [
  { panel: "chat", label: "Chat" },
  { panel: "editor", label: "Editor" },
  { panel: "memory", label: "Memory" },
  { panel: "diff", label: "Diff" },
  { panel: "agent", label: "Agent" },
  { panel: "shell", label: "Shell" },
];

interface PaneLeafHeaderProps {
  leafId: string;
  panel: PanelType;
  usedSingletons: Set<PanelType>;
  onRemove: () => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
  onSplitLeaf: (leafId: string, panel: PanelType, direction: SplitDirection) => void;
}

export function PaneLeafHeader({ leafId, panel, usedSingletons, onRemove, onDrop, onSplitLeaf }: PaneLeafHeaderProps) {
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
      <PaneSplitMenu leafId={leafId} usedSingletons={usedSingletons} onSplitLeaf={onSplitLeaf} />
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

function PaneSplitMenu({ leafId, usedSingletons, onSplitLeaf }: { leafId: string; usedSingletons: Set<PanelType>; onSplitLeaf: (leafId: string, panel: PanelType, direction: SplitDirection) => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  return (
    <div ref={ref} style={{ position: "relative" }}>
      <button
        onClick={() => setOpen((v) => !v)}
        title="Add panel"
        style={btnStyle}
        onMouseEnter={hoverIn}
        onMouseLeave={hoverOut}
      >
        <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
          <line x1="12" y1="5" x2="12" y2="19" />
          <line x1="5" y1="12" x2="19" y2="12" />
        </svg>
      </button>
      {open && (
        <div style={{
          position: "absolute",
          top: "100%",
          right: 0,
          zIndex: 100,
          backgroundColor: colors.surface,
          border: `1px solid ${colors.border}`,
          borderRadius: 6,
          padding: 4,
          minWidth: 150,
          boxShadow: "0 4px 12px rgba(0,0,0,0.3)",
          marginTop: 2,
        }}>
          {PANEL_OPTIONS.map(({ panel: p, label: l }) => {
            const disabled = SINGLETON_PANELS.includes(p) && usedSingletons.has(p);
            return (
              <div key={p} style={{ display: "flex", gap: 2, opacity: disabled ? 0.35 : 1 }}>
                <button
                  style={{ ...menuItemStyle, cursor: disabled ? "default" : "pointer" }}
                  disabled={disabled}
                  onClick={() => { setOpen(false); onSplitLeaf(leafId, p, "vertical"); }}
                  onMouseEnter={disabled ? undefined : menuHoverIn}
                  onMouseLeave={disabled ? undefined : menuHoverOut}
                >
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="3" y1="12" x2="21" y2="12" /></svg>
                  {l} ↓
                </button>
                <button
                  style={{ ...menuItemStyle, cursor: disabled ? "default" : "pointer" }}
                  disabled={disabled}
                  onClick={() => { setOpen(false); onSplitLeaf(leafId, p, "horizontal"); }}
                  onMouseEnter={disabled ? undefined : menuHoverIn}
                  onMouseLeave={disabled ? undefined : menuHoverOut}
                >
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="12" y1="3" x2="12" y2="21" /></svg>
                  {l} →
                </button>
              </div>
            );
          })}
        </div>
      )}
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

const menuItemStyle: React.CSSProperties = {
  flex: 1,
  background: "none",
  border: "none",
  color: colors.textLight,
  cursor: "pointer",
  padding: "4px 8px",
  fontSize: 11,
  display: "flex",
  alignItems: "center",
  gap: 6,
  borderRadius: 4,
  whiteSpace: "nowrap",
};

function menuHoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)";
}

function menuHoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
}
