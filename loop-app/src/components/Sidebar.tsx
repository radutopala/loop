import type { Channel } from "../types";

interface SidebarProps {
  channels: Channel[];
  selectedId: string | null;
  onSelect: (id: string) => void;
}

export function Sidebar({ channels, selectedId, onSelect }: SidebarProps) {
  const projects = channels.filter((c) => !c.parent_id);
  const threadsByParent = channels.reduce<Record<string, Channel[]>>(
    (acc, c) => {
      if (c.parent_id) {
        (acc[c.parent_id] ??= []).push(c);
      }
      return acc;
    },
    {},
  );

  return (
    <div
      style={{
        width: 220,
        borderRight: "1px solid #2d2d2d",
        backgroundColor: "#161622",
        display: "flex",
        flexDirection: "column",
        overflow: "auto",
      }}
    >
      <div
        style={{
          padding: "16px 12px 8px",
          fontSize: 11,
          fontWeight: 700,
          color: "#6b7280",
          textTransform: "uppercase",
          letterSpacing: 1,
        }}
      >
        Projects
      </div>
      {projects.map((project) => (
        <div key={project.id}>
          <button
            onClick={() => onSelect(project.id)}
            style={{
              display: "block",
              width: "100%",
              padding: "6px 12px",
              border: "none",
              background:
                selectedId === project.id ? "#2d2d4d" : "transparent",
              color: selectedId === project.id ? "#e2e8f0" : "#9ca3af",
              fontSize: 13,
              textAlign: "left",
              cursor: "pointer",
            }}
          >
            {project.name}
          </button>
          {threadsByParent[project.id]?.map((thread) => (
            <button
              key={thread.id}
              onClick={() => onSelect(thread.id)}
              style={{
                display: "block",
                width: "100%",
                padding: "4px 12px 4px 28px",
                border: "none",
                background:
                  selectedId === thread.id ? "#2d2d4d" : "transparent",
                color: selectedId === thread.id ? "#e2e8f0" : "#6b7280",
                fontSize: 12,
                textAlign: "left",
                cursor: "pointer",
              }}
            >
              {thread.name}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}
