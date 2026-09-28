import { useLayoutEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { LearnView } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { learnBadgeLabel } from "./learnState";

interface LearnBadgeProps {
  /** Pane leaf id — the portal target is `pane-header-slot-${leafId}`. */
  leafId: string;
  learn: LearnView;
  /** This badge sits in the open Learn view. */
  open: boolean;
  onToggle: () => void;
}

/**
 * The chat pane's Learn label, at the right of its header: lit while a learn
 * pass runs or proposals wait, dim once the channel has a learn thread to
 * look back at. A click opens the Learn view; in the Learn view's own chat
 * header, it closes it.
 * Portals into the pane header like PaneHeaderStatus, so it returns null
 * until the slot mounts.
 */
export function LearnBadge({ leafId, learn, open, onToggle }: LearnBadgeProps) {
  const { colors } = useTheme();
  const [slot, setSlot] = useState<HTMLElement | null>(null);

  // Before paint: a Learn view opening with its header must not paint a
  // frame without the badge.
  useLayoutEffect(() => {
    const find = () => document.getElementById(`pane-header-slot-${leafId}`);
    setSlot(find());
    if (find()) return;
    // Slot may mount in the same frame; retry once after paint.
    const raf = requestAnimationFrame(() => setSlot(find()));
    return () => cancelAnimationFrame(raf);
  }, [leafId]);

  const label = learnBadgeLabel(learn.running, learn.open.length);
  if (!slot || (!label && !learn.learnChannelId && !open)) return null;
  const lit = label !== null;
  return createPortal(
    <button
      data-testid="learn-badge"
      data-running={learn.running ? "true" : "false"}
      onClick={onToggle}
      title={open ? "Close the Learn view" : "Open the Learn view"}
      aria-expanded={open}
      style={{
        display: "flex",
        alignItems: "center",
        flexShrink: 0,
        gap: 4,
        height: 16,
        boxSizing: "border-box",
        whiteSpace: "nowrap",
        background: open ? colors.hoverBg : "transparent",
        border: `1px solid ${lit ? colors.active : colors.border}`,
        color: lit ? colors.active : colors.textDim,
        cursor: "pointer",
        padding: "0 6px",
        fontSize: 10,
        fontFamily: fonts.mono,
        lineHeight: 1,
        borderRadius: 8,
      }}
    >
      <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M9 18h6" />
        <path d="M10 22h4" />
        <path d="M12 2a7 7 0 0 0-4 12.7V17h8v-2.3A7 7 0 0 0 12 2z" />
      </svg>
      {label ?? "learn"}
    </button>,
    slot,
  );
}
