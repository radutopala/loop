import { useState } from "react";
import type { Channel } from "../types";
import { colors } from "../theme";
import { ThreadItem } from "./ThreadItem";
import { NewThreadInput } from "./NewThreadInput";

interface ProjectItemProps {
  project: Channel;
  threads: Channel[];
  selected: boolean;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onCreateThread: (parentId: string, name: string) => void;
}

export function ProjectItem({
  project,
  threads,
  selected,
  selectedId,
  onSelect,
  onCreateThread,
}: ProjectItemProps) {
  const [expanded, setExpanded] = useState(true);
  const [creating, setCreating] = useState(false);
  const hasThreads = threads.length > 0;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
        {hasThreads ? (
          <button
            onClick={() => setExpanded((v) => !v)}
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "6px 2px 6px 8px",
              fontSize: 10,
              lineHeight: 1,
            }}
          >
            {expanded ? "\u25BC" : "\u25B6"}
          </button>
        ) : (
          <span style={{ width: 20 }} />
        )}
        <button
          onClick={() => {
            onSelect(project.id);
            if (!expanded) setExpanded(true);
          }}
          style={{
            flex: 1,
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "6px 4px",
            border: "none",
            background: selected ? colors.selectedBg : "transparent",
            color: selected ? colors.textLight : colors.textMuted,
            fontSize: 13,
            fontWeight: selected ? 600 : 400,
            textAlign: "left",
            cursor: "pointer",
            borderRadius: 4,
          }}
        >
          <span
            style={{
              width: 6,
              height: 6,
              borderRadius: "50%",
              backgroundColor: project.active ? colors.active : colors.textDisabled,
              flexShrink: 0,
            }}
          />
          <span
            style={{
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {project.name}
          </span>
        </button>
        <button
          onClick={(e) => {
            e.stopPropagation();
            setCreating((v) => !v);
          }}
          title="New thread"
          style={{
            background: "none",
            border: "none",
            color: colors.textDim,
            cursor: "pointer",
            padding: "4px 8px 4px 4px",
            fontSize: 14,
            lineHeight: 1,
          }}
        >
          +
        </button>
      </div>

      {creating && (
        <NewThreadInput
          onSubmit={(name) => {
            onCreateThread(project.id, name);
            setCreating(false);
          }}
          onCancel={() => setCreating(false)}
        />
      )}

      {expanded &&
        threads.map((thread) => (
          <ThreadItem
            key={thread.id}
            thread={thread}
            selected={selectedId === thread.id}
            onSelect={onSelect}
          />
        ))}
    </div>
  );
}
