import type { PanelType } from "./types";
import { colors } from "../theme";

const PANEL_OPTIONS: { panel: PanelType; label: string }[] = [
  { panel: "chat", label: "Chat" },
  { panel: "editor", label: "Editor" },
  { panel: "memory", label: "Memory" },
  { panel: "diff", label: "Diff" },
  { panel: "agent", label: "Agent" },
  { panel: "shell", label: "Shell" },
];

const PANEL_ICONS: Record<PanelType, React.ReactNode> = {
  chat: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
    </svg>
  ),
  editor: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z" />
      <polyline points="13 2 13 9 20 9" />
    </svg>
  ),
  memory: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </svg>
  ),
  diff: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <line x1="12" y1="5" x2="12" y2="19" />
      <polyline points="19 12 12 19 5 12" />
    </svg>
  ),
  agent: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <path d="M9 9h6M9 13h4" />
    </svg>
  ),
  shell: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 17 10 11 4 5" />
      <line x1="12" y1="19" x2="20" y2="19" />
    </svg>
  ),
};

const PANEL_DESCRIPTIONS: Record<PanelType, string> = {
  chat: "Chat with the agent",
  editor: "Browse and edit files",
  memory: "Agent memory explorer",
  diff: "Git diff viewer",
  agent: "Docker isolated terminal",
  shell: "Local machine shell",
};

/** Centered picker for when layout is empty (no tree). */
export function EmptyLayoutPicker({ onAdd }: { onAdd: (panel: PanelType) => void }) {
  const pickerBtnStyle: React.CSSProperties = {
    padding: "12px 24px",
    borderRadius: 6,
    border: `1px solid ${colors.border}`,
    backgroundColor: "transparent",
    color: colors.textDim,
    cursor: "pointer",
    fontSize: 13,
    display: "flex",
    alignItems: "center",
    gap: 8,
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 12 }}>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 10, justifyContent: "center" }}>
        {PANEL_OPTIONS.map(({ panel, label }) => (
          <button
            key={panel}
            onClick={() => onAdd(panel)}
            style={pickerBtnStyle}
            onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.textDim; e.currentTarget.style.color = colors.textLight; e.currentTarget.style.backgroundColor = colors.hoverBg; }}
            onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; e.currentTarget.style.color = colors.textDim; e.currentTarget.style.backgroundColor = "transparent"; }}
          >
            {PANEL_ICONS[panel]}
            <div style={{ display: "flex", flexDirection: "column", alignItems: "flex-start" }}>
              <span>{label}</span>
              <span style={{ fontSize: 10, opacity: 0.6 }}>{PANEL_DESCRIPTIONS[panel]}</span>
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}
