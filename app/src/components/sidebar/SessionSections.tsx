import { type ReactNode, useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Channel } from "../../types";
import type { PillKind } from "./pills";
import { SIDEBAR_PILLS } from "./pills";
import { RowInfoPopup } from "./RowInfoPopup";
import { StatusPill } from "./StatusPill";
import { RECENT_LIMIT, relativeTime, sessionContext, sessionName } from "./sessions";

export type SectionKey = "recent" | "all";

interface SectionHeaderProps {
  section: SectionKey;
  label: string;
  count?: number;
  collapsed: boolean;
  onToggle: () => void;
}

/** A collapsible section title: "▾ ACTIVE · 3". */
export function SectionHeader({ section, label, count, collapsed, onToggle }: SectionHeaderProps) {
  const { colors } = useTheme();
  return (
    <button
      data-testid={`sidebar-section-${section}`}
      onClick={onToggle}
      aria-expanded={!collapsed}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 4,
        width: "100%",
        padding: "8px 12px 4px",
        border: "none",
        background: "transparent",
        cursor: "pointer",
        fontSize: 10,
        fontWeight: 700,
        color: colors.textDim,
        textTransform: "uppercase",
        letterSpacing: 1,
        textAlign: "left",
      }}
    >
      <svg
        width="8"
        height="8"
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
      {label}
      {count !== undefined && <span style={{ fontWeight: 400 }}>· {count}</span>}
    </button>
  );
}

interface SessionRowProps {
  channel: Channel;
  context: string;
  selected: boolean;
  unread: boolean;
  running: boolean;
  pills: PillKind[];
  /** Shown on the right when there's no pill, e.g. "12m". */
  trailing?: string;
  onSelect: (id: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
}

function SessionRow({ channel, context, selected, unread, running, pills, trailing, onSelect, onContextMenu }: SessionRowProps) {
  const { colors } = useTheme();
  const [hovered, setHovered] = useState(false);
  const rowRef = useRef<HTMLButtonElement>(null);
  const waiting = pills.length > 0;
  return (
    <div style={{ margin: "0 8px" }}>
      <RowInfoPopup channel={channel} anchorRef={rowRef} hovered={hovered} />
      <button
        ref={rowRef}
        data-testid="sidebar-session-row"
        data-channel-id={channel.id}
        onClick={() => onSelect(channel.id)}
        onContextMenu={(e) => onContextMenu(e, channel)}
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          width: "100%",
          padding: "4px 8px",
          border: "none",
          borderRadius: 6,
          background: selected ? colors.selectedBg : hovered ? colors.hoverBg : "transparent",
          color: selected ? colors.textLight : colors.textDim,
          fontSize: 14,
          textAlign: "left",
          cursor: "pointer",
        }}
      >
        <span style={{ width: 10, display: "flex", justifyContent: "center", flexShrink: 0 }}>
          {waiting ? (
            <span style={{ width: 7, height: 7, borderRadius: "50%", backgroundColor: colors.warning }} />
          ) : running ? (
            <span
              style={{
                width: 8,
                height: 8,
                borderRadius: "50%",
                border: `1.5px solid ${colors.active}`,
                borderTopColor: "transparent",
                animation: "spin 1s linear infinite",
              }}
            />
          ) : null}
        </span>
        <span style={{ display: "flex", flexDirection: "column", minWidth: 0, flex: 1 }}>
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontWeight: unread ? 600 : undefined }}>{sessionName(channel)}</span>
          {context && <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 10, color: colors.textDisabled }}>{context}</span>}
        </span>
        {(channel.diff_additions > 0 || channel.diff_deletions > 0) && (
          <span data-testid="sidebar-session-diff" style={{ flexShrink: 0, fontSize: 9, fontFamily: fonts.mono }}>
            {channel.diff_additions > 0 && <span style={{ color: colors.diffAddText }}>+{channel.diff_additions}</span>}
            {channel.diff_additions > 0 && channel.diff_deletions > 0 && " "}
            {channel.diff_deletions > 0 && <span style={{ color: colors.diffDelText }}>-{channel.diff_deletions}</span>}
          </span>
        )}
        {unread && !selected && <span style={{ width: 6, height: 6, borderRadius: "50%", backgroundColor: "#5b9cf5", flexShrink: 0 }} />}
        {SIDEBAR_PILLS.filter((p) => pills.includes(p.kind)).map((p) => (
          <StatusPill key={p.kind} label={p.label} color={colors[p.color]} title={p.title} />
        ))}
        {!waiting && trailing && <span style={{ flexShrink: 0, fontSize: 10, fontFamily: fonts.mono, color: colors.textDisabled }}>{trailing}</span>}
      </button>
    </div>
  );
}

interface SessionSectionsProps {
  active: Channel[];
  recent: Channel[];
  byId: Map<string, Channel>;
  selectedId: string | null;
  isRunning: (id: string) => boolean;
  pillsFor: (id: string) => PillKind[];
  isUnread: (id: string) => boolean;
  lastActivity: (channel: Channel) => number | undefined;
  collapsed: Record<SectionKey, boolean>;
  onToggle: (section: SectionKey) => void;
  onSelect: (id: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
}

/**
 * SessionSections renders the Recent section above the channel tree, newest
 * first, which puts the active sessions (also in recent) on top. It hides
 * itself when empty, and shows RECENT_LIMIT rows, or all the active ones if
 * there are more; each "show more" adds another RECENT_LIMIT.
 */
export function SessionSections({ active, recent, byId, selectedId, isRunning, pillsFor, isUnread, lastActivity, collapsed, onToggle, onSelect, onContextMenu }: SessionSectionsProps) {
  const { colors } = useTheme();
  // How many times "show more" was clicked.
  const [pages, setPages] = useState(0);
  // Re-render every minute so the Recent ages stay current.
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(id);
  }, []);

  const row = (c: Channel, trailing?: string): ReactNode => (
    <SessionRow
      key={c.id}
      channel={c}
      context={sessionContext(c, byId)}
      selected={selectedId === c.id}
      unread={isUnread(c.id)}
      running={isRunning(c.id)}
      pills={pillsFor(c.id)}
      trailing={trailing}
      onSelect={onSelect}
      onContextMenu={onContextMenu}
    />
  );

  const activeIds = new Set(active.map((c) => c.id));
  const limit = Math.max(RECENT_LIMIT, active.length);
  const shownRecent = recent.slice(0, limit + pages * RECENT_LIMIT);
  const hidden = recent.length - shownRecent.length;
  const moreStyle: React.CSSProperties = {
    padding: "2px 8px",
    border: "none",
    background: "transparent",
    color: colors.textDisabled,
    fontSize: 11,
    cursor: "pointer",
  };
  return (
    <>
      {recent.length > 0 && (
        <div data-testid="sidebar-recent">
          <SectionHeader section="recent" label="Recent" count={recent.length} collapsed={collapsed.recent} onToggle={() => onToggle("recent")} />
          {!collapsed.recent && (
            <>
              {shownRecent.map((c) => {
                // An active session's age would only say "now".
                const at = activeIds.has(c.id) ? undefined : lastActivity(c);
                return row(c, at ? relativeTime(Math.max(0, now - at)) : undefined);
              })}
              {(hidden > 0 || pages > 0) && (
                <div style={{ display: "flex", margin: "0 8px", paddingLeft: 16 }}>
                  {hidden > 0 && (
                    <button data-testid="sidebar-recent-more" onClick={() => setPages((n) => n + 1)} style={moreStyle}>
                      show {Math.min(RECENT_LIMIT, hidden)} more
                    </button>
                  )}
                  {pages > 0 && (
                    <button data-testid="sidebar-recent-less" onClick={() => setPages(0)} style={moreStyle}>
                      show less
                    </button>
                  )}
                </div>
              )}
            </>
          )}
        </div>
      )}
    </>
  );
}
