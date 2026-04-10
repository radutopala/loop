import { useState } from "react";
import type { Channel } from "../../types";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";

interface ThreadItemProps {
  thread: Channel;
  subThreads?: Channel[];
  threadsByParent?: Record<string, Channel[]>;
  selected: boolean;
  selectedId?: string | null;
  isLast?: boolean;
  onSelect: (id: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
  selectMode?: boolean;
  checked?: boolean;
  onToggleCheck?: (id: string) => void;
  /** Real-time running status from app-level chat state store. */
  isRunningMapRef?: React.RefObject<Map<string, string>>;
  unreadIdsRef?: React.RefObject<Set<string>>;
}

export function ThreadItem({ thread, subThreads, threadsByParent, selected, selectedId, isLast, onSelect, onContextMenu, selectMode, checked, onToggleCheck, isRunningMapRef, unreadIdsRef }: ThreadItemProps) {
  const { colors } = useTheme();
  const [hovered, setHovered] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const hasChildren = (subThreads?.length ?? 0) > 0;
  const isUnread = unreadIdsRef?.current?.has(thread.id) ?? false;
  const isEphemeral = thread.name.startsWith("[ephemeral] ");
  const isTaskThread = /^(\[ephemeral] )?(🧵 |⏱ )?task #/.test(thread.name);
  const displayName = isTaskThread
    ? thread.name.replace(/^(\[ephemeral] )?(🧵 |⏱ )?/, "")
    : (thread.name || thread.id);

  return (
    <div style={{ position: "relative", margin: "0 8px" }}>
      {/* Tree connector line — positioned absolutely to span full row height */}
      <svg
        width="10"
        height="100%"
        style={{
          position: "absolute",
          left: 16,
          top: 0,
          bottom: 0,
          height: "100%",
          overflow: "visible",
          zIndex: 1,
        }}
      >
        <line x1="1" y1="0" x2="1" y2={isLast ? "50%" : "100%"} stroke={colors.textDisabled} strokeWidth="1.5" />
        <line x1="1" y1="50%" x2="10" y2="50%" stroke={colors.textDisabled} strokeWidth="1.5" />
      </svg>
      <button
        title={thread.dir_path || undefined}
        onClick={() => onSelect(thread.id)}
        onContextMenu={(e) => onContextMenu(e, thread)}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 4,
          width: "100%",
          padding: "4px 8px 4px 30px",
          border: "none",
          background: selected
            ? colors.selectedBg
            : hovered
              ? colors.hoverBg
              : "transparent",
          color: selected ? colors.textLight : colors.textDim,
          fontSize: 14,
          textAlign: "left",
          cursor: "pointer",
          borderRadius: 6,
        }}
      >
      {hasChildren && (
        <span
          onClick={(e) => { e.stopPropagation(); setCollapsed((c) => !c); }}
          style={{ display: "flex", alignItems: "center", flexShrink: 0, cursor: "pointer", marginRight: 2 }}
        >
          <svg width="8" height="8" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"
            style={{ transition: "transform 0.15s ease", transform: collapsed ? "rotate(-90deg)" : "rotate(0deg)" }}>
            <path d="M2.5 3.5L5 6.5L7.5 3.5" />
          </svg>
        </span>
      )}
      {selectMode && (
        <span
          onClick={(e) => { e.stopPropagation(); onToggleCheck?.(thread.id); }}
          style={{ display: "flex", alignItems: "center", flexShrink: 0, cursor: "pointer" }}
        >
          <span style={{
            width: 12,
            height: 12,
            borderRadius: 3,
            border: `1.5px solid ${checked ? colors.active : colors.textDim}`,
            backgroundColor: checked ? colors.active : "transparent",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            transition: "all 0.15s",
          }}>
            {checked && (
              <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke={colors.white} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="20 6 9 17 4 12" />
              </svg>
            )}
          </span>
        </span>
      )}
      {thread.worktree && (
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={colors.active} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
          <circle cx="18" cy="18" r="3" />
          <circle cx="6" cy="6" r="3" />
          <path d="M6 21V9a9 9 0 0 0 9 9" />
        </svg>
      )}
      {isTaskThread && !isEphemeral && (
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
        </svg>
      )}
      {isEphemeral && (
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.6 }}>
          <path d="M17.7 7.7A7.5 7.5 0 1 0 5 16.6" />
          <path d="M8 22l-4-4 4-4" />
        </svg>
      )}
      <span
        style={{
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          fontWeight: isUnread ? 600 : undefined,
        }}
      >
        {displayName}
      </span>
      {(thread.diff_additions > 0 || thread.diff_deletions > 0) && (
        <span style={{ flexShrink: 0, fontSize: 9, fontFamily: fonts.mono, marginLeft: "auto" }}>
          {thread.diff_additions > 0 && <span style={{ color: colors.diffAddText }}>+{thread.diff_additions}</span>}
          {thread.diff_additions > 0 && thread.diff_deletions > 0 && " "}
          {thread.diff_deletions > 0 && <span style={{ color: colors.diffDelText }}>-{thread.diff_deletions}</span>}
        </span>
      )}
      {isUnread && !selected && (
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            backgroundColor: "#5b9cf5",
            flexShrink: 0,
            marginLeft: (thread.diff_additions > 0 || thread.diff_deletions > 0) ? 4 : "auto",
          }}
        />
      )}
      {(thread.container_running || thread.agent_running || isRunningMapRef?.current?.get(thread.id)) && (
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            backgroundColor: colors.active,
            flexShrink: 0,
            marginLeft: (isUnread || thread.diff_additions > 0 || thread.diff_deletions > 0) ? 4 : "auto",
          }}
        />
      )}
    </button>
      {hasChildren && !collapsed &&
        subThreads!.map((sub, i) => (
          <ThreadItem
            key={sub.id}
            thread={sub}
            subThreads={threadsByParent?.[sub.id] ?? []}
            threadsByParent={threadsByParent}
            selected={selectedId === sub.id}
            selectedId={selectedId}
            isLast={i === subThreads!.length - 1}
            onSelect={onSelect}
            onContextMenu={onContextMenu}
            selectMode={selectMode}
            checked={checked}
            onToggleCheck={onToggleCheck}
            isRunningMapRef={isRunningMapRef}
            unreadIdsRef={unreadIdsRef}
          />
        ))}
    </div>
  );
}
