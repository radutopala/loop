import { type ReactNode, type RefObject, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

interface InfoPopupProps {
  /** The element the popup sits beside. */
  anchorRef: RefObject<HTMLElement | null>;
  /** Whether the pointer is on the anchor. */
  hovered: boolean;
  testId: string;
  /** The bold first line. */
  title: ReactNode;
  /** A two-column grid of label and value cells under the title. */
  children?: ReactNode;
}

/**
 * InfoPopup is the card the sidebar shows past its right edge, centered on
 * what the pointer rests on, with an arrow at it. A click, drag or scroll
 * hides it until the pointer comes back.
 */
export function InfoPopup({ anchorRef, hovered, testId, title, children }: InfoPopupProps) {
  const { colors } = useTheme();
  // mid is the anchor's vertical middle, where the arrow points.
  const [pos, setPos] = useState<{ top: number; left: number; mid: number } | null>(null);
  const popupRef = useRef<HTMLDivElement>(null);

  // A layout effect, so the popup shows in the same frame as the anchor's
  // hover background.
  useLayoutEffect(() => {
    if (!hovered) {
      setPos(null);
      return;
    }
    const anchor = anchorRef.current;
    const r = anchor?.getBoundingClientRect();
    // Past the sidebar's edge, not the anchor's, so the arrow doesn't sit on
    // the sidebar's border.
    const right = anchor?.closest("[data-testid='sidebar']")?.getBoundingClientRect().right ?? r?.right ?? 0;
    if (r) setPos({ top: r.top, left: right + 10, mid: r.top + r.height / 2 });
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
  }, [hovered, anchorRef]);

  // Center it on the anchor, kept on screen; the arrow still points at the
  // anchor when it's shifted. After every render, since content arriving
  // late (a row's PR line) makes it taller.
  useLayoutEffect(() => {
    const el = popupRef.current;
    if (!el || !pos) return;
    const h = el.getBoundingClientRect().height;
    const top = Math.round(Math.max(8, Math.min(pos.mid - h / 2, window.innerHeight - 8 - h)));
    if (top !== pos.top) setPos({ ...pos, top });
  });

  if (!pos) return null;
  // A step lighter than anything under it on dark themes, so it stands out
  // against the sidebar and the chat.
  const bg = colors.isDark ? colors.inputBorder : colors.bg;
  const border = infoBorder(colors);
  return createPortal(
    <div
      ref={popupRef}
      data-testid={testId}
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
          top: pos.mid - pos.top - 5,
          width: 10,
          height: 10,
          backgroundColor: bg,
          borderLeft: border,
          borderBottom: border,
          transform: "rotate(45deg)",
        }}
      />
      <div
        data-testid={`${testId}-title`}
        style={{
          gridColumn: "1 / -1",
          color: colors.textLight,
          fontWeight: 600,
          whiteSpace: "pre-wrap",
          overflowWrap: "anywhere",
          ...(children ? { paddingBottom: 6, marginBottom: 4, borderBottom: border } : {}),
        }}
      >
        {title}
      </div>
      {children}
    </div>,
    document.body,
  );
}

/** The popup's border, also for the rules between its sections. */
export function infoBorder(colors: { isDark: boolean; textDisabled: string; inputBorder: string }): string {
  return `1px solid ${colors.isDark ? colors.textDisabled : colors.inputBorder}`;
}
