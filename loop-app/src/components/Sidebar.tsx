import { useState } from "react";
import type { Channel } from "../types";

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

  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [creatingFor, setCreatingFor] = useState<string | null>(null);
  const [newThreadName, setNewThreadName] = useState("");

  const toggleExpand = (id: string) => {
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const submitThread = (parentId: string) => {
    const name = newThreadName.trim();
    if (name) {
      onCreateThread(parentId, name);
    }
    setCreatingFor(null);
    setNewThreadName("");
  };

  return (
    <div
      style={{
        width: 240,
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
      {projects.map((project) => {
        const threads = threadsByParent[project.id] ?? [];
        const hasThreads = threads.length > 0;
        const isExpanded = expanded[project.id] !== false;
        const isSelected = selectedId === project.id;

        return (
          <div key={project.id}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 4,
              }}
            >
              {hasThreads ? (
                <button
                  onClick={() => toggleExpand(project.id)}
                  style={{
                    background: "none",
                    border: "none",
                    color: "#6b7280",
                    cursor: "pointer",
                    padding: "6px 2px 6px 8px",
                    fontSize: 10,
                    lineHeight: 1,
                  }}
                >
                  {isExpanded ? "\u25BC" : "\u25B6"}
                </button>
              ) : (
                <span style={{ width: 20 }} />
              )}
              <button
                onClick={() => {
                  onSelect(project.id);
                  if (!isExpanded) toggleExpand(project.id);
                }}
                style={{
                  flex: 1,
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                  padding: "6px 4px",
                  border: "none",
                  background: isSelected ? "#2d2d4d" : "transparent",
                  color: isSelected ? "#e2e8f0" : "#9ca3af",
                  fontSize: 13,
                  fontWeight: isSelected ? 600 : 400,
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
                    backgroundColor: project.active ? "#22c55e" : "#4b5563",
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
                  setCreatingFor(
                    creatingFor === project.id ? null : project.id,
                  );
                  setNewThreadName("");
                }}
                title="New thread"
                style={{
                  background: "none",
                  border: "none",
                  color: "#6b7280",
                  cursor: "pointer",
                  padding: "4px 8px 4px 4px",
                  fontSize: 14,
                  lineHeight: 1,
                }}
              >
                +
              </button>
            </div>

            {creatingFor === project.id && (
              <div style={{ padding: "4px 12px 4px 32px" }}>
                <input
                  autoFocus
                  value={newThreadName}
                  onChange={(e) => setNewThreadName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") submitThread(project.id);
                    if (e.key === "Escape") {
                      setCreatingFor(null);
                      setNewThreadName("");
                    }
                  }}
                  placeholder="Thread name..."
                  style={{
                    width: "100%",
                    padding: "4px 8px",
                    fontSize: 12,
                    backgroundColor: "#1e1e2e",
                    border: "1px solid #3d3d5d",
                    borderRadius: 4,
                    color: "#e2e8f0",
                    outline: "none",
                    boxSizing: "border-box",
                  }}
                />
              </div>
            )}

            {isExpanded &&
              threads.map((thread) => {
                const isThreadSelected = selectedId === thread.id;
                return (
                  <button
                    key={thread.id}
                    onClick={() => onSelect(thread.id)}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      width: "100%",
                      padding: "4px 12px 4px 32px",
                      border: "none",
                      background: isThreadSelected
                        ? "#2d2d4d"
                        : "transparent",
                      color: isThreadSelected ? "#e2e8f0" : "#6b7280",
                      fontSize: 12,
                      textAlign: "left",
                      cursor: "pointer",
                      borderRadius: 4,
                    }}
                  >
                    <span
                      style={{
                        width: 5,
                        height: 5,
                        borderRadius: "50%",
                        backgroundColor: thread.active ? "#22c55e" : "#4b5563",
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
                      {thread.name}
                    </span>
                  </button>
                );
              })}
          </div>
        );
      })}
    </div>
  );
}
