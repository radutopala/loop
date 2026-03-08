import type { Channel } from "../types";
import { colors } from "../theme";
import { ProjectItem } from "./ProjectItem";

interface SidebarProps {
  channels: Channel[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onCreateThread: (parentId: string, name: string) => void;
}

export function Sidebar({
  channels,
  selectedId,
  onSelect,
  onCreateThread,
}: SidebarProps) {
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
        width: 240,
        borderRight: `1px solid ${colors.border}`,
        backgroundColor: colors.sidebar,
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
          color: colors.textDim,
          textTransform: "uppercase",
          letterSpacing: 1,
        }}
      >
        Projects
      </div>
      {projects.map((project) => (
        <ProjectItem
          key={project.id}
          project={project}
          threads={threadsByParent[project.id] ?? []}
          selected={selectedId === project.id}
          selectedId={selectedId}
          onSelect={onSelect}
          onCreateThread={onCreateThread}
        />
      ))}
    </div>
  );
}
