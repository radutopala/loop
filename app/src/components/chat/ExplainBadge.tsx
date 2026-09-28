import { createPortal } from "react-dom";
import type { ExplainView } from "../../hooks/useExplain";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ExplainIcon } from "./ExplainIcon";
import { usePaneHeaderSlot } from "./LearnBadge";

interface ExplainBadgeProps {
  /** Pane leaf id — the portal target is `pane-header-slot-${leafId}`. */
  leafId: string;
  explain: ExplainView;
  /** This badge sits by the open Explain view. */
  open: boolean;
  onToggle: () => void;
}

/**
 * The chat pane's Explain label, at the right of its header before the Learn
 * one, once the channel has explanations to look back at. It only opens the
 * Explain view (open, it closes it): each explanation's state shows at the
 * end of its turn (ExplainButton).
 */
export function ExplainBadge({ leafId, explain, open, onToggle }: ExplainBadgeProps) {
  const { colors } = useTheme();
  const slot = usePaneHeaderSlot(leafId);
  if (!slot || !explain.available || (explain.explanations.length === 0 && !open)) return null;
  return createPortal(
    <button
      data-testid="explain-badge"
      onClick={onToggle}
      title={open ? "Close the Explain view" : "Open the Explain view"}
      aria-expanded={open}
      style={{
        display: "flex",
        alignItems: "center",
        flexShrink: 0,
        gap: 4,
        height: 16,
        boxSizing: "border-box",
        whiteSpace: "nowrap",
        order: 1,
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
      <ExplainIcon size={9} />
      explain
    </button>,
    slot,
  );
}
