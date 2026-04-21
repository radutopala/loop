import { PANEL_OPTIONS, type PanelType } from "../types/panels";
import { useTheme } from "../ThemeContext";

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
  git: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <line x1="12" y1="5" x2="12" y2="19" />
      <polyline points="19 12 12 19 5 12" />
    </svg>
  ),
  "docker-agent": (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <path d="M9 9h6M9 13h4" />
    </svg>
  ),
  "host-shell": (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 17 10 11 4 5" />
      <line x1="12" y1="19" x2="20" y2="19" />
    </svg>
  ),
  "docker-shell": (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <polyline points="7 15 11 11 7 7" />
      <line x1="13" y1="15" x2="17" y2="15" />
    </svg>
  ),
  "docker-browser": (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <line x1="3" y1="9" x2="21" y2="9" />
      <circle cx="7" cy="6" r="1" fill="currentColor" />
      <circle cx="11" cy="6" r="1" fill="currentColor" />
    </svg>
  ),
  "host-browser": (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="3" width="20" height="14" rx="2" />
      <line x1="8" y1="21" x2="16" y2="21" />
      <line x1="12" y1="17" x2="12" y2="21" />
    </svg>
  ),
  sessions: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 8v4l3 3" />
      <circle cx="12" cy="12" r="10" />
    </svg>
  ),
  playground: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <polygon points="5 3 19 12 5 21 5 3" />
    </svg>
  ),
  notes: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="16" y1="13" x2="8" y2="13" />
      <line x1="16" y1="17" x2="8" y2="17" />
      <polyline points="10 9 9 9 8 9" />
    </svg>
  ),
  tasks: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 8v4l3 3" />
      <circle cx="12" cy="12" r="10" />
      <path d="M16 3.13a4 4 0 0 1 0 .74" />
    </svg>
  ),
  kanban: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="5" height="18" rx="1" />
      <rect x="10" y="3" width="5" height="12" rx="1" />
      <rect x="17" y="3" width="5" height="15" rx="1" />
    </svg>
  ),
  workflows: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="5" cy="6" r="2" />
      <circle cx="12" cy="6" r="2" />
      <circle cx="12" cy="18" r="2" />
      <circle cx="19" cy="12" r="2" />
      <line x1="7" y1="6" x2="10" y2="6" />
      <line x1="14" y1="6" x2="17" y2="10.5" />
      <line x1="14" y1="18" x2="17" y2="13.5" />
    </svg>
  ),
};

const PANEL_DESCRIPTIONS: Record<PanelType, string> = {
  chat: "Chat with the agent",
  editor: "Browse and edit files",
  memory: "Agent memory explorer",
  git: "Git diff viewer",
  "docker-agent": "Docker isolated terminal",
  "host-shell": "Local machine shell",
  "docker-shell": "Shell inside the Docker container",
  "docker-browser": "Browser in Docker container",
  "host-browser": "Browser on host machine",
  sessions: "Browse and resume Claude sessions",
  playground: "Live interactive code sandbox",
  notes: "Quick scratch notes (Markdown)",
  tasks: "Manage scheduled tasks",
  kanban: "Kanban board for tickets",
  workflows: "Workflow runs and status",
};

/** Centered picker for when layout is empty (no tree). */
export function EmptyLayoutPicker({ onAdd, hiddenPanels }: { onAdd: (panel: PanelType) => void; hiddenPanels?: PanelType[] }) {
  const { colors } = useTheme();

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
        {PANEL_OPTIONS.filter(({ panel }) => !hiddenPanels?.includes(panel)).map(({ panel, label }) => (
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
