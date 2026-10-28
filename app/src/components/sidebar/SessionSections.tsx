import { Fragment, type ReactNode, type RefObject, useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Channel } from "../../types";
import { InfoPopup } from "./InfoPopup";
import type { PillKind } from "./pills";
import { fixingPill, REVIEWING_PILL, SIDEBAR_PILLS, useShownPills } from "./pills";
import { RowInfoPopup } from "./RowInfoPopup";
import { useRevealSelected } from "./reveal";
import { SessionKindIcon } from "./SessionKindIcon";
import { StatusPill } from "./StatusPill";
import { relativeTime, sessionContext, sessionName } from "./sessions";

export type SectionKey = "recent" | "tree";

interface SectionTabsProps {
  tab: SectionKey;
  /** False when nothing is recent: only the task filter is shown, for Tree. */
  showTabs: boolean;
  recentCount: number;
  onChange: (tab: SectionKey) => void;
  /** Whether each tab hides task threads; the toggle flips the open tab's. */
  hideTasks: Record<SectionKey, boolean>;
  onToggleHideTasks: () => void;
  /** Whether both tabs show only the rows with a lit pill. */
  needsYouOnly: boolean;
  onToggleNeedsYouOnly: () => void;
  /** Opens the menu that picks which pills show and count as needing you. */
  onPickPills: (e: React.MouseEvent) => void;
}

/**
 * Tabs switching the sidebar list between Recent sessions and the channel
 * tree, with the open tab's task-thread filter beside them.
 */
export function SectionTabs({ tab, showTabs, recentCount, onChange, hideTasks, onToggleHideTasks, needsYouOnly, onToggleNeedsYouOnly, onPickPills }: SectionTabsProps) {
  const { colors } = useTheme();
  const pills = useShownPills();
  const tabs: { key: SectionKey; label: string; count?: number }[] = [
    { key: "recent", label: "Recent", count: recentCount },
    { key: "tree", label: "Tree" },
  ];
  const tabLabel = tab === "recent" ? "Recent" : "Tree";
  // Which control the pointer rests on, for its info card.
  const [hovered, setHovered] = useState<SectionInfoKey | null>(null);
  const refs = {
    recent: useRef<HTMLButtonElement>(null),
    tree: useRef<HTMLButtonElement>(null),
    bell: useRef<HTMLButtonElement>(null),
    tasks: useRef<HTMLButtonElement>(null),
  };
  const hover = (key: SectionInfoKey) => ({
    ref: refs[key],
    onMouseEnter: () => setHovered(key),
    onMouseLeave: () => setHovered(null),
  });
  const hideTasksHere = hideTasks[tab];
  const showing = needsYouOnly ? "only the sessions that need you" : "all sessions";
  return (
    <div
      data-sidebar-sticky
      style={{
        position: "sticky",
        top: 0,
        // Above the tree's connector lines (zIndex 1) and a thread's fork
        // chevron (zIndex 2) in ThreadItem, which come later in the DOM and
        // would otherwise paint over the bar.
        zIndex: 3,
        display: "flex",
        alignItems: "center",
        justifyContent: "flex-end",
        gap: 4,
        padding: "0 8px 4px",
        backgroundColor: colors.sidebarNav,
      }}
    >
      {showTabs && (
        <div
          role="tablist"
          data-testid="sidebar-tabs"
          style={{
            flex: 1,
            display: "flex",
            gap: 2,
            padding: 2,
            borderRadius: 7,
            boxShadow: `inset 0 0 0 1px ${colors.border}`,
          }}
        >
          {tabs.map((t) => {
            const selected = t.key === tab;
            return (
              <button
                key={t.key}
                role="tab"
                aria-selected={selected}
                data-testid={`sidebar-tab-${t.key}`}
                {...hover(t.key)}
                onClick={() => onChange(t.key)}
                style={{
                  position: "relative",
                  flex: 1,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  gap: 4,
                  padding: "3px 8px",
                  border: "none",
                  borderRadius: 5,
                  background: selected ? colors.selectedBg : "transparent",
                  color: selected ? colors.textLight : colors.textDim,
                  fontSize: 11,
                  fontWeight: selected ? 600 : 500,
                  cursor: "pointer",
                }}
              >
                {t.label}
                {/* Pinned to the right edge so the label doesn't shift as the count gains a digit. */}
                {t.count !== undefined && (
                  <span data-testid={`sidebar-tab-${t.key}-count`} style={{ position: "absolute", right: 8, fontWeight: 400, color: colors.textDisabled, fontVariantNumeric: "tabular-nums" }}>
                    {t.count}
                  </span>
                )}
              </button>
            );
          })}
        </div>
      )}
      <button
        data-testid="sidebar-needs-you"
        onClick={onToggleNeedsYouOnly}
        onContextMenu={onPickPills}
        aria-pressed={needsYouOnly}
        aria-label={needsYouOnly ? "Show all sessions" : "Show only sessions that need you"}
        {...hover("bell")}
        style={{
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          padding: 4,
          border: "none",
          borderRadius: 4,
          cursor: "pointer",
          color: needsYouOnly ? colors.warning : colors.textDim,
          background: needsYouOnly ? colors.hoverBg : "none",
        }}
      >
        {/* A bell: the rows that are calling for you. */}
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" />
          <path d="M10.3 21a1.94 1.94 0 0 0 3.4 0" />
        </svg>
      </button>
      <button
        data-testid="sidebar-hide-tasks"
        onClick={onToggleHideTasks}
        aria-pressed={hideTasksHere}
        aria-label={hideTasksHere ? `Show task threads in ${tabLabel}` : `Hide task threads in ${tabLabel}`}
        {...hover("tasks")}
        style={{
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          padding: 4,
          border: "none",
          borderRadius: 4,
          cursor: "pointer",
          color: hideTasksHere ? colors.active : colors.textDim,
          background: hideTasksHere ? colors.hoverBg : "none",
        }}
      >
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
          {hideTasksHere && <line x1="4" y1="4" x2="20" y2="20" />}
        </svg>
      </button>
      <SectionInfo
        anchorRef={refs.recent}
        hovered={hovered === "recent"}
        testId="sidebar-tab-recent-info"
        title="Recent"
        description="Sessions active in the last 48 hours, from anywhere in the tree; those waiting on you or running come first."
        lines={[
          ["sessions", String(recentCount)],
          ["showing", showing, "showing"],
          ["task threads", hideTasks.recent ? "hidden" : "shown"],
        ]}
      />
      <SectionInfo
        anchorRef={refs.tree}
        hovered={hovered === "tree"}
        testId="sidebar-tab-tree-info"
        title="Tree"
        description="Every channel with its threads and worktrees."
        lines={[
          ["showing", showing, "showing"],
          ["task threads", hideTasks.tree ? "hidden" : "shown"],
        ]}
      />
      <SectionInfo
        anchorRef={refs.bell}
        hovered={hovered === "bell"}
        testId="sidebar-needs-you-info"
        title={`Showing ${showing}`}
        description={`Click to show ${needsYouOnly ? "all sessions" : "only the sessions waiting on you: those with one of these pills"}. Right-click to turn pills on or off.`}
        lines={SIDEBAR_PILLS.map((p) => {
          const on = pills.some((s) => s.kind === p.kind);
          return [<StatusPill key={p.kind} label={p.label} color={on ? colors[p.color] : colors.textDisabled} />, `${p.hint}${on ? "" : " · turned off"}`, p.kind];
        })}
      />
      <SectionInfo
        anchorRef={refs.tasks}
        hovered={hovered === "tasks"}
        testId="sidebar-hide-tasks-info"
        title="Task threads"
        description={`The threads scheduled tasks post to. Click to ${hideTasksHere ? "show" : "hide"} them in ${tabLabel}; running ones and those waiting on you always show.`}
        lines={[
          ["Recent", hideTasks.recent ? "hidden" : "shown", "recent"],
          ["Tree", hideTasks.tree ? "hidden" : "shown", "tree"],
        ]}
      />
    </div>
  );
}

type SectionInfoKey = SectionKey | "bell" | "tasks";

interface SectionInfoProps {
  anchorRef: RefObject<HTMLElement | null>;
  hovered: boolean;
  testId: string;
  title: string;
  description: string;
  /** Label, value and an optional key for the value's test id (the label's text otherwise). */
  lines: [ReactNode, string, string?][];
}

/** The info card of a control in the bar above the list. */
function SectionInfo({ anchorRef, hovered, testId, title, description, lines }: SectionInfoProps) {
  const { colors } = useTheme();
  return (
    <InfoPopup anchorRef={anchorRef} hovered={hovered} testId={testId} title={title}>
      <div style={{ gridColumn: "1 / -1", color: colors.textLight, maxWidth: 320, marginBottom: 4 }}>{description}</div>
      {lines.map(([label, value, key]) => {
        const id = key ?? String(label);
        return (
          <Fragment key={id}>
            <span style={{ color: colors.textMuted }}>{label}</span>
            <span data-testid={`${testId}-${id.replace(/ /g, "-")}`} style={{ color: colors.textLight }}>
              {value}
            </span>
          </Fragment>
        );
      })}
    </InfoPopup>
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
  useRevealSelected(rowRef, selected);
  const shown = useShownPills();
  const waiting = pills.length > 0;
  const fixing = fixingPill(channel.pr_attention, shown);
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
          // As tall as a row with its parents line, so channels (which
          // have none) match threads.
          minHeight: 42,
          padding: "6px 8px",
          border: "none",
          borderRadius: 6,
          background: selected ? colors.selectedBg : hovered ? colors.hoverBg : "transparent",
          color: selected ? colors.textLight : colors.textDim,
          fontSize: 14,
          textAlign: "left",
          cursor: "pointer",
        }}
      >
        {/* Status on top, the thread's kind icon under it. */}
        <span style={{ width: 12, display: "flex", flexDirection: "column", alignItems: "center", gap: 4, flexShrink: 0 }}>
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
          <SessionKindIcon channel={channel} showPlainThread />
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
        {shown
          .filter((p) => pills.includes(p.kind))
          .map((p) => (
            <StatusPill key={p.kind} label={p.label} color={colors[p.color]} />
          ))}
        {channel.review_running && <StatusPill label={REVIEWING_PILL.label} color={colors[REVIEWING_PILL.color]} />}
        {fixing && <StatusPill label={fixing.label} color={colors[fixing.color]} />}
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
  onSelect: (id: string) => void;
  onContextMenu: (e: React.MouseEvent, channel: Channel) => void;
}

/**
 * SessionSections renders the Recent tab's list: every session active in the
 * last 48 hours, newest first, which puts the active sessions on top.
 */
export function SessionSections({ active, recent, byId, selectedId, isRunning, pillsFor, isUnread, lastActivity, onSelect, onContextMenu }: SessionSectionsProps) {
  const { colors } = useTheme();
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
  if (recent.length === 0) {
    return (
      <div data-testid="sidebar-recent-empty" style={{ padding: "8px 16px", fontSize: 11, color: colors.textDisabled }}>
        No matching sessions
      </div>
    );
  }
  return (
    <div data-testid="sidebar-recent">
      {recent.map((c) => {
        // An active session's age would only say "now".
        const at = activeIds.has(c.id) ? undefined : lastActivity(c);
        return row(c, at ? relativeTime(Math.max(0, now - at)) : undefined);
      })}
    </div>
  );
}
