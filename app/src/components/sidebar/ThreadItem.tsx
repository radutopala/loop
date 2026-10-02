import { useRef, useState } from "react";
import { forkThread } from "../../api/channels";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Channel } from "../../types";
import type { PillKind } from "./pills";
import { SIDEBAR_PILLS } from "./pills";
import { RowInfoPopup } from "./RowInfoPopup";
import { SessionKindIcon } from "./SessionKindIcon";
import { StatusPill } from "./StatusPill";
import { sessionName } from "./sessions";

/** Drag-to-reorder wiring for threads/worktrees under a common parent. */
export interface ThreadReorder {
  onDragStart: (threadId: string, parentId: string) => void;
  onDragOver: (e: React.DragEvent, threadId: string, parentId: string) => void;
  onDrop: (e: React.DragEvent, targetId: string, parentId: string) => void;
  onDragEnd: () => void;
  dragOverId: string | null;
}

interface ThreadItemProps {
  thread: Channel;
  subThreads?: Channel[];
  threadsByParent?: Record<string, Channel[]>;
  selected: boolean;
  selectedId?: string | null;
  isLast?: boolean;
  onSelect: (id: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
  reorder?: ThreadReorder;
  selectMode?: boolean;
  checked?: boolean;
  onToggleCheck?: (id: string) => void;
  /** Real-time running status from app-level chat state store. */
  isRunningMapRef?: React.RefObject<Map<string, string>>;
  unreadIdsRef?: React.RefObject<Set<string>>;
  pillsRef?: React.RefObject<Map<PillKind, Set<string>>>;
}

const connectorStyle: React.CSSProperties = {
  position: "absolute",
  // Line at 13px (+8px margin = 21px), under the centre of the channel chevron.
  left: 12,
  top: 0,
  bottom: 0,
  height: "100%",
  overflow: "visible",
  zIndex: 1,
};

export function ThreadItem({
  thread,
  subThreads,
  threadsByParent,
  selected,
  selectedId,
  isLast,
  onSelect,
  onContextMenu,
  reorder,
  selectMode,
  checked,
  onToggleCheck,
  isRunningMapRef,
  unreadIdsRef,
  pillsRef,
}: ThreadItemProps) {
  const { colors } = useTheme();
  const [hovered, setHovered] = useState(false);
  const rowRef = useRef<HTMLDivElement>(null);
  const [forking, setForking] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const hasChildren = (subThreads?.length ?? 0) > 0;
  const isUnread = unreadIdsRef?.current?.has(thread.id) ?? false;
  const activePills = SIDEBAR_PILLS.filter((p) => pillsRef?.current?.get(p.kind)?.has(thread.id));
  const hasAnyPill = activePills.length > 0;
  const displayName = sessionName(thread);

  return (
    <div style={{ position: "relative", margin: "0 8px" }}>
      {/* Tree connector line — spans this thread and its sub-threads so a non-last sibling's line reaches the next one */}
      {!isLast && (
        <svg width="10" height="100%" style={connectorStyle}>
          <line x1="1" y1="0" x2="1" y2="100%" stroke={colors.textDisabled} strokeWidth="1.5" />
        </svg>
      )}
      <RowInfoPopup channel={thread} anchorRef={rowRef} hovered={hovered} />
      <div
        ref={rowRef}
        data-testid="sidebar-thread-row"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        style={{
          position: "relative",
          display: "flex",
          alignItems: "center",
          borderRadius: 6,
          background: selected ? colors.selectedBg : hovered ? colors.hoverBg : "transparent",
          // Drop indicator: inset top line so reordering doesn't shift layout.
          boxShadow: reorder?.dragOverId === thread.id ? `inset 0 2px 0 ${colors.active}` : undefined,
        }}
      >
        {/* Elbow — sized to the row alone so it stays centred on it when sub-threads are expanded */}
        <svg width="14" height="100%" style={connectorStyle}>
          {isLast && <line x1="1" y1="0" x2="1" y2="50%" stroke={colors.textDisabled} strokeWidth="1.5" />}
          {/* With sub-threads the elbow stops short of the chevron below */}
          <line x1="1" y1="50%" x2={hasChildren ? "4" : "14"} y2="50%" stroke={colors.textDisabled} strokeWidth="1.5" />
        </svg>
        {hasChildren && (
          <>
            {/* Chevron at the elbow's end, on the sub-threads' line (8px margin further in), so the icon stays aligned with its siblings */}
            <span
              onClick={(e) => {
                e.stopPropagation();
                setCollapsed((c) => !c);
              }}
              style={{
                position: "absolute",
                left: 14,
                top: "50%",
                width: 14,
                height: 14,
                transform: "translateY(-50%)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                cursor: "pointer",
                color: colors.textDim,
                zIndex: 2,
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
                style={{ transition: "transform 0.15s ease", transform: collapsed ? "rotate(-90deg)" : "rotate(0deg)" }}
              >
                <path d="M2.5 3.5L5 6.5L7.5 3.5" />
              </svg>
            </span>
            {/* Drop from under the chevron to the sub-threads' line */}
            {!collapsed && <span style={{ position: "absolute", left: 20.25, top: "calc(50% + 5px)", bottom: 0, width: 1.5, background: colors.textDisabled, zIndex: 1 }} />}
          </>
        )}
        <button
          draggable={!!reorder}
          onDragStart={
            reorder
              ? (e) => {
                  e.stopPropagation();
                  reorder.onDragStart(thread.id, thread.parent_id);
                }
              : undefined
          }
          onDragOver={reorder ? (e) => reorder.onDragOver(e, thread.id, thread.parent_id) : undefined}
          onDrop={reorder ? (e) => reorder.onDrop(e, thread.id, thread.parent_id) : undefined}
          onDragEnd={
            reorder
              ? (e) => {
                  e.stopPropagation();
                  reorder.onDragEnd();
                }
              : undefined
          }
          onClick={() => onSelect(thread.id)}
          onContextMenu={(e) => onContextMenu(e, thread)}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 4,
            flex: 1,
            minWidth: 0,
            padding: "4px 8px 4px 30px",
            border: "none",
            background: "transparent",
            color: selected ? colors.textLight : colors.textDim,
            fontSize: 14,
            textAlign: "left",
            cursor: "pointer",
          }}
        >
          {selectMode && (
            <span
              onClick={(e) => {
                e.stopPropagation();
                onToggleCheck?.(thread.id);
              }}
              style={{ display: "flex", alignItems: "center", flexShrink: 0, cursor: "pointer" }}
            >
              <span
                style={{
                  width: 12,
                  height: 12,
                  borderRadius: 3,
                  border: `1.5px solid ${checked ? colors.active : colors.textDim}`,
                  backgroundColor: checked ? colors.active : "transparent",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  transition: "all 0.15s",
                }}
              >
                {checked && (
                  <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke={colors.white} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                )}
              </span>
            </span>
          )}
          <SessionKindIcon channel={thread} />
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
                marginLeft: thread.diff_additions > 0 || thread.diff_deletions > 0 ? 4 : "auto",
              }}
            />
          )}
          {activePills.map((p, i) => (
            <StatusPill key={p.kind} label={p.label} color={colors[p.color]} title={p.title} marginLeft={isUnread || i > 0 || thread.diff_additions > 0 || thread.diff_deletions > 0 ? 4 : "auto"} />
          ))}
          {(thread.container_running || thread.agent_running || isRunningMapRef?.current?.get(thread.id)) && (
            <span
              style={{
                width: 6,
                height: 6,
                borderRadius: "50%",
                backgroundColor: colors.active,
                flexShrink: 0,
                marginLeft: isUnread || hasAnyPill || thread.diff_additions > 0 || thread.diff_deletions > 0 ? 4 : "auto",
              }}
            />
          )}
        </button>
        {hovered && !selectMode && (
          <span
            title={thread.worktree ? "Fork: new worktree from this branch + continue this conversation" : "Fork: new thread continuing this conversation"}
            onClick={(e) => {
              e.stopPropagation();
              if (forking) return;
              setForking(true);
              forkThread(thread.id)
                .then((newId) => onSelect(newId))
                .catch((err) => console.warn("[sidebar] fork thread failed:", err))
                .finally(() => setForking(false));
            }}
            style={{
              flexShrink: 0,
              marginRight: 4,
              cursor: "pointer",
              padding: "2px 6px",
              fontSize: 11,
              lineHeight: 1,
              borderRadius: 4,
              whiteSpace: "nowrap",
              color: colors.textDim,
              opacity: forking ? 0.4 : 1,
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
            +fork
          </span>
        )}
      </div>
      {hasChildren &&
        !collapsed &&
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
            reorder={reorder}
            selectMode={selectMode}
            checked={checked}
            onToggleCheck={onToggleCheck}
            isRunningMapRef={isRunningMapRef}
            unreadIdsRef={unreadIdsRef}
            pillsRef={pillsRef}
          />
        ))}
    </div>
  );
}
