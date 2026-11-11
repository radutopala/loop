import { type ReactNode, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

const SHOW_DELAY_MS = 250;
const WIDTH = 260;
const MARGIN = 8;

/**
 * Shows `text` above its child on hover, sooner than a native title (which
 * Electron holds back about a second) and styled like the composer's other
 * popovers. The first line is the heading; the rest wraps below it. It
 * renders on the body, kept inside the window, so a pane's edge can't cut it.
 * With `shrink` the anchor gives up width like its child would, for a pill
 * whose label ellipsises. With `mono` the lines below the heading are code,
 * such as a command, in the monospace font and `width` pixels wide.
 */
export function HoverTip({ text, shrink, mono, width = WIDTH, children }: { text: string; shrink?: boolean; mono?: boolean; width?: number; children: ReactNode }) {
  const { colors } = useTheme();
  const [at, setAt] = useState<{ left: number; bottom: number } | null>(null);
  const anchor = useRef<HTMLSpanElement>(null);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const show = () => {
    window.clearTimeout(timer.current);
    if (!text) return;
    timer.current = window.setTimeout(() => {
      const r = anchor.current?.getBoundingClientRect();
      if (!r) return;
      const centred = r.left + r.width / 2 - width / 2;
      setAt({
        left: Math.max(MARGIN, Math.min(centred, window.innerWidth - width - MARGIN)),
        bottom: window.innerHeight - r.top + 6,
      });
    }, SHOW_DELAY_MS);
  };
  const hide = () => {
    window.clearTimeout(timer.current);
    setAt(null);
  };
  const [heading, ...rest] = text.split("\n");

  return (
    <span ref={anchor} style={{ display: "flex", flexShrink: shrink ? 1 : 0, minWidth: shrink ? 0 : undefined }} onMouseEnter={show} onMouseLeave={hide} onMouseDown={hide}>
      {children}
      {at &&
        createPortal(
          <span
            role="tooltip"
            data-testid="hover-tip"
            style={{
              position: "fixed",
              left: at.left,
              bottom: at.bottom,
              zIndex: 1000,
              width,
              padding: "6px 8px",
              backgroundColor: colors.surface,
              border: `1px solid ${colors.border}`,
              borderRadius: 8,
              boxShadow: `0 4px 12px ${colors.shadow}`,
              color: colors.textDim,
              // On the body it's outside the app root, which sets the font.
              fontFamily: fonts.sans,
              fontSize: 11,
              lineHeight: 1.4,
              whiteSpace: mono ? "pre-wrap" : "pre-line",
              // A long path or URL wraps instead of running out of the box.
              overflowWrap: "anywhere",
              pointerEvents: "none",
            }}
          >
            <span style={{ display: "block", color: colors.text, fontWeight: 600, marginBottom: rest.length ? 2 : 0 }}>{heading}</span>
            {mono ? <span style={{ fontFamily: fonts.mono }}>{rest.join("\n")}</span> : rest.join("\n")}
          </span>,
          document.body,
        )}
    </span>
  );
}
