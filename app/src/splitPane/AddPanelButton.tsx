import { useEffect, useRef, useState } from "react";
import type { PaneNode, PanelType, SplitDirection } from "./types";
import { canAddPanel } from "./treeOps";
import { colors } from "../theme";

const PANEL_OPTIONS: { panel: PanelType; label: string }[] = [
  { panel: "chat", label: "Chat" },
  { panel: "editor", label: "Editor" },
  { panel: "memory", label: "Memory" },
  { panel: "diff", label: "Diff" },
  { panel: "agent", label: "Agent" },
  { panel: "shell", label: "Shell" },
];

interface AddPanelButtonProps {
  tree: PaneNode | null;
  onAdd: (panel: PanelType, direction: SplitDirection) => void;
}

export function AddPanelButton({ tree, onAdd }: AddPanelButtonProps) {
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
        style={{
          background: "none",
          border: `1px solid ${colors.border}`,
          color: colors.textDim,
          cursor: "pointer",
          padding: "2px 6px",
          fontSize: 10,
          lineHeight: 1,
          borderRadius: 6,
          display: "flex",
          alignItems: "center",
          gap: 3,
        }}
        onMouseEnter={hoverIn}
        onMouseLeave={hoverOut}
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
          <line x1="12" y1="5" x2="12" y2="19" />
          <line x1="5" y1="12" x2="19" y2="12" />
        </svg>
        Add
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
          {PANEL_OPTIONS.map(({ panel, label }) => {
            const available = canAddPanel(tree, panel);
            return (
              <div key={panel} style={{ opacity: available ? 1 : 0.35 }}>
                <div style={{ display: "flex", gap: 2 }}>
                  <button
                    disabled={!available}
                    style={itemStyle}
                    onClick={() => { setOpen(false); onAdd(panel, "vertical"); }}
                    onMouseEnter={available ? itemHoverIn : undefined}
                    onMouseLeave={available ? itemHoverOut : undefined}
                  >
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="3" y1="12" x2="21" y2="12" /></svg>
                    {label} ↓
                  </button>
                  <button
                    disabled={!available}
                    style={itemStyle}
                    onClick={() => { setOpen(false); onAdd(panel, "horizontal"); }}
                    onMouseEnter={available ? itemHoverIn : undefined}
                    onMouseLeave={available ? itemHoverOut : undefined}
                  >
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="12" y1="3" x2="12" y2="21" /></svg>
                    {label} →
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** Centered picker for when layout is empty (no tree). */
export function EmptyLayoutPicker({ onAdd }: { onAdd: (panel: PanelType) => void }) {
  const btnStyle: React.CSSProperties = {
    padding: "10px 18px",
    borderRadius: 6,
    border: `1px solid ${colors.border}`,
    backgroundColor: "transparent",
    color: colors.textDim,
    cursor: "pointer",
    fontSize: 12,
    display: "flex",
    alignItems: "center",
    gap: 6,
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 10 }}>
      <span style={{ fontSize: 11, color: colors.textDim, marginBottom: 4 }}>Add a panel to get started</span>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 8, justifyContent: "center" }}>
        {PANEL_OPTIONS.map(({ panel, label }) => (
          <button
            key={panel}
            onClick={() => onAdd(panel)}
            style={btnStyle}
            onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.textDim; e.currentTarget.style.color = colors.textLight; e.currentTarget.style.backgroundColor = colors.hoverBg; }}
            onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; e.currentTarget.style.color = colors.textDim; e.currentTarget.style.backgroundColor = "transparent"; }}
          >
            {label}
          </button>
        ))}
      </div>
    </div>
  );
}

const itemStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 4,
  padding: "4px 8px",
  background: "none",
  border: "none",
  color: colors.text,
  cursor: "pointer",
  fontSize: 11,
  flex: 1,
  textAlign: "left",
  borderRadius: 3,
};

function hoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
  e.currentTarget.style.color = colors.textLight;
}

function hoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
  e.currentTarget.style.color = colors.textDim;
}

function itemHoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
}

function itemHoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
}
