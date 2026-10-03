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
 */
export function HoverTip({ text, children }: { text: string; children: ReactNode }) {
  const { colors } = useTheme();
  const [at, setAt] = useState<{ left: number; bottom: number } | null>(null);
  const anchor = useRef<HTMLSpanElement>(null);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const show = () => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      const r = anchor.current?.getBoundingClientRect();
      if (!r) return;
      const centred = r.left + r.width / 2 - WIDTH / 2;
      setAt({
        left: Math.max(MARGIN, Math.min(centred, window.innerWidth - WIDTH - MARGIN)),
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
    <span ref={anchor} style={{ display: "flex", flexShrink: 0 }} onMouseEnter={show} onMouseLeave={hide} onMouseDown={hide}>
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
              width: WIDTH,
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
              whiteSpace: "pre-line",
              pointerEvents: "none",
            }}
          >
            <span style={{ display: "block", color: colors.text, fontWeight: 600, marginBottom: rest.length ? 2 : 0 }}>{heading}</span>
            {rest.join("\n")}
          </span>,
          document.body,
        )}
    </span>
  );
}
