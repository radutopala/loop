import { useState } from "react";
import type { Channel } from "../types";
import { colors } from "../theme";
import { ThreadItem } from "./ThreadItem";
import { NewThreadInput } from "./NewThreadInput";

interface ChannelItemProps {
  channel: Channel;
  threads: Channel[];
  selected: boolean;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onCreateThread: (parentId: string, name: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
  onDragStart: (channelId: string) => void;
  onDragOver: (e: React.DragEvent, channelId: string) => void;
  onDrop: (e: React.DragEvent, channelId: string) => void;
  onDragEnd: () => void;
  isDragOver: boolean;
}

export function ChannelItem({
  channel,
  threads,
  selected,
  selectedId,
  onSelect,
  onCreateThread,
  onContextMenu,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
  isDragOver,
}: ChannelItemProps) {
  const [creating, setCreating] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [hovered, setHovered] = useState(false);

  const hasThreads = threads.length > 0;

  return (
    <div
      draggable
      onDragStart={() => onDragStart(channel.id)}
      onDragOver={(e) => onDragOver(e, channel.id)}
      onDrop={(e) => onDrop(e, channel.id)}
      onDragEnd={onDragEnd}
    >
      <div
        title={channel.dir_path || undefined}
        style={{
          display: "flex",
          alignItems: "center",
          borderRadius: 6,
          margin: "0 8px",
          backgroundColor: selected
            ? colors.selectedBg
            : hovered
              ? colors.hoverBg
              : "transparent",
          borderTop: isDragOver ? `2px solid ${colors.active}` : "2px solid transparent",
        }}
        onContextMenu={(e) => onContextMenu(e, channel)}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        {hasThreads ? (
          <button
            onClick={() => setCollapsed((v) => !v)}
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "4px 2px 4px 8px",
              lineHeight: 1,
              flexShrink: 0,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <svg
              width="10"
              height="10"
              viewBox="0 0 10 10"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
              style={{
                transition: "transform 0.15s ease",
                transform: collapsed ? "rotate(-90deg)" : "rotate(0deg)",
              }}
            >
              <path d="M2.5 3.5L5 6.5L7.5 3.5" />
            </svg>
          </button>
        ) : (
          <span style={{ width: 18, flexShrink: 0 }} />
        )}
        <button
          onClick={() => onSelect(channel.id)}
          style={{
            flex: 1,
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "5px 4px 5px 2px",
            border: "none",
            background: "transparent",
            color: selected ? colors.textLight : colors.textMuted,
            fontSize: 15,
            fontWeight: selected ? 600 : 400,
            textAlign: "left",
            cursor: "pointer",
          }}
        >
          <span style={{ color: colors.textDim, flexShrink: 0 }}>#</span>
          <span
            style={{
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {channel.name || channel.dir_path?.split("/").pop() || channel.id}
          </span>
          {(channel.container_running || channel.agent_running) && (
            <span
              style={{
                width: 6,
                height: 6,
                borderRadius: "50%",
                backgroundColor: colors.active,
                flexShrink: 0,
              }}
            />
          )}
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
            padding: "2px 6px",
            marginRight: 4,
            fontSize: 11,
            lineHeight: 1,
            flexShrink: 0,
            borderRadius: 4,
            opacity: hovered ? 1 : 0,
            whiteSpace: "nowrap",
          }}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
        >
          + thread
        </button>
      </div>

      {creating && (
        <NewThreadInput
          onSubmit={(name) => {
            onCreateThread(channel.id, name);
            setCreating(false);
          }}
          onCancel={() => setCreating(false)}
        />
      )}

      {!collapsed &&
        threads.map((thread, i) => (
          <ThreadItem
            key={thread.id}
            thread={thread}
            selected={selectedId === thread.id}
            isLast={i === threads.length - 1}
            onSelect={onSelect}
            onContextMenu={onContextMenu}
          />
        ))}
    </div>
  );
}
