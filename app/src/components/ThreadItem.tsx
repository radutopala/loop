import { useState } from "react";
import type { Channel } from "../types";
import { useTheme } from "../ThemeContext";

interface ThreadItemProps {
  thread: Channel;
  selected: boolean;
  isLast?: boolean;
  onSelect: (id: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
  selectMode?: boolean;
  checked?: boolean;
  onToggleCheck?: (id: string) => void;
  /** Real-time running status from app-level chat state store. */
  isRunningMapRef?: React.RefObject<Map<string, boolean>>;
}

export function ThreadItem({ thread, selected, isLast, onSelect, onContextMenu, selectMode, checked, onToggleCheck, isRunningMapRef }: ThreadItemProps) {
  const { colors } = useTheme();
  const [hovered, setHovered] = useState(false);

  return (
    <div style={{ position: "relative", margin: "0 8px" }}>
      {/* Tree connector line — positioned absolutely to span full row height */}
      <svg
        width="10"
        height="100%"
        style={{
          position: "absolute",
          left: 26,
          top: 0,
          bottom: 0,
          height: "100%",
          overflow: "visible",
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
          padding: "4px 8px 4px 40px",
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
      <span
        style={{
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {thread.name || thread.id}
      </span>
      {(thread.container_running || thread.agent_running || isRunningMapRef?.current?.get(thread.id)) && (
        <span
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            backgroundColor: colors.active,
            flexShrink: 0,
            marginLeft: "auto",
          }}
        />
      )}
    </button>
    </div>
  );
}
