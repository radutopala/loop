import { useLayoutEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { LearnView } from "../../hooks/useLearn";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { LearnIcon } from "./LearnIcon";

interface LearnBadgeProps {
  /** Pane leaf id — the portal target is `pane-header-slot-${leafId}`. */
  leafId: string;
  learn: LearnView;
  /** This badge sits in the open Learn view. */
  open: boolean;
  onToggle: () => void;
}

/**
 * The chat pane's Learn label, at the right of its header, once the channel
 * has a learn thread or proposals to look back at. It only opens the Learn view (in the
 * view's own chat header, it closes it): what a pass is doing, and what it
 * found, shows at the end of the turn it reviewed (LearnTurnButton).
 * Portals into the pane header like PaneHeaderStatus, so it returns null
 * until the slot mounts.
 */
export function LearnBadge({ leafId, learn, open, onToggle }: LearnBadgeProps) {
  const { colors } = useTheme();
  const slot = usePaneHeaderSlot(leafId);
  // Proposals mean a learn thread, even one the window hasn't heard of.
  if (!slot || (!learn.learnChannelId && learn.proposals.length === 0 && !open)) return null;
  return createPortal(
    <button
      data-testid="learn-badge"
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
        // Last in the header, after the Explain label.
        order: 2,
        background: open ? colors.hoverBg : "transparent",
        border: `1px solid ${colors.border}`,
        color: colors.textDim,
        cursor: "pointer",
        padding: "0 6px",
        fontSize: 10,
        fontFamily: fonts.mono,
        lineHeight: 1,
        borderRadius: 8,
      }}
    >
      <LearnIcon size={9} />
      learn
    </button>,
    slot,
  );
}

/**
 * The pane header's slot for leafId, found before paint: a view opening
 * with its header must not paint a frame without its labels.
 */
export function usePaneHeaderSlot(leafId: string): HTMLElement | null {
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  useLayoutEffect(() => {
    const find = () => document.getElementById(`pane-header-slot-${leafId}`);
    const el = find();
    setSlot(el);
    if (el) return;
    // Slot may mount in the same frame; retry once after paint.
    const raf = requestAnimationFrame(() => setSlot(find()));
    return () => cancelAnimationFrame(raf);
  }, [leafId]);
  return slot;
}
