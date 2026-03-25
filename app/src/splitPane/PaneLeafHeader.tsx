import { useCallback, useEffect, useRef, useState } from "react";
import type { PanelType, SplitDirection, DropPosition } from "./types";
import { SINGLETON_PANELS } from "./types";
import { emitLayoutDragStart, emitLayoutDragEnd, DRAG_MIME } from "./DropZoneOverlay";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";
import type { AgentInfo } from "../hooks/useAgentRegistry";

const PANEL_LABELS: Record<PanelType, string> = {
  chat: "Chat",
  editor: "Editor",
  memory: "Memory",
  diff: "Diff",
  agent: "Agent",
  shell: "Shell",
  browser: "Browser",
};

const PANEL_OPTIONS: { panel: PanelType; label: string }[] = [
  { panel: "chat", label: "Chat" },
  { panel: "editor", label: "Editor" },
  { panel: "memory", label: "Memory" },
  { panel: "diff", label: "Diff" },
  { panel: "agent", label: "Agent" },
  { panel: "shell", label: "Shell" },
  { panel: "browser", label: "Browser" },
];

function buildBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
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
}

function buildMenuItemStyle(colors: ColorPalette): React.CSSProperties {
  return {
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
}

interface PaneLeafHeaderProps {
  leafId: string;
  panel: PanelType;
  usedSingletons: Set<PanelType>;
  isMaximized?: boolean;
  agentInfo?: AgentInfo;
  onRemove: () => void;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
  onSplitLeaf: (leafId: string, panel: PanelType, direction: SplitDirection) => void;
  onToggleMaximize?: () => void;
}

export function PaneLeafHeader({ leafId, panel, usedSingletons, isMaximized, agentInfo, onRemove, onDrop, onSplitLeaf, onToggleMaximize }: PaneLeafHeaderProps) {
  const { colors } = useTheme();
  const isAgent = panel === "agent";
  const label = isAgent && agentInfo?.name ? agentInfo.name : PANEL_LABELS[panel];

  const btnStyle = buildBtnStyle(colors);

  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };

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
          backgroundColor: isAgent ? colors.dirSelectedBg : colors.panelLabelBg,
        }}
      >
        {isAgent && agentInfo && (
          <span
            title={agentInfo.status + (agentInfo.work_summary ? `: ${agentInfo.work_summary}` : "")}
            style={{
              display: "inline-block",
              width: 6,
              height: 6,
              borderRadius: "50%",
              marginRight: 4,
              backgroundColor: agentInfo.status === "running" ? colors.active : agentInfo.status === "error" ? colors.error : colors.textDim,
            }}
          />
        )}
        {label}
      </span>
      <span style={{ width: 1, height: 10, backgroundColor: colors.border, flexShrink: 0, marginLeft: 2, marginRight: 2 }} />
      <PaneSplitMenu leafId={leafId} usedSingletons={usedSingletons} onSplitLeaf={onSplitLeaf} />
      {onToggleMaximize && (
        <button
          onClick={onToggleMaximize}
          title={isMaximized ? "Restore pane" : "Expand pane"}
          style={btnStyle}
          onMouseEnter={hoverIn}
          onMouseLeave={hoverOut}
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
      <div style={{ flex: 1 }} />
    </div>
  );
}

function PaneSplitMenu({ leafId, usedSingletons, onSplitLeaf }: { leafId: string; usedSingletons: Set<PanelType>; onSplitLeaf: (leafId: string, panel: PanelType, direction: SplitDirection) => void }) {
  const { colors } = useTheme();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  const btnStyle = buildBtnStyle(colors);
  const menuItemStyle = buildMenuItemStyle(colors);

  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };
  const menuHoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverAlpha;
  };
  const menuHoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
  };

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
          left: 0,
          zIndex: 100,
          backgroundColor: colors.surface,
          border: `1px solid ${colors.border}`,
          borderRadius: 6,
          padding: 4,
          boxShadow: `0 4px 12px ${colors.shadow}`,
          marginTop: 2,
          display: "grid",
          gridTemplateColumns: "auto auto",
          gap: 2,
        }}>
          {PANEL_OPTIONS.map(({ panel: p, label: l }) => {
            const disabled = SINGLETON_PANELS.includes(p) && usedSingletons.has(p);
            return (
              <>
                <button
                  key={`${p}-v`}
                  style={{ ...menuItemStyle, cursor: disabled ? "default" : "pointer", opacity: disabled ? 0.35 : 1 }}
                  disabled={disabled}
                  onClick={() => { setOpen(false); onSplitLeaf(leafId, p, "vertical"); }}
                  onMouseEnter={disabled ? undefined : menuHoverIn}
                  onMouseLeave={disabled ? undefined : menuHoverOut}
                >
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="3" y1="12" x2="21" y2="12" /></svg>
                  {l} ↓
                </button>
                <button
                  key={`${p}-h`}
                  style={{ ...menuItemStyle, cursor: disabled ? "default" : "pointer", opacity: disabled ? 0.35 : 1 }}
                  disabled={disabled}
                  onClick={() => { setOpen(false); onSplitLeaf(leafId, p, "horizontal"); }}
                  onMouseEnter={disabled ? undefined : menuHoverIn}
                  onMouseLeave={disabled ? undefined : menuHoverOut}
                >
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="12" y1="3" x2="12" y2="21" /></svg>
                  {l} →
                </button>
              </>
            );
          })}
        </div>
      )}
    </div>
  );
}
