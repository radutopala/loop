import { useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Channel } from "../../types";
import { NewThreadInput } from "./NewThreadInput";
import type { PillKind } from "./pills";
import { SIDEBAR_PILLS } from "./pills";
import { SidebarWorktreeButton } from "./SidebarWorktreeButton";
import { StatusPill } from "./StatusPill";
import { ThreadItem, type ThreadReorder } from "./ThreadItem";

interface ChannelItemProps {
  channel: Channel;
  threads: Channel[];
  threadsByParent: Record<string, Channel[]>;
  selected: boolean;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onCreateThread: (parentId: string, name: string) => void;
  onCreateWorktree?: (channelId: string, branch: string) => void;
  threadReorder?: ThreadReorder;
  onOpenConfig?: (dirPath: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
  onDragStart: (channelId: string) => void;
  onDragOver: (e: React.DragEvent, channelId: string) => void;
  onDrop: (e: React.DragEvent, channelId: string) => void;
  onDragEnd: () => void;
  isDragOver: boolean;
  pinned?: boolean;
  selectMode?: boolean;
  checkedIds?: Set<string>;
  onToggleCheck?: (id: string) => void;
  /** Real-time running status from app-level chat state store. */
  isRunningMapRef?: React.RefObject<Map<string, string>>;
  unreadIdsRef?: React.RefObject<Set<string>>;
  pillsRef?: React.RefObject<Map<PillKind, Set<string>>>;
}

export function ChannelItem({
  channel,
  threads,
  threadsByParent,
  selected,
  selectedId,
  onSelect,
  onCreateThread,
  onCreateWorktree,
  threadReorder,
  onOpenConfig,
  onContextMenu,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
  isDragOver,
  pinned,
  selectMode,
  checkedIds,
  onToggleCheck,
  isRunningMapRef,
  unreadIdsRef,
  pillsRef,
}: ChannelItemProps) {
  const { colors } = useTheme();
  const [creating, setCreating] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [hovered, setHovered] = useState(false);

  const hasThreads = threads.length > 0;

  return (
    <div draggable onDragStart={() => onDragStart(channel.id)} onDragOver={(e) => onDragOver(e, channel.id)} onDrop={(e) => onDrop(e, channel.id)} onDragEnd={onDragEnd}>
      <div
        title={channel.dir_path || undefined}
        style={{
          display: "flex",
          alignItems: "center",
          borderRadius: 6,
          margin: "0 8px",
          backgroundColor: selected ? colors.selectedBg : hovered ? colors.hoverBg : "transparent",
          borderTop: isDragOver ? `2px solid ${colors.active}` : "2px solid transparent",
        }}
        onContextMenu={(e) => onContextMenu(e, channel)}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        {selectMode && !pinned ? (
          <span
            onClick={(e) => {
              e.stopPropagation();
              onToggleCheck?.(channel.id);
            }}
            style={{
              width: 20,
              flexShrink: 0,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              cursor: "pointer",
            }}
          >
            <span
              style={{
                width: 12,
                height: 12,
                borderRadius: 3,
                border: `1.5px solid ${checkedIds?.has(channel.id) ? colors.active : colors.textDim}`,
                backgroundColor: checkedIds?.has(channel.id) ? colors.active : "transparent",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                transition: "all 0.15s",
              }}
            >
              {checkedIds?.has(channel.id) && (
                <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke={colors.white} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              )}
            </span>
          </span>
        ) : hasThreads ? (
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
          <span style={{ width: 20, flexShrink: 0 }} />
        )}
        <button
          onClick={() => onSelect(channel.id)}
          style={{
            flex: 1,
            // Allow the button to shrink below its text width so the name
            // ellipsizes and the hover action buttons (+thread/+wt) stay in bounds.
            minWidth: 0,
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
          {SIDEBAR_PILLS.filter((p) => pillsRef?.current?.get(p.kind)?.has(channel.id)).map((p) => (
            <StatusPill key={p.kind} label={p.label} color={colors[p.color]} title={p.title} />
          ))}
          {(channel.container_running || channel.agent_running || isRunningMapRef?.current?.get(channel.id)) && (
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
          {(channel.diff_additions > 0 || channel.diff_deletions > 0) && (
            <span style={{ flexShrink: 0, fontSize: 9, fontFamily: fonts.mono, marginLeft: "auto" }}>
              {channel.diff_additions > 0 && <span style={{ color: colors.diffAddText }}>+{channel.diff_additions}</span>}
              {channel.diff_additions > 0 && channel.diff_deletions > 0 && " "}
              {channel.diff_deletions > 0 && <span style={{ color: colors.diffDelText }}>-{channel.diff_deletions}</span>}
            </span>
          )}
        </button>
        {hovered && (
          <div style={{ display: "flex", alignItems: "center", gap: 2, marginRight: 4, flexShrink: 0 }}>
            {channel.dir_path && onOpenConfig && (
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  onOpenConfig(channel.dir_path);
                }}
                title="Project config"
                style={{
                  background: "none",
                  border: "none",
                  color: colors.textDim,
                  cursor: "pointer",
                  padding: "3px 4px",
                  lineHeight: 1,
                  borderRadius: 4,
                  display: "flex",
                  alignItems: "center",
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.backgroundColor = colors.hoverBg;
                  e.currentTarget.style.color = colors.textLight;
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.backgroundColor = "transparent";
                  e.currentTarget.style.color = colors.textDim;
                }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
                  <circle cx="12" cy="12" r="3" />
                </svg>
              </button>
            )}
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
                fontSize: 11,
                lineHeight: 1,
                borderRadius: 4,
                whiteSpace: "nowrap",
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.backgroundColor = colors.hoverBg;
                e.currentTarget.style.color = colors.textLight;
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = "transparent";
                e.currentTarget.style.color = colors.textDim;
              }}
            >
              + thread
            </button>
            {channel.dir_path && onCreateWorktree && <SidebarWorktreeButton channelId={channel.id} onCreateWorktree={onCreateWorktree} />}
          </div>
        )}
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
            subThreads={threadsByParent[thread.id] ?? []}
            threadsByParent={threadsByParent}
            selected={selectedId === thread.id}
            selectedId={selectedId}
            isLast={i === threads.length - 1}
            onSelect={onSelect}
            onContextMenu={onContextMenu}
            reorder={threadReorder}
            selectMode={selectMode}
            checked={checkedIds?.has(thread.id)}
            onToggleCheck={onToggleCheck}
            isRunningMapRef={isRunningMapRef}
            unreadIdsRef={unreadIdsRef}
            pillsRef={pillsRef}
          />
        ))}
    </div>
  );
}
