import { Fragment, type ReactNode, type RefObject, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Channel } from "../../types";
import { type RowInfoLine, rowInfoLines } from "./rowInfo";

interface RowInfoPopupProps {
  channel: Channel;
  /** The row the popup sits beside. */
  anchorRef: RefObject<HTMLElement | null>;
  /** Whether the pointer is on the row. */
  hovered: boolean;
}

/**
 * RowInfoPopup shows a sidebar row's directory, git state and agent settings
 * beside it as soon as the pointer is on the row. A click, drag or scroll hides it until the
 * pointer comes back.
 */
export function RowInfoPopup({ channel, anchorRef, hovered }: RowInfoPopupProps) {
  const { colors } = useTheme();
  // rowMid is the row's vertical middle, where the arrow points.
  const [pos, setPos] = useState<{ top: number; left: number; rowMid: number } | null>(null);
  const popupRef = useRef<HTMLDivElement>(null);
  const lines = rowInfoLines(channel);
  const hasInfo = lines.length > 0;

  // A layout effect, so the popup shows in the same frame as the row's
  // hover background.
  useLayoutEffect(() => {
    if (!hovered || !hasInfo) {
      setPos(null);
      return;
    }
    const row = anchorRef.current;
    const r = row?.getBoundingClientRect();
    // Past the sidebar's edge, not the row's, so the arrow doesn't sit on
    // the sidebar's border.
    const right = row?.closest("[data-testid='sidebar']")?.getBoundingClientRect().right ?? r?.right ?? 0;
    if (r) setPos({ top: r.top, left: right + 10, rowMid: r.top + r.height / 2 });
    const hide = () => setPos(null);
    window.addEventListener("mousedown", hide, true);
    window.addEventListener("dragstart", hide, true);
    window.addEventListener("wheel", hide, true);
    return () => {
      hide();
      window.removeEventListener("mousedown", hide, true);
      window.removeEventListener("dragstart", hide, true);
      window.removeEventListener("wheel", hide, true);
    };
  }, [hovered, hasInfo, anchorRef]);

  // Center it on the row, kept on screen; the arrow still points at the row
  // when it's shifted.
  useLayoutEffect(() => {
    const el = popupRef.current;
    if (!el || !pos) return;
    const h = el.getBoundingClientRect().height;
    const top = Math.round(Math.max(8, Math.min(pos.rowMid - h / 2, window.innerHeight - 8 - h)));
    if (top !== pos.top) setPos({ ...pos, top });
  }, [pos]);

  if (!pos) return null;
  // A step lighter than anything under it on dark themes, so it stands out
  // against the sidebar and the chat.
  const bg = colors.isDark ? colors.inputBorder : colors.bg;
  const border = `1px solid ${colors.isDark ? colors.textDisabled : colors.inputBorder}`;
  const label = { color: colors.textMuted };
  const value = { color: colors.textLight, fontFamily: fonts.mono, fontSize: 12, overflowWrap: "anywhere" as const };
  // The branch is what's usually looked for, so it's picked out like the
  // sidebar's worktree icon.
  const branch = { color: colors.active, fontWeight: 600 };
  return createPortal(
    <div
      ref={popupRef}
      data-testid="sidebar-row-info"
      style={{
        position: "fixed",
        top: pos.top,
        left: pos.left,
        maxWidth: 640,
        padding: "8px 12px",
        backgroundColor: bg,
        border,
        borderRadius: 6,
        boxShadow: `0 4px 12px ${colors.shadow}`,
        // Portaled outside the app root, so it doesn't inherit the app's font.
        fontFamily: fonts.sans,
        fontSize: 12.5,
        lineHeight: 1.5,
        display: "grid",
        gridTemplateColumns: "auto 1fr",
        columnGap: 12,
        rowGap: 2,
        alignItems: "baseline",
        zIndex: 1000,
        pointerEvents: "none",
      }}
    >
      <span
        style={{
          position: "absolute",
          left: -6,
          top: pos.rowMid - pos.top - 5,
          width: 10,
          height: 10,
          backgroundColor: bg,
          borderLeft: border,
          borderBottom: border,
          transform: "rotate(45deg)",
        }}
      />
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
    </div>,
    document.body,
  );
}

/** Each line's icon, drawn like the sidebar's other stroke icons. */
const ICONS: Record<RowInfoLine["key"], ReactNode> = {
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
