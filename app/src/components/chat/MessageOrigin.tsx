import { useLayoutEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { HoverTip } from "./HoverTip";

const HINT = "Sent by the agent in this thread: go to the message it was working on";

/** The "from #thread" pill on a message another channel's agent sent. A
 * long thread name ends in an ellipsis, and then hovering shows it whole. */
export function MessageOrigin({ name, href }: { name: string; href: string }) {
  const { colors } = useTheme();
  const ref = useRef<HTMLAnchorElement>(null);
  const [truncated, setTruncated] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setTruncated(el.scrollWidth > el.clientWidth);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const label = `from #${name}`;

  return (
    <HoverTip text={truncated ? `${label}\n${HINT}` : ""} shrink width={320}>
      <a
        ref={ref}
        data-testid="message-origin"
        href={href}
        title={truncated ? undefined : `Sent by the agent in ${name}: go to the message it was working on`}
        style={{
          display: "inline-block",
          boxSizing: "border-box",
          height: 20,
          minWidth: 0,
          padding: "0 8px",
          border: `1px solid ${colors.active}`,
          borderRadius: 8,
          color: colors.active,
          fontFamily: fonts.mono,
          fontSize: 11,
          lineHeight: "18px",
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
          textDecoration: "none",
        }}
      >
        {label}
      </a>
    </HoverTip>
  );
}
