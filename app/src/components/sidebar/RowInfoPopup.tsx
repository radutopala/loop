import { Fragment, type ReactNode, type RefObject } from "react";
import { useChannelPR, useChannelPRStatus } from "../../hooks/useChannelPR";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Channel } from "../../types";
import { InfoPopup, infoBorder } from "./InfoPopup";
import { type RowInfoLine, rowInfoLines } from "./rowInfo";
import { sessionName } from "./sessions";

interface RowInfoPopupProps {
  channel: Channel;
  /** The row the popup sits beside. */
  anchorRef: RefObject<HTMLElement | null>;
  /** Whether the pointer is on the row. */
  hovered: boolean;
}

/**
 * RowInfoPopup shows a sidebar row's full name, description, ticket,
 * directory, git state, PR and agent settings beside it as soon as the pointer is
 * on the row. A click, drag or scroll hides it until the pointer comes back.
 */
export function RowInfoPopup({ channel, anchorRef, hovered }: RowInfoPopupProps) {
  const { colors } = useTheme();
  // Looked up only while the popup shows, not for every row in the sidebar.
  const pr = useChannelPR(channel.id, channel.branch, hovered);
  // The watcher's status, only for a row with a PR/MR pill to explain.
  const status = useChannelPRStatus(channel.id, hovered && (!!channel.pr_attention || (channel.unresolved_threads ?? 0) > 0));
  const lines = rowInfoLines(channel, pr, status);
  const description = channel.description?.trim();

  const border = infoBorder(colors);
  const label = { color: colors.textMuted };
  const value = { color: colors.textLight, fontFamily: fonts.mono, fontSize: 12, overflowWrap: "anywhere" as const };
  // The branch is what's usually looked for, so it's picked out like the
  // sidebar's worktree icon.
  const branch = { color: colors.active, fontWeight: 600 };
  return (
    // The row cuts a long name short; here it shows in full.
    <InfoPopup anchorRef={anchorRef} hovered={hovered} testId="sidebar-row-info" title={sessionName(channel)}>
      {(description || lines.length > 0) && (
        <>
          {description && (
            <div
              data-testid="sidebar-row-info-description"
              style={{
                gridColumn: "1 / -1",
                color: colors.textLight,
                whiteSpace: "pre-wrap",
                overflowWrap: "anywhere",
                ...(lines.length > 0 ? { paddingBottom: 6, marginBottom: 4, borderBottom: border } : {}),
              }}
            >
              {description}
            </div>
          )}
          {lines.map((line) => (
            <Fragment key={line.key}>
              <span style={{ ...label, ...(line.key === "branch" ? { color: colors.active } : {}), display: "inline-flex", alignItems: "center", gap: 5 }}>
                <RowInfoIcon kind={line.key} />
                {line.key}
              </span>
              <span style={value}>
                <span data-testid={`sidebar-row-info-${line.key}`} style={line.key === "branch" ? branch : undefined}>
                  {line.value}
                </span>
                {line.detail && (
                  <span data-testid={`sidebar-row-info-${line.key}-detail`} style={{ ...label, fontFamily: fonts.sans, fontSize: 12.5 }}>
                    {` ${line.detail}`}
                  </span>
                )}
              </span>
            </Fragment>
          ))}
        </>
      )}
    </InfoPopup>
  );
}

/** Each line's icon, drawn like the sidebar's other stroke icons. */
const ICONS: Record<RowInfoLine["key"], ReactNode> = {
  ticket: (
    <>
      <path d="M2 9a3 3 0 0 0 0 6v2a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-2a3 3 0 0 0 0-6V7a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2z" />
      <path d="M13 5v2M13 17v2M13 11v2" />
    </>
  ),
  path: <path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.7-.9l-.8-1.2A2 2 0 0 0 7.9 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2z" />,
  branch: (
    <>
      <line x1="6" y1="3" x2="6" y2="15" />
      <circle cx="18" cy="6" r="3" />
      <circle cx="6" cy="18" r="3" />
      <path d="M18 9a9 9 0 0 1-9 9" />
    </>
  ),
  commit: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M3 12h6M15 12h6" />
    </>
  ),
  pr: (
    <>
      <circle cx="6" cy="6" r="3" />
      <circle cx="18" cy="18" r="3" />
      <path d="M6 9v12M13 6h3a2 2 0 0 1 2 2v7" />
    </>
  ),
  ci: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M15 9l-6 6M9 9l6 6" />
    </>
  ),
  conflict: (
    <>
      <path d="M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
      <path d="M12 9v4M12 17h.01" />
    </>
  ),
  cmt: <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />,
  sync: <path d="M7 20V4M3 8l4-4 4 4M17 4v16M13 16l4 4 4-4" />,
  model: <path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z" />,
  status: <path d="M22 12h-4l-3 9L9 3l-3 9H2" />,
};

function RowInfoIcon({ kind }: { kind: RowInfoLine["key"] }) {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
      {ICONS[kind]}
    </svg>
  );
}
